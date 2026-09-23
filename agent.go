package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"system1/internal/agent"
	"system1/internal/engine"
)

// Settings choose which agent plays when the user asks for one.
type Settings struct {
	// Agent is "builtin" (Laya, run locally by the app) or "custom".
	Agent string `json:"agent"`
	// URL of a custom agent's decision endpoint.
	URL string `json:"url"`
	// APIKey is sent as a Bearer token to a custom endpoint, if set.
	APIKey string `json:"apiKey"`
	// Batch sends several short states in one request; off sends one request
	// per state, which plain Laya/Jev predict endpoints accept.
	Batch bool `json:"batch"`
}

func defaultSettings() Settings {
	return Settings{Agent: "builtin", URL: "http://127.0.0.1:8000/predict", Batch: true}
}

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "System 1 Arcade", "settings.json")
}

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

// agentManager runs one agent at a time: the built-in Laya server as a child
// process plus the decision loop, or just the loop against a custom URL.
type agentManager struct {
	mu     sync.Mutex
	status AgentStatus
	cancel context.CancelFunc
	server *exec.Cmd
	done   chan struct{} // closed when the server process exits
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
	m.mu.Lock()
	m.cancel = cancel
	m.status = AgentStatus{State: "starting", Kind: s.Agent}
	m.mu.Unlock()
	e.SetPaused(true) // nothing moves until the agent answers

	set := func(state, detail string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if ctx.Err() == nil {
			m.status = AgentStatus{State: state, Detail: detail, Kind: s.Agent}
		}
	}
	go func() {
		client := &agent.Client{URL: s.URL, APIKey: s.APIKey, Batch: s.Batch}
		if s.Agent != "custom" {
			set("starting", "Loading the Laya model…")
			url, err := m.launchServer(ctx)
			if err != nil {
				set("error", err.Error())
				return
			}
			client = &agent.Client{URL: url, Batch: true}
		}
		agent.Run(ctx, e, client, set)
	}()
}

func (m *agentManager) stop() {
	m.mu.Lock()
	cancel, cmd, done := m.cancel, m.server, m.done
	m.cancel, m.server, m.status = nil, nil, AgentStatus{State: "off"}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd == nil {
		return
	}
	if runtime.GOOS == "windows" {
		cmd.Process.Kill()
	} else {
		cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
	}
}

// launchServer starts agents/laya_server.py and returns its predict URL once
// the model has loaded.
func (m *agentManager) launchServer(ctx context.Context) (string, error) {
	root, err := projectRoot()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(python(root), "-u", filepath.Join(root, "agents", "laya_server.py"))
	cmd.Dir = root
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not start %s: %w", cmd.Path, err)
	}
	done := make(chan struct{})
	m.mu.Lock()
	if ctx.Err() != nil { // stopped while starting
		m.mu.Unlock()
		cmd.Process.Kill()
		return "", ctx.Err()
	}
	m.server, m.done = cmd, done
	m.mu.Unlock()

	ready := make(chan string, 1)
	var last string
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if rest, ok := strings.CutPrefix(line, "agent ready "); ok {
				ready <- rest
			} else if line != "" && !strings.Contains(line, "Fetching") && !strings.Contains(line, "return Agent(") {
				last = line
			}
		}
		cmd.Wait()
		close(done)
	}()
	select {
	case url := <-ready:
		return url, nil
	case <-done:
		if last == "" {
			last = "the Laya server exited"
		}
		return "", errors.New(last)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// projectRoot finds the directory holding agents/laya_server.py: the working
// directory under `wails dev`, or an ancestor of the built app bundle.
func projectRoot() (string, error) {
	if dir := os.Getenv("SYSTEM1_ROOT"); dir != "" {
		return dir, nil
	}
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, dir := range starts {
		for d := dir; ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "agents", "laya_server.py")); err == nil {
				return d, nil
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	return "", errors.New("agents/laya_server.py not found; set SYSTEM1_ROOT to the project folder or use a custom agent")
}

// python prefers the project's virtualenv, where laya is installed.
func python(root string) string {
	venv := filepath.Join(root, ".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		venv = filepath.Join(root, ".venv", "Scripts", "python.exe")
	}
	if _, err := os.Stat(venv); err == nil {
		return venv
	}
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}
