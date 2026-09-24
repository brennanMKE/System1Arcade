package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"system1/internal/engine"
)

func TestParseAutostart(t *testing.T) {
	for _, tc := range []struct {
		in   string
		game string
		seed int64
		ok   bool
	}{
		{"frogger", "frogger", 0, true},
		{"frogger:7", "frogger", 7, true},
		{" tetris:12 ", "tetris", 12, true},
		{"", "", 0, false},
		{":3", "", 0, false},
		{"invaders:x", "", 0, false},
	} {
		game, seed, err := parseAutostart(tc.in)
		if (err == nil) != tc.ok || game != tc.game || seed != tc.seed {
			t.Errorf("parseAutostart(%q) = %q, %d, %v", tc.in, game, seed, err)
		}
	}
}

// TestAutostartStartsCustomAgent checks that autostart loads the seeded game
// and that the custom agent from Settings then plays it.
func TestAutostartStartsCustomAgent(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]map[string]struct {
			Questions map[string]struct {
				Type     string            `json:"type"`
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		answers := map[string]any{}
		for key, p := range body["batch"] {
			for name, q := range p.Questions {
				if q.Type == "noul" {
					answers[key+"."+name] = map[string]any{"type": "noul", "noul": 0.5}
					continue
				}
				probs := map[string]float64{}
				for o := range q.Criteria {
					probs[o] = 0.5
				}
				answers[key+"."+name] = map[string]any{"type": "choice", "choice": "safe", "probabilities": probs}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer agentSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{ctx: ctx, engine: engine.New(),
		settings: Settings{Agent: "custom", URL: agentSrv.URL, Batch: true}}
	a.engine.SetPaused(true) // as startup leaves it
	go a.engine.Run(ctx)
	defer a.agent.stop()

	if err := a.autostart("nosuchgame:1"); err == nil || !strings.Contains(err.Error(), "unknown game") {
		t.Fatalf("autostart of an unknown game: %v", err)
	}
	if err := a.autostart("frogger:42"); err != nil {
		t.Fatal(err)
	}
	if st := a.engine.State(); st.Game != "frogger" || st.Seed != 42 {
		t.Fatalf("loaded %s seed %d, want frogger seed 42", st.Game, st.Seed)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.AgentStatus().State != "running" || a.engine.State().Paused {
		if time.Now().After(deadline) {
			t.Fatalf("agent did not start playing: %+v, paused=%v", a.AgentStatus(), a.engine.State().Paused)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
