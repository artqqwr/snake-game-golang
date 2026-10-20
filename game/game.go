package game

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/artqqwr/snake-game-golang/network"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

const (
	hudHeight        = 44
	menuItemCount    = 5
	multiplayerItems = 3

	defaultRoomName   = "Snake LAN Room"
	defaultMaxPlayers = 8
	scanTimeout       = 1500 * time.Millisecond
	joinTimeout       = 6 * time.Second
	inputHeartbeat    = 100 * time.Millisecond
	snapshotTimeout   = 5 * time.Second
	refreshInterval   = 250 * time.Millisecond
	defaultTCPPort    = network.DefaultTCPPort
	maxRoomListItems  = 7
)

const (
	screenMenu = iota
	screenRooms
	screenLobby
	screenPlaying
	screenPaused
	screenGameOver
)

const (
	multiplayerOff = iota
	multiplayerHost
	multiplayerJoin
)

// Options configures automatic network startup from command line flags.
type Options struct {
	Host       bool
	Join       string
	RoomName   string
	MaxPlayers int
}

type asyncKind int

const (
	asyncRooms asyncKind = iota
	asyncJoined
)

type asyncResult struct {
	kind   asyncKind
	scanID uint64
	rooms  []network.RoomInfo
	client *network.Client
	err    error
}

// Game implements the Ebitengine game loop and all screens.
type Game struct {
	board  *Board
	camera *Camera
	screen int

	mapIndex        int
	difficultyIndex int
	collisionMode   CollisionMode
	menuIndex       int
	highScore       int
	multiplayerMode int
	maxPlayers      int

	session *session

	rooms         []network.RoomInfo
	roomIndex     int
	scanning      bool
	manualMode    bool
	manualAddress string

	networkStatus string
	networkDetail string

	asyncCh    chan asyncResult
	scanCancel context.CancelFunc
	scanID     uint64

	roomName string
	autoHost bool
	autoJoin string
	autoDone bool
}

// New creates a game that starts in the main menu.
func New() *Game {
	return &Game{
		screen:     screenMenu,
		asyncCh:    make(chan asyncResult, 8),
		roomName:   defaultRoomName,
		maxPlayers: defaultMaxPlayers,
	}
}

// NewWithOptions creates a game that can start hosting or joining immediately.
func NewWithOptions(options Options) *Game {
	game := New()
	if options.RoomName != "" {
		game.roomName = options.RoomName
	}
	if options.MaxPlayers >= 2 && options.MaxPlayers <= network.MaxPlayers {
		game.maxPlayers = options.MaxPlayers
	}
	game.autoHost = options.Host
	game.autoJoin = options.Join
	if options.Host {
		game.multiplayerMode = multiplayerHost
	}
	if options.Join != "" {
		game.multiplayerMode = multiplayerJoin
	}
	return game
}

func (g *Game) Update() error {
	g.handleAutoStart()

	switch g.screen {
	case screenMenu:
		g.updateMenu()
	case screenRooms:
		g.updateRooms()
	case screenLobby:
		g.updateLobby()
	case screenPlaying:
		g.updatePlaying()
	case screenPaused:
		g.updatePaused()
	case screenGameOver:
		g.updateGameOver()
	}
	if g.board != nil && g.camera != nil {
		g.camera.Follow(g.board.CameraTarget())
	}
	return nil
}

func (g *Game) handleAutoStart() {
	if g.autoDone || g.screen != screenMenu {
		return
	}
	if g.autoJoin != "" {
		address := g.autoJoin
		g.autoJoin = ""
		g.autoDone = true
		g.startJoin(address)
		return
	}
	if g.autoHost {
		g.autoHost = false
		g.autoDone = true
		g.startHost()
	}
}

