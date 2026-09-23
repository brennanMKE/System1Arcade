package tetris

import (
	"strings"
	"testing"

	"system1/internal/game"
)

func tap(action string) game.Input {
	b := map[string]game.Button{"left": game.Left, "right": game.Right, "rotate_cw": game.Up, "rotate_ccw": game.B, "hard_drop": game.A}[action]
	return game.Input{Held: map[game.Button]bool{b: true}, Pressed: map[game.Button]bool{b: true}}
}

// fill sets the bottom h rows of column x.
func fill(b *[rows][cols]int, x, h int) {
	for y := rows - h; y < rows; y++ {
		b[y][x] = 1
	}
}

func TestDeepWells(t *testing.T) {
	var b [rows][cols]int
	for x := 0; x < cols; x++ {
		fill(&b, x, 4)
	}
	if w := boardStats(&b).wells; w != 0 {
		t.Fatalf("flat board has %d deep wells, want 0", w)
	}
	b = [rows][cols]int{}
	for x := 1; x < cols; x++ {
		fill(&b, x, 3) // column 0 is 3 below its neighbor and the wall
	}
	if w := boardStats(&b).wells; w != 1 {
		t.Fatalf("board with an empty edge column has %d deep wells, want 1", w)
	}
}

func TestSentencesNameWellsAndFit(t *testing.T) {
	g := New()
	g.Reset(1)
	g.board = [rows][cols]int{}
	for x := 1; x < cols; x++ {
		fill(&g.board, x, 4)
	}
	g.cur = piece{kind: 0, x: spawnX} // an I piece, with a 4-deep well at the left wall
	spots := g.spots()
	_, text := g.sentences(spots)
	var all []string
	for _, s := range text {
		all = append(all, s)
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{"It clears four lines. It fits snugly. It fills a deep well.", "It fits loosely."} {
		if !strings.Contains(joined, want) {
			t.Errorf("no sentence contains %q:\n%s", want, joined)
		}
	}
}

// With perfect answers the chosen spot is always the best one, even when
// other spots read the same, and its sentence says it fits snugly.
func TestOracleTakesTheBestSpot(t *testing.T) {
	g := New()
	g.Reset(11)
	for n := 0; n < 200 && !g.over; n++ {
		spots := g.spots()
		best := spots[0]
		for _, p := range spots {
			if p.value > best.value {
				best = p
			}
		}
		ans := g.Oracle()
		k := ""
		for i, p := range g.asked.spots {
			if p.rot == best.rot && p.x == best.x {
				k = g.asked.keys[i]
			}
		}
		_, text := g.sentences(g.asked.spots)
		if best.newHoles < 4 && !strings.Contains(text[k], "fits snugly") {
			t.Fatalf("piece %d: the best spot reads %q", n, text[k])
		}
		d := g.Decide(ans)
		want, _, _ := g.plan(best.rot, best.x)
		if strings.Join(d.Actions, " ") != strings.Join(want, " ") {
			t.Fatalf("piece %d: oracle plan %v, want %v for the best spot", n, d.Actions, want)
		}
		for _, a := range d.Actions {
			g.Tick(tap(a))
		}
	}
}
