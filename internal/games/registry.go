// Package games registers every playable game.
package games

import (
	"system1/internal/game"
	"system1/internal/games/frogger"
	"system1/internal/games/invaders"
	"system1/internal/games/tetris"
)

// Factory creates a fresh game instance.
type Factory func() game.Game

// All returns the built-in games in menu order.
func All() []Factory {
	return []Factory{
		func() game.Game { return tetris.New() },
		func() game.Game { return frogger.New() },
		func() game.Game { return invaders.New() },
	}
}
