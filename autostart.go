package main

import (
	"fmt"
	"strconv"
	"strings"
)

// AutostartEnv names a test-only switch: SYSTEM1_AUTOSTART=<game>[:<seed>]
// loads that game at launch and starts the agent chosen in Settings, exactly
// as pressing Start on the start screen with Agent selected. It exists so a
// disposable VM can run the real app unattended (scripts/run-agent-vm.sh)
// without synthesizing a click, which would need an Accessibility grant.
// Unset, the app opens on the start screen as usual.
const AutostartEnv = "SYSTEM1_AUTOSTART"

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
	if err := a.engine.Load(id, seed); err != nil {
		return fmt.Errorf("%s: %w", AutostartEnv, err)
	}
	a.StartAgent()
	return nil
}
