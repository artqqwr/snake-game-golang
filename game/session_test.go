package game

import (
	"testing"

	"github.com/artqqwr/snake-game-golang/network"
)

func TestGuestBeginRoundCreatesLocalBoard(t *testing.T) {
	game := New()
	game.session = &session{role: roleGuest, playerID: 1}
	start := &network.StartInfo{
		MapIndex:      2,
		Difficulty:    1,
		Round:         4,
		WorldSize:     64,
		ActivePlayers: []int{0, 1, 2},
	}

	game.beginRound(start)

	if game.screen != screenPlaying {
		t.Fatalf("screen is %d, want playing", game.screen)
	}
	if game.board == nil || game.camera == nil {
		t.Fatal("round did not create a board and camera")
	}
	if game.board.Size() != 64 || game.board.LocalID() != 1 {
		t.Fatalf("board is size %d, local id %d", game.board.Size(), game.board.LocalID())
	}
	if game.board.PlayerCount() != 3 {
		t.Fatalf("board has %d players, want 3", game.board.PlayerCount())
	}
	if game.session.round != 4 {
		t.Fatalf("session round is %d, want 4", game.session.round)
	}
}

func TestGuestStartWithoutLocalPlayerWaitsForNextRound(t *testing.T) {
	game := New()
	game.session = &session{role: roleGuest, playerID: 1, round: 1}
	game.board = NewMultiplayerBoard(32, 0, 0, 1, []int{0, 1})
	game.camera = NewCamera(game.board.WorldPixels(), screenWidth)
	game.screen = screenPlaying

	game.beginRound(&network.StartInfo{
		Round:         2,
		WorldSize:     64,
		ActivePlayers: []int{0, 2},
	})

	if game.board != nil || game.camera != nil {
		t.Fatal("waiting guest kept a board from the previous round")
	}
	if game.screen != screenLobby {
		t.Fatalf("screen is %d, want lobby", game.screen)
	}
	if !game.session.waiting || game.session.round != 2 {
		t.Fatalf("session is waiting=%v round=%d", game.session.waiting, game.session.round)
	}
}

func TestDedicatedGuestCanUsePlayerZero(t *testing.T) {
	game := New()
	game.session = &session{role: roleGuest, playerID: 0}
	game.beginRound(&network.StartInfo{
		MapIndex:      0,
		Difficulty:    1,
		Round:         1,
		WorldSize:     64,
		ActivePlayers: []int{0, 1},
	})
	if game.screen != screenPlaying {
		t.Fatalf("screen is %d, want playing", game.screen)
	}
	if game.board == nil || game.board.LocalID() != 0 {
		t.Fatalf("board local id is %v, want 0", game.board)
	}
	if game.session.isHost() {
		t.Fatal("guest with slot zero was treated as host")
	}
}
