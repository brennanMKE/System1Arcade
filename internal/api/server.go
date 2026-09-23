// Package api exposes the engine over local HTTP so an out-of-process agent
// (a Laya or Jev loop, a script, a test) can observe and play the game.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"system1/internal/engine"
	"system1/internal/game"
)

// DefaultAddr is where the agent API listens unless overridden.
const DefaultAddr = "127.0.0.1:8765"

// Handler returns the HTTP API for e.
func Handler(e *engine.Engine) http.Handler {
	s := &server{e: e}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1", s.index)
	mux.HandleFunc("GET /v1/games", s.games)
	mux.HandleFunc("POST /v1/load", s.load)
	mux.HandleFunc("POST /v1/reset", s.reset)
	mux.HandleFunc("POST /v1/pause", s.pause)
	mux.HandleFunc("POST /v1/mode", s.mode)
	mux.HandleFunc("GET /v1/state", s.state)
	mux.HandleFunc("GET /v1/laya", s.laya)
	mux.HandleFunc("GET /v1/frame", s.frame)
	mux.HandleFunc("POST /v1/action", s.action)
	mux.HandleFunc("POST /v1/press", s.press)
	mux.HandleFunc("POST /v1/step", s.step)
	mux.HandleFunc("POST /v1/decide", s.decide)
	mux.HandleFunc("GET /v1/oracle", s.oracle)
	mux.HandleFunc("GET /v1/stream", s.stream)
	return cors(mux)
}

type server struct{ e *engine.Engine }

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// decode reads an optional JSON body into v.
func decode(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("bad JSON body: %w", err)
	}
	return nil
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"endpoints": []string{
			"GET  /v1/games                      list games",
			"POST /v1/load   {game, seed?}       switch game",
			"POST /v1/reset  {seed?}             restart current game",
			"POST /v1/pause  {paused}            pause or resume",
			"POST /v1/mode   {mode}              realtime | lockstep",
			"GET  /v1/state                      observation, status, legal actions",
			"GET  /v1/laya                       {state, questions} ready for Laya /predict",
			"GET  /v1/frame                      draw list",
			"POST /v1/action {action, hold_ticks?, hold_ms?, meta?}  act (realtime)",
			"POST /v1/press  {buttons, hold_ticks?}                   raw buttons",
			"POST /v1/step   {action?, ticks?, meta?}                act + advance (lockstep)",
			"POST /v1/decide {answers, meta?}    Laya's answers to /v1/laya's questions; the game acts on them",
			"GET  /v1/oracle                     the game's own correct answers (a ceiling to compare models against)",
			"GET  /v1/stream?every=N             SSE of state every N ticks",
		},
		"tick_rate": game.TickRate,
	})
}

func (s *server) games(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID       string        `json:"id"`
		Title    string        `json:"title"`
		Controls string        `json:"controls"`
		Actions  []game.Action `json:"actions"`
	}
	var out []item
	for _, g := range s.e.Games() {
		out = append(out, item{g.ID, g.Title, g.Controls, g.Actions})
	}
	writeJSON(w, 200, out)
}

func (s *server) load(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Game string `json:"game"`
		Seed int64  `json:"seed"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if err := s.e.Load(req.Game, req.Seed); err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, s.e.State())
}

func (s *server) reset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seed int64 `json:"seed"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	s.e.Reset(req.Seed)
	writeJSON(w, 200, s.e.State())
}

func (s *server) pause(w http.ResponseWriter, r *http.Request) {
	req := struct {
		Paused *bool `json:"paused"`
	}{}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Paused == nil {
		s.e.TogglePause()
	} else {
		s.e.SetPaused(*req.Paused)
	}
	writeJSON(w, 200, s.e.State())
}

func (s *server) mode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode engine.Mode `json:"mode"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if err := s.e.SetMode(req.Mode); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, s.e.State())
}

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.e.State())
}

func (s *server) laya(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.e.LayaRequest())
}

func (s *server) frame(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.e.Frame())
}

func msToTicks(ms int) int {
	if ms <= 0 {
		return 0
	}
	return max(1, (ms*game.TickRate+500)/1000)
}

func (s *server) action(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action    string         `json:"action"`
		HoldTicks int            `json:"hold_ticks"`
		HoldMs    int            `json:"hold_ms"`
		Meta      map[string]any `json:"meta"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	hold := req.HoldTicks
	if hold == 0 {
		hold = msToTicks(req.HoldMs)
	}
	if err := s.e.Act(req.Action, hold, req.Meta); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *server) press(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Buttons   []game.Button `json:"buttons"`
		HoldTicks int           `json:"hold_ticks"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if err := s.e.Press(req.Buttons, req.HoldTicks); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *server) step(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string         `json:"action"`
		Ticks  int            `json:"ticks"`
		Meta   map[string]any `json:"meta"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	st, err := s.e.Step(req.Action, req.Ticks, req.Meta)
	if err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) decide(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Answers game.Answers   `json:"answers"`
		Meta    map[string]any `json:"meta"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	d, st, err := s.e.Decide(req.Answers, req.Meta)
	if err != nil {
		fail(w, 409, err)
		return
	}
	out := map[string]any{"decision": d}
	if st.Game != "" {
		out["state"] = st
	}
	writeJSON(w, 200, out)
}

func (s *server) oracle(w http.ResponseWriter, r *http.Request) {
	a, ok := s.e.Oracle()
	if !ok {
		fail(w, 404, fmt.Errorf("this game has no oracle"))
		return
	}
	writeJSON(w, 200, map[string]any{"answers": a})
}

// stream pushes the agent state as Server-Sent Events every N ticks.
func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	every := uint64(1)
	if v, err := strconv.Atoi(r.URL.Query().Get("every")); err == nil && v > 0 {
		every = uint64(v)
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, cancel := s.e.Subscribe()
	defer cancel()
	last := ^uint64(0)
	for {
		select {
		case <-r.Context().Done():
			return
		case u, ok := <-ch:
			if !ok {
				return
			}
			if u.Tick == last || u.Tick%every != 0 {
				continue
			}
			last = u.Tick
			b, _ := json.Marshal(s.e.State())
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}
