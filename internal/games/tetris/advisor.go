package tetris

import (
	"fmt"
	"sort"

	"system1/internal/game"
)

// A placement is one reachable resting spot for the falling piece.
type placement struct {
	rot, x    int // target rotation and x (piece origin)
	actions   []string
	minC      int
	maxC      int
	lines     int
	newHoles  int
	height    int
	top       int // highest row the piece reaches, counted from the floor
	aggregate int
	bumpiness int
	wells     int
	value     float64
}

// stats measures a board. wells counts deep wells: columns at least 3 rows
// below both neighbors, with the walls counting as tall neighbors.
type stats struct{ holes, height, aggregate, bumpiness, wells int }

func boardStats(b *[rows][cols]int) stats {
	var s stats
	var hs [cols]int
	prev := -1
	for x := 0; x < cols; x++ {
		h := 0
		for y := 0; y < rows; y++ {
			if b[y][x] != 0 {
				if h == 0 {
					h = rows - y
				}
			} else if h > 0 {
				s.holes++
			}
		}
		hs[x] = h
		s.aggregate += h
		s.height = max(s.height, h)
		if prev >= 0 {
			s.bumpiness += abs(h - prev)
		}
		prev = h
	}
	for x := 0; x < cols; x++ {
		l, r := rows, rows
		if x > 0 {
			l = hs[x-1]
		}
		if x < cols-1 {
			r = hs[x+1]
		}
		if min(l, r)-hs[x] >= 3 {
			s.wells++
		}
	}
	return s
}

// plan simulates the taps that move the current piece to (rot, x) and
// returns them, or false if the spot is unreachable.
func (t *Tetris) plan(rot, x int) ([]string, piece, bool) {
	sim := *t
	var acts []string
	switch (rot - sim.cur.rot + 4) % 4 {
	case 1:
		sim.rotate(1)
		acts = append(acts, "rotate_cw")
	case 2:
		sim.rotate(1)
		sim.rotate(1)
		acts = append(acts, "rotate_cw", "rotate_cw")
	case 3:
		sim.rotate(-1)
		acts = append(acts, "rotate_ccw")
	}
	if sim.cur.rot != rot {
		return nil, piece{}, false
	}
	for sim.cur.x != x {
		dx, name := 1, "right"
		if x < sim.cur.x {
			dx, name = -1, "left"
		}
		p := sim.cur
		p.x += dx
		if !sim.try(p) {
			return nil, piece{}, false
		}
		acts = append(acts, name)
	}
	sim.cur.y += sim.dropDistance()
	return append(acts, "hard_drop"), sim.cur, true
}

// placements lists every distinct reachable placement, best first.
func (t *Tetris) placements() []placement {
	before := boardStats(&t.board)
	seen := map[string]bool{}
	var out []placement
	for rot := 0; rot < 4; rot++ {
		for x := -2; x < cols; x++ {
			acts, p, ok := t.plan(rot, x)
			if !ok {
				continue
			}
			b := t.board
			key := ""
			minC, maxC, top := cols, 0, 0
			for _, c := range cells[p.kind][p.rot] {
				top = max(top, rows-(p.y+c.y))
				cx, cy := p.x+c.x, p.y+c.y
				if cy >= 0 {
					b[cy][cx] = 1
				}
				minC, maxC = min(minC, cx), max(maxC, cx)
				key += fmt.Sprint(cx, cy, ";")
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			lines := 0
			for y := rows - 1; y >= 0; y-- {
				full := true
				for cx := 0; cx < cols; cx++ {
					full = full && b[y][cx] != 0
				}
				if full {
					lines++
					copy(b[1:y+1], b[0:y])
					b[0] = [cols]int{}
					y++
				}
			}
			s := boardStats(&b)
			out = append(out, placement{
				rot: rot, x: x, actions: acts, minC: minC, maxC: maxC, top: top,
				lines: lines, newHoles: s.holes - before.holes, height: s.height, aggregate: s.aggregate, bumpiness: s.bumpiness, wells: s.wells,
				// Weights from Yiyuan Lee's near-perfect Tetris heuristic.
				value: -0.51*float64(s.aggregate) + 0.76*float64(lines) - 0.36*float64(s.holes) - 0.18*float64(s.bumpiness),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].value > out[j].value })
	return out
}

// spots are every reachable placement, left to right. Each is offered to
// the model as its own yes/no-style question.
func (t *Tetris) spots() []placement {
	ps := t.placements()
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].minC != ps[j].minC {
			return ps[i].minC < ps[j].minC
		}
		return ps[i].rot < ps[j].rot
	})
	return ps
}

var (
	holeWords = []string{"no holes", "one hole", "two holes", "three holes", "many holes"}
	lineWords = []string{"", "one line", "two lines", "three lines", "four lines"}
	bumpWords = []string{"no bump", "a small bump", "a big bump", "a tall tower"}
)

func grade(v, from, step int) int {
	if v <= from {
		return 0
	}
	return min(3, (v-from+step-1)/step)
}

// describe turns placement p into one sentence. The model cannot count or
// compare, so the arithmetic happens here and it gets the conclusions in
// words. snug says p leaves the flattest top of the spots with the same holes
// and lines, which separates spots that would otherwise read the same.
func (p placement) describe(before stats, snug bool) string {
	bump := max(grade(p.bumpiness-before.bumpiness, 0, 2), grade(p.top*cols-before.aggregate, 3*cols, cols))
	s := fmt.Sprintf("The piece leaves %s under it and makes %s on top.", holeWords[p.holeClass()], bumpWords[bump])
	if p.lines > 0 {
		s += fmt.Sprintf(" It clears %s.", lineWords[p.lines])
	}
	if snug {
		s += " It fits snugly."
	} else {
		s += " It fits loosely."
	}
	switch {
	case p.wells > before.wells:
		s += " It leaves a deep well."
	case p.wells < before.wells:
		s += " It fills a deep well."
	}
	return s
}

