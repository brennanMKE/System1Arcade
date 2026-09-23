// Package tetris is a NES-flavored falling-block game.
package tetris

import (
	"fmt"
	"math/rand"
	"strings"

	"system1/internal/game"
)

const (
	cols = 10
	rows = 20
	cell = 24
	ox   = 20 // playfield origin
	oy   = 20

	dasDelay  = 10 // ticks before a held left/right auto-repeats
	dasRepeat = 3
	softDrop  = 2 // ticks per row while down is held
	spawnX    = 3
)

const names = "IOTSZJL"

var colors = []string{"", "#3fd0e0", "#f0d040", "#b060e0", "#50d060", "#e05050", "#4070e0", "#f09030"}

// Base shapes in an n×n box; rotations are derived.
var shapes = [7][]string{
	{"....", "####", "....", "...."},
	{"##", "##"},
	{".#.", "###", "..."},
	{".##", "##.", "..."},
	{"##.", ".##", "..."},
	{"#..", "###", "..."},
	{"..#", "###", "..."},
}

type pt struct{ x, y int }

// cells[kind][rot] -> the four occupied offsets.
var cells [7][4][4]pt

func init() {
	for k, s := range shapes {
		n := len(s)
		grid := make([][]bool, n)
		for y := range s {
			grid[y] = make([]bool, n)
			for x := range s[y] {
				grid[y][x] = s[y][x] == '#'
			}
		}
		for r := 0; r < 4; r++ {
			i := 0
			for y := 0; y < n; y++ {
				for x := 0; x < n; x++ {
					if grid[y][x] {
						cells[k][r][i] = pt{x, y}
						i++
					}
				}
			}
			// rotate clockwise: (x, y) -> (n-1-y, x)
			next := make([][]bool, n)
			for y := range next {
				next[y] = make([]bool, n)
			}
			for y := 0; y < n; y++ {
				for x := 0; x < n; x++ {
					next[x][n-1-y] = grid[y][x]
				}
			}
			grid = next
		}
	}
}

var gravity = []int{48, 43, 38, 33, 28, 23, 18, 13, 8, 6, 5, 5, 5, 4, 4, 4, 3, 3, 3, 2}

type piece struct{ kind, rot, x, y int }

type Tetris struct {
	rng   *rand.Rand
	board [rows][cols]int // 0 empty, else kind+1
	cur   piece
	bag   []int
	next  int

	fall     int // ticks since last gravity step
	dasLeft  int
	dasRight int
	softTick int

	score, lines, level int
	over                bool

	pieces  int // pieces spawned so far
	planned int // piece number the current plan is for
	asked   asked
}

func New() *Tetris { t := &Tetris{}; t.Reset(1); return t }

func (t *Tetris) Info() game.Info {
	return game.Info{
		ID:       "tetris",
		Title:    "Tetris",
		Controls: "←/→ move · ↑ rotate · Z rotate back · ↓ soft drop · Space hard drop",
		Width:    ox*2 + cols*cell + 140,
		Height:   oy*2 + rows*cell,
		Actions: []game.Action{
			game.Noop,
			{Name: "left", Description: "shift the falling piece one column left", Buttons: []game.Button{game.Left}, HoldTicks: 1},
			{Name: "right", Description: "shift the falling piece one column right", Buttons: []game.Button{game.Right}, HoldTicks: 1},
			{Name: "rotate_cw", Description: "rotate the falling piece clockwise", Buttons: []game.Button{game.Up}, HoldTicks: 1},
			{Name: "rotate_ccw", Description: "rotate the falling piece counterclockwise", Buttons: []game.Button{game.B}, HoldTicks: 1},
			{Name: "soft_drop", Description: "move the falling piece down one row", Buttons: []game.Button{game.Down}, HoldTicks: 1},
			{Name: "hard_drop", Description: "drop the piece straight down and lock it; use only when it is above the right spot", Buttons: []game.Button{game.A}, HoldTicks: 1},
		},
	}
}

func (t *Tetris) Instructions() string {
	return "You are playing Tetris. Pick the next control input that moves the falling piece toward a placement that keeps the stack low and flat, avoids holes, and completes rows."
}

func (t *Tetris) Reset(seed int64) {
	*t = Tetris{rng: rand.New(rand.NewSource(seed))}
	t.next = t.draw()
	t.spawn()
}

