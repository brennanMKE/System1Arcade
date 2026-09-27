package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	laya "github.com/brennanMKE/laya-go"
	"github.com/brennanMKE/laya-go/hub"

	"system1/internal/agent"
	"system1/internal/engine"
	"system1/internal/game"
)

// Settings choose which agent plays when the user asks for one.
type Settings struct {
	// Agent is "builtin" (Laya, run inside the app) or "custom".
	Agent string `json:"agent"`
	// URL of a custom agent's decision endpoint.
	URL string `json:"url"`
	// APIKey is sent as a Bearer token to a custom endpoint, if set.
	APIKey string `json:"apiKey"`
	// Model is sent as "model" to a custom endpoint, if set; TypeSafe's Jev
	// API requires it (e.g. "jev-latest").
	Model string `json:"model"`
	// Batch sends several short states in one request; off sends one request
	// per state, which plain Laya/Jev predict endpoints accept.
	Batch bool `json:"batch"`
	// InputRate caps the agent's new inputs per second, so it plays at a
	// watchable, human-like pace. 0 means full speed.
	InputRate int `json:"inputRate"`
}

// InputRates are the speeds offered in Settings (0 = full speed).
var InputRates = []int{0, 10, 8, 6, 4, 3, 2}

// paceTicks converts inputs per second to the engine's minimum gap in ticks.
func paceTicks(rate int) int {
	if rate <= 0 {
		return 0
	}
	return (game.TickRate + rate/2) / rate
}

func defaultSettings() Settings {
	return Settings{Agent: "builtin", URL: "http://127.0.0.1:8000/predict", Batch: true, InputRate: 6}
}

func settingsPath() string { return filepath.Join(supportDir(), "settings.json") }

func loadSettings() Settings {
	s := defaultSettings()
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func saveSettings(s Settings) error {
	p := settingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

// AgentStatus is what the UI shows.
type AgentStatus struct {
	State  string `json:"state"` // off, starting, running, error
	Detail string `json:"detail"`
	Kind   string `json:"kind"` // builtin or custom
}

// agentManager runs one agent at a time: the built-in agent, Laya loaded
// into this process with laya-go, or a custom agent at a URL. Either way the
// decision loop is agent.Run.
type agentManager struct {
	mu     sync.Mutex
	status AgentStatus
	cancel context.CancelFunc
	done   chan struct{} // closed once the running agent has stopped and freed its model
	local  *agent.Local  // the built-in agent's model while it's loaded
}

func (m *agentManager) get() AgentStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == "" {
		return AgentStatus{State: "off"}
	}
	return m.status
}

func (m *agentManager) start(parent context.Context, e *engine.Engine, s Settings) {
	m.stop()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel, m.done = cancel, done
	m.status = AgentStatus{State: "starting", Kind: s.Agent}
	m.mu.Unlock()
	e.SetPaused(true) // nothing moves until the agent answers

	set := func(state, detail string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if ctx.Err() == nil {
			if state == "error" && m.status.Detail != detail {
				// Also in the log, since the UI only shows the latest status.
				log.Printf("agent error: %s", detail)
			}
			m.status = AgentStatus{State: state, Detail: detail, Kind: s.Agent}
		}
	}
	go func() {
		defer close(done)
		// Keep App Nap away while the agent plays, even with the window
		// hidden: it would slow the agent's decisions but not the game.
		defer beginActivity("An agent is playing")()
		var asker agent.Asker = &agent.Client{URL: s.URL, APIKey: s.APIKey, Model: s.Model, Batch: s.Batch}
		if s.Agent != "custom" {
			local, err := loadLaya(ctx, set)
			if err != nil {
				if ctx.Err() == nil {
					set("error", err.Error())
				}
				return
			}
			defer func() {
				m.mu.Lock()
				if m.local == local {
					m.local = nil
				}
				m.mu.Unlock()
				local.Close() // waits for a forward pass in progress
			}()
			m.mu.Lock()
			m.local = local
			m.mu.Unlock()
			asker = local
		}
		agent.Run(ctx, e, asker, set)
	}()
}

// stop stops the agent and waits until the built-in agent has freed its
// model, so a restart never holds two copies.
func (m *agentManager) stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done, m.status = nil, nil, AgentStatus{State: "off"}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// loadLaya loads the built-in agent's model: from the Hugging Face cache, or
// downloaded into it on first use, reporting progress through set.
// SYSTEM1_LAYA_MODEL, as for agents/laya_server.py, names another checkpoint:
// a local directory or a Hugging Face repo.
func loadLaya(ctx context.Context, set agent.Status) (*agent.Local, error) {
	set("starting", "Loading the Laya model…")
	opts := laya.Options{Progress: func(p hub.Progress) {
		if p.Total > 0 && p.Done >= p.Total {
			set("starting", "Loading the Laya model…")
			return
		}
		set("starting", fmt.Sprintf("Downloading the Laya model (%d of %d MB)…", p.Done>>20, (p.Total+1<<19)>>20))
	}}
	if v := os.Getenv("SYSTEM1_LAYA_MODEL"); v != "" {
		if st, err := os.Stat(v); err == nil && st.IsDir() {
			opts.Dir = v
		} else {
			opts.Repo, opts.Revision = v, "main"
		}
	}
	start := time.Now()
	local, err := agent.LoadLocal(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("could not load the Laya model: %w", err)
	}
	log.Printf("built-in agent: Laya loaded in %v", time.Since(start).Round(time.Millisecond))
	return local, nil
}

// testBuiltin describes the built-in agent for Settings' "Test connection":
// it asks the loaded model the test question when the agent is running, and
// otherwise says whether the model still has to be downloaded.
func (m *agentManager) testBuiltin(ctx context.Context) (string, error) {
	m.mu.Lock()
	local := m.local
	m.mu.Unlock()
	if local != nil {
		start := time.Now()
		msg, err := local.Test(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Laya is running in the app: %s in %d ms.", msg, time.Since(start).Milliseconds()), nil
	}
	if os.Getenv("SYSTEM1_LAYA_MODEL") == "" {
		if _, err := hub.Find(hub.Options{}); err != nil {
			return "The built-in agent runs Laya inside the app. Press Start to download the model (about 800 MB, first time only).", nil
		}
	}
	return "The built-in agent runs Laya inside the app. The model is downloaded; it loads when you press Start.", nil
}

func supportDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "System 1 Arcade")
}
