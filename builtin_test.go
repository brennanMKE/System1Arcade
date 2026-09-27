package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/laya-go/hub"

	"system1/internal/engine"
)

// TestBuiltinAgent starts the built-in agent as the Start button does: it
// loads Laya into this process, plays, answers Test connection from the
// loaded model, and frees the model on stop. Skipped when the model isn't in
// the Hugging Face cache (it never downloads) or with -short.
func TestBuiltinAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the Laya model")
	}
	if _, err := hub.Find(hub.Options{}); err != nil {
		t.Skipf("Laya model not in the Hugging Face cache: %v", err)
	}
	t.Setenv("HF_HUB_OFFLINE", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{ctx: ctx, engine: engine.New(), settings: Settings{Agent: "builtin"}}
	if err := a.engine.Load("invaders", 1); err != nil {
		t.Fatal(err)
	}
	a.engine.SetMode(engine.Realtime)
	go a.engine.Run(ctx)

	msg, err := a.TestAgent(a.settings)
	if err != nil || !strings.Contains(msg, "The model is downloaded") {
		t.Errorf("TestAgent before Start = %q, %v", msg, err)
	}
	a.StartAgent()
	deadline := time.Now().Add(60 * time.Second)
	for a.AgentStatus().State != "running" || a.engine.State().Paused {
		if s := a.AgentStatus(); s.State == "error" || time.Now().After(deadline) {
			t.Fatalf("agent did not start playing: %+v", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
	msg, err = a.TestAgent(a.settings)
	if err != nil || !strings.HasPrefix(msg, `Laya is running in the app: answered "green"`) {
		t.Errorf("TestAgent while running = %q, %v", msg, err)
	}
	t.Log(msg)
	a.StopAgent()
	a.agent.mu.Lock()
	local := a.agent.local
	a.agent.mu.Unlock()
	if local != nil || a.AgentStatus().State != "off" {
		t.Errorf("after stop: model still held (%v) or status %+v", local != nil, a.AgentStatus())
	}
}
