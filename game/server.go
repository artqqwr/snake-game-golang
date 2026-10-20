package game

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/artqqwr/snake-game-golang/network"
)

const (
	serverTickRate     = time.Second / 60
	defaultLobbyDelay  = 3 * time.Second
	defaultResultDelay = 5 * time.Second
)

// ServerOptions configures a headless dedicated server. A dedicated server is
// not a player: guests and bots occupy every slot, up to MaxPlayers.
type ServerOptions struct {
	Name       string
	MapIndex   int
	Difficulty int
	MaxPlayers int
	Bots       int
	// Ports: 0 selects defaults, -1 uses an ephemeral port. Discovery -1
	// disables discovery.
	TCPPort       int
	UDPPort       int
	DiscoveryPort int
	LobbyDelay    time.Duration
	ResultDelay   time.Duration
}

// Server runs an authoritative match without a window or a local snake.
type Server struct {
	opts      ServerOptions
	room      *network.Room
	bots      *BotManager
	closeOnce sync.Once

	board       *Board
	round       uint32
	snapshotSeq uint64
	inRound     bool
	lobbySince  time.Time
	resultAt    time.Time
	lastSend    time.Time
}

// NewServer prepares a dedicated server. Start opens the sockets.
func NewServer(opts ServerOptions) *Server {
	if opts.LobbyDelay <= 0 {
		opts.LobbyDelay = defaultLobbyDelay
	}
	if opts.ResultDelay <= 0 {
		opts.ResultDelay = defaultResultDelay
	}
	return &Server{opts: opts}
}

// Start opens the room and launches the configured bot clients. The caller must
// call Close if Start succeeds but Run is not used.
func (s *Server) Start(ctx context.Context) error {
	s.room = network.NewRoom(network.RoomOptions{
		Name:          s.opts.Name,
		MapIndex:      s.opts.MapIndex,
		Difficulty:    s.opts.Difficulty,
		MaxPlayers:    s.opts.MaxPlayers,
		Dedicated:     true,
		TCPPort:       s.opts.TCPPort,
		UDPPort:       s.opts.UDPPort,
		DiscoveryPort: s.opts.DiscoveryPort,
	})
	if err := s.room.Start(ctx); err != nil {
		return err
	}
	if s.opts.Bots > 0 {
		info := s.room.InfoSnapshot()
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(info.TCPPort))
		s.bots = NewBotManager(ctx, address)
		for index := 0; index < s.opts.Bots; index++ {
			s.bots.Add()
		}
		log.Printf("server: starting %d bot(s)", s.opts.Bots)
	}
	return nil
}

// Info returns the current room information.
func (s *Server) Info() network.RoomInfo {
	if s == nil || s.room == nil {
		return network.RoomInfo{}
	}
	return s.room.InfoSnapshot()
}

// Close stops bots and the room.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.bots != nil {
			s.bots.Close()
			s.bots = nil
		}
		if s.room != nil {
			s.room.Close()
		}
	})
}

// Run processes the authoritative simulation until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.room == nil {
		return fmt.Errorf("server is not started")
	}
	defer s.Close()

	ticker := time.NewTicker(serverTickRate)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-s.room.Events():
			s.handleEvent(event)
		case input := <-s.room.Inputs():
			if s.inRound && s.board != nil {
				s.board.QueueDirection(input.Player, Direction(input.Input.Direction))
			}
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

// RunServer is the convenience entry point used by main.
func RunServer(ctx context.Context, opts ServerOptions) error {
	server := NewServer(opts)
	if err := server.Start(ctx); err != nil {
		return err
	}
	info := server.Info()
	log.Printf("server: room %q listening on tcp %d, udp %d, max %d player(s)",
		info.Name, info.TCPPort, info.UDPPort, info.MaxPlayers)
	if info.Address != "" {
		log.Printf("server: join with --join %s", net.JoinHostPort(info.Address, strconv.Itoa(info.TCPPort)))
	}
	return server.Run(ctx)
}

