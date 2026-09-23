package tetris

import (
	"math/rand"
	"testing"

	"system1/internal/game"
)

// playPolicy drops pieces with pick choosing among the offered options.
func playPolicy(seed int64, pick func(opts []placement, r *rand.Rand) int) (int, int) {
	t := New()
	t.Reset(seed)
	r := rand.New(rand.NewSource(seed))
	for n := 0; n < 3000 && !t.over; n++ {
		opts := t.spots()
		if len(opts) == 0 {
			break
		}
		p := opts[pick(opts, r)]
		acts, _, _ := t.plan(p.rot, p.x)
		for _, a := range acts {
			b := map[string]game.Button{"left": game.Left, "right": game.Right, "rotate_cw": game.Up, "rotate_ccw": game.B, "hard_drop": game.A}[a]
			t.Tick(game.Input{Held: map[game.Button]bool{b: true}, Pressed: map[game.Button]bool{b: true}})
		}
	}
	return t.score, t.lines
}

func TestPolicyBaselines(t *testing.T) {
	pols := map[string]func([]placement, *rand.Rand) int{
		"best": func(o []placement, _ *rand.Rand) int {
			b := 0
			for i := range o {
				if o[i].value > o[b].value {
					b = i
				}
			}
			return b
		},
		"random-of-4": func(o []placement, r *rand.Rand) int { return r.Intn(len(o)) },
		"leftmost":    func(o []placement, _ *rand.Rand) int { return 0 },
		"random-of-2": func(o []placement, r *rand.Rand) int {
			// the two best
			b1, b2 := 0, -1
			for i := range o {
				if o[i].value > o[b1].value {
					b1 = i
				}
			}
			for i := range o {
				if i != b1 && (b2 < 0 || o[i].value > o[b2].value) {
					b2 = i
				}
			}
			if b2 < 0 || r.Intn(2) == 0 {
				return b1
			}
			return b2
		},
	}
	for name, p := range pols {
		tot := 0
		for s := int64(1); s <= 5; s++ {
			sc, _ := playPolicy(s, p)
			tot += sc
		}
		t.Logf("%-12s mean score %d", name, tot/5)
	}
}