func (p placement) holeClass() int { return min(4, max(0, p.newHoles)) }

var lookQuestion = map[string]any{"look": game.Choice("How does the stack look after the piece lands?", map[string]string{
	"clean": "flat with no holes", "messy": "holes or a tall tower"})}

// sentences maps each spot to the key of its sentence. Spots that read the
// same share a key, so each distinct sentence is asked once.
func (t *Tetris) sentences(spots []placement) (keys []string, text map[string]string) {
	before := boardStats(&t.board)
	byText := map[string]string{}
	text = map[string]string{}
	// The flattest top among the spots with the same holes and lines.
	flattest := map[[2]int]int{}
	for _, p := range spots {
		c := [2]int{p.holeClass(), p.lines}
		if f, ok := flattest[c]; !ok || p.bumpiness < f {
			flattest[c] = p.bumpiness
		}
	}
	for _, p := range spots {
		d := p.describe(before, p.bumpiness == flattest[[2]int{p.holeClass(), p.lines}])
		k, ok := byText[d]
		if !ok {
			// Keys carry the piece number so late answers about an earlier
			// piece are never applied to this one.
			k = fmt.Sprintf("p%ds%d", t.pieces, len(byText)+1)
			byText[d], text[k] = k, d
		}
		keys = append(keys, k)
	}
	return keys, text
}

func (t *Tetris) Situation() string {
	if t.over {
		return "The game is over."
	}
	_, text := t.sentences(t.spots())
	return fmt.Sprintf("The falling piece is %s. Laya reads %d distinct landing spots, one sentence each, and the piece goes where P(clean) is highest.", pieceName(t.cur.kind), len(text))
}

func pieceName(k int) string {
	return []string{"an I", "an O", "a T", "an S", "a Z", "a J", "an L"}[k]
}

// asked remembers the spots described to the model for one piece, so its
// answers are applied to exactly those spots even if they arrive after the
// piece has fallen a little.
type asked struct {
	piece int
	spots []placement
	keys  []string
}

func (t *Tetris) Prompt() game.Prompt {
	if t.over || t.planned == t.pieces {
		return game.Prompt{Batch: map[string]game.Prompt{}} // nothing to ask while placing
	}
	spots := t.spots()
	keys, text := t.sentences(spots)
	t.asked = asked{piece: t.pieces, spots: spots, keys: keys}
	batch := map[string]game.Prompt{}
	for k, d := range text {
		batch[k] = game.Prompt{State: d, Questions: lookQuestion}
	}
	return game.Prompt{Batch: batch}
}

func (t *Tetris) Decide(a game.Answers) game.Decision {
	if t.over {
		return game.Decision{Actions: []string{"noop"}, Note: "game over"}
	}
	if t.planned == t.pieces {
		return game.Decision{Keep: true, Note: "placing this piece"}
	}
	if t.asked.piece != t.pieces {
		return game.Decision{Actions: []string{"noop"}, Note: "answers were for an earlier piece"}
	}
	// Rank the spots that were asked about, best answer first, and take the
	// first one the piece can still reach from where it is now. Spots that
	// share a sentence get the same answer; among them the game's own ranking
	// decides, which for spots that read the same prefers the flattest top.
	order := make([]int, 0, len(t.asked.spots))
	for i, k := range t.asked.keys {
		if _, ok := a[k+".look"]; ok {
			order = append(order, i)
		}
	}
	pClean := func(i int) float64 { return a[t.asked.keys[i]+".look"].Probabilities["clean"] }
	sort.SliceStable(order, func(x, y int) bool {
		px, py := pClean(order[x]), pClean(order[y])
		if px == py {
			return t.asked.spots[order[x]].value > t.asked.spots[order[y]].value
		}
		return px > py
	})
	_, text := t.sentences(t.asked.spots)
	for n, i := range order {
		p := t.asked.spots[i]
		if acts, _, ok := t.plan(p.rot, p.x); ok {
			t.planned = t.pieces
			note := fmt.Sprintf("P(clean) %.2f: %s", pClean(i), text[t.asked.keys[i]])
			if n > 0 {
				note += fmt.Sprintf(" (choice %d; better spots were out of reach)", n+1)
			}
			return game.Decision{Actions: acts, Note: note}
		}
	}
	return game.Decision{Actions: []string{"noop"}, Note: "no answered spot is reachable"}
}

func (t *Tetris) Oracle() game.Answers {
	a := game.Answers{}
	if t.over || t.planned == t.pieces {
		return a
	}
	t.Prompt() // answer exactly the questions the model would be asked
	best := -1
	for i, p := range t.asked.spots {
		if best < 0 || p.value > t.asked.spots[best].value {
			best = i
		}
	}
	for i, k := range t.asked.keys {
		if _, done := a[k+".look"]; done && i != best {
			continue
		}
		c := 0.0
		if t.asked.keys[best] == k {
			c = 1
		}
		a[k+".look"] = game.Answer{Type: "choice", Probabilities: map[string]float64{"clean": c, "messy": 1 - c}}
	}
	return a
}
