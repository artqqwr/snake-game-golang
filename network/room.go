package network

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"sync"
	"time"
)

const (
	DefaultTCPPort = 4040
	DefaultUDPPort = 4041
	DiscoveryPort  = 4042

	// MaxPlayers is the protocol-wide maximum for one round. A normal room
	// includes the host; a dedicated room uses every slot for guests.
	MaxPlayers   = 8
	maxPortTries = 10
)

// RoomOptions configures a LAN room.
type RoomOptions struct {
	Name       string
	MapIndex   int
	Difficulty int
	// MaxPlayers includes the host in a normal room. In a dedicated room the
	// server does not play, so MaxPlayers is the guest limit. Zero selects the
	// protocol maximum.
	MaxPlayers int
	// Dedicated hosts a match without a local host player. Guest IDs start at
	// zero and the server is not included in the roster.
	Dedicated bool
	// TCPPort/UDPPort: 0 selects the default range, -1 binds an ephemeral port.
	TCPPort int
	UDPPort int
	// DiscoveryPort: 0 selects DiscoveryPort, -1 disables discovery.
	DiscoveryPort int
}

// EventType describes a change in room membership.
type EventType string

const (
	EventJoined EventType = "joined"
	EventLeft   EventType = "left"
	EventReady  EventType = "ready"
)

// Event is emitted by the room when a guest joins, leaves or changes readiness.
type Event struct {
	Type   EventType
	Player int
	Ready  bool
}

// InputEvent is a validated direction intent from a guest.
type InputEvent struct {
	Player int
	Input  Input
}

type clientState struct {
	conn    net.Conn
	encoder *json.Encoder
	writeMu sync.Mutex
	udpAddr *net.UDPAddr
	ready   bool
	bot     bool
	lastSeq uint64
}

// Room is the authoritative network endpoint hosted by one player.
type Room struct {
	mu         sync.Mutex
	info       RoomInfo
	opts       RoomOptions
	maxPlayers int
	tcp        net.Listener
	udp        *net.UDPConn
	disc       *net.UDPConn
	users      map[int]*clientState

	closeOnce sync.Once
	closed    chan struct{}
	events    chan Event
	inputs    chan InputEvent
}

// NewRoom prepares a room. Sockets are opened by Start.
func NewRoom(opts RoomOptions) *Room {
	if opts.Name == "" {
		opts.Name = "Snake LAN Room"
	}
	maxPlayers := opts.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = MaxPlayers
	}
	if maxPlayers < 2 {
		maxPlayers = 2
	}
	if maxPlayers > MaxPlayers {
		maxPlayers = MaxPlayers
	}
	return &Room{
		opts:       opts,
		maxPlayers: maxPlayers,
		info: RoomInfo{
			Name:       opts.Name,
			MaxPlayers: maxPlayers,
			MapIndex:   opts.MapIndex,
			Difficulty: opts.Difficulty,
		},
		users:  make(map[int]*clientState),
		closed: make(chan struct{}),
		events: make(chan Event, 32),
		inputs: make(chan InputEvent, 128),
	}
}

// Start binds the TCP control channel, the UDP game channel and - when
// available - the discovery socket.
func (r *Room) Start(ctx context.Context) error {
	listener, tcpPort, err := listenTCP(r.opts.TCPPort)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	udpConn, udpPort, err := listenUDP(r.opts.UDPPort)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("listen udp: %w", err)
	}
	discovery := listenDiscovery(r.opts.DiscoveryPort)
	address := localAddress()

	r.mu.Lock()
	r.tcp = listener
	r.udp = udpConn
	r.disc = discovery
	r.info.TCPPort = tcpPort
	r.info.UDPPort = udpPort
	r.info.Address = address
	r.info.Players = 1
	r.mu.Unlock()

	if r.isClosed() {
		_ = listener.Close()
		_ = udpConn.Close()
		if discovery != nil {
			_ = discovery.Close()
		}
		return errors.New("room closed during startup")
	}

	go r.acceptLoop(listener)
	go r.inputLoop(udpConn)
	if discovery != nil {
		go r.discoveryLoop(discovery)
	}
	go func() {
		<-ctx.Done()
		r.Close()
	}()
	return nil
}

