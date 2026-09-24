package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"system1/internal/agent"
	"system1/internal/engine"
	"system1/internal/game"
)

// TestAgentLive runs Settings' "Test connection" (App.TestAgent) and one real
// decision per game against a running custom agent, e.g. laya-go's
// laya-server or agents/laya_server.py:
//
//	SYSTEM1_TEST_AGENT_URL=http://127.0.0.1:8000/predict go test -run TestAgentLive -v .
//
// Skipped when the variable is unset.
func TestAgentLive(t *testing.T) {
	url := os.Getenv("SYSTEM1_TEST_AGENT_URL")
	if url == "" {
		t.Skip("set SYSTEM1_TEST_AGENT_URL to a running agent's predict URL")
	}
	a := &App{ctx: context.Background()}
	for _, s := range []Settings{
		{Agent: "custom", URL: url, Batch: true},
		{Agent: "custom", URL: url, Batch: false},
		// What a hosted endpoint's settings send: a key and a model name,
		// which a local server must accept and ignore.
		{Agent: "custom", URL: url, Batch: true, APIKey: "test-key", Model: "jev-latest"},
	} {
		msg, err := a.TestAgent(s)
		if err != nil {
			t.Fatalf("TestAgent(%+v): %v", s, err)
		}
		if !strings.HasPrefix(msg, `Connected: answered "green" (expected "green") in `) {
			t.Errorf("TestAgent(%+v) = %q", s, msg)
		}
		t.Log(msg)
	}

	// A decision from each game, batched and one request per state: every
	// question asked gets an answer of its type, and the game accepts them.
	for _, id := range []string{"frogger", "tetris", "invaders"} {
		for _, batch := range []bool{true, false} {
			e := engine.New()
			if err := e.Load(id, 1); err != nil {
				t.Fatal(err)
			}
			prompt := e.LayaRequest()
			want := map[string]string{}
			if b, ok := prompt["batch"].(map[string]game.Prompt); ok {
				for key, p := range b {
					for q, spec := range p.Questions {
						typ, _ := spec.(map[string]any)["type"].(string)
						want[key+"."+q] = typ
					}
				}
			}
			c := &agent.Client{URL: url, Batch: batch}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			start := time.Now()
			ans, err := c.Ask(ctx, prompt)
			cancel()
			if err != nil {
				t.Fatalf("%s batch=%v: %v", id, batch, err)
			}
			if len(want) == 0 {
				t.Fatalf("%s: expected a batch prompt, got %T", id, prompt["batch"])
			}
			for name, typ := range want {
				a, ok := ans[name]
				if !ok {
					t.Errorf("%s batch=%v: no answer for %s", id, batch, name)
					continue
				}
				if a.Type != typ {
					t.Errorf("%s batch=%v: %s is a %q answer, asked %q", id, batch, name, a.Type, typ)
				}
			}
			if len(ans) != len(want) {
				t.Errorf("%s batch=%v: %d answers for %d questions", id, batch, len(ans), len(want))
			}
			if _, _, err := e.Decide(ans, nil); err != nil {
				t.Errorf("%s batch=%v: Decide: %v", id, batch, err)
			}
			t.Logf("%s batch=%v: %d answers in %v", id, batch, len(ans), time.Since(start).Round(time.Millisecond))
		}
	}
}
