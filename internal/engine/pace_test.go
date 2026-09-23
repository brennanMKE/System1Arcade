package engine

import "testing"

// With perfect answers, each game should still play at every input speed
// offered in Settings, down to 2 inputs a second (30 ticks between presses).
func TestOracleAtInputSpeeds(t *testing.T) {
	floor := map[string]int{"tetris": 1000, "frogger": 1000, "invaders": 300}
	for _, info := range New().Games() {
		for _, gap := range []int{0, HumanPace, 30} {
			e := New()
			e.Load(info.ID, 1)
			e.SetMode(Lockstep)
			e.SetAgentPace(gap)
			var st State
			for i := 0; i < 3000; i++ {
				ans, _ := e.Oracle()
				_, st, _ = e.Decide(ans, nil)
				if st.Status.Over {
					break
				}
			}
			t.Logf("%-8s gap %2d ticks: score %d", info.ID, gap, st.Status.Score)
			if st.Status.Score < floor[info.ID] {
				t.Errorf("%s at a %d-tick gap scored %d, want at least %d", info.ID, gap, st.Status.Score, floor[info.ID])
			}
		}
	}
}
