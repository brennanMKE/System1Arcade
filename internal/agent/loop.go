package agent

import (
	"context"
	"time"

	"system1/internal/engine"
	"system1/internal/game"
)

// Status reports the loop's progress to the UI.
type Status func(state, detail string)

// Run plays the current game with c until ctx is cancelled. The game stays
// paused until the first answer arrives, so slow agent startup costs nothing;
// after that it respects the user's pause button. Game overs restart after a
// short pause so the demo keeps going.
func Run(ctx context.Context, e *engine.Engine, c *Client, status Status) {
	e.SetPaused(true)
	status("starting", "Waiting for the agent's first answer…")
	first := true
	sleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	for ctx.Err() == nil {
		st := e.State()
		if st.Status.Over {
			if !sleep(2 * time.Second) {
				return
			}
			e.Reset(0)
			continue
		}
		if st.Paused && !first {
			if !sleep(50 * time.Millisecond) {
				return
			}
			continue
		}
		prompt := e.LayaRequest()
		if b, ok := prompt["batch"].(map[string]game.Prompt); ok && len(b) == 0 {
			sleep(time.Second / game.TickRate) // nothing to decide yet
			continue
		}
		start := time.Now()
		ans, err := c.Ask(ctx, prompt)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			status("error", err.Error())
			if !sleep(time.Second) {
				return
			}
			continue
		}
		latency := time.Since(start)
		if first {
			first = false
			e.SetPaused(false)
		}
		status("running", "")
		e.Decide(ans, map[string]any{"latency_ms": float64(latency.Microseconds()) / 1000})
	}
}
