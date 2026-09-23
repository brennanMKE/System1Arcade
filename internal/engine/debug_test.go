package engine

import (
	"fmt"
	"testing"
)

func TestDebugInvaderDeaths(t *testing.T) {
	for seed := int64(1); seed <= 4; seed++ {
		e := New()
		e.Load("invaders", seed)
		e.SetMode(Lockstep)
		var hist []string
		was := false
		var st State
		for i := 0; i < 20000; i++ {
			ans, _ := e.Oracle()
			pre := e.State().Observation
			d, s2, _ := e.Decide(ans, nil)
			st = s2
			o := st.Observation
			hist = append(hist, fmt.Sprintf("%s | x=%v threats=%v", d.Note, o["cannon_x"], pre["threats"]))
			boom := o["exploding"].(bool)
			if boom && !was {
				t.Logf("seed %d DIED tick %d left %v formation %v", seed, st.Tick, o["invaders_left"], o["formation"])
				for _, h := range hist[max(0, len(hist)-4):] {
					t.Log("   ", h)
				}
			}
			was = boom
			if st.Status.Over {
				break
			}
		}
		t.Logf("seed %d END score %d wave %v left %v formation %v", seed, st.Status.Score, st.Observation["wave"], st.Observation["invaders_left"], st.Observation["formation"])
	}
}
