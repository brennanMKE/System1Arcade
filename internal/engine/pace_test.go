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

// An agent whose answers come from a cache decides many times per tick in
// realtime. A press it has made must still reach the game rather than be
// replaced by the next decision before a tick sees it.
func TestFastAgentInRealtime(t *testing.T) {
	floor := map[string]int{"tetris": 1000, "frogger": 1000, "invaders": 300}
	for _, info := range New().Games() {
		e := New()
		e.Load(info.ID, 1)
		e.SetAgentPace(HumanPace)
		for tick := 0; tick < 20000 && !e.State().Status.Over; tick++ {
			for i := 0; i < 5; i++ {
				ans, _ := e.Oracle()
				e.Decide(ans, nil)
			}
			e.mu.Lock()
			e.tickLocked()
			e.mu.Unlock()
		}
		st := e.State()
		t.Logf("%-8s score %d at tick %d", info.ID, st.Status.Score, st.Tick)
		if st.Status.Score < floor[info.ID] {
			t.Errorf("%s scored %d deciding 5 times a tick, want at least %d", info.ID, st.Status.Score, floor[info.ID])
		}
	}
}
