package invaders

import (
	"fmt"
	"strings"

	"system1/internal/game"
)

// Bombs that will reach the cannon within this many ticks are "about to hit".
// The cannon needs ~9 ticks to slide clear plus the model's reaction time.
const dangerTicks = 50

func (g *Invaders) muzzle() int { return int(g.px) + 6 }

// shieldAbove reports whether an intact shield pixel sits in column x
// between y0 and the cannon.
func (g *Invaders) shieldAbove(x int, y0 float64) bool {
	for i, sx := range shieldXs {
		if x < sx || x >= sx+shieldW {
			continue
		}
		for sy := 0; sy < shieldH; sy++ {
			if float64(shieldY+sy) >= y0 && g.shield[i][sy][x-sx] {
				return true
			}
		}
	}
	return false
}

// invaderAbove reports whether a shot from column x would hit an invader.
func (g *Invaders) invaderAbove(x int) bool {
	_, ok := g.targetAt(x)
	return ok
}

// incoming returns the most urgent bomb that will hit the cannon where it
// stands, and the ticks until impact.
func (g *Invaders) incoming() (*bullet, int) { return g.incomingAt(g.px) }

// moveSafe reports whether sliding one move (6px) in dir keeps the cannon out
// of every bomb that lands soon.
func (g *Invaders) moveSafe(dir string) bool {
	px := g.px - 6
	if dir == "right" {
		px = g.px + 6
	}
	if px < margin || px > width-margin-playerW {
		return false
	}
	b, t := g.incomingAt(px)
	return b == nil || t > dangerTicks
}

// incomingAt is incoming for a cannon at px.
func (g *Invaders) incomingAt(px float64) (*bullet, int) {
	speed := min(2+0.25*float64(g.level), 4)
	var best *bullet
	bestT := 1 << 30
	for i := range g.bombs {
		b := &g.bombs[i]
		if b.x < px-2 || b.x >= px+playerW+2 || b.y >= playerY+8 {
			continue
		}
		if g.shieldAbove(int(b.x), b.y+7) {
			continue // a shield will stop it
		}
		t := int((playerY - (b.y + 7)) / speed)
		if t < bestT {
			best, bestT = b, t
		}
	}
	return best, bestT
}

// escapeSide is the shorter way out of bomb b's path.
func (g *Invaders) escapeSide(b *bullet) string {
	left := g.px - (b.x - playerW - 3)
	right := b.x + 3 - g.px
	if g.px-left < margin {
		return "right"
	}
	if g.px+right > width-margin-playerW {
		return "left"
	}
	if left <= right {
		return "left"
	}
	return "right"
}

// aimOffset is the signed distance in pixels to the nearest cannon position
// with a clear shot at an invader (0 = here), and whether one exists. It
// prefers the formation's outermost columns: clearing them gives the
// formation farther to march before each drop, as arcade players know.
func (g *Invaders) aimOffset() (int, bool) {
	lo, hi := gridC, -1
	for c := 0; c < gridC; c++ {
		if _, ok := g.shooter(c); ok {
			lo, hi = min(lo, c), max(hi, c)
		}
	}
	for _, outer := range []bool{true, false} {
		for d := 0; d < width; d++ {
			for _, s := range []int{-d, d} {
				x := g.muzzle() + s
				if x-6 < margin || x-6 > width-margin-playerW {
					continue
				}
				// Sliding there takes |s| ticks plus a decision or two.
				if c, ok := g.targetAfter(x, abs(s)+8); ok && (!outer || c == lo || c == hi) {
					return s, true
				}
			}
		}
	}
	return 0, false
}

// march predicts the formation origin after n more steps, bounces included.
func (g *Invaders) march(n int) (fx, fy int) {
	minX, maxX, _ := g.bounds()
	lo, hi := minX-g.fx, maxX-g.fx
	fx, fy, dir := g.fx, g.fy, g.dir
	for i := 0; i < n; i++ {
		if dir > 0 && fx+hi+2 > width-margin || dir < 0 && fx+lo-2 < margin {
			fy += 8
			dir = -dir
		} else {
			fx += 2 * dir
		}
	}
	return fx, fy
}

// targetAt is the column of the invader a shot fired from x now would hit,
// leading the target: the formation keeps marching while the shot climbs.
func (g *Invaders) targetAt(x int) (int, bool) { return g.targetAfter(x, 0) }

// targetAfter is targetAt for a shot fired delay ticks from now.
func (g *Invaders) targetAfter(x, delay int) (int, bool) {
	if g.shieldAbove(x, 0) {
		return 0, false
	}
	interval := 1 + g.aliveCount()*2/5
	for r := gridR - 1; r >= 0; r-- {
		for c := range g.alive[r] {
			if g.alive[r][c] {
				_, iy, w, h := g.invRect(r, c)
				flight := (playerY - 4 - (iy + h)) / bulletSpeed
				fx, fy := g.march((g.stepT + delay + flight) / interval)
				ix := fx + c*cellW + rowTypes[r].off
				_ = fy
				if x >= ix && x < ix+w {
					return c, true
				}
			}
		}
	}
	return 0, false
}

func side(dx int) string {
	if dx < 0 {
		return "left"
	}
	return "right"
}

