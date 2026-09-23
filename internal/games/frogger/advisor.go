package frogger

import (
	"fmt"
	"math"
	"strings"

	"system1/internal/game"
)

// After landing, the frog must survive this long: the hop cooldown plus the
// model's reaction time before it can move again.
const safeWindow = 24

// coversAt reports whether an object covers [a, b] t ticks from now.
func (l *lane) coversAt(a, b float64, t int) bool {
	w := float64(l.spec.length * cell)
	span := l.span()
	for _, x0 := range l.xs {
		x := x0 + l.speed*float64(t)
		if l.speed > 0 {
			for x > float64(width+cell) {
				x -= span
			}
		} else {
			for x+w < -float64(cell) {
				x += span
			}
		}
		if b > x && a < x+w {
			return true
		}
	}
	return false
}

// outcome describes the square at (row, fx) over the next safeWindow ticks.
type outcome struct {
	safe bool
	desc string
}

// SetInputTiming tells the frog when a decided hop happens (delay) and how
// long it then stays before it can hop again (gap), at a human-like pace.
func (f *Frogger) SetInputTiming(delay, gap int) { f.delay, f.gap = delay, gap }

// window is how long a square must stay safe after the frog lands there:
// until it can hop again, plus a margin for the model's reaction time.
func (f *Frogger) window() int { return max(safeWindow, f.gap+safeWindow-hopCooldown) }

func secs(t int) string { return fmt.Sprintf("%.1f seconds", float64(t)/game.TickRate) }

// outcomeAt describes landing at (row, fx). A hop lands f.delay ticks from
// now; staying put happens now.
func (f *Frogger) outcomeAt(row int, fx float64, hop bool) outcome {
	if row < 0 {
		return outcome{false, "nothing; it is off the board"}
	}
	// At a human-like pace a hop can wait a few ticks, and a frog on the
	// river drifts with its log until then.
	drift, delay := 0.0, 0
	if hop {
		delay = f.delay
	}
	if cur := f.lanes[f.frow]; cur != nil && cur.spec.kind == river {
		drift = cur.speed
	}
	if row == 0 {
		x := fx + drift*float64(delay)
		c := int(math.Floor((x + cell/2) / cell))
		for i, hc := range homeCols {
			if hc == c {
				if f.homes[i] {
					return outcome{false, "a home that is already filled, so the frog would die"}
				}
				return outcome{true, "an empty home, which is safe and scores"}
			}
		}
		return outcome{false, "the bank wall between homes, so the frog would die"}
	}
	l := f.lanes[row]
	if l == nil {
		return outcome{true, "safe ground"}
	}
	switch l.spec.kind {
	case road:
		name := map[byte]string{'C': "a car", 'K': "a truck"}[l.spec.char]
		hit := -1
		for d := delay; d <= delay; d++ {
			x := fx + drift*float64(d)
			for t := d; t <= d+f.window(); t++ {
				if l.coversAt(x+6, x+cell-6, t) && (hit < 0 || t < hit) {
					hit = t
				}
			}
		}
		switch {
		case hit == 0:
			return outcome{false, fmt.Sprintf("a square with %s on it right now, so the frog would be hit", name)}
		case hit > 0:
			return outcome{false, fmt.Sprintf("a square %s will drive through in %s, so the frog would be hit", name, secs(hit))}
		}
		return outcome{true, "clear road with no traffic coming, which is safe"}
	default:
		ride := map[byte]string{'L': "a log", 'T': "turtles"}[l.spec.char]
		for d := delay; d <= delay; d++ {
			for t := d; t <= d+f.window(); t++ {
				x := fx + drift*float64(d) + l.speed*float64(t-d)
				c := x + cell/2
				if x < -cell/2 || x > float64(width-cell/2) {
					return outcome{false, fmt.Sprintf("%s that carries the frog off the screen in %s, so the frog would die", ride, secs(t))}
				}
				if !l.coversAt(c-2, c+2, t) {
					if t == 0 {
						return outcome{false, "open water, so the frog would drown"}
					}
					return outcome{false, fmt.Sprintf("the end of %s, which drifts away in %s and the frog would drown", ride, secs(t))}
				}
			}
		}
		return outcome{true, fmt.Sprintf("%s the frog can ride safely", ride)}
	}
}

// options describes each move: where the frog would land.
func (f *Frogger) options() map[string]outcome {
	o := map[string]outcome{
		"up":   f.outcomeAt(f.frow-1, f.fx, true),
		"stay": f.outcomeAt(f.frow, f.fx, false),
	}
	if f.frow < rows-1 {
		o["down"] = f.outcomeAt(f.frow+1, f.fx, true)
	} else {
		o["down"] = outcome{false, "the bottom edge, where the frog cannot go"}
	}
	if f.fx-cell < 0 {
		o["left"] = outcome{false, "the left edge, where the frog cannot go"}
	} else {
		o["left"] = f.outcomeAt(f.frow, f.fx-cell, true)
	}
	if f.fx+cell > float64(width-cell) {
		o["right"] = outcome{false, "the right edge, where the frog cannot go"}
	} else {
		o["right"] = f.outcomeAt(f.frow, f.fx+cell, true)
	}
	return o
}

