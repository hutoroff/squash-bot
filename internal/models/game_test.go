package models

import "testing"

func TestGameCapacity(t *testing.T) {
	for _, tc := range []struct {
		name string
		game Game
		want int
	}{
		{"padel", Game{CourtsCount: 2, PlayersPerCourt: 4}, 8},
		{"bowling", Game{CourtsCount: 3, PlayersPerCourt: 6}, 18},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.game.Capacity(); got != tc.want {
				t.Fatalf("Capacity() = %d, want %d", got, tc.want)
			}
		})
	}
}