// Events exposes membership changes.
func (r *Room) Events() <-chan Event { return r.events }

// Inputs exposes validated guest inputs.
func (r *Room) Inputs() <-chan InputEvent { return r.inputs }

// InfoSnapshot returns a consistent copy of the room info.
func (r *Room) InfoSnapshot() RoomInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := r.info
	result.Players = r.playerCountLocked()
	return result
}

// playerCountLocked returns the number of match participants. A normal host
// occupies a slot; a dedicated server does not. The caller must hold r.mu.
func (r *Room) playerCountLocked() int {
	if r.opts.Dedicated {
		return len(r.users)
	}
	return len(r.users) + 1
}

// DiscoveryAvailable reports whether the room was able to bind the discovery port.
func (r *Room) DiscoveryAvailable() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disc != nil
}

// HasGuest reports whether at least one guest is currently connected.
func (r *Room) HasGuest() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.users) > 0
}

// GuestCount returns the number of connected guests.
func (r *Room) GuestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.users)
}

// MaxPlayers returns the configured participant limit. In a normal room it
// includes the host; in a dedicated room it is the guest limit.
func (r *Room) MaxPlayers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxPlayers
}

// PlayerIDs returns the sorted match roster. It includes the host in a normal
// room and only connected guests in a dedicated room.
func (r *Room) PlayerIDs() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]int, 0, len(r.users)+1)
	if !r.opts.Dedicated {
		ids = append(ids, 0)
	}
	for id := range r.users {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// StartRoster returns the sorted list of players that may start a round. In a
// normal room it includes the host and every connected guest; in a dedicated
// room it contains only guests. The roster is returned only when at least one
// guest is present and all connected guests are ready. The check and snapshot
// happen atomically so a guest cannot slip into the roster while the host is
// already preparing the round.
func (r *Room) StartRoster() ([]int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.users) == 0 {
		return nil, false
	}
	ids := make([]int, 0, len(r.users)+1)
	if !r.opts.Dedicated {
		ids = append(ids, 0)
	}
	for id, client := range r.users {
		if !client.ready {
			return nil, false
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, true
}

// AllReady reports whether there is at least one guest and every guest is ready.
func (r *Room) AllReady() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.users) == 0 {
		return false
	}
	for _, client := range r.users {
		if !client.ready {
			return false
		}
	}
	return true
}

// ReadyCount returns ready participants. In a normal room the host is always
// counted; a dedicated server has no host player.
func (r *Room) ReadyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	if !r.opts.Dedicated {
		count = 1
	}
	for _, client := range r.users {
		if client.ready {
			count++
		}
	}
	return count
}

// LobbySnapshot returns a consistent roster for the lobby UI.
func (r *Room) LobbySnapshot() LobbyInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	lobby := LobbyInfo{
		Players:    make([]LobbyPlayer, 0, len(r.users)+1),
		MaxPlayers: r.maxPlayers,
		InRound:    r.info.InRound,
	}
	if !r.opts.Dedicated {
		lobby.Players = append(lobby.Players, LobbyPlayer{ID: 0, Ready: true, Host: true})
	}
	for id, client := range r.users {
		lobby.Players = append(lobby.Players, LobbyPlayer{ID: id, Ready: client.ready, Bot: client.bot})
	}
	sort.Slice(lobby.Players, func(i, j int) bool { return lobby.Players[i].ID < lobby.Players[j].ID })
	return lobby
}

// SetInRound marks the room as busy; discovery and joining guests use this flag.
func (r *Room) SetInRound(inRound bool) {
	r.mu.Lock()
	r.info.InRound = inRound
	r.mu.Unlock()
	r.BroadcastLobby()
}

// BroadcastLobby sends the current roster to every connected guest.
func (r *Room) BroadcastLobby() {
	lobby := r.LobbySnapshot()
	_ = r.broadcast(Message{Type: MessageLobby, Version: ProtocolVersion, Lobby: &lobby})
}

