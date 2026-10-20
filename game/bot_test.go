package game

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/artqqwr/snake-game-golang/network"
)

func botTestSnapshot(own network.PlayerState, players []network.PlayerState, food []network.Cell) network.Snapshot {
	all := append([]network.PlayerState{own}, players...)
	return network.Snapshot{Seq: 7, Tick: 11, Players: all, Food: food}
}

func TestChooseBotDirectionPrefersFood(t *testing.T) {
	own := network.PlayerState{
		ID:        0,
		Alive:     true,
		Direction: int(Right),
		Segments:  []network.Cell{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}},
	}
	snapshot := botTestSnapshot(own, nil, []network.Cell{{X: 6, Y: 5}})
	if got := chooseBotDirection(snapshot, &snapshot.Players[0], obstacleGrid(16, 0), 16, 0); got != int(Right) {
		t.Fatalf("bot direction is %v, want Right toward food", Direction(got))
	}
}

func TestChooseBotDirectionAvoidsObstacle(t *testing.T) {
	own := network.PlayerState{
		ID:        0,
		Alive:     true,
		Direction: int(Right),
		Segments:  []network.Cell{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}},
	}
	snapshot := botTestSnapshot(own, nil, nil)
	obstacles := obstacleGrid(16, 0)
	obstacles[5*16+6] = true
	if got := chooseBotDirection(snapshot, &snapshot.Players[0], obstacles, 16, 0); got == int(Right) {
		t.Fatal("bot chose the obstacle cell")
	}
}

func TestChooseBotDirectionAvoidsEnemyHead(t *testing.T) {
	own := network.PlayerState{
		ID:        0,
		Alive:     true,
		Direction: int(Right),
		Segments:  []network.Cell{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}},
	}
	enemy := network.PlayerState{
		ID:        1,
		Alive:     true,
		Direction: int(Left),
		Segments:  []network.Cell{{X: 6, Y: 5}, {X: 7, Y: 5}, {X: 8, Y: 5}},
	}
	snapshot := botTestSnapshot(own, []network.PlayerState{enemy}, nil)
	if got := chooseBotDirection(snapshot, &snapshot.Players[0], obstacleGrid(16, 0), 16, 0); got == int(Right) {
		t.Fatal("bot chose an enemy head cell")
	}
}

func TestChooseBotDirectionTurnsAtBorder(t *testing.T) {
	own := network.PlayerState{
		ID:        0,
		Alive:     true,
		Direction: int(Left),
		Segments:  []network.Cell{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 0}},
	}
	snapshot := botTestSnapshot(own, nil, nil)
	if got := chooseBotDirection(snapshot, &snapshot.Players[0], obstacleGrid(16, 0), 16, 0); got != int(Down) {
		t.Fatalf("bot direction at corner is %v, want Down", Direction(got))
	}
}

func TestChooseBotDirectionHandlesEmptyWorld(t *testing.T) {
	own := network.PlayerState{ID: 3}
	if got := chooseBotDirection(network.Snapshot{}, &own, nil, 0, 0); got != int(Right) {
		t.Fatalf("fallback direction is %v, want Right", Direction(got))
	}
}

func TestBotManagerJoinsAndMarksBots(t *testing.T) {
	room := network.NewRoom(network.RoomOptions{
		Name:          "bot manager test",
		TCPPort:       -1,
		UDPPort:       -1,
		DiscoveryPort: -1,
		MaxPlayers:    4,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := room.Start(ctx); err != nil {
		t.Fatalf("start room: %v", err)
	}
	defer room.Close()
	info := room.InfoSnapshot()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(info.TCPPort))
	manager := NewBotManager(ctx, address)
	defer manager.Close()
	if !manager.Add() {
		t.Fatal("could not add first bot")
	}
	if !manager.Add() {
		t.Fatal("could not add second bot")
	}
	deadline := time.After(3 * time.Second)
	joined := 0
	ready := 0
	for joined < 2 || ready < 2 {
		select {
		case event := <-room.Events():
			switch {
			case event.Type == network.EventJoined:
				joined++
			case event.Type == network.EventReady && event.Ready:
				ready++
			}
		case <-deadline:
			t.Fatalf("only %d bots joined and %d became ready", joined, ready)
		}
	}
	lobby := room.LobbySnapshot()
	if len(lobby.Players) != 3 {
		t.Fatalf("lobby has %d players, want host plus 2 bots", len(lobby.Players))
	}
	bots := 0
	for _, player := range lobby.Players {
		if player.Bot {
			bots++
			if !player.Ready {
				t.Fatalf("bot lobby row is not ready: %+v", player)
			}
		}
	}
	if bots != 2 {
		t.Fatalf("lobby has %d bots, want 2", bots)
	}
}