func (g *Game) updateMenu() {
	g.drainAsync()
	if justPressed(ebiten.KeyArrowDown) {
		g.menuIndex = (g.menuIndex + 1) % menuItemCount
	}
	if justPressed(ebiten.KeyArrowUp) {
		g.menuIndex = (g.menuIndex + menuItemCount - 1) % menuItemCount
	}
	if justPressed(ebiten.KeyArrowLeft) {
		g.changeMenuValue(-1)
	}
	if justPressed(ebiten.KeyArrowRight) {
		g.changeMenuValue(1)
	}
	numberKeys := []ebiten.Key{ebiten.Key1, ebiten.Key2, ebiten.Key3, ebiten.Key4, ebiten.Key5}
	for index, key := range numberKeys {
		if justPressed(key) {
			g.menuIndex = index
		}
	}
	if !justPressed(ebiten.KeyEnter) {
		return
	}
	switch g.menuIndex {
	case 3:
		g.startGame()
	case 4:
		g.startMultiplayer()
	}
}

func (g *Game) changeMenuValue(delta int) {
	switch g.menuIndex {
	case 0:
		mapCount := len(maps(boardSize))
		g.mapIndex = (g.mapIndex + delta + mapCount) % mapCount
	case 1:
		g.difficultyIndex = (g.difficultyIndex + delta + 3) % 3
	case 2:
		if g.collisionMode == CollisionGameOver {
			g.collisionMode = CollisionTailCut
		} else {
			g.collisionMode = CollisionGameOver
		}
	case 4:
		g.multiplayerMode = (g.multiplayerMode + delta + multiplayerItems) % multiplayerItems
	}
}

func (g *Game) startGame() {
	g.closeSession()
	g.networkStatus = ""
	g.networkDetail = ""
	g.board = NewSoloBoard(boardSize, g.mapIndex, g.difficultyIndex, g.collisionMode)
	g.camera = NewCamera(g.board.WorldPixels(), screenWidth)
	g.camera.SnapTo(g.board.CameraTarget())
	g.screen = screenPlaying
}

func (g *Game) startMultiplayer() {
	g.networkDetail = ""
	switch g.multiplayerMode {
	case multiplayerHost:
		g.startHost()
	case multiplayerJoin:
		g.openRooms()
	default:
		g.networkStatus = "SELECT HOST OR JOIN WITH LEFT/RIGHT"
	}
}

func (g *Game) startHost() {
	g.closeSession()
	g.drainAsync()
	g.networkStatus = ""
	g.networkDetail = ""
	session, err := newHostSession(g.roomName, g.mapIndex, g.difficultyIndex, g.maxPlayers)
	if err != nil {
		g.networkStatus = "HOST FAILED"
		g.networkDetail = err.Error()
		g.screen = screenMenu
		return
	}
	g.session = session
	g.screen = screenLobby
}

func (g *Game) openRooms() {
	g.closeSession()
	g.drainAsync()
	g.rooms = nil
	g.roomIndex = 0
	g.manualMode = false
	g.manualAddress = ""
	g.networkStatus = ""
	g.networkDetail = ""
	g.screen = screenRooms
	g.startScan()
}

func (g *Game) startScan() {
	g.cancelScan()
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout+time.Second)
	g.scanCancel = cancel
	g.scanID++
	scanID := g.scanID
	g.scanning = true
	go func() {
		rooms, err := network.Discover(ctx, scanTimeout)
		select {
		case g.asyncCh <- asyncResult{kind: asyncRooms, scanID: scanID, rooms: rooms, err: err}:
		default:
		}
	}()
}

func (g *Game) cancelScan() {
	if g.scanCancel != nil {
		g.scanCancel()
		g.scanCancel = nil
	}
	g.scanID++
	g.scanning = false
}

func (g *Game) updateRooms() {
	g.collectAsync()
	if g.screen != screenRooms {
		return
	}

	if justPressed(ebiten.KeyEscape) {
		if g.manualMode {
			g.manualMode = false
			g.manualAddress = ""
			return
		}
		g.cancelScan()
		g.screen = screenMenu
		return
	}

	if g.manualMode {
		g.updateManualAddress()
		return
	}

	if justPressed(ebiten.KeyR) {
		g.startScan()
	}
	if justPressed(ebiten.KeyI) {
		g.manualMode = true
		g.manualAddress = ""
		return
	}

	count := len(g.rooms)
	if count == 0 {
		return
	}
	if justPressed(ebiten.KeyArrowDown) {
		g.roomIndex = (g.roomIndex + 1) % count
	}
	if justPressed(ebiten.KeyArrowUp) {
		g.roomIndex = (g.roomIndex + count - 1) % count
	}
	if justPressed(ebiten.KeyEnter) {
		room := g.rooms[g.roomIndex]
		address := net.JoinHostPort(room.Address, strconv.Itoa(room.TCPPort))
		if room.Address == "" {
			address = net.JoinHostPort("127.0.0.1", strconv.Itoa(room.TCPPort))
		}
		g.startJoin(address)
	}
}