func (t *Tetris) draw() int {
	if len(t.bag) == 0 {
		t.bag = t.rng.Perm(7)
	}
	k := t.bag[0]
	t.bag = t.bag[1:]
	return k
}

func (t *Tetris) spawn() {
	t.cur = piece{kind: t.next, x: spawnX}
	if t.cur.kind == 1 { // O is 2 wide
		t.cur.x = 4
	}
	t.next = t.draw()
	t.fall = 0
	t.pieces++
	if !t.fits(t.cur) {
		t.over = true
	}
}

func (t *Tetris) fits(p piece) bool {
	for _, c := range cells[p.kind][p.rot] {
		x, y := p.x+c.x, p.y+c.y
		if x < 0 || x >= cols || y >= rows {
			return false
		}
		if y >= 0 && t.board[y][x] != 0 {
			return false
		}
	}
	return true
}

func (t *Tetris) try(p piece) bool {
	if t.fits(p) {
		t.cur = p
		return true
	}
	return false
}

func (t *Tetris) rotate(dir int) {
	p := t.cur
	p.rot = (p.rot + dir + 4) % 4
	for _, k := range []pt{{0, 0}, {-1, 0}, {1, 0}, {-2, 0}, {2, 0}, {0, -1}} {
		q := p
		q.x += k.x
		q.y += k.y
		if t.try(q) {
			return
		}
	}
}

func (t *Tetris) lock() {
	for _, c := range cells[t.cur.kind][t.cur.rot] {
		x, y := t.cur.x+c.x, t.cur.y+c.y
		if y < 0 {
			t.over = true
			return
		}
		t.board[y][x] = t.cur.kind + 1
	}
	cleared := 0
	for y := rows - 1; y >= 0; y-- {
		full := true
		for x := 0; x < cols; x++ {
			if t.board[y][x] == 0 {
				full = false
				break
			}
		}
		if full {
			cleared++
			copy(t.board[1:y+1], t.board[0:y])
			t.board[0] = [cols]int{}
			y++ // re-check the row that moved down
		}
	}
	t.score += []int{0, 40, 100, 300, 1200}[cleared] * (t.level + 1)
	t.lines += cleared
	t.level = t.lines / 10
	t.spawn()
}

// stepDown moves the piece down one row, locking it if it can't.
func (t *Tetris) stepDown() bool {
	p := t.cur
	p.y++
	if t.try(p) {
		return true
	}
	t.lock()
	return false
}

func (t *Tetris) dropDistance() int {
	p := t.cur
	d := 0
	for {
		p.y++
		if !t.fits(p) {
			return d
		}
		d++
	}
}

func (t *Tetris) Tick(in game.Input) {
	if t.over {
		return
	}
	shift := func(b game.Button, das *int, dx int) {
		if !in.Down(b) {
			*das = 0
			return
		}
		if in.Hit(b) || *das >= dasDelay && (*das-dasDelay)%dasRepeat == 0 {
			p := t.cur
			p.x += dx
			t.try(p)
		}
		*das++
	}
	shift(game.Left, &t.dasLeft, -1)
	shift(game.Right, &t.dasRight, 1)
	if in.Hit(game.Up) {
		t.rotate(1)
	}
	if in.Hit(game.B) {
		t.rotate(-1)
	}
	if in.Hit(game.A) {
		d := t.dropDistance()
		t.cur.y += d
		t.score += 2 * d
		t.lock()
		return
	}
	if in.Down(game.Down) {
		if in.Hit(game.Down) || t.softTick%softDrop == 0 {
			t.fall = 0
			if t.stepDown() {
				t.score++
			} else {
				return
			}
		}
		t.softTick++
	} else {
		t.softTick = 0
	}
	t.fall++
	g := gravity[min(t.level, len(gravity)-1)]
	if t.fall >= g {
		t.fall = 0
		t.stepDown()
	}
}

func (t *Tetris) Status() game.Status {
	s := game.Status{Score: t.score, Level: t.level, Lives: 1, Over: t.over}
	if t.over {
		s.Message = "GAME OVER"
	}
	return s
}

