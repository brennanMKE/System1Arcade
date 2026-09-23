package games_test

import (
	"testing"

	"system1/internal/game"
	"system1/internal/games/frogger"
	"system1/internal/games/invaders"
	"system1/internal/games/tetris"
)

func tap(b game.Button) game.Input {
	return game.Input{Held: map[game.Button]bool{b: true}, Pressed: map[game.Button]bool{b: true}}
}

var idle = game.Input{}

func TestTetrisHardDropLocksAndScores(t *testing.T) {
	g := tetris.New()
	g.Reset(3)
	g.Tick(tap(game.A))
	obs := g.Observe()
	if obs["max_height"].(int) == 0 {
		t.Fatal("hard drop did not lock a piece")
	}
	if g.Status().Score == 0 {
		t.Fatal("hard drop should score drop distance")
	}
}

func TestTetrisTopsOut(t *testing.T) {
	g := tetris.New()
	g.Reset(3)
	for i := 0; i < 200 && !g.Status().Over; i++ {
		g.Tick(tap(game.A))
		g.Tick(idle)
	}
	if !g.Status().Over {
		t.Fatal("stacking in the middle never ended the game")
	}
}

func TestFroggerHopScoresAndRiverDrowns(t *testing.T) {
	g := frogger.New()
	g.Reset(5)
	g.Tick(tap(game.Up))
	if g.Status().Score != 10 {
		t.Fatalf("score after first hop = %d, want 10", g.Status().Score)
	}
	lives := g.Status().Lives
	for i := 0; i < 60*60 && g.Status().Lives == lives; i++ {
		in := idle
		if i%10 == 0 {
			in = tap(game.Up)
		}
		g.Tick(in)
	}
	if g.Status().Lives >= lives {
		t.Fatal("hopping blindly forward for a minute never died")
	}
}

func TestInvadersShootingScores(t *testing.T) {
	g := invaders.New()
	g.Reset(9)
	for i := 0; i < 60*20 && g.Status().Score == 0; i++ {
		in := idle
		if i%2 == 0 {
			in = tap(game.A)
		}
		g.Tick(in)
	}
	if g.Status().Score == 0 {
		t.Fatal("20s of firing never hit an invader")
	}
}
