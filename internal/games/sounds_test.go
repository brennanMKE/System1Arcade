package games_test

import (
	"slices"
	"testing"

	"system1/internal/game"
	"system1/internal/games/frogger"
	"system1/internal/games/invaders"
	"system1/internal/games/tetris"
)

// heard runs ticks and returns every sound the game raised, in order.
func heard(g interface {
	game.Game
	game.Sounder
}, inputs ...game.Input) []string {
	var out []string
	for _, in := range inputs {
		g.Tick(in)
		out = append(out, g.Sounds()...)
	}
	return out
}

func repeat(in game.Input, n int) []game.Input {
	return slices.Repeat([]game.Input{in}, n)
}

func TestTetrisSounds(t *testing.T) {
	g := tetris.New()
	g.Reset(3)
	g.Sounds()
	if s := heard(g, tap(game.Left)); !slices.Equal(s, []string{"move"}) {
		t.Fatalf("tap left: %v, want [move]", s)
	}
	if s := heard(g, tap(game.Up)); !slices.Equal(s, []string{"rotate"}) {
		t.Fatalf("tap up: %v, want [rotate]", s)
	}
	if s := heard(g, idle); len(s) != 0 {
		t.Fatalf("idle tick: %v, want none", s)
	}
	if s := heard(g, tap(game.A)); !slices.Equal(s, []string{"drop"}) {
		t.Fatalf("hard drop: %v, want [drop]", s)
	}
	// Gravity alone brings the next piece down until it locks.
	if s := heard(g, repeat(idle, 20*48)...); !slices.Equal(s, []string{"lock"}) {
		t.Fatalf("letting a piece fall: %v, want [lock]", s)
	}
}

func TestTetrisGameOverSoundsOnce(t *testing.T) {
	g := tetris.New()
	g.Reset(3)
	var all []string
	for i := 0; i < 200 && !g.Status().Over; i++ {
		all = append(all, heard(g, tap(game.A), idle)...)
	}
	all = append(all, heard(g, repeat(idle, 10)...)...)
	n := 0
	for _, s := range all {
		if s == "gameover" {
			n++
		}
	}
	if !g.Status().Over || n != 1 {
		t.Fatalf("over %v, gameover sounded %d times", g.Status().Over, n)
	}
}

func TestFroggerSounds(t *testing.T) {
	g := frogger.New()
	g.Reset(5)
	g.Sounds()
	// Standing still on the start row runs the clock down.
	s := heard(g, repeat(idle, 30*game.TickRate)...)
	if !slices.Equal(s, []string{"hurry", "timeout"}) {
		t.Fatalf("waiting out the timer: %v, want [hurry timeout]", s)
	}
	g.Reset(5)
	g.Sounds()
	if s := heard(g, tap(game.Up)); !slices.Equal(s, []string{"hop"}) {
		t.Fatalf("hop: %v, want [hop]", s)
	}
	var all []string
	for i := 0; i < 60*60*5 && !g.Status().Over; i++ {
		in := idle
		if i%10 == 0 {
			in = tap(game.Up)
		}
		all = append(all, heard(g, in)...)
	}
	for _, want := range []string{"splash", "gameover"} {
		if !slices.Contains(all, want) {
			t.Errorf("hopping blindly never sounded %q: %v", want, all)
		}
	}
}

func TestInvadersSounds(t *testing.T) {
	g := invaders.New()
	g.Reset(9)
	g.Sounds()
	s := heard(g, tap(game.A))
	if !slices.Contains(s, "shoot") {
		t.Fatalf("fire: %v, want shoot", s)
	}
	var marches []string
	var all []string
	for i := 0; i < 60*20; i++ {
		in := idle
		if i%2 == 0 {
			in = tap(game.A)
		}
		for _, x := range heard(g, in) {
			all = append(all, x)
			if len(x) > 5 && x[:5] == "march" {
				marches = append(marches, x)
			}
		}
	}
	if !slices.Contains(all, "invader") {
		t.Error("20s of firing never sounded an invader hit")
	}
	if len(marches) < 8 || !slices.Equal(marches[:4], []string{"march1", "march2", "march3", "march4"}) {
		t.Errorf("march notes %v, want the four notes in turn", marches[:min(8, len(marches))])
	}
}