func (g *Game) updateManualAddress() {
	for _, char := range ebiten.AppendInputChars(nil) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune(".:-_[]", char) {
			g.manualAddress += string(char)
		}
	}
	if justPressed(ebiten.KeyBackspace) {
		runes := []rune(g.manualAddress)
		if len(runes) > 0 {
			g.manualAddress = string(runes[:len(runes)-1])
		}
	}
	if justPressed(ebiten.KeyEnter) && g.manualAddress != "" {
		g.startJoin(normalizeAddress(g.manualAddress))
	}
}

func (g *Game) startJoin(address string) {
	g.cancelScan()
	g.drainAsync()
	g.closeSession()
	g.screen = screenLobby
	g.networkStatus = "CONNECTING"
	g.networkDetail = address

	ctx, cancel := context.WithTimeout(context.Background(), joinTimeout)
	go func() {
		client, err := network.Join(ctx, address)
		cancel()
		result := asyncResult{kind: asyncJoined, client: client, err: err}
		select {
		case g.asyncCh <- result:
		default:
			if client != nil {
				_ = client.Close()
			}
		}
	}()
}

func (g *Game) updateLobby() {
	g.collectAsync()
	if g.session == nil {
		if justPressed(ebiten.KeyEscape) {
			g.drainAsync()
			g.screen = screenMenu
			g.networkStatus = "JOIN CANCELLED"
			g.networkDetail = ""
		}
		return
	}
	if g.session.isHost() {
		g.updateHostLobby(g.session)
		return
	}
	g.updateGuestLobby(g.session)
}

func (g *Game) updateHostLobby(s *session) {
	g.drainHostEvents(s)
	s.lobby = s.room.LobbySnapshot()
	players := len(s.lobby.Players)
	if s.room.AllReady() {
		s.status = "ALL PLAYERS READY - PRESS ENTER"
	} else if players > 1 {
		s.status = "WAITING FOR PLAYERS TO READY"
	} else {
		s.status = "WAITING FOR PLAYERS"
	}
	if justPressed(ebiten.KeyEscape) {
		g.leaveNetwork("ROOM CLOSED")
		return
	}
	if justPressed(ebiten.KeyEnter) {
		if players > 1 && s.room.AllReady() {
			g.beginRound(nil)
		} else {
			s.status = "WAITING FOR PLAYERS TO READY"
		}
	}
}

func (g *Game) updateGuestLobby(s *session) {
	select {
	case <-s.client.Disconnected():
		g.leaveNetwork("HOST DISCONNECTED")
		return
	default:
	}
	for {
		select {
		case message := <-s.client.Messages():
			switch message.Type {
			case network.MessageLobby:
				if message.Lobby != nil {
					s.lobby = *message.Lobby
					s.waiting = message.Lobby.InRound
				}
			case network.MessageStart:
				g.beginRound(message.Start)
				return
			case network.MessageDisconnect:
				reason := message.Reason
				if reason == "" {
					reason = "HOST CLOSED THE ROOM"
				}
				g.leaveNetwork(reason)
				return
			}
		default:
			goto drained
		}
	}
drained:
	if s.lobby.InRound {
		s.waiting = true
		s.status = "ROUND IN PROGRESS - WAITING FOR NEXT ROUND"
	} else if s.waiting {
		s.waiting = false
		s.status = "CONNECTED - WAITING FOR HOST"
	}
	if justPressed(ebiten.KeyEscape) {
		g.leaveNetwork("LEFT ROOM")
	}
}

