// Package frogger is a road-and-river crossing game.
package frogger

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"system1/internal/game"
)

const (
	cols   = 13
	rows   = 13 // 0 = homes, 1-5 river, 6 median, 7-11 road, 12 start
	cell   = 32
	top    = 32 // HUD height
	width  = cols * cell
	height = top + rows*cell + 32

	hopCooldown = 8
	dieTicks    = 50
	frogTime    = 30 * game.TickRate
	startLives  = 3
)

var homeCols = []int{2, 4, 6, 8, 10}

type kind int

const (
	safe kind = iota
	road
	river
	home
)

type laneSpec struct {
	row    int
	kind   kind
	speed  float64 // px/tick; negative moves left
	length int     // cells per object
	gap    int     // cells between objects
	char   byte    // observation glyph
	color  string
}

var laneSpecs = []laneSpec{
	{1, river, 0.9, 4, 3, 'L', "#8a5a2b"},
	{2, river, -1.1, 2, 2, 'T', "#2fa66a"},
	{3, river, 1.6, 6, 4, 'L', "#8a5a2b"},
	{4, river, 0.6, 3, 3, 'L', "#8a5a2b"},
	{5, river, -0.8, 3, 2, 'T', "#2fa66a"},
	{7, road, -1.2, 2, 4, 'K', "#d8d8d8"},
	{8, road, 1.0, 1, 4, 'C', "#e04d9b"},
	{9, road, -2.0, 1, 5, 'C', "#f2f2f2"},
	{10, road, 0.8, 1, 3, 'C', "#e8d23a"},
	{11, road, -0.7, 1, 3, 'C', "#e8783a"},
}

type lane struct {
	spec  laneSpec
	speed float64
	xs    []float64 // left edge of each object, in px
}

func (l *lane) span() float64 {
	return float64(len(l.xs) * (l.spec.length + l.spec.gap) * cell)
}

// covers reports whether any object in the lane covers px range [a, b].
func (l *lane) covers(a, b float64) bool {
	w := float64(l.spec.length * cell)
	for _, x := range l.xs {
		if b > x && a < x+w {
			return true
		}
	}
	return false
}

type Frogger struct {
	rng    *rand.Rand
	lanes  map[int]*lane
	fx     float64 // frog left edge in px
	frow   int
	best   int // furthest row reached this life (lower is further)
	hopCD  int
	dying  int
	timer  int
	homes  [5]bool
	score  int
	lives  int
	level  int
	over   bool
	facing game.Button
	delay  int // ticks until the agent's next press can happen
	gap    int // minimum ticks between the agent's presses
}

func New() *Frogger { f := &Frogger{}; f.Reset(1); return f }

func (f *Frogger) Info() game.Info {
	return game.Info{
		ID:       "frogger",
		Title:    "Frogger",
		Controls: "Arrow keys hop one square",
		Width:    width,
		Height:   height,
		Sprites:  sprites,
		Actions: []game.Action{
			{Name: "noop", Description: "stay in place this turn", HoldTicks: 1},
			{Name: "up", Description: "hop forward one row toward the homes", Buttons: []game.Button{game.Up}, HoldTicks: 1},
			{Name: "down", Description: "hop back one row toward the start", Buttons: []game.Button{game.Down}, HoldTicks: 1},
			{Name: "left", Description: "hop one square left", Buttons: []game.Button{game.Left}, HoldTicks: 1},
			{Name: "right", Description: "hop one square right", Buttons: []game.Button{game.Right}, HoldTicks: 1},
		},
	}
}

func (f *Frogger) Instructions() string {
	return "You are the frog (@) in Frogger. Choose the next hop to reach an empty home (_) at the top. Cars (C) and trucks (K) on the road kill you; in the river you must stand on logs (L) or turtles (T), because water (~) kills you."
}

func (f *Frogger) Reset(seed int64) {
	*f = Frogger{rng: rand.New(rand.NewSource(seed)), lives: startLives}
	f.setupLanes()
	f.respawn()
}

