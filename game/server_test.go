package game

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/artqqwr/snake-game-golang/network"
)

func startServerTest(t *testing.T, opts ServerOptions) (*Server, context.CancelFunc, <-chan error) {
	t.Helper()
	opts.TCPPort = -1
	opts.UDPPort = -1
	opts.DiscoveryPort = -1
	if opts.LobbyDelay == 0 {
		opts.LobbyDelay = 20 * time.Millisecond
	}
	if opts.ResultDelay == 0 {
		opts.ResultDelay = 50 * time.Millisecond
	}
	server := NewServer(opts)
	ctx, cancel := context.WithCancel(context.Background())
	if err := server.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start server: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		server.Close()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("server run: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	})
	return server, cancel, errCh
}

func joinServerTest(t *testing.T, server *Server) *network.Client {
	t.Helper()
	info := server.Info()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(info.TCPPort))
	client, err := network.Join(context.Background(), address)
	if err != nil {
		t.Fatalf("join server: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func waitServerStart(t *testing.T, client *network.Client) network.StartInfo {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case message := <-client.Messages():
			if message.Type == network.MessageStart && message.Start != nil {
				return *message.Start
			}
		case <-deadline:
			t.Fatal("timed out waiting for server round start")
		}
	}
}

func waitServerSnapshot(t *testing.T, client *network.Client, gameOver bool) network.Snapshot {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case snapshot := <-client.Snapshots():
			if snapshot.GameOver == gameOver {
				return snapshot
			}
		case <-deadline:
			t.Fatalf("timed out waiting for snapshot (game_over=%v)", gameOver)
		}
	}
}

func TestServerAutoStartsDedicatedRoundAndEliminatesDisconnect(t *testing.T) {
	server, _, _ := startServerTest(t, ServerOptions{Name: "test server", MaxPlayers: 4})
	first := joinServerTest(t, server)
	second := joinServerTest(t, server)
	if first.PlayerID != 0 || second.PlayerID != 1 {
		t.Fatalf("dedicated player ids are %d and %d, want 0 and 1", first.PlayerID, second.PlayerID)
	}
	if err := first.SendInput(int(Right)); err != nil {
		t.Fatalf("register first udp: %v", err)
	}
	if err := second.SendInput(int(Right)); err != nil {
		t.Fatalf("register second udp: %v", err)
	}
	if err := first.SendReady(); err != nil {
		t.Fatalf("first ready: %v", err)
	}
	if err := second.SendReady(); err != nil {
		t.Fatalf("second ready: %v", err)
	}
	start := waitServerStart(t, first)
	if len(start.ActivePlayers) != 2 || start.ActivePlayers[0] != 0 || start.ActivePlayers[1] != 1 {
		t.Fatalf("unexpected active roster: %v", start.ActivePlayers)
	}
	if start.WorldSize != boardSize {
		t.Fatalf("world size is %d, want %d", start.WorldSize, boardSize)
	}
	waitServerSnapshot(t, first, false)

	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	over := waitServerSnapshot(t, second, true)
	if over.Winner != 1 {
		t.Fatalf("winner is %d, want P2 (id 1)", over.Winner)
	}
}

func TestServerAutomaticallyRematchesAfterResults(t *testing.T) {
	server, _, _ := startServerTest(t, ServerOptions{Name: "rematch server", MaxPlayers: 4})
	first := joinServerTest(t, server)
	second := joinServerTest(t, server)
	for _, client := range []*network.Client{first, second} {
		if err := client.SendInput(int(Right)); err != nil {
			t.Fatalf("register udp: %v", err)
		}
		if err := client.SendReady(); err != nil {
			t.Fatalf("send ready: %v", err)
		}
	}
	start := waitServerStart(t, second)
	if start.Round != 1 {
		t.Fatalf("first round is %d, want 1", start.Round)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	third := joinServerTest(t, server)
	if err := third.SendInput(int(Right)); err != nil {
		t.Fatalf("register third udp: %v", err)
	}
	if err := third.SendReady(); err != nil {
		t.Fatalf("third ready: %v", err)
	}
	start = waitServerStart(t, second)
	if start.Round != 2 {
		t.Fatalf("rematch round is %d, want 2", start.Round)
	}
	if len(start.ActivePlayers) != 2 || !containsPlayer(start.ActivePlayers, second.PlayerID) || !containsPlayer(start.ActivePlayers, third.PlayerID) {
		t.Fatalf("rematch roster is %v, want ids %d and %d", start.ActivePlayers, second.PlayerID, third.PlayerID)
	}
}

func TestServerStartsWithConfiguredBots(t *testing.T) {
	server, _, _ := startServerTest(t, ServerOptions{
		Name:       "bot server",
		MaxPlayers: 4,
		Bots:       2,
		LobbyDelay: 250 * time.Millisecond,
	})
	observer := joinServerTest(t, server)
	if err := observer.SendInput(int(Right)); err != nil {
		t.Fatalf("register observer udp: %v", err)
	}
	if err := observer.SendReady(); err != nil {
		t.Fatalf("observer ready: %v", err)
	}
	start := waitServerStart(t, observer)
	if len(start.ActivePlayers) != 3 {
		t.Fatalf("active players are %v, want two bots and the observer", start.ActivePlayers)
	}
	waitServerSnapshot(t, observer, false)
}
