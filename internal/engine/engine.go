// Package engine runs one game at a fixed timestep and merges input from a
// human keyboard and an agent into the same virtual controller.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"system1/internal/game"
	"system1/internal/games"
)

// Mode controls who drives the clock.
type Mode string

const (
	// Realtime ticks at game.TickRate whether or not the agent keeps up.
	Realtime Mode = "realtime"
	// Lockstep only advances when the agent calls Step, so decision latency
	// does not cost the agent anything.
	Lockstep Mode = "lockstep"
)

// State is what an agent sees.
type State struct {
	Game        string         `json:"game"`
	Seed        int64          `json:"seed"`
	Tick        uint64         `json:"tick"`
	Mode        Mode           `json:"mode"`
	Paused      bool           `json:"paused"`
	Status      game.Status    `json:"status"`
	Observation map[string]any `json:"observation"`
	Actions     []game.Action  `json:"actions"`
}

// AgentView summarizes the agent's most recent decision for display.
type AgentView struct {
	Action string         `json:"action"`
	Note   string         `json:"note,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
	AgeMs  int64          `json:"age_ms"`
	Rate   float64        `json:"rate"` // decisions per second, recent window
	Total  int            `json:"total"`
}

// Update is pushed to subscribers (the UI, SSE clients) after every tick.
type Update struct {
	Game   string      `json:"game"`
	Tick   uint64      `json:"tick"`
	Mode   Mode        `json:"mode"`
	Paused bool        `json:"paused"`
	Status game.Status `json:"status"`
	Frame  game.Frame  `json:"frame"`
	Agent  *AgentView  `json:"agent,omitempty"`
	// Situation is what the model is told, for games that describe themselves.
	Situation string `json:"situation,omitempty"`
}

type agentEvent struct {
	action string
	note   string
	meta   map[string]any
	at     time.Time
}

type Engine struct {
	mu        sync.Mutex
	factories map[string]games.Factory
	infos     []game.Info
	g         game.Game
	info      game.Info
	seed      int64
	tick      uint64
	mode      Mode
	paused    bool
	dirty     bool

	human      map[game.Button]bool
	agentHold  map[game.Button]int
	agentPress map[game.Button]bool
	prevHeld   map[game.Button]bool
	macro      []string // queued actions from Decide, one per tick

	last      *agentEvent
	total     int
	decisions []time.Time

	subs    map[int]chan Update
	nextSub int
}

// New creates an engine with the built-in games and loads the first one.
func New() *Engine {
	e := &Engine{
		factories:  map[string]games.Factory{},
		mode:       Realtime,
		human:      map[game.Button]bool{},
		agentHold:  map[game.Button]int{},
		agentPress: map[game.Button]bool{},
		prevHeld:   map[game.Button]bool{},
		subs:       map[int]chan Update{},
	}
	for _, f := range games.All() {
		info := f().Info()
		e.factories[info.ID] = f
		e.infos = append(e.infos, info)
	}
	if err := e.Load(e.infos[0].ID, 0); err != nil {
		panic(err)
	}
	return e
}

// Games lists available games.
func (e *Engine) Games() []game.Info { return e.infos }

// Info describes the current game.
func (e *Engine) Info() game.Info {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.info
}

// Load switches to game id. A zero seed picks one from the clock.
func (e *Engine) Load(id string, seed int64) error {
	f, ok := e.factories[id]
	if !ok {
		return fmt.Errorf("unknown game %q", id)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.g = f()
	e.info = e.g.Info()
	e.resetLocked(seed)
	return nil
}

// Reset restarts the current game. A zero seed picks one from the clock.
func (e *Engine) Reset(seed int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetLocked(seed)
}

func (e *Engine) resetLocked(seed int64) {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	e.seed = seed
	e.g.Reset(seed)
	e.tick = 0
	e.paused = false
	clear(e.agentHold)
	clear(e.agentPress)
	clear(e.prevHeld)
	e.macro = nil
	e.dirty = true
}

// Touch makes the engine publish an update even when nothing has changed,
// e.g. for a UI that just started listening.
func (e *Engine) Touch() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dirty = true
}

func (e *Engine) SetPaused(p bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused, e.dirty = p, true
}

func (e *Engine) TogglePause() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused, e.dirty = !e.paused, true
	return e.paused
}

func (e *Engine) SetMode(m Mode) error {
	if m != Realtime && m != Lockstep {
		return fmt.Errorf("mode must be %q or %q", Realtime, Lockstep)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode, e.dirty = m, true
	return nil
}

// HumanDown / HumanUp track physical keys.
func (e *Engine) HumanDown(b game.Button) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if b == game.Start && e.g.Status().Over {
		e.resetLocked(0) // works even in lockstep, where no tick would see it
		return
	}
	e.human[b] = true
}

func (e *Engine) HumanUp(b game.Button) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.human, b)
}

// HumanReleaseAll releases every key, e.g. when the window loses focus.
func (e *Engine) HumanReleaseAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	clear(e.human)
}

// Act presses the buttons behind action for holdTicks ticks (0 = the
// action's default), starting on the next tick. meta is free-form data from
// the agent (probabilities, latency) that the UI displays.
func (e *Engine) Act(action string, holdTicks int, meta map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.macro = nil // an explicit action overrides any plan in progress
	if err := e.actLocked(action, holdTicks, meta); err != nil {
		return err
	}
	e.countDecision(action, "", meta)
	return nil
}

func (e *Engine) countDecision(action, note string, meta map[string]any) {
	now := time.Now()
	e.last = &agentEvent{action: action, note: note, meta: meta, at: now}
	e.total++
	e.decisions = append(e.decisions, now)
	cutoff := now.Add(-2 * time.Second)
	i := 0
	for i < len(e.decisions) && e.decisions[i].Before(cutoff) {
		i++
	}
	e.decisions = e.decisions[i:]
}

// ErrNoAdvisor is returned by Decide for games that do not describe themselves.
var ErrNoAdvisor = errors.New("this game does not support /v1/decide; use /v1/action")

// Decide hands the model's answers to the game, which turns them into a plan
// of actions pressed one per tick. In lockstep mode it also advances the game
// until the plan is done plus the game's requested wait, and returns the new
// state. meta is shown in the UI alongside the answers.
func (e *Engine) Decide(ans game.Answers, meta map[string]any) (game.Decision, State, error) {
	e.mu.Lock()
	adv, ok := e.g.(game.Advisor)
	if !ok {
		e.mu.Unlock()
		return game.Decision{}, State{}, ErrNoAdvisor
	}
	d := adv.Decide(ans)
	if d.Keep && (len(e.macro) > 0 || len(e.agentHold) > 0) {
		e.mu.Unlock()
		return d, State{}, nil
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["answers"] = ans
	e.macro = nil
	clear(e.agentHold) // let go of the previous input, as a player would
	for _, a := range d.Actions {
		if a != "" && a != game.Noop.Name && e.hasAction(a) {
			e.macro = append(e.macro, a)
		}
	}
	e.countDecision(strings.Join(d.Actions, " "), d.Note, meta)
	if e.mode != Lockstep {
		e.pumpMacroLocked() // start on the very next tick
		e.mu.Unlock()
		return d, State{}, nil
	}
	e.pumpMacroLocked()
	for i := 0; i < 600 && (len(e.macro) > 0 || len(e.agentHold) > 0) && !e.g.Status().Over; i++ {
		e.tickLocked()
	}
	for i := 0; i < max(d.Wait, 1) && !e.g.Status().Over; i++ {
		e.tickLocked()
	}
	st := e.stateLocked()
	u := e.updateLocked()
	e.mu.Unlock()
	e.broadcast(u)
	return d, st, nil
}

func (e *Engine) hasAction(name string) bool {
	for _, a := range e.info.Actions {
		if a.Name == name {
			return true
		}
	}
	return false
}

// pumpMacroLocked presses the next queued action once the previous one has
// been released.
func (e *Engine) pumpMacroLocked() {
	if len(e.macro) == 0 || len(e.agentHold) > 0 {
		return
	}
	e.actLocked(e.macro[0], 0, nil)
	e.macro = e.macro[1:]
}

func (e *Engine) actLocked(action string, holdTicks int, meta map[string]any) error {
	var a *game.Action
	for i := range e.info.Actions {
		if e.info.Actions[i].Name == action {
			a = &e.info.Actions[i]
		}
	}
	if a == nil {
		return fmt.Errorf("unknown action %q for %s", action, e.info.ID)
	}
	if holdTicks <= 0 {
		holdTicks = max(a.HoldTicks, 1)
	}
	for _, b := range a.Buttons {
		e.agentHold[b] = max(e.agentHold[b], holdTicks)
		e.agentPress[b] = true
	}
	return nil
}

// Press presses raw buttons as an agent, for controllers that think in
// buttons rather than named actions.
func (e *Engine) Press(buttons []game.Button, holdTicks int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range buttons {
		if !game.ValidButton(b) {
			return fmt.Errorf("unknown button %q", b)
		}
	}
	for _, b := range buttons {
		e.agentHold[b] = max(e.agentHold[b], max(holdTicks, 1))
		e.agentPress[b] = true
	}
	return nil
}

// Step applies action (may be empty) and advances ticks ticks (0 = the
// action's hold time). Only valid in lockstep mode.
func (e *Engine) Step(action string, ticks int, meta map[string]any) (State, error) {
	e.mu.Lock()
	if e.mode != Lockstep {
		e.mu.Unlock()
		return State{}, fmt.Errorf("step requires lockstep mode")
	}
	if action != "" {
		if err := e.actLocked(action, ticks, meta); err != nil {
			e.mu.Unlock()
			return State{}, err
		}
		e.countDecision(action, "", meta)
	}
	if ticks <= 0 {
		ticks = 1
		for _, h := range e.agentHold {
			ticks = max(ticks, h)
		}
	}
	for i := 0; i < ticks && !e.g.Status().Over; i++ {
		e.tickLocked()
	}
	st := e.stateLocked()
	u := e.updateLocked()
	e.mu.Unlock()
	e.broadcast(u)
	return st, nil
}

func (e *Engine) tickLocked() {
	held := map[game.Button]bool{}
	for b := range e.human {
		held[b] = true
	}
	for b, n := range e.agentHold {
		if n > 0 {
			held[b] = true
		}
	}
	pressed := map[game.Button]bool{}
	for b := range held {
		if !e.prevHeld[b] || e.agentPress[b] {
			pressed[b] = true
		}
	}
	if e.g.Status().Over && pressed[game.Start] {
		e.resetLocked(0)
		return
	}
	e.g.Tick(game.Input{Held: held, Pressed: pressed})
	e.tick++
	e.prevHeld = held
	for b, n := range e.agentHold {
		if n <= 1 {
			delete(e.agentHold, b)
		} else {
			e.agentHold[b] = n - 1
		}
	}
	clear(e.agentPress)
	e.pumpMacroLocked()
	e.dirty = true
}

// State returns the agent-facing state.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stateLocked()
}

func (e *Engine) stateLocked() State {
	return State{
		Game:        e.info.ID,
		Seed:        e.seed,
		Tick:        e.tick,
		Mode:        e.mode,
		Paused:      e.paused,
		Status:      e.g.Status(),
		Observation: e.g.Observe(),
		Actions:     e.info.Actions,
	}
}

// Frame returns the current draw list.
func (e *Engine) Frame() game.Frame {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.g.Frame()
}

// LayaRequest builds a request body for a Laya / Jev style System 1 model:
// the observation as state, plus one typed choice question over the legal
// actions. POST it as-is to a Laya sidecar's /predict.
func (e *Engine) LayaRequest() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if adv, ok := e.g.(game.Advisor); ok {
		p := adv.Prompt()
		if p.Batch != nil {
			return map[string]any{"batch": p.Batch}
		}
		return map[string]any{"state": p.State, "questions": p.Questions}
	}
	criteria := map[string]string{}
	for _, a := range e.info.Actions {
		criteria[a.Name] = a.Description
	}
	return map[string]any{
		"state": e.g.Observe(),
		"questions": map[string]any{
			"action": map[string]any{
				"type":         "choice",
				"instructions": e.g.Instructions(),
				"criteria":     criteria,
			},
		},
	}
}

// Oracle returns the game's own perfect answers to its questions, if it has them.
func (e *Engine) Oracle() (game.Answers, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if o, ok := e.g.(game.Oracle); ok {
		return o.Oracle(), true
	}
	return nil, false
}

// Subscribe receives an Update after each tick. Slow subscribers drop frames.
func (e *Engine) Subscribe() (<-chan Update, func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.nextSub
	e.nextSub++
	ch := make(chan Update, 4)
	e.subs[id] = ch
	return ch, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if _, ok := e.subs[id]; ok {
			delete(e.subs, id)
			close(ch)
		}
	}
}

func (e *Engine) updateLocked() Update {
	u := Update{
		Game:   e.info.ID,
		Tick:   e.tick,
		Mode:   e.mode,
		Paused: e.paused,
		Status: e.g.Status(),
		Frame:  e.g.Frame(),
	}
	if e.last != nil {
		rate := 0.0
		if n := len(e.decisions); n > 1 {
			if span := e.decisions[n-1].Sub(e.decisions[0]).Seconds(); span > 0 {
				rate = float64(n-1) / span
			}
		}
		if time.Since(e.last.at) > 2*time.Second {
			rate = 0
		}
		u.Agent = &AgentView{Action: e.last.action, Note: e.last.note, Meta: e.last.meta, AgeMs: time.Since(e.last.at).Milliseconds(), Rate: rate, Total: e.total}
	}
	if adv, ok := e.g.(game.Advisor); ok {
		u.Situation = adv.Situation()
	}
	e.dirty = false
	return u
}

func (e *Engine) broadcast(u Update) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ch := range e.subs {
		select {
		case ch <- u:
		default:
		}
	}
}

// Run drives the realtime clock until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(time.Second / game.TickRate)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		e.mu.Lock()
		if e.mode == Realtime && !e.paused {
			e.tickLocked()
		}
		var u Update
		send := e.dirty || e.last != nil && time.Since(e.last.at) < 3*time.Second
		if send {
			u = e.updateLocked()
		}
		e.mu.Unlock()
		if send {
			e.broadcast(u)
		}
	}
}