func (g *Game) beginRound(start *network.StartInfo) {
	s := g.session
	if s == nil {
		return
	}
	if start != nil {
		s.mapIndex = start.MapIndex
		s.difficulty = start.Difficulty
		s.round = start.Round
	}
	worldSize := boardSize
	if start != nil && start.WorldSize >= 16 {
		worldSize = start.WorldSize
	}

	var active []int
	if s.isHost() {
		g.drainHostEvents(s)
		var ready bool
		active, ready = s.room.StartRoster()
		if !ready {
			return
		}
		s.round++
	} else {
		if start == nil {
			return
		}
		active = append([]int(nil), start.ActivePlayers...)
		if !containsPlayer(active, s.playerID) {
			// This guest connected while the host was already assembling the
			// round. Drop any previous-round board so no stale state remains
			// visible, and wait for the next start message.
			g.board = nil
			g.camera = nil
			if s.client != nil {
				s.client.ClearSnapshots()
			}
			s.waiting = true
			s.status = "ROUND IN PROGRESS - WAITING FOR NEXT ROUND"
			g.screen = screenLobby
			return
		}
	}
	active = filterPlayerIDs(active)
	if len(active) == 0 {
		return
	}

	s.mapIndex = clampMapIndex(s.mapIndex)
	s.difficulty = clampDifficulty(s.difficulty)
	g.mapIndex = s.mapIndex
	g.difficultyIndex = s.difficulty

	s.paused = false
	s.waiting = false
	s.lastSnapshotAt = time.Now()
	s.lastSentAt = time.Time{}
	s.lastDirection = Up
	s.desired = Right
	s.lastSnapshotSeq = 0

	if s.isGuest() && s.client != nil {
		s.client.ClearSnapshots()
	}
	g.board = NewMultiplayerBoard(worldSize, s.mapIndex, s.difficulty, s.playerID, active)
	g.camera = NewCamera(g.board.WorldPixels(), screenWidth)
	g.camera.SnapTo(g.board.CameraTarget())
	if s.isHost() {
		// Drop inputs queued during the previous round.
		for {
			select {
			case <-s.room.Inputs():
			default:
				goto inputsDrained
			}
		}
	inputsDrained:
		s.room.SetInRound(true)
		_ = s.room.SendStart(s.mapIndex, s.difficulty, s.round, worldSize, active)
		g.broadcastSnapshot()
	}
	g.screen = screenPlaying
}

func (g *Game) updatePlaying() {
	if g.session != nil {
		if g.session.isHost() {
			g.updateHostRound(g.session)
		} else {
			g.updateGuestRound(g.session)
		}
		return
	}
	g.updateSoloRound()
}

func (g *Game) updateSoloRound() {
	if justPressed(ebiten.KeyP) || justPressed(ebiten.KeyEscape) {
		g.screen = screenPaused
		return
	}
	if direction, ok := readDirection(); ok {
		g.board.QueueDirection(g.board.LocalID(), direction)
	}
	g.board.Update()
	if g.board.IsGameOver() {
		if g.board.LocalScore() > g.highScore {
			g.highScore = g.board.LocalScore()
		}
		g.screen = screenGameOver
	}
}

func (g *Game) updateHostRound(s *session) {
	if justPressed(ebiten.KeyEscape) {
		g.leaveNetwork("ROOM CLOSED")
		return
	}
	if justPressed(ebiten.KeyP) {
		s.paused = !s.paused
		_ = s.room.SendPause(s.paused)
		g.broadcastSnapshot()
	}
	g.drainHostEvents(s)
	s.lobby = s.room.LobbySnapshot()
	if g.screen == screenGameOver {
		return
	}

	if g.board == nil {
		g.screen = screenLobby
		return
	}

	g.drainHostInputs(s)
	if s.paused {
		if time.Since(s.lastBroadcastAt) >= refreshInterval {
			g.broadcastSnapshot()
		}
		return
	}
	if direction, ok := readDirection(); ok {
		g.board.QueueDirection(s.playerID, direction)
	}
	if g.board.Update() {
		g.broadcastSnapshot()
	}
	if g.board.IsGameOver() {
		s.room.SetInRound(false)
		g.screen = screenGameOver
	}
}