// goal is the direction that makes progress toward an empty home.
func (f *Frogger) goal() (string, string) {
	c := f.col()
	best, bestD := -1, 1<<30
	for i, hc := range homeCols {
		if !f.homes[i] && abs(hc-c) < abs(bestD) {
			best, bestD = hc, hc-c
		}
	}
	// Line up with an open home on safe ground (the start row and the
	// median), where walking sideways is free; only the top river row needs
	// a last correction.
	onSafeGround := f.frow == rows-1 || f.frow == 6
	switch {
	case best < 0:
		return "up", "The homes are ahead."
	case f.frow > 1 && !(onSafeGround && bestD != 0):
		return "up", fmt.Sprintf("The homes are %d rows ahead.", f.frow)
	case bestD == 0:
		return "up", "An empty home is directly ahead."
	case bestD < 0:
		return "left", fmt.Sprintf("The nearest empty home is %d squares to the left.", -bestD)
	default:
		return "right", fmt.Sprintf("The nearest empty home is %d squares to the right.", bestD)
	}
}

var moveWords = map[string]string{
	"up": "Hopping up lands in", "stay": "Staying put keeps the frog in", "left": "Hopping left lands in",
	"right": "Hopping right lands in", "down": "Hopping down lands in",
}

var moveOrder = []string{"up", "stay", "left", "right", "down"}

// facts are one short sentence per question. Laya reads short states best.
func (f *Frogger) facts() map[string]string {
	o := f.options()
	out := map[string]string{}
	for _, m := range moveOrder {
		kind := "a deadly place"
		if o[m].safe {
			kind = "a safe place"
		}
		out[m] = fmt.Sprintf("%s %s: %s.", moveWords[m], kind, o[m].desc)
	}
	g, _ := f.goal()
	out["goal"] = map[string]string{
		"up":    "The nearest empty home is up.",
		"left":  "The nearest empty home is to the left.",
		"right": "The nearest empty home is to the right.",
	}[g]
	return out
}

func (f *Frogger) Situation() string {
	if f.over {
		return "The game is over."
	}
	if f.dying > 0 {
		return "The frog died and is respawning."
	}
	fs := f.facts()
	s := []string{fs["goal"]}
	for _, m := range moveOrder {
		s = append(s, fs[m])
	}
	return strings.Join(s, " ")
}

// Options that reuse the state's own words are read far more reliably than
// a yes/no question about consequences.
var safeQuestion = game.Choice("What kind of place is it?", map[string]string{
	"safe": "a safe place", "deadly": "a deadly place"})

func (f *Frogger) Prompt() game.Prompt {
	fs := f.facts()
	batch := map[string]game.Prompt{
		"goal": {State: fs["goal"], Questions: map[string]any{"a": game.Choice("Where is the nearest empty home?", map[string]string{
			"up": "up", "left": "to the left", "right": "to the right"})}},
	}
	for _, m := range moveOrder {
		batch[m] = game.Prompt{State: fs[m], Questions: map[string]any{"a": safeQuestion}}
	}
	return game.Prompt{Batch: batch}
}

var moveAction = map[string]string{"up": "up", "down": "down", "left": "left", "right": "right", "stay": "noop"}

func (f *Frogger) Decide(a game.Answers) game.Decision {
	if f.over || f.dying > 0 {
		return game.Decision{Actions: []string{"noop"}, Note: "waiting"}
	}
	if f.hopCD > 0 {
		return game.Decision{Actions: []string{"noop"}, Wait: f.hopCD, Note: "mid-hop"}
	}
	goal := a["goal.a"].Choice
	order := []string{goal, "up", "stay", "left", "right", "down"}
	if safeP(a, "right") > safeP(a, "left") {
		order[3], order[4] = "right", "left"
	}
	for _, m := range order {
		if _, ok := moveAction[m]; ok && safeP(a, m) > 0.5 {
			return game.Decision{Actions: []string{moveAction[m]}, Wait: hopCooldown, Note: fmt.Sprintf("goal %s → %s is safe", goal, m)}
		}
	}
	best := "stay"
	for _, m := range []string{"up", "left", "right", "down"} {
		if safeP(a, m) > safeP(a, best) {
			best = m
		}
	}
	return game.Decision{Actions: []string{moveAction[best]}, Wait: hopCooldown, Note: "nothing looks safe → " + best}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// safeP is the model's probability that move m is safe.
func safeP(a game.Answers, m string) float64 { return a[m+".a"].Probabilities["safe"] }

func (f *Frogger) Oracle() game.Answers {
	a := game.Answers{}
	for m, o := range f.options() {
		if o.safe {
			a[m+".a"] = game.Pick("safe")
		} else {
			a[m+".a"] = game.Pick("deadly")
		}
	}
	g, _ := f.goal()
	a["goal.a"] = game.Pick(g)
	return a
}
