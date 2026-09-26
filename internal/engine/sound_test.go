package engine

import (
	"slices"
	"testing"
)

// A game's sounds reach the next Update, once each, and are then cleared.
func TestSoundsReachUpdate(t *testing.T) {
	e := New()
	e.Load("tetris", 3)
	e.SetMode(Lockstep)
	ch, stop := e.Subscribe()
	defer stop()

	e.Step("left", 1, nil)
	if u := <-ch; !slices.Equal(u.Sounds, []string{"move"}) {
		t.Fatalf("after a tap left, sounds = %v, want [move]", u.Sounds)
	}
	e.Step("noop", 1, nil)
	if u := <-ch; len(u.Sounds) != 0 {
		t.Fatalf("after a quiet tick, sounds = %v, want none", u.Sounds)
	}
	e.Step("hard_drop", 1, nil)
	if u := <-ch; !slices.Contains(u.Sounds, "drop") {
		t.Fatalf("after a hard drop, sounds = %v, want drop", u.Sounds)
	}

	// Many ticks in one step deliver each sound once, not a burst.
	e.Load("invaders", 3)
	e.Step("noop", 600, nil)
	u := <-ch
	for _, s := range u.Sounds {
		if n := countOf(u.Sounds, s); n != 1 {
			t.Fatalf("sound %q delivered %d times in one update: %v", s, n, u.Sounds)
		}
	}
	if !slices.Contains(u.Sounds, "march1") || len(u.Sounds) > maxPendingSounds {
		t.Fatalf("600 ticks of invaders gave sounds %v", u.Sounds)
	}

	// A reset drops sounds no update has carried yet.
	e.mu.Lock()
	e.sounds = append(e.sounds, "stale")
	e.resetLocked(3)
	e.mu.Unlock()
	e.Step("noop", 1, nil)
	if u := <-ch; slices.Contains(u.Sounds, "stale") {
		t.Fatalf("sounds survived a reset: %v", u.Sounds)
	}
}

func countOf(xs []string, x string) int {
	n := 0
	for _, y := range xs {
		if y == x {
			n++
		}
	}
	return n
}
