package game

import "testing"

func TestEnemyCollision(t *testing.T) {
	if !EnemyCollision(Position{X: 3, Y: 2}, []Position{{X: 3, Y: 2}}) {
		t.Fatal("expected enemy collision")
	}
	if EnemyCollision(Position{X: 3, Y: 2}, []Position{{X: 4, Y: 2}}) {
		t.Fatal("unexpected enemy collision")
	}
}

func TestNormalizeAddress(t *testing.T) {
	cases := map[string]string{
		"192.168.1.5":      "192.168.1.5:4040",
		"192.168.1.5:5000": "192.168.1.5:5000",
		"::1":              "[::1]:4040",
		"[::1]":            "[::1]:4040",
		"[::1]:4041":       "[::1]:4041",
	}
	for input, want := range cases {
		if got := normalizeAddress(input); got != want {
			t.Fatalf("normalizeAddress(%q) = %q, want %q", input, got, want)
		}
	}
}
