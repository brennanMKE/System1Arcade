// Command headless runs the games and agent API without a window, for
// batch experiments and CI. It serves the same /v1 API as the desktop app.
//
// With -agent it also plays: -agent laya runs the built-in agent (Laya in
// this process, no Python), and -agent <url> asks a custom agent's predict
// endpoint, as the app does. It plays -games games on seeds -seed, -seed+1, …,
// prints each score and the mean, and exits:
//
//	go run ./cmd/headless -agent laya -game frogger -mode realtime -pace 10 -seed 1 -games 3
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	laya "github.com/brennanMKE/laya-go"

	"system1/internal/agent"
	"system1/internal/api"
	"system1/internal/engine"
	"system1/internal/game"
)

func main() {
	addr := flag.String("addr", api.DefaultAddr, "agent API listen address (\"\" = none)")
	gameID := flag.String("game", "tetris", "game to load: tetris, frogger, invaders")
	seed := flag.Int64("seed", 0, "random seed (0 = from clock)")
	mode := flag.String("mode", string(engine.Lockstep), "realtime or lockstep")
	pace := flag.Int("pace", 0, "minimum ticks between the agent's presses (0 = no limit; the app's default 6 inputs/s is 10)")
	agentFlag := flag.String("agent", "", "play with an agent: \"laya\" (built in, in this process) or a custom agent's predict URL")
	games := flag.Int("games", 1, "with -agent: games to play, on seeds -seed, -seed+1, …")
	maxTime := flag.Duration("max", 0, "with -agent: stop a game after this long (0 = play until game over)")
	noBatch := flag.Bool("no-batch", false, "with -agent <url>: one request per state instead of batches")
	flag.Parse()

	e := engine.New()
	if err := e.Load(*gameID, *seed); err != nil {
		log.Fatal(err)
	}
	if err := e.SetMode(engine.Mode(*mode)); err != nil {
		log.Fatal(err)
	}
	e.SetAgentPace(*pace)
	go e.Run(context.Background())

	if *agentFlag == "" {
		log.Printf("system1 headless: %s (%s) agent API on http://%s/v1", *gameID, *mode, *addr)
		log.Fatal(http.ListenAndServe(*addr, api.Handler(e)))
	}
	if *addr != "" {
		go func() {
			log.Printf("agent API on http://%s/v1", *addr)
			if err := http.ListenAndServe(*addr, api.Handler(e)); err != nil {
				log.Printf("agent API disabled: %v", err)
			}
		}()
	}

	ctx := context.Background()
	var asker agent.Asker
	var local *agent.Local
	if *agentFlag == "laya" {
		start := time.Now()
		var err error
		local, err = agent.LoadLocal(ctx, laya.Options{})
		if err != nil {
			log.Fatal(err)
		}
		defer local.Close()
		log.Printf("Laya loaded in %v", time.Since(start).Round(time.Millisecond))
		asker = local
	} else if strings.HasPrefix(*agentFlag, "http://") || strings.HasPrefix(*agentFlag, "https://") {
		asker = &agent.Client{URL: *agentFlag, Batch: !*noBatch}
	} else {
		log.Fatalf("-agent %q: want laya or a URL", *agentFlag)
	}

	var scores []float64
	for i := range *games {
		s := *seed
		if s != 0 {
			s += int64(i)
		}
		timed := &timing{Asker: asker}
		r := play(ctx, e, *gameID, s, timed, *maxTime)
		scores = append(scores, float64(r.score))
		line := fmt.Sprintf("%s seed %d: %d points, level %d, %s, %v, %s", *gameID, s, r.score, r.level, r.end,
			r.took.Round(time.Second), timed.summary())
		if local != nil {
			line += "; " + local.CacheStats().Summary()
		}
		fmt.Println(line)
	}
	if len(scores) > 1 {
		mean, sd := meanSD(scores)
		fmt.Printf("%s: mean %.0f (sd %.0f) points over %d games\n", *gameID, mean, sd, len(scores))
	}
}

type result struct {
	score, level int
	end          string
	took         time.Duration
}

// play plays one game with a until it's over (or max passes) and returns
// its score.
func play(ctx context.Context, e *engine.Engine, id string, seed int64, a agent.Asker, max time.Duration) result {
	if err := e.Load(id, seed); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var lastErr string
	go func() {
		defer close(done)
		agent.Run(ctx, e, a, func(state, detail string) {
			if state == "error" && detail != lastErr {
				log.Printf("agent error: %s", detail)
				lastErr = detail
			}
		})
	}()
	start := time.Now()
	var r result
	for {
		time.Sleep(50 * time.Millisecond)
		st := e.State()
		r.score, r.level = st.Status.Score, st.Status.Level
		if st.Status.Over {
			r.end = "game over"
			break
		}
		if max > 0 && time.Since(start) > max {
			r.end = "stopped at -max"
			break
		}
	}
	r.took = time.Since(start)
	cancel()
	<-done
	return r
}

// timing measures how long the agent takes per decision.
type timing struct {
	agent.Asker
	mu    sync.Mutex
	times []time.Duration
}

func (t *timing) Ask(ctx context.Context, prompt map[string]any) (game.Answers, error) {
	start := time.Now()
	ans, err := t.Asker.Ask(ctx, prompt)
	if err == nil {
		t.mu.Lock()
		t.times = append(t.times, time.Since(start))
		t.mu.Unlock()
	}
	return ans, err
}

func (t *timing) summary() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.times) == 0 {
		return "no decisions"
	}
	ts := slices.Clone(t.times)
	slices.Sort(ts)
	var sum time.Duration
	slow := 0
	for _, d := range ts {
		sum += d
		if d > 10*time.Millisecond {
			slow++
		}
	}
	q := func(p float64) time.Duration { return ts[min(len(ts)-1, int(p*float64(len(ts))))] }
	return fmt.Sprintf("%d decisions, mean %.2f ms, p50 %.2f ms, p99 %.1f ms, max %.0f ms, %d over 10 ms",
		len(ts), ms(sum/time.Duration(len(ts))), ms(q(0.5)), ms(q(0.99)), ms(ts[len(ts)-1]), slow)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func meanSD(xs []float64) (float64, float64) {
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(ss / float64(len(xs)-1))
}