func (f *Frogger) setupLanes() {
	f.lanes = map[int]*lane{}
	mult := 1 + 0.15*float64(f.level)
	for _, s := range laneSpecs {
		l := &lane{spec: s, speed: s.speed * mult}
		// The wrap span must exceed screen + one object so nothing pops in visibly.
		n := int(math.Ceil(float64(cols+3+s.length) / float64(s.length+s.gap)))
		off := f.rng.Float64() * float64((s.length+s.gap)*cell)
		for i := 0; i < n; i++ {
			l.xs = append(l.xs, off+float64(i*(s.length+s.gap)*cell)-float64(3*cell))
		}
		f.lanes[s.row] = l
	}
}

func (f *Frogger) respawn() {
	f.fx = float64(6 * cell)
	f.frow = rows - 1
	f.best = f.frow
	f.timer = frogTime
	f.facing = game.Up
}

func (f *Frogger) die() {
	f.dying = dieTicks
}

func (f *Frogger) Tick(in game.Input) {
	if f.over {
		return
	}
	for _, l := range f.lanes {
		span := l.span()
		for i := range l.xs {
			l.xs[i] += l.speed
			if l.speed > 0 && l.xs[i] > float64(width+cell) {
				l.xs[i] -= span
			} else if l.speed < 0 && l.xs[i]+float64(l.spec.length*cell) < -float64(cell) {
				l.xs[i] += span
			}
		}
	}
	if f.dying > 0 {
		f.dying--
		if f.dying == 0 {
			f.lives--
			if f.lives <= 0 {
				f.over = true
				return
			}
			f.respawn()
		}
		return
	}
	f.timer--
	if f.timer <= 0 {
		f.die()
		return
	}
	if f.hopCD > 0 {
		f.hopCD--
	} else {
		for _, b := range []game.Button{game.Up, game.Down, game.Left, game.Right} {
			if in.Hit(b) {
				f.hop(b)
				break
			}
		}
	}
	if f.dying > 0 {
		return
	}
	if l := f.lanes[f.frow]; l != nil && l.spec.kind == river {
		f.fx += l.speed
	}
	f.checkCollision()
}

func (f *Frogger) hop(b game.Button) {
	f.facing = b
	f.hopCD = hopCooldown
	switch b {
	case game.Up:
		f.frow--
	case game.Down:
		if f.frow < rows-1 {
			f.frow++
		}
	case game.Left:
		f.fx = math.Max(0, f.fx-cell)
	case game.Right:
		f.fx = math.Min(float64(width-cell), f.fx+cell)
	}
	if f.frow < f.best {
		f.best = f.frow
		f.score += 10
	}
	if f.frow == 0 {
		f.reachHome()
	}
}

func (f *Frogger) reachHome() {
	c := f.col()
	for i, hc := range homeCols {
		if hc == c && !f.homes[i] {
			f.homes[i] = true
			f.score += 50 + f.timer/game.TickRate*10
			done := true
			for _, h := range f.homes {
				done = done && h
			}
			if done {
				f.score += 1000
				f.level++
				f.homes = [5]bool{}
				f.setupLanes()
			}
			f.respawn()
			return
		}
	}
	f.die() // hit the bank or an occupied home
}

func (f *Frogger) col() int { return int(math.Floor((f.fx + cell/2) / cell)) }

func (f *Frogger) checkCollision() {
	l := f.lanes[f.frow]
	if l == nil {
		return
	}
	switch l.spec.kind {
	case road:
		if l.covers(f.fx+6, f.fx+cell-6) {
			f.die()
		}
	case river:
		c := f.fx + cell/2
		if !l.covers(c-2, c+2) || f.fx < -cell/2 || f.fx > float64(width-cell/2) {
			f.die()
		}
	}
}

func (f *Frogger) Status() game.Status {
	s := game.Status{Score: f.score, Lives: f.lives, Level: f.level, Over: f.over}
	if f.over {
		s.Message = "GAME OVER"
	}
	return s
}

