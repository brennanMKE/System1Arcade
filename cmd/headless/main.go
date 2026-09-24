// Command headless runs the games and agent API without a window, for
// batch experiments and CI. It serves the same /v1 API as the desktop app.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"system1/internal/api"
	"system1/internal/engine"
)

func main() {
	addr := flag.String("addr", api.DefaultAddr, "agent API listen address")
	gameID := flag.String("game", "tetris", "game to load: tetris, frogger, invaders")
	seed := flag.Int64("seed", 0, "random seed (0 = from clock)")
	mode := flag.String("mode", string(engine.Lockstep), "realtime or lockstep")
	pace := flag.Int("pace", 0, "minimum ticks between the agent's presses (0 = no limit; the app's default 6 inputs/s is 10)")
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
	log.Printf("system1 headless: %s (%s) agent API on http://%s/v1", *gameID, *mode, *addr)
	log.Fatal(http.ListenAndServe(*addr, api.Handler(e)))
}