// facts are one short sentence per question. Laya reads short states best.
func (g *Invaders) facts() map[string]string {
	f := map[string]string{
		"threat": "No bomb is falling toward the cannon.",
		"escape": "There is open space on both sides of the cannon.",
	}
	if b, t := g.incoming(); b != nil && t <= dangerTicks {
		f["threat"] = fmt.Sprintf("A bomb is falling straight at the cannon and will hit it in %.1f seconds.", float64(t)/game.TickRate)
		f["escape"] = fmt.Sprintf("The open space away from the falling bomb is to the %s of the cannon.", g.escapeSide(b))
	}
	for _, dir := range []string{"left", "right"} {
		if g.moveSafe(dir) {
			f[dir] = fmt.Sprintf("The space to the %s of the cannon is clear.", dir)
		} else {
			f[dir] = fmt.Sprintf("A bomb is about to land to the %s of the cannon, or the wall is there.", dir)
		}
	}
	f["above"] = map[string]string{
		"invader": "An invader is directly above the cannon.",
		"shield":  "The cannon's own shield is directly above the cannon.",
		"empty":   "The space directly above the cannon is empty.",
	}[g.above()]
	switch dx, ok := g.aimOffset(); {
	case !ok:
		f["aim"] = "The cannon is lined up with its target."
	case abs(dx) <= 1:
		f["aim"] = "The cannon is lined up with its target."
	case abs(dx) < 16:
		f["aim"] = fmt.Sprintf("The target is a little to the %s of the cannon.", side(dx))
	default:
		f["aim"] = fmt.Sprintf("The target is far to the %s of the cannon.", side(dx))
	}
	return f
}

var factOrder = []string{"threat", "escape", "left", "right", "above", "aim"}

// above is what a shot fired now would meet: "invader", "shield" or "empty".
func (g *Invaders) above() string {
	switch {
	case g.shieldAbove(g.muzzle(), 0):
		return "shield"
	case g.invaderAbove(g.muzzle()):
		return "invader"
	}
	return "empty"
}

func (g *Invaders) Situation() string {
	if g.over {
		return "The game is over."
	}
	if g.dying > 0 {
		return "The cannon has been destroyed and is respawning."
	}
	f := g.facts()
	var s []string
	for _, k := range factOrder {
		s = append(s, f[k])
	}
	return strings.Join(s, " ")
}

var invaderQuestions = map[string]map[string]any{
	"threat": game.Noul("Is a bomb about to hit the cannon?"),
	"escape": game.Choice("Which way is the open space?", map[string]string{"left": "to the left", "right": "to the right"}),
	"left":   game.Noul("Is it safe to move left?"),
	"right":  game.Noul("Is it safe to move right?"),
	// Options that reuse the state's own words are read far more reliably
	// than a yes/no question about consequences.
	"above": game.Choice("What is directly above the cannon?", map[string]string{
		"invader": "an invader", "empty": "empty space", "shield": "the cannon's own shield"}),
	"aim": game.Choice("Where is the target?", map[string]string{
		"left": "to the left", "right": "to the right", "here": "lined up with the cannon"}),
}

func (g *Invaders) Prompt() game.Prompt {
	f := g.facts()
	batch := map[string]game.Prompt{}
	for k, q := range invaderQuestions {
		batch[k] = game.Prompt{State: f[k], Questions: map[string]any{"a": q}}
	}
	return game.Prompt{Batch: batch}
}

func (g *Invaders) Decide(a game.Answers) game.Decision {
	ready := !g.shot.on && g.dying == 0
	switch {
	case g.over || g.dying > 0:
		return game.Decision{Actions: []string{"noop"}, Note: "waiting"}
	case a.Yes("threat.a"):
		dir := a["escape.a"].Choice
		return game.Decision{Actions: []string{dir}, Note: "bomb incoming → dodge " + dir}
	case a["above.a"].Choice == "invader" && ready:
		return game.Decision{Actions: []string{"fire"}, Note: "lined up → fire"}
	case a["aim.a"].Choice == "here" || a["aim.a"].Choice == "":
		return game.Decision{Actions: []string{"noop"}, Note: "in position → wait for the shot"}
	default:
		dir := a["aim.a"].Choice
		if !a.Yes(dir + ".a") {
			return game.Decision{Actions: []string{"noop"}, Note: "target " + dir + " but a bomb is there → wait"}
		}
		return game.Decision{Actions: []string{dir}, Note: "line up → move " + dir}
	}
}

func (g *Invaders) Oracle() game.Answers {
	a := game.Answers{"escape.a": game.Pick("left"), "aim.a": game.Pick("here")}
	b, t := g.incoming()
	a["threat.a"] = game.Truth(b != nil && t <= dangerTicks)
	if b != nil {
		a["escape.a"] = game.Pick(g.escapeSide(b))
	}
	a["above.a"] = game.Pick(g.above())
	a["left.a"] = game.Truth(g.moveSafe("left"))
	a["right.a"] = game.Truth(g.moveSafe("right"))
	if dx, ok := g.aimOffset(); ok && abs(dx) > 1 {
		a["aim.a"] = game.Pick(side(dx))
	}
	return a
}
