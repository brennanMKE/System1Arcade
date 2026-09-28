package agent

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"system1/internal/engine"
	"system1/internal/game"
)

// Asker answers a decision prompt (as built by engine.LayaRequest) with
// answers keyed the way the game's Decide expects: by question name, or
// "key.question" for a batch. Client asks an HTTP endpoint; Local runs Laya
// in this process.
type Asker interface {
	Ask(ctx context.Context, prompt map[string]any) (game.Answers, error)
}

// Status reports the loop's progress to the UI.
type Status func(state, detail string)

// Run plays the current game with the answers of c until ctx is cancelled. The game stays
// paused until the first answer arrives, so slow agent startup costs nothing;
// after that it respects the user's pause button. Game overs restart after a
// short pause so the demo keeps going.
func Run(ctx context.Context, e *engine.Engine, c Asker, status Status) {
	e.SetPaused(true)
	status("starting", "Waiting for the agent's first answer…")
	first := true
	var lat latencies
	defer func() {
		if st := e.State(); lat.n() > 0 {
			log.Printf("agent: stopped in %s seed %d at %d points: %s", st.Game, st.Seed, st.Status.Score, lat.summary())
		}
	}()
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
			if lat.n() > 0 {
				log.Printf("agent: game over in %s seed %d at %d points: %s", st.Game, st.Seed, st.Status.Score, lat.summary())
				lat = latencies{}
			}
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
		tick := st.Tick
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
		lat.add(latency)
		if first {
			first = false
			e.SetPaused(false)
		}
		status("running", "")
		e.Decide(ans, map[string]any{"latency_ms": float64(latency.Microseconds()) / 1000})
		// The game only changes on a tick, so asking again before the next
		// one would get the same prompt and the same answers. An agent that
		// answers from its cache in microseconds would otherwise spin a CPU
		// core deciding thousands of times a second.
		for e.Tick() == tick && time.Since(start) < time.Second/game.TickRate {
			if !sleep(time.Millisecond) {
				return
			}
		}
	}
}

// latencies are one game's decision times, for the log line at its end: an
// unattended run (scripts/run-agent-vm.sh) reads the built-in agent's speed
// from it, since the model runs inside the app.
type latencies struct{ d []time.Duration }

func (l *latencies) add(d time.Duration) { l.d = append(l.d, d) }
func (l *latencies) n() int              { return len(l.d) }

// summary is "N decisions, median, p99 and slowest in ms, and how many took
// over 20 ms" (with the built-in agent, about the ones the answer cache missed).
func (l *latencies) summary() string {
	d := slices.Sorted(slices.Values(l.d))
	ms := func(x time.Duration) float64 { return float64(x.Microseconds()) / 1000 }
	slow := 0
	for _, x := range d {
		if x > 20*time.Millisecond {
			slow++
		}
	}
	return fmt.Sprintf("%d decisions, median %.2f ms, p99 %.1f ms, slowest %.1f ms, %d over 20 ms",
		len(d), ms(d[len(d)/2]), ms(d[len(d)*99/100]), ms(d[len(d)-1]), slow)
}