func (g *Game) updateGuestRound(s *session) {
	if justPressed(ebiten.KeyEscape) {
		g.leaveNetwork("LEFT ROOM")
		return
	}
	select {
	case <-s.client.Disconnected():
		g.leaveNetwork("HOST DISCONNECTED")
		return
	default:
	}

	if g.drainGuestSnapshots(s) {
		g.screen = screenGameOver
		return
	}
	if start, reason, leave := g.drainGuestMessages(s); leave {
		g.leaveNetwork(reason)
		return
	} else if start != nil {
		g.beginRound(start)
		return
	}
	if time.Since(s.lastSnapshotAt) > snapshotTimeout {
		g.leaveNetwork("CONNECTION LOST")
		return
	}
	if g.board == nil {
		return
	}
	if direction, ok := readDirection(); ok {
		// The host validates borders authoritatively. Sending the intent anyway
		// avoids dropping a valid turn when the guest snapshot is slightly stale.
		s.desired = direction
	}
	if !g.board.LocalAlive() {
		return
	}
	desired := s.desired
	now := time.Now()
	if desired != s.lastDirection || now.Sub(s.lastSentAt) >= inputHeartbeat {
		if err := s.client.SendInput(int(desired)); err != nil {
			g.leaveNetwork("SEND FAILED")
			return
		}
		s.lastDirection = desired
		s.lastSentAt = now
	}
}

func (g *Game) drainHostEvents(s *session) {
	for {
		select {
		case event := <-s.room.Events():
			if event.Type == network.EventLeft && g.board != nil {
				g.board.Eliminate(event.Player)
				if g.board.IsGameOver() {
					s.room.SetInRound(false)
					g.screen = screenGameOver
				}
			}
		default:
			return
		}
	}
}

func (g *Game) drainHostInputs(s *session) {
	for {
		select {
		case event := <-s.room.Inputs():
			g.board.QueueDirection(event.Player, Direction(event.Input.Direction))
		default:
			return
		}
	}
}

func (g *Game) drainGuestSnapshots(s *session) bool {
	for {
		select {
		case snapshot := <-s.client.Snapshots():
			if snapshot.Round != s.round || snapshot.Seq <= s.lastSnapshotSeq || g.board == nil {
				continue
			}
			if !g.board.ApplySnapshot(snapshot) {
				continue
			}
			s.lastSnapshotSeq = snapshot.Seq
			s.lastSnapshotAt = time.Now()
			s.paused = snapshot.Paused
			if snapshot.GameOver {
				return true
			}
		default:
			return false
		}
	}
}

func (g *Game) drainGuestMessages(s *session) (*network.StartInfo, string, bool) {
	for {
		select {
		case message := <-s.client.Messages():
			switch message.Type {
			case network.MessageLobby:
				if message.Lobby != nil {
					s.lobby = *message.Lobby
				}
			case network.MessageStart:
				return message.Start, "", false
			case network.MessagePause:
				s.paused = message.Paused
			case network.MessageDisconnect:
				reason := message.Reason
				if reason == "" {
					reason = "HOST CLOSED THE ROOM"
				}
				return nil, reason, true
			}
		default:
			return nil, "", false
		}
	}
}

func (g *Game) updatePaused() {
	if justPressed(ebiten.KeyP) || justPressed(ebiten.KeyEscape) {
		g.screen = screenPlaying
	}
}

func (g *Game) updateGameOver() {
	if g.session == nil {
		if justPressed(ebiten.KeyEnter) {
			g.startGame()
		}
		if justPressed(ebiten.KeyEscape) {
			g.screen = screenMenu
		}
		return
	}

	s := g.session
	if s.isHost() {
		g.drainHostEvents(s)
		s.lobby = s.room.LobbySnapshot()
		if time.Since(s.lastBroadcastAt) >= refreshInterval {
			g.broadcastSnapshot()
		}
		if justPressed(ebiten.KeyEnter) && s.room.HasGuest() && s.room.AllReady() {
			g.beginRound(nil)
			return
		}
		if justPressed(ebiten.KeyEscape) {
			g.leaveNetwork("ROOM CLOSED")
		}
		return
	}

	select {
	case <-s.client.Disconnected():
		g.leaveNetwork("HOST DISCONNECTED")
		return
	default:
	}
	for {
		select {
		case message := <-s.client.Messages():
			switch message.Type {
			case network.MessageLobby:
				if message.Lobby != nil {
					s.lobby = *message.Lobby
				}
			case network.MessageStart:
				g.beginRound(message.Start)
				return
			case network.MessageDisconnect:
				reason := message.Reason
				if reason == "" {
					reason = "HOST CLOSED THE ROOM"
				}
				g.leaveNetwork(reason)
				return
			}
		default:
			if justPressed(ebiten.KeyEscape) {
				g.leaveNetwork("LEFT ROOM")
			}
			return
		}
	}
}