func (t *Tetris) Frame() game.Frame {
	info := t.Info()
	f := game.Frame{W: info.Width, H: info.Height, Bg: "#0b0d17"}
	f.Rects = append(f.Rects, game.Rect{X: ox - 2, Y: oy - 2, W: cols*cell + 4, H: rows*cell + 4, C: "#2a2f45"})
	f.Rects = append(f.Rects, game.Rect{X: ox, Y: oy, W: cols * cell, H: rows * cell, C: "#11142a"})
	block := func(x, y float64, c string) {
		f.Rects = append(f.Rects, game.Rect{X: x + 1, Y: y + 1, W: cell - 2, H: cell - 2, C: c})
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			if k := t.board[y][x]; k != 0 {
				block(float64(ox+x*cell), float64(oy+y*cell), colors[k])
			}
		}
	}
	if !t.over {
		d := t.dropDistance()
		for _, c := range cells[t.cur.kind][t.cur.rot] {
			x, y := t.cur.x+c.x, t.cur.y+c.y+d
			if y >= 0 {
				block(float64(ox+x*cell), float64(oy+y*cell), "#ffffff22")
			}
		}
		for _, c := range cells[t.cur.kind][t.cur.rot] {
			x, y := t.cur.x+c.x, t.cur.y+c.y
			if y >= 0 {
				block(float64(ox+x*cell), float64(oy+y*cell), colors[t.cur.kind+1])
			}
		}
	}
	px := float64(ox + cols*cell + 24)
	label := func(y float64, s string, size float64, c string) {
		f.Texts = append(f.Texts, game.Text{X: px, Y: y, S: s, C: c, Size: size})
	}
	label(oy+14, "NEXT", 14, "#8890b0")
	for _, c := range cells[t.next][0] {
		f.Rects = append(f.Rects, game.Rect{X: px + float64(c.x*18), Y: oy + 30 + float64(c.y*18), W: 16, H: 16, C: colors[t.next+1]})
	}
	label(oy+130, "SCORE", 14, "#8890b0")
	label(oy+154, fmt.Sprint(t.score), 20, "#ffffff")
	label(oy+194, "LINES", 14, "#8890b0")
	label(oy+218, fmt.Sprint(t.lines), 20, "#ffffff")
	label(oy+258, "LEVEL", 14, "#8890b0")
	label(oy+282, fmt.Sprint(t.level), 20, "#ffffff")
	if t.over {
		f.Rects = append(f.Rects, game.Rect{X: ox, Y: oy + rows*cell/2 - 40, W: cols * cell, H: 80, C: "#000000cc"})
		f.Texts = append(f.Texts,
			game.Text{X: ox + cols*cell/2, Y: oy + rows*cell/2 - 4, S: "GAME OVER", C: "#ff5060", Size: 26, Align: "center"},
			game.Text{X: ox + cols*cell/2, Y: oy + rows*cell/2 + 24, S: "press Enter", C: "#aab", Size: 13, Align: "center"})
	}
	return f
}

func (t *Tetris) Observe() map[string]any {
	grid := make([][]byte, rows)
	for y := range grid {
		grid[y] = make([]byte, cols)
		for x := range grid[y] {
			grid[y][x] = '.'
			if t.board[y][x] != 0 {
				grid[y][x] = '#'
			}
		}
	}
	heights := make([]int, cols)
	holes, bump, maxH := 0, 0, 0
	for x := 0; x < cols; x++ {
		for y := 0; y < rows; y++ {
			if t.board[y][x] != 0 {
				heights[x] = rows - y
				for yy := y + 1; yy < rows; yy++ {
					if t.board[yy][x] == 0 {
						holes++
					}
				}
				break
			}
		}
		maxH = max(maxH, heights[x])
		if x > 0 {
			bump += abs(heights[x] - heights[x-1])
		}
	}
	minX, maxX := cols, 0
	for _, c := range cells[t.cur.kind][t.cur.rot] {
		x, y := t.cur.x+c.x, t.cur.y+c.y
		minX, maxX = min(minX, x), max(maxX, x)
		if y >= 0 && !t.over {
			grid[y][x] = '@'
		}
	}
	board := make([]string, rows)
	for y := range grid {
		board[y] = string(grid[y])
	}
	return map[string]any{
		"piece":          string(names[t.cur.kind]),
		"rotation":       t.cur.rot,
		"piece_columns":  []int{minX, maxX},
		"next_piece":     string(names[t.next]),
		"drop_distance":  t.dropDistance(),
		"column_heights": heights,
		"max_height":     maxH,
		"holes":          holes,
		"bumpiness":      bump,
		"board":          strings.Join(board, "\n"),
		"lines":          t.lines,
		"level":          t.level,
		"score":          t.score,
		"game_over":      t.over,
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
