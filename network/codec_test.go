package network

import (
	"errors"
	"testing"
)

func TestInputCodecRoundTrip(t *testing.T) {
	encoded := EncodeInput(1, Input{Direction: 3, Seq: 42})
	player, input, err := DecodeInput(encoded)
	if err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if player != 1 || input.Direction != 3 || input.Seq != 42 {
		t.Fatalf("decoded %d %+v, want 1 {3 42}", player, input)
	}
}

func TestInputCodecRejectsGarbage(t *testing.T) {
	if _, _, err := DecodeInput([]byte("garbage")); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("expected ErrMalformedPacket, got %v", err)
	}
	if _, _, err := DecodeInput(EncodeSnapshot(Snapshot{})); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("snapshot packet accepted as input: %v", err)
	}
}

func TestSnapshotCodecRoundTrip(t *testing.T) {
	source := Snapshot{
		Seq:        13,
		Round:      4,
		Tick:       99,
		MapIndex:   3,
		Difficulty: 2,
		Paused:     true,
		GameOver:   true,
		Winner:     1,
		Players: []PlayerState{
			{ID: 0, Alive: false, Score: 7, Direction: 1, Segments: []Cell{{X: 1, Y: 2}, {X: 3, Y: 4}}},
			{ID: 1, Alive: true, Score: 11, Direction: 2, Segments: []Cell{{X: 5, Y: 6}, {X: 7, Y: 8}, {X: 9, Y: 10}}},
		},
		Food: []Cell{{X: 20, Y: 21}, {X: 22, Y: 23}, {X: 24, Y: 25}},
	}

	decoded, err := DecodeSnapshot(EncodeSnapshot(source))
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if decoded.Seq != source.Seq || decoded.Round != source.Round || decoded.Tick != source.Tick || decoded.MapIndex != source.MapIndex {
		t.Fatalf("header mismatch: %+v", decoded)
	}
	if decoded.Difficulty != source.Difficulty || !decoded.Paused || !decoded.GameOver || decoded.Winner != source.Winner {
		t.Fatalf("flags mismatch: %+v", decoded)
	}
	if len(decoded.Players) != len(source.Players) {
		t.Fatalf("got %d players, want %d", len(decoded.Players), len(source.Players))
	}
	for index, player := range decoded.Players {
		want := source.Players[index]
		if player.ID != want.ID || player.Alive != want.Alive || player.Score != want.Score || player.Direction != want.Direction {
			t.Fatalf("player %d mismatch: %+v != %+v", index, player, want)
		}
		if len(player.Segments) != len(want.Segments) {
			t.Fatalf("player %d has %d segments, want %d", index, len(player.Segments), len(want.Segments))
		}
		for segment, cell := range player.Segments {
			if cell != want.Segments[segment] {
				t.Fatalf("player %d segment %d is %+v, want %+v", index, segment, cell, want.Segments[segment])
			}
		}
	}
	if len(decoded.Food) != len(source.Food) {
		t.Fatalf("got %d food, want %d", len(decoded.Food), len(source.Food))
	}
	for index := range decoded.Food {
		if decoded.Food[index] != source.Food[index] {
			t.Fatalf("food %d is %+v, want %+v", index, decoded.Food[index], source.Food[index])
		}
	}
}

func TestSnapshotCodecDrawWinner(t *testing.T) {
	decoded, err := DecodeSnapshot(EncodeSnapshot(Snapshot{Winner: -1}))
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if decoded.Winner != -1 {
		t.Fatalf("draw winner decoded as %d, want -1", decoded.Winner)
	}
}

func TestSnapshotCodecSupportsMaxPlayers(t *testing.T) {
	players := make([]PlayerState, 0, MaxPlayers)
	for id := 0; id < MaxPlayers; id++ {
		players = append(players, PlayerState{
			ID:       id,
			Alive:    id%2 == 0,
			Score:    id,
			Segments: []Cell{{X: id, Y: id}},
		})
	}
	decoded, err := DecodeSnapshot(EncodeSnapshot(Snapshot{Players: players, Food: []Cell{{X: 127, Y: 127}}}))
	if err != nil {
		t.Fatalf("decode max-player snapshot: %v", err)
	}
	if len(decoded.Players) != MaxPlayers {
		t.Fatalf("decoded %d players, want %d", len(decoded.Players), MaxPlayers)
	}
	if len(decoded.Food) != 1 || decoded.Food[0] != (Cell{X: 127, Y: 127}) {
		t.Fatalf("decoded food is %+v", decoded.Food)
	}
}

func TestSnapshotCodecRejectsTruncatedPacket(t *testing.T) {
	encoded := EncodeSnapshot(Snapshot{
		Players: []PlayerState{{ID: 0, Segments: []Cell{{X: 1, Y: 2}, {X: 3, Y: 4}}}},
	})
	if _, err := DecodeSnapshot(encoded[:len(encoded)-2]); err == nil {
		t.Fatal("truncated snapshot was accepted")
	}
}

func TestInputCodecAcceptsDedicatedPlayerZero(t *testing.T) {
	encoded := EncodeInput(0, Input{Direction: 2, Seq: 9})
	player, input, err := DecodeInput(encoded)
	if err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if player != 0 || input.Direction != 2 || input.Seq != 9 {
		t.Fatalf("decoded player %d input %+v, want player 0 direction 2 seq 9", player, input)
	}
}