func rowY(r int) float64 { return float64(top + r*cell) }

func (f *Frogger) Frame() game.Frame {
	fr := game.Frame{W: width, H: height, Bg: "#000000"}
	R := func(x, y, w, h float64, c string) {
		fr.Rects = append(fr.Rects, game.Rect{X: x, Y: y, W: w, H: h, C: c})
	}
	R(0, rowY(0), width, cell, "#1c5e2a")
	for i, hc := range homeCols {
		R(float64(hc*cell+2), rowY(0)+4, cell-4, cell-4, "#0a1f5c")
		if f.homes[i] {
			fr.Sprites = append(fr.Sprites, game.Sprite{ID: "frog_up", X: float64(hc*cell + 4), Y: rowY(0) + 4, C: "#3de25b", Scale: 3})
		}
	}
	R(0, rowY(1), width, 5*cell, "#0a1f5c")
	R(0, rowY(6), width, cell, "#5b2e8c")
	R(0, rowY(7), width, 5*cell, "#101010")
	R(0, rowY(12), width, cell, "#5b2e8c")
	for r := 8; r <= 11; r++ {
		for x := 0; x < width; x += 24 {
			R(float64(x), rowY(r)-1, 12, 2, "#555")
		}
	}
	for _, l := range f.lanes {
		y := rowY(l.spec.row)
		w := float64(l.spec.length * cell)
		for _, x := range l.xs {
			switch {
			case l.spec.char == 'T':
				for i := 0; i < l.spec.length; i++ {
					cx := x + float64(i*cell)
					R(cx+3, y+5, cell-6, cell-10, l.spec.color)
					R(cx+8, y+10, cell-16, cell-20, "#1b6b43")
				}
			case l.spec.kind == river:
				R(x+2, y+5, w-4, cell-10, l.spec.color)
				R(x+6, y+13, w-12, 2, "#6b4420")
			default:
				R(x+2, y+6, w-4, cell-12, l.spec.color)
				front := x + w - 10
				if l.speed < 0 {
					front = x + 4
				}
				R(front, y+8, 6, cell-16, "#40a0ff")
			}
		}
	}
	if f.dying > 0 {
		fr.Sprites = append(fr.Sprites, game.Sprite{ID: "splat", X: f.fx + 4, Y: rowY(f.frow) + 4, C: "#ff4040", Scale: 3})
	} else if !f.over {
		fr.Sprites = append(fr.Sprites, game.Sprite{ID: "frog_" + string(f.facing), X: f.fx + 4, Y: rowY(f.frow) + 4, C: "#3de25b", Scale: 3})
	}
	T := func(x, y float64, s, c string, size float64, align string) {
		fr.Texts = append(fr.Texts, game.Text{X: x, Y: y, S: s, C: c, Size: size, Align: align})
	}
	T(8, 22, fmt.Sprintf("SCORE %d", f.score), "#fff", 16, "")
	T(width-8, 22, fmt.Sprintf("LEVEL %d", f.level+1), "#fff", 16, "right")
	by := float64(top + rows*cell)
	for i := 0; i < f.lives-1; i++ {
		fr.Sprites = append(fr.Sprites, game.Sprite{ID: "frog_up", X: float64(8 + i*20), Y: by + 10, C: "#3de25b", Scale: 2})
	}
	tw := float64(160) * float64(f.timer) / frogTime
	tc := "#3de25b"
	if f.timer < 10*game.TickRate {
		tc = "#ff5040"
	}
	R(width-8-tw, by+12, tw, 10, tc)
	T(width-176, by+22, "TIME", "#ff0", 13, "right")
	if f.over {
		R(0, float64(height/2-40), width, 80, "#000000cc")
		T(width/2, float64(height/2), "GAME OVER", "#ff5060", 28, "center")
		T(width/2, float64(height/2+26), "press Enter", "#aab", 13, "center")
	}
	return fr
}