// SendStart tells every connected player that a round begins.
func (r *Room) SendStart(mapIndex, difficulty int, round uint32, worldSize int, activePlayers []int) error {
	return r.broadcast(Message{
		Type:    MessageStart,
		Version: ProtocolVersion,
		Start: &StartInfo{
			MapIndex:      mapIndex,
			Difficulty:    difficulty,
			Round:         round,
			WorldSize:     worldSize,
			ActivePlayers: append([]int(nil), activePlayers...),
		},
	})
}

// SendPause tells the guest about the host pause state.
func (r *Room) SendPause(paused bool) error {
	return r.broadcast(Message{Type: MessagePause, Version: ProtocolVersion, Paused: paused})
}

// SendDisconnect tells the guest that the room is closing.
func (r *Room) SendDisconnect(reason string) error {
	return r.broadcast(Message{Type: MessageDisconnect, Version: ProtocolVersion, Reason: reason})
}

// BroadcastSnapshot sends the world state to every guest that has registered a
// UDP endpoint. Delivery is unreliable by design.
func (r *Room) BroadcastSnapshot(snapshot Snapshot) error {
	data := EncodeSnapshot(snapshot)

	r.mu.Lock()
	addresses := make([]*net.UDPAddr, 0, len(r.users))
	for _, client := range r.users {
		if client.udpAddr != nil {
			addresses = append(addresses, client.udpAddr)
		}
	}
	udpConn := r.udp
	r.mu.Unlock()

	if udpConn == nil {
		return errors.New("room is not started")
	}
	var firstErr error
	for _, address := range addresses {
		if _, err := udpConn.WriteToUDP(data, address); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *Room) broadcast(message Message) error {
	r.mu.Lock()
	clients := make([]*clientState, 0, len(r.users))
	for _, client := range r.users {
		clients = append(clients, client)
	}
	r.mu.Unlock()

	var firstErr error
	for _, client := range clients {
		if err := r.send(client, message); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *Room) send(client *clientState, message Message) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	_ = client.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return client.encoder.Encode(message)
}

func (r *Room) acceptLoop(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if r.isClosed() {
				return
			}
			log.Printf("network: accept: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		r.mu.Lock()
		limit := r.maxPlayers - 1
		firstID := 1
		if r.opts.Dedicated {
			limit = r.maxPlayers
			firstID = 0
		}
		if len(r.users) >= limit {
			r.mu.Unlock()
			_ = conn.Close()
			continue
		}
		playerID := -1
		for id := firstID; id < r.maxPlayers; id++ {
			if _, exists := r.users[id]; !exists {
				playerID = id
				break
			}
		}
		if playerID < 0 {
			r.mu.Unlock()
			_ = conn.Close()
			continue
		}
		client := &clientState{conn: conn, encoder: json.NewEncoder(conn)}
		r.users[playerID] = client
		r.info.Players = r.playerCountLocked()
		r.mu.Unlock()
		go r.handleClient(playerID, client)
	}
}

func (r *Room) handleClient(playerID int, client *clientState) {
	defer func() {
		_ = client.conn.Close()
		r.mu.Lock()
		if r.users[playerID] == client {
			delete(r.users, playerID)
		}
		r.info.Players = r.playerCountLocked()
		r.mu.Unlock()
		r.emit(Event{Type: EventLeft, Player: playerID})
		r.BroadcastLobby()
	}()

	info := r.InfoSnapshot()
	if err := r.send(client, Message{Type: MessageHello, Version: ProtocolVersion, Player: playerID}); err != nil {
		return
	}
	if err := r.send(client, Message{Type: MessageRoomInfo, Version: ProtocolVersion, Player: playerID, Room: &info}); err != nil {
		return
	}
	r.emit(Event{Type: EventJoined, Player: playerID})
	r.BroadcastLobby()

	// A guest that never reports readiness must not occupy a slot forever.
	_ = client.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	readyReceived := false
	scanner := bufio.NewScanner(client.conn)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		var message Message
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		if message.Version != ProtocolVersion {
			continue
		}
		switch message.Type {
		case MessageReady:
			r.mu.Lock()
			client.ready = message.Ready
			if message.Bot {
				client.bot = true
			}
			r.mu.Unlock()
			if !readyReceived {
				readyReceived = true
				_ = client.conn.SetReadDeadline(time.Time{})
			}
			r.emit(Event{Type: EventReady, Player: playerID, Ready: message.Ready})
			r.BroadcastLobby()
		case MessageDisconnect:
			return
		}
	}
}

