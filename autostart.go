package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// AutostartEnv names a test-only switch: SYSTEM1_AUTOSTART=<game>[:<seed>]
// loads that game at launch and starts the agent chosen in Settings, exactly
// as pressing Start on the start screen with Agent selected. It exists so a
// disposable VM can run the real app unattended (scripts/run-agent-vm.sh)
// without synthesizing a click, which would need an Accessibility grant.
// Unset, the app opens on the start screen as usual.
const AutostartEnv = "SYSTEM1_AUTOSTART"

// AutostopEnv names another test-only switch: with SYSTEM1_AUTOSTART, also
// SYSTEM1_AUTOSTOP_AFTER=<duration> (e.g. 90s, or plain seconds) stops the
// agent that long after it was started, exactly as pressing Stop, so a run can
// measure the app's memory once the built-in agent has freed its model. The
// game carries on under keyboard control. Unset, the agent plays until
// stopped by hand.
const AutostopEnv = "SYSTEM1_AUTOSTOP_AFTER"

// parseAutostop reads a Go duration or a number of seconds.
func parseAutostop(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	d, err := time.ParseDuration(v)
	if err != nil {
		var secs float64
		secs, err = strconv.ParseFloat(v, 64)
		d = time.Duration(secs * float64(time.Second))
	}
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s=%q: want a positive duration such as 90s", AutostopEnv, v)
	}
	return d, nil
}

// parseAutostart reads "<game>[:<seed>]". A missing or zero seed picks one
// from the clock, as the start screen does.
func parseAutostart(v string) (game string, seed int64, err error) {
	game, s, hasSeed := strings.Cut(strings.TrimSpace(v), ":")
	if game == "" {
		return "", 0, fmt.Errorf("%s=%q: want <game>[:<seed>]", AutostartEnv, v)
	}
	if hasSeed {
		if seed, err = strconv.ParseInt(s, 10, 64); err != nil {
			return "", 0, fmt.Errorf("%s=%q: seed must be an integer", AutostartEnv, v)
		}
	}
	return game, seed, nil
}

// autostart loads the game named by spec and starts the agent through the
// same StartAgent the Start button calls.
func (a *App) autostart(spec string) error {
	id, seed, err := parseAutostart(spec)
	if err != nil {
		return err
	}
	var stopAfter time.Duration
	if v := os.Getenv(AutostopEnv); v != "" {
		if stopAfter, err = parseAutostop(v); err != nil {
			return err
		}
	}
	if err := a.engine.Load(id, seed); err != nil {
		return fmt.Errorf("%s: %w", AutostartEnv, err)
	}
	a.StartAgent()
	if stopAfter > 0 {
		time.AfterFunc(stopAfter, func() {
			a.StopAgent()
			log.Printf("autostop: agent stopped after %v", stopAfter)
		})
	}
	return nil
}
