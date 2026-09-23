package engine

import (
	"encoding/json"

	"math/rand"
	"reflect"
	"system1/internal/game"
	"testing"
)

// play runs a seeded random agent in lockstep and returns the final state.
func play(t *testing.T, id string, seed int64, steps int) State {
	t.Helper()
	e := New()
	if err := e.Load(id, seed); err != nil {
		t.Fatal(err)
	}
	if err := e.SetMode(Lockstep); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(seed))
	actions := e.Info().Actions
	var st State
	for i := 0; i < steps; i++ {
		var err error
		st, err = e.Step(actions[rng.Intn(len(actions))].Name, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if st.Status.Over {
			break
		}
	}
	return st
}

func TestGamesAreDeterministicAndSerializable(t *testing.T) {
	for _, info := range New().Games() {
		t.Run(info.ID, func(t *testing.T) {
			a := play(t, info.ID, 42, 3000)
			b := play(t, info.ID, 42, 3000)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("same seed and actions diverged:\n%v\n%v", a.Status, b.Status)
			}
			if a.Tick == 0 {
				t.Fatal("game never advanced")
			}
			if _, err := json.Marshal(a); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLayaRequestShape(t *testing.T) {
	e := New()
	for _, info := range e.Games() {
		e.Load(info.ID, 1)
		req := e.LayaRequest()
		prompts := map[string]game.Prompt{}
		if b, ok := req["batch"].(map[string]game.Prompt); ok {
			prompts = b
		} else {
			prompts[""] = game.Prompt{State: req["state"].(string), Questions: req["questions"].(map[string]any)}
		}
		for key, p := range prompts {
			if p.State == "" {
				t.Fatalf("%s %s: empty state", info.ID, key)
			}
			if len(p.State) > 1200 { // Laya reads ~512 tokens
				t.Errorf("%s %s: state is %d chars; keep it short", info.ID, key, len(p.State))
			}
			for name, q := range p.Questions {
				switch typ := q.(map[string]any)["type"]; typ {
				case "choice", "noul", "score":
				default:
					t.Errorf("%s: question %s has type %v", info.ID, name, typ)
				}
			}
		}
	}
}

// With perfect answers, each game's Decide logic should play well. This is
// the ceiling a model can reach with these descriptions.
func TestOracleCeiling(t *testing.T) {
	floor := map[string]int{"tetris": 2000, "frogger": 300, "invaders": 500}
	for _, info := range New().Games() {
		t.Run(info.ID, func(t *testing.T) {
			e := New()
			e.Load(info.ID, 11)
			e.SetMode(Lockstep)
			var st State
			for i := 0; i < 3000; i++ {
				ans, _ := e.Oracle()
				_, st, _ = e.Decide(ans, nil)
				if st.Status.Over {
					break
				}
			}
			t.Logf("%s oracle: score %d, lives %d, over %v, tick %d", info.ID, st.Status.Score, st.Status.Lives, st.Status.Over, st.Tick)
			if st.Status.Score < floor[info.ID] {
				t.Errorf("oracle scored %d, want at least %d", st.Status.Score, floor[info.ID])
			}
		})
	}
}

func TestStepRequiresLockstep(t *testing.T) {
	e := New()
	if _, err := e.Step("noop", 1, nil); err == nil {
		t.Fatal("step succeeded in realtime mode")
	}
}

func TestAgentTapIsAnEdgeEvenWhileHeld(t *testing.T) {
	e := New()
	e.Load("tetris", 7)
	e.SetMode(Lockstep)
	x0 := e.State().Observation["piece_columns"].([]int)[0]
	e.Step("left", 1, nil)
	e.Step("left", 1, nil) // back-to-back taps must both register
	x1 := e.State().Observation["piece_columns"].([]int)[0]
	if x1 != x0-2 {
		t.Fatalf("piece moved from %d to %d, want %d", x0, x1, x0-2)
	}
}