// grid renders the playfield as rows of glyphs, frog as '@'.
func (f *Frogger) grid() [][]byte {
	g := make([][]byte, rows)
	for r := range g {
		g[r] = make([]byte, cols)
		for c := range g[r] {
			a, b := float64(c*cell+cell/2-2), float64(c*cell+cell/2+2)
			switch l := f.lanes[r]; {
			case r == 0:
				g[r][c] = '='
			case r == 6 || r == rows-1:
				g[r][c] = '-'
			case l.spec.kind == river:
				g[r][c] = '~'
				if l.covers(a, b) {
					g[r][c] = l.spec.char
				}
			default:
				g[r][c] = '.'
				if l.covers(a, b) {
					g[r][c] = l.spec.char
				}
			}
		}
	}
	for i, hc := range homeCols {
		g[0][hc] = '_'
		if f.homes[i] {
			g[0][hc] = 'F'
		}
	}
	return g
}

var glyphName = map[byte]string{
	'=': "wall", '_': "empty home", 'F': "filled home", '-': "safe ground",
	'~': "water", 'L': "log", 'T': "turtles", '.': "empty road", 'C': "car", 'K': "truck",
}

func (f *Frogger) Observe() map[string]any {
	g := f.grid()
	c := max(0, min(cols-1, f.col()))
	look := func(r, cc int) string {
		if r < 0 || r >= rows || cc < 0 || cc >= cols {
			return "edge"
		}
		return glyphName[g[r][cc]]
	}
	neighbors := map[string]string{
		"here":  look(f.frow, c),
		"up":    look(f.frow-1, c),
		"down":  look(f.frow+1, c),
		"left":  look(f.frow, c-1),
		"right": look(f.frow, c+1),
	}
	g[f.frow][c] = '@'
	lines := make([]string, rows)
	for r := range g {
		lines[r] = string(g[r])
	}
	var lanes []string
	for r := 1; r < rows-1; r++ {
		if l := f.lanes[r]; l != nil {
			dir := "right"
			if l.speed < 0 {
				dir = "left"
			}
			lanes = append(lanes, fmt.Sprintf("row %d %s moving %s %.1f", r, map[kind]string{road: "road", river: "river"}[l.spec.kind], dir, math.Abs(l.speed)))
		}
	}
	return map[string]any{
		"frog_row":       f.frow,
		"frog_col":       c,
		"rows_to_go":     f.frow,
		"neighbors":      neighbors,
		"field":          strings.Join(lines, "\n"),
		"lanes":          lanes,
		"homes_filled":   f.homes,
		"time_left_secs": f.timer / game.TickRate,
		"can_hop":        f.hopCD == 0 && f.dying == 0,
		"dying":          f.dying > 0,
		"lives":          f.lives,
		"score":          f.score,
		"level":          f.level + 1,
		"game_over":      f.over,
	}
}

var sprites = map[string][]string{
	"frog_up": {
		"X.X..X.X",
		"XXX..XXX",
		".XXXXXX.",
		"..XXXX..",
		".XXXXXX.",
		"XXXXXXXX",
		"X.XXXX.X",
		"X......X",
	},
	"frog_down": {
		"X......X",
		"X.XXXX.X",
		"XXXXXXXX",
		".XXXXXX.",
		"..XXXX..",
		".XXXXXX.",
		"XXX..XXX",
		"X.X..X.X",
	},
	"frog_left": {
		"XX...XXX",
		"X.XXXX..",
		"..XXXXXX",
		"XXXXXX..",
		"XXXXXX..",
		"..XXXXXX",
		"X.XXXX..",
		"XX...XXX",
	},
	"frog_right": {
		"XXX...XX",
		"..XXXX.X",
		"XXXXXX..",
		"..XXXXXX",
		"..XXXXXX",
		"XXXXXX..",
		"..XXXX.X",
		"XXX...XX",
	},
	"splat": {
		"X..X..X.",
		".X.X.X..",
		"..XXX..X",
		"XXX.XXX.",
		".XXX.XX.",
		"X.XXX..X",
		"..X.X.X.",
		".X..X..X",
	},
}
