package game

import (
	"context"
	"fmt"
	"time"

	"github.com/artqqwr/snake-game-golang/network"
)

type sessionRole int

const (
	roleHost sessionRole = iota + 1
	roleGuest
)

// session owns the network transport and the multiplayer flow state. The board
// itself lives in Game because it is replaced on every round.
type session struct {
	role       sessionRole
	playerID   int
	mapIndex   int
	difficulty int

	room   *network.Room
	client *network.Client
	cancel context.CancelFunc

	status string
	detail string

	lobby   network.LobbyInfo
	waiting bool
	paused  bool

	round           uint32
	snapshotSeq     uint64
	lastSnapshotSeq uint64
	lastSnapshotAt  time.Time
	lastSentAt      time.Time
	lastDirection   Direction
	desired         Direction
	lastBroadcastAt time.Time

	closed bool
}

func newHostSession(name string, mapIndex, difficulty, maxPlayers int) (*session, error) {
	ctx, cancel := context.WithCancel(context.Background())
	room := network.NewRoom(network.RoomOptions{
		Name:       name,
		MapIndex:   mapIndex,
		Difficulty: difficulty,
		MaxPlayers: maxPlayers,
	})
	if err := room.Start(ctx); err != nil {
		cancel()
		return nil, err
	}
	info := room.InfoSnapshot()
	result := &session{
		role:       roleHost,
		playerID:   0,
		mapIndex:   mapIndex,
		difficulty: difficulty,
		room:       room,
		cancel:     cancel,
		status:     "WAITING FOR PLAYERS",
		detail:     fmt.Sprintf("%s  TCP %d  UDP %d", info.Address, info.TCPPort, info.UDPPort),
		lobby:      room.LobbySnapshot(),
	}
	if !room.DiscoveryAvailable() {
		result.detail += "  (DISCOVERY UNAVAILABLE)"
	}
	return result, nil
}

func newGuestSession(client *network.Client) *session {
	result := &session{
		role:       roleGuest,
		playerID:   client.PlayerID,
		mapIndex:   client.Room.MapIndex,
		difficulty: client.Room.Difficulty,
		client:     client,
		status:     "CONNECTED - WAITING FOR HOST",
		detail:     fmt.Sprintf("ROOM %s", client.Room.Name),
		lobby: network.LobbyInfo{
			MaxPlayers: client.Room.MaxPlayers,
			InRound:    client.Room.InRound,
		},
		lastSnapshotAt: time.Now(),
	}
	if client.Room.InRound {
		result.waiting = true
		result.status = "ROUND IN PROGRESS - WAITING FOR NEXT ROUND"
	}
	return result
}

func (s *session) isHost() bool {
	return s != nil && s.role == roleHost
}

func (s *session) isGuest() bool {
	return s != nil && s.role == roleGuest
}

func (s *session) close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.room != nil {
		_ = s.room.SendDisconnect("room closed")
		s.room.Close()
	}
	if s.cancel != nil {
		s.cancel()
	}
}