func (g *Game) broadcastSnapshot() {
	s := g.session
	if s == nil || s.room == nil || g.board == nil {
		return
	}
	s.snapshotSeq++
	s.lastBroadcastAt = time.Now()
	if err := s.room.BroadcastSnapshot(g.board.BuildSnapshot(s.snapshotSeq, s.round, s.paused)); err != nil {
		s.detail = "SEND ERROR: " + err.Error()
	}
}

func (g *Game) collectAsync() {
	for {
		select {
		case result := <-g.asyncCh:
			switch result.kind {
			case asyncRooms:
				if result.scanID != g.scanID {
					continue
				}
				g.scanning = false
				g.rooms = result.rooms
				if len(g.rooms) == 0 {
					g.roomIndex = 0
				} else if g.roomIndex >= len(g.rooms) {
					g.roomIndex = 0
				}
				if result.err != nil && !errors.Is(result.err, context.Canceled) && !errors.Is(result.err, context.DeadlineExceeded) {
					g.networkDetail = result.err.Error()
				}
			case asyncJoined:
				g.adoptClient(result.client, result.err)
			}
		default:
			return
		}
	}
}

func (g *Game) adoptClient(client *network.Client, err error) {
	if g.screen != screenLobby {
		if client != nil {
			_ = client.Close()
		}
		return
	}
	if err != nil {
		g.screen = screenMenu
		g.networkStatus = "JOIN FAILED"
		g.networkDetail = err.Error()
		return
	}
	if client == nil {
		g.screen = screenMenu
		g.networkStatus = "JOIN FAILED"
		g.networkDetail = "empty client"
		return
	}
	g.session = newGuestSession(client)
	g.board = nil
	g.screen = screenLobby
	g.networkStatus = ""
	g.networkDetail = ""
	if err := client.SendReady(); err != nil {
		g.leaveNetwork("READY FAILED")
	}
}

func (g *Game) drainAsync() {
	for {
		select {
		case result := <-g.asyncCh:
			if result.client != nil {
				_ = result.client.Close()
			}
		default:
			return
		}
	}
}

func (g *Game) closeSession() {
	g.cancelScan()
	if g.session != nil {
		g.session.close()
		g.session = nil
	}
	g.board = nil
}

func (g *Game) leaveNetwork(status string) {
	g.closeSession()
	g.screen = screenMenu
	g.networkStatus = status
	g.networkDetail = ""
}

func readDirection() (Direction, bool) {
	switch {
	case justPressed(ebiten.KeyArrowUp), justPressed(ebiten.KeyW):
		return Up, true
	case justPressed(ebiten.KeyArrowDown), justPressed(ebiten.KeyS):
		return Down, true
	case justPressed(ebiten.KeyArrowLeft), justPressed(ebiten.KeyA):
		return Left, true
	case justPressed(ebiten.KeyArrowRight), justPressed(ebiten.KeyD):
		return Right, true
	}
	return Right, false
}

func normalizeAddress(value string) string {
	value = strings.TrimSpace(value)
	if host, port, err := net.SplitHostPort(value); err == nil && host != "" && port != "" {
		return value
	}
	// A bare bracketed IPv6 address is accepted too; JoinHostPort adds the
	// brackets back together with the default port.
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	return net.JoinHostPort(value, strconv.Itoa(defaultTCPPort))
}

func justPressed(key ebiten.Key) bool {
	return inpututil.IsKeyJustPressed(key)
}

func clampMapIndex(index int) int {
	count := len(maps(boardSize))
	if index < 0 || index >= count {
		return 0
	}
	return index
}

func clampDifficulty(index int) int {
	if index < 0 {
		return 0
	}
	if index > 2 {
		return 2
	}
	return index
}

func containsPlayer(ids []int, id int) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func filterPlayerIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id < 0 || id >= network.MaxPlayers {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Ints(result)
	return result
}
