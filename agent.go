package main

import (
	"bufio"
	"context"
	"embed"
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
	"system1/internal/game"
)

// Settings choose which agent plays when the user asks for one.
type Settings struct {
	// Agent is "builtin" (Laya, run locally by the app) or "custom".
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
		client := &agent.Client{URL: s.URL, APIKey: s.APIKey, Model: s.Model, Batch: s.Batch}
		if s.Agent != "custom" {
			url, err := m.launchServer(ctx, set)
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

// Embedded so the built-in agent works wherever the app is installed, not
// only from the project folder.
//
//go:embed agents/laya_server.py agents/laya_batch.py
var agentScripts embed.FS

func supportDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "System 1 Arcade")
}

// installScripts writes the embedded agent scripts to dir.
func installScripts(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"laya_server.py", "laya_batch.py"} {
		b, err := agentScripts.ReadFile("agents/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// launchServer starts the built-in Laya server and returns its predict URL
// once the model has loaded. On first use it sets up a Python environment
// with Laya, reporting progress through set.
func (m *agentManager) launchServer(ctx context.Context, set agent.Status) (string, error) {
	dir := filepath.Join(supportDir(), "agent")
	if err := installScripts(dir); err != nil {
		return "", fmt.Errorf("could not install the agent scripts: %w", err)
	}
	py, err := layaPython(ctx, dir, set)
	if err != nil {
		return "", err
	}
	set("starting", "Loading the Laya model…")
	cmd := exec.Command(py, "-u", filepath.Join(dir, "laya_server.py"))
	cmd.Dir = dir
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

// layaPython returns a Python interpreter that can import laya: one named by
// SYSTEM1_PYTHON, the project's .venv, or the app's own environment in dir,
// which it creates and installs Laya into on first use.
func layaPython(ctx context.Context, dir string, set agent.Status) (string, error) {
	if py := os.Getenv("SYSTEM1_PYTHON"); py != "" {
		return py, nil
	}
	var candidates []string
	if root, err := projectRoot(); err == nil {
		candidates = append(candidates, venvPython(filepath.Join(root, ".venv")))
	}
	venv := filepath.Join(dir, "venv")
	candidates = append(candidates, venvPython(venv))
	for _, py := range candidates {
		if hasLaya(ctx, py) {
			return py, nil
		}
	}

	base, err := basePython(ctx)
	if err != nil {
		return "", err
	}
	set("starting", "Setting up the built-in agent (first run only): creating a Python environment…")
	if _, err := os.Stat(venvPython(venv)); err != nil {
		if out, err := exec.CommandContext(ctx, base, "-m", "venv", venv).CombinedOutput(); err != nil {
			return "", fmt.Errorf("could not create a Python environment: %v: %s", err, lastLine(string(out)))
		}
	}
	py := venvPython(venv)
	set("starting", "Setting up the built-in agent (first run only): installing Laya and PyTorch, which takes a few minutes…")
	cmd := exec.CommandContext(ctx, py, "-m", "pip", "install", "--disable-pip-version-check", "laya")
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var last string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			last = line
			switch {
			case strings.HasPrefix(line, "Installing collected packages"):
				set("starting", "Setting up the built-in agent (first run only): installing packages…")
			case strings.HasPrefix(line, "Collecting") || strings.HasPrefix(line, "Downloading"):
				set("starting", "Setting up the built-in agent (first run only): "+line)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("could not install Laya: %s", last)
	}
	return py, nil
}

func venvPython(venv string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", "python.exe")
	}
	return filepath.Join(venv, "bin", "python")
}

func hasLaya(ctx context.Context, py string) bool {
	if _, err := os.Stat(py); err != nil {
		return false
	}
	return exec.CommandContext(ctx, py, "-c", "import importlib.util, sys; sys.exit(importlib.util.find_spec('laya') is None)").Run() == nil
}

// basePython finds Python 3.10 or newer. Apps opened from the Finder don't
// inherit the shell's PATH, so common install locations are searched too.
func basePython(ctx context.Context) (string, error) {
	names := []string{"python3.14", "python3.13", "python3.12", "python3.11", "python3.10", "python3", "python"}
	var dirs []string
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".pyenv", "shims"))
		if vs, _ := filepath.Glob("/Library/Frameworks/Python.framework/Versions/3.*/bin"); vs != nil {
			dirs = append(dirs, vs...)
		}
	case "linux":
		dirs = append(dirs, "/usr/local/bin", "/usr/bin", filepath.Join(home, ".pyenv", "shims"))
	case "windows":
		names = []string{"python.exe", "python3.exe"}
		if vs, _ := filepath.Glob(filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Python", "Python3*")); vs != nil {
			dirs = append(dirs, vs...)
		}
	}
	for _, name := range names {
		for _, d := range dirs {
			py := filepath.Join(d, name)
			if _, err := os.Stat(py); err != nil {
				continue
			}
			if exec.CommandContext(ctx, py, "-c", "import sys; sys.exit(sys.version_info < (3, 10))").Run() == nil {
				return py, nil
			}
		}
	}
	return "", errors.New("the built-in agent needs Python 3.10 or newer (from python.org or Homebrew); install it, or use a custom agent")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// projectRoot finds the project folder (with agents/laya_server.py) when the
// app runs from it: the working directory under `wails dev`, or an ancestor
// of the built app bundle. Its .venv is used when present.
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
	return "", errors.New("project folder not found")
}