func (r *Room) inputLoop(conn *net.UDPConn) {
	buffer := make([]byte, 64*1024)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if r.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("network: udp read: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}

		player, input, decodeErr := DecodeInput(buffer[:n])
		if decodeErr != nil {
			continue
		}

		r.mu.Lock()
		client, exists := r.users[player]
		accepted := exists
		if exists {
			if client.udpAddr == nil || client.udpAddr.String() != addr.String() {
				client.udpAddr = &net.UDPAddr{IP: append(net.IP(nil), addr.IP...), Port: addr.Port, Zone: addr.Zone}
			}
			if input.Seq <= client.lastSeq {
				accepted = false
			} else {
				client.lastSeq = input.Seq
			}
		}
		r.mu.Unlock()

		if !accepted {
			continue
		}
		select {
		case r.inputs <- InputEvent{Player: player, Input: input}:
		default:
		}
	}
}

func (r *Room) discoveryLoop(conn *net.UDPConn) {
	buffer := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if r.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("network: discovery read: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if string(buffer[:n]) != discoveryRequest {
			continue
		}
		info := r.InfoSnapshot()
		data, err := json.Marshal(info)
		if err != nil {
			continue
		}
		_, _ = conn.WriteToUDP(data, addr)
	}
}

// Close releases every socket and stops background goroutines. It is safe to
// call multiple times and from multiple goroutines.
func (r *Room) Close() {
	r.closeOnce.Do(func() {
		close(r.closed)

		r.mu.Lock()
		listener, udpConn, discovery := r.tcp, r.udp, r.disc
		clients := make([]*clientState, 0, len(r.users))
		for _, client := range r.users {
			clients = append(clients, client)
		}
		r.users = make(map[int]*clientState)
		r.info.Players = 0
		r.mu.Unlock()

		if listener != nil {
			_ = listener.Close()
		}
		if udpConn != nil {
			_ = udpConn.Close()
		}
		if discovery != nil {
			_ = discovery.Close()
		}
		for _, client := range clients {
			_ = client.conn.Close()
		}
	})
}

func (r *Room) emit(event Event) {
	select {
	case <-r.closed:
	case r.events <- event:
	default:
	}
}

func (r *Room) isClosed() bool {
	select {
	case <-r.closed:
		return true
	default:
		return false
	}
}

func listenTCP(preferred int) (net.Listener, int, error) {
	if preferred < 0 {
		listener, err := net.Listen("tcp", ":0")
		if err != nil {
			return nil, 0, err
		}
		return listener, listener.Addr().(*net.TCPAddr).Port, nil
	}
	if preferred == 0 {
		preferred = DefaultTCPPort
	}
	var lastErr error
	for attempt := 0; attempt < maxPortTries; attempt++ {
		port := preferred + attempt
		if port > 65535 {
			break
		}
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			return listener, port, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

func listenUDP(preferred int) (*net.UDPConn, int, error) {
	if preferred < 0 {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{})
		if err != nil {
			return nil, 0, err
		}
		return conn, conn.LocalAddr().(*net.UDPAddr).Port, nil
	}
	if preferred == 0 {
		preferred = DefaultUDPPort
	}
	var lastErr error
	for attempt := 0; attempt < maxPortTries; attempt++ {
		port := preferred + attempt
		if port > 65535 {
			break
		}
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
		if err == nil {
			return conn, port, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

func listenDiscovery(preferred int) *net.UDPConn {
	if preferred < 0 {
		return nil
	}
	if preferred == 0 {
		preferred = DiscoveryPort
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: preferred})
	if err != nil {
		log.Printf("network: discovery disabled: %v", err)
		return nil
	}
	return conn
}

// localAddress returns the interface address that is most likely reachable
// from another machine on the same LAN.
func localAddress() string {
	if conn, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer conn.Close()
		if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && udpAddr.IP != nil && !udpAddr.IP.IsUnspecified() {
			return udpAddr.IP.String()
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			if ipv4 := ipNet.IP.To4(); ipv4 != nil {
				return ipv4.String()
			}
		}
	}
	return "127.0.0.1"
}