func (s *Server) handleEvent(event network.Event) {
	if !s.inRound && (event.Type == network.EventJoined || event.Type == network.EventReady) {
		// A new participant restarts the settle timer so they are not dropped
		// into a round that starts immediately after their arrival.
		s.lobbySince = time.Time{}
	}
	switch event.Type {
	case network.EventJoined:
		log.Printf("server: player P%d joined", event.Player+1)
	case network.EventLeft:
		log.Printf("server: player P%d left", event.Player+1)
		if s.inRound && s.board != nil {
			s.board.Eliminate(event.Player)
			if s.board.IsGameOver() {
				s.finishRound(time.Now())
			}
		}
	case network.EventReady:
		if event.Ready {
			log.Printf("server: player P%d ready", event.Player+1)
		}
	}
}

func (s *Server) tick(now time.Time) {
	if !s.inRound {
		roster, ok := s.room.StartRoster()
		if !ok || len(roster) < 2 {
			s.lobbySince = time.Time{}
			return
		}
		if s.lobbySince.IsZero() {
			s.lobbySince = now
			log.Printf("server: %d player(s) ready; round starts in %s", len(roster), s.opts.LobbyDelay.Round(time.Millisecond))
			return
		}
		if now.Sub(s.lobbySince) >= s.opts.LobbyDelay {
			s.beginRound(roster)
		}
		return
	}
	if s.board == nil {
		return
	}
	if s.board.IsGameOver() {
		if s.resultAt.IsZero() {
			s.finishRound(now)
		}
		if now.Sub(s.resultAt) >= s.opts.ResultDelay {
			s.endRound()
		} else if now.Sub(s.lastSend) >= 250*time.Millisecond {
			s.broadcastSnapshot(now)
		}
		return
	}
	if s.board.Update() {
		s.broadcastSnapshot(now)
	}
	if s.board.IsGameOver() {
		s.finishRound(now)
	}
}

func (s *Server) beginRound(roster []int) {
	active := filterPlayerIDs(roster)
	if len(active) < 2 {
		return
	}
	s.round++
	s.board = NewMultiplayerBoard(boardSize, clampMapIndex(s.opts.MapIndex), clampDifficulty(s.opts.Difficulty), active[0], active)
	s.inRound = true
	s.lobbySince = time.Time{}
	s.resultAt = time.Time{}
	s.lastSend = time.Time{}

	// Drop inputs that arrived while waiting for the lobby.
	for {
		select {
		case <-s.room.Inputs():
		default:
			goto inputsDrained
		}
	}
inputsDrained:
	s.room.SetInRound(true)
	_ = s.room.SendStart(clampMapIndex(s.opts.MapIndex), clampDifficulty(s.opts.Difficulty), s.round, boardSize, active)
	log.Printf("server: round %d started with %s", s.round, joinPlayerLabels(active))
	s.broadcastSnapshot(time.Now())
}

func (s *Server) finishRound(now time.Time) {
	if s.board == nil || !s.board.IsGameOver() || !s.resultAt.IsZero() {
		return
	}
	s.resultAt = now
	winner := s.board.Winner()
	if winner >= 0 {
		log.Printf("server: round %d over; winner P%d", s.round, winner+1)
	} else {
		log.Printf("server: round %d over; draw", s.round)
	}
	s.broadcastSnapshot(now)
}

func (s *Server) endRound() {
	if s.board == nil {
		return
	}
	s.inRound = false
	s.board = nil
	s.resultAt = time.Time{}
	s.lobbySince = time.Time{}
	s.room.SetInRound(false)
}

func (s *Server) broadcastSnapshot(now time.Time) {
	if s.board == nil {
		return
	}
	s.snapshotSeq++
	_ = s.room.BroadcastSnapshot(s.board.BuildSnapshot(s.snapshotSeq, s.round, false))
	s.lastSend = now
}

func joinPlayerLabels(ids []int) string {
	result := ""
	for index, id := range ids {
		if index > 0 {
			result += ","
		}
		result += "P" + strconv.Itoa(id+1)
	}
	return result
}
