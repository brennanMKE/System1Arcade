package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"system1/internal/agent"
	"system1/internal/api"
	"system1/internal/engine"
	"system1/internal/game"
)

// App is the Wails binding between the window and the engine.
type App struct {
	ctx      context.Context
	engine   *engine.Engine
	server   *http.Server
	apiAddr  string
	apiErr   string
	agent    agentManager
	settings Settings
}

func NewApp() *App {
	return &App{engine: engine.New(), settings: loadSettings()}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.engine.SetPaused(true) // the launch screen decides how play starts
	a.engine.SetAgentPace(paceTicks(a.settings.InputRate))
	go a.engine.Run(ctx)

	updates, _ := a.engine.Subscribe()
	go func() {
		for u := range updates {
			runtime.EventsEmit(ctx, "update", u)
		}
	}()

	addr := os.Getenv("SYSTEM1_ADDR")
	if addr == "" {
		addr = api.DefaultAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		a.apiErr = err.Error()
		log.Printf("agent API disabled: %v", err)
		return
	}
	a.apiAddr = ln.Addr().String()
	a.server = &http.Server{Handler: api.Handler(a.engine)}
	go func() {
		if err := a.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("agent API: %v", err)
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	a.agent.stop()
	if a.server != nil {
		a.server.Shutdown(ctx)
	}
}

// Setup is what the UI needs on load.
type Setup struct {
	Games   []game.Info `json:"games"`
	Current string      `json:"current"`
	APIAddr string      `json:"apiAddr"`
	APIErr  string      `json:"apiErr"`
}

func (a *App) Setup() Setup {
	a.engine.Touch() // draw the paused game behind the launch screen
	return Setup{Games: a.engine.Games(), Current: a.engine.Info().ID, APIAddr: a.apiAddr, APIErr: a.apiErr}
}

func (a *App) Load(id string) error      { return a.engine.Load(id, 0) }
func (a *App) Reset()                    { a.engine.Reset(0) }
func (a *App) TogglePause() bool         { return a.engine.TogglePause() }
func (a *App) SetMode(mode string) error { return a.engine.SetMode(engine.Mode(mode)) }
func (a *App) KeyDown(button string)     { a.engine.HumanDown(game.Button(button)) }
func (a *App) KeyUp(button string)       { a.engine.HumanUp(game.Button(button)) }
func (a *App) ReleaseAll()               { a.engine.HumanReleaseAll() }

// StartAgent starts the agent chosen in Settings on the game on screen. The
// game stays paused until the agent's first answer.
func (a *App) StartAgent() AgentStatus {
	a.agent.start(a.ctx, a.engine, a.settings)
	return a.agent.get()
}

// StopAgent stops the agent; the game carries on under keyboard control.
func (a *App) StopAgent() AgentStatus {
	a.agent.stop()
	return a.agent.get()
}

func (a *App) AgentStatus() AgentStatus { return a.agent.get() }

// Play starts the game for a human player.
func (a *App) Play() { a.engine.SetPaused(false) }

func (a *App) GetSettings() Settings { return a.settings }

func (a *App) SaveSettings(s Settings) error {
	if s.Agent != "custom" {
		s.Agent = "builtin"
	}
	a.settings = s
	a.engine.SetAgentPace(paceTicks(s.InputRate))
	return saveSettings(s)
}

// TestAgent checks that the agent in s answers a simple question.
func (a *App) TestAgent(s Settings) (string, error) {
	if s.Agent != "custom" {
		return "The built-in agent starts when you press Start.", nil
	}
	c := &agent.Client{URL: s.URL, APIKey: s.APIKey, Model: s.Model, Batch: s.Batch}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	msg, d, err := c.Test(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Connected: %s in %d ms.", msg, d.Milliseconds()), nil
}
