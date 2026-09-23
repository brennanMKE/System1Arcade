// Package invaders is a fixed-shooter in the style of the 1978 arcade game.
package invaders

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"system1/internal/game"
)

const (
	width   = 224
	height  = 256
	playerY = 216
	playerW = 13
	margin  = 8
	gridR   = 5
	gridC   = 11
	cellW   = 16
	cellH   = 16
	shieldY = 192
	shieldW = 22
	shieldH = 16
	groundY = 239

	bulletSpeed = 5
	dieTicks    = 90
	startLives  = 3
)

var shieldXs = []int{32, 77, 122, 167}

type invType struct {
	sprite string
	w, off int
	points int
	color  string
}

var rowTypes = [gridR]invType{
	{"squid", 8, 4, 30, "#ff6ad5"},
	{"crab", 11, 2, 20, "#62e3ff"},
	{"crab", 11, 2, 20, "#62e3ff"},
	{"octopus", 12, 2, 10, "#7dff6a"},
	{"octopus", 12, 2, 10, "#7dff6a"},
}

type bullet struct {
	x, y float64
	on   bool
}

type boom struct {
	x, y float64
	t    int
	id   string
	c    string
}

type Invaders struct {
	rng *rand.Rand

	px     float64
	shot   bullet
	alive  [gridR][gridC]bool
	fx, fy int
	dir    int
	anim   int
	stepT  int
	bombs  []bullet
	bombT  int
	shield [4][shieldH][shieldW]bool

	ufoOn     bool
	ufoX      float64
	ufoDir    float64
	ufoT      int
	ufoScore  int
	ufoScoreT int
	ufoScoreX float64

	booms []boom
	dying int
	lives int
	score int
	level int
	over  bool
	ticks int
	delay int // ticks until the agent's next press can happen
	gap   int // minimum ticks between the agent's presses
}

func New() *Invaders { g := &Invaders{}; g.Reset(1); return g }

func (g *Invaders) Info() game.Info {
	return game.Info{
		ID:       "invaders",
		Title:    "Space Invaders",
		Controls: "←/→ move · Space fire",
		Width:    width,
		Height:   height,
		Sprites:  sprites,
		Actions: []game.Action{
			{Name: "noop", Description: "hold position this turn", HoldTicks: 1},
			{Name: "left", Description: "slide the cannon left", Buttons: []game.Button{game.Left}, HoldTicks: 6},
			{Name: "right", Description: "slide the cannon right", Buttons: []game.Button{game.Right}, HoldTicks: 6},
			{Name: "fire", Description: "shoot straight up; only one shot can be in the air at a time", Buttons: []game.Button{game.A}, HoldTicks: 1},
		},
	}
}

func (g *Invaders) Instructions() string {
	return "You are the laser cannon at the bottom in Space Invaders. Choose the next move: dodge enemy bombs falling toward you (threats with small dx and small dy), line up under an invader (target dx near 0) and fire when can_fire is true."
}

func (g *Invaders) Reset(seed int64) {
	*g = Invaders{rng: rand.New(rand.NewSource(seed)), lives: startLives}
	g.px = margin + 16
	g.newWave()
}

func (g *Invaders) newWave() {
	for r := range g.alive {
		for c := range g.alive[r] {
			g.alive[r][c] = true
		}
	}
	g.fx, g.fy, g.dir = 26, 56+8*min(g.level, 5), 1
	g.bombs = nil
	g.shot.on = false
	g.bombT = 60
	g.ufoT = 25 * game.TickRate
	for i := range g.shield {
		for y := 0; y < shieldH; y++ {
			for x := 0; x < shieldW; x++ {
				corner := y < 4 && (x < 4-y || x > shieldW-5+y)
				arch := y >= 12 && x >= 6 && x <= 15 || y == 11 && x >= 7 && x <= 14
				g.shield[i][y][x] = !corner && !arch
			}
		}
	}
}

func (g *Invaders) invRect(r, c int) (x, y, w, h int) {
	t := rowTypes[r]
	return g.fx + c*cellW + t.off, g.fy + r*cellH, t.w, 8
}

func (g *Invaders) aliveCount() int {
	n := 0
	for r := range g.alive {
		for c := range g.alive[r] {
			if g.alive[r][c] {
				n++
			}
		}
	}
	return n
}

// bounds of the living formation.
func (g *Invaders) bounds() (minX, maxX, bottom int) {
	minX, maxX = width, 0
	for r := range g.alive {
		for c := range g.alive[r] {
			if g.alive[r][c] {
				x, y, w, h := g.invRect(r, c)
				minX, maxX, bottom = min(minX, x), max(maxX, x+w), max(bottom, y+h)
			}
		}
	}
	return
}

// shooter returns the lowest living invader in column c.
func (g *Invaders) shooter(c int) (int, bool) {
	for r := gridR - 1; r >= 0; r-- {
		if g.alive[r][c] {
			return r, true
		}
	}
	return 0, false
}

func (g *Invaders) Tick(in game.Input) {
	if g.over {
		return
	}
	g.ticks++
	g.tickBooms()
	if g.ufoScoreT > 0 {
		g.ufoScoreT--
	}
	if g.dying > 0 {
		g.dying--
		if g.dying == 0 {
			g.lives--
			if g.lives <= 0 {
				g.over = true
			}
			g.px = margin + 16
		}
		return
	}

	if in.Down(game.Left) {
		g.px = math.Max(margin, g.px-1)
	}
	if in.Down(game.Right) {
		g.px = math.Min(width-margin-playerW, g.px+1)
	}
	if in.Hit(game.A) && !g.shot.on {
		g.shot = bullet{x: g.px + 6, y: playerY - 4, on: true}
	}

	g.moveShot()
	g.moveFormation()
	g.moveBombs()
	g.moveUFO()

	if g.aliveCount() == 0 {
		g.level++
		g.newWave()
	}
}

func (g *Invaders) tickBooms() {
	out := g.booms[:0]
	for _, b := range g.booms {
		if b.t--; b.t > 0 {
			out = append(out, b)
		}
	}
	g.booms = out
}

func (g *Invaders) moveShot() {
	if !g.shot.on {
		return
	}
	for i := 0; i < bulletSpeed; i++ { // sub-step so nothing tunnels
		g.shot.y--
		x, y := int(g.shot.x), int(g.shot.y)
		if y < 32 {
			g.shot.on = false
			g.booms = append(g.booms, boom{g.shot.x - 4, 32, 12, "shotboom", "#ff5050"})
			return
		}
		if g.hitShield(x, y, 1) {
			g.shot.on = false
			return
		}
		for r := range g.alive {
			for c := range g.alive[r] {
				if !g.alive[r][c] {
					continue
				}
				ix, iy, w, h := g.invRect(r, c)
				if x >= ix && x < ix+w && y >= iy && y < iy+h {
					g.alive[r][c] = false
					g.shot.on = false
					g.score += rowTypes[r].points
					g.booms = append(g.booms, boom{float64(ix + w/2 - 6), float64(iy), 14, "explode", rowTypes[r].color})
					return
				}
			}
		}
		if g.ufoOn && y >= 40 && y < 47 && float64(x) >= g.ufoX && float64(x) < g.ufoX+16 {
			g.ufoOn = false
			g.shot.on = false
			g.ufoScore = []int{50, 100, 150, 300}[g.rng.Intn(4)]
			g.score += g.ufoScore
			g.ufoScoreT = 60
			g.ufoScoreX = g.ufoX
			return
		}
	}
}

func (g *Invaders) moveFormation() {
	g.stepT++
	interval := 1 + g.aliveCount()*2/5
	if g.stepT < interval {
		return
	}
	g.stepT = 0
	g.anim ^= 1
	minX, maxX, _ := g.bounds()
	if g.dir > 0 && maxX+2 > width-margin || g.dir < 0 && minX-2 < margin {
		g.fy += 8
		g.dir = -g.dir
	} else {
		g.fx += 2 * g.dir
	}
	_, _, bottom := g.bounds()
	// Invaders chew through shields they touch.
	for r := range g.alive {
		for c := range g.alive[r] {
			if !g.alive[r][c] {
				continue
			}
			x, y, w, h := g.invRect(r, c)
			for yy := y; yy < y+h; yy++ {
				for xx := x; xx < x+w; xx++ {
					g.clearShieldPx(xx, yy)
				}
			}
		}
	}
	if bottom >= playerY {
		g.lives = 0
		g.over = true
	}
}

func (g *Invaders) moveBombs() {
	speed := math.Min(2+0.25*float64(g.level), 4)
	out := g.bombs[:0]
	for _, b := range g.bombs {
		hit := false
		for i := 0; i < int(math.Ceil(speed)); i++ {
			b.y += speed / math.Ceil(speed)
			x, y := int(b.x), int(b.y)
			if y+7 >= groundY {
				g.booms = append(g.booms, boom{b.x - 4, groundY - 8, 12, "shotboom", "#ffffff"})
				hit = true
				break
			}
			if g.hitShield(x, y+7, 2) {
				hit = true
				break
			}
			if b.y+7 >= playerY+2 && b.y < playerY+8 && b.x >= g.px && b.x < g.px+playerW {
				g.killPlayer()
				return
			}
			if g.shot.on && math.Abs(g.shot.x-b.x) < 2 && g.shot.y <= b.y+7 && g.shot.y >= b.y {
				g.shot.on = false
				hit = true
				break
			}
		}
		if !hit {
			out = append(out, b)
		}
	}
	g.bombs = out

	g.bombT--
	if g.bombT > 0 || len(g.bombs) >= 3+min(g.level, 3) {
		return
	}
	g.bombT = max(20, 40+g.rng.Intn(40)-5*g.level)
	var cols []int
	for c := 0; c < gridC; c++ {
		if _, ok := g.shooter(c); ok {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return
	}
	c := cols[g.rng.Intn(len(cols))]
	if g.rng.Intn(2) == 0 { // aim at the player half the time
		best := math.MaxFloat64
		for _, cc := range cols {
			d := math.Abs(float64(g.fx+cc*cellW+8) - (g.px + 6))
			if d < best {
				best, c = d, cc
			}
		}
	}
	r, _ := g.shooter(c)
	x, y, w, h := g.invRect(r, c)
	g.bombs = append(g.bombs, bullet{x: float64(x + w/2), y: float64(y + h), on: true})
}

func (g *Invaders) moveUFO() {
	if !g.ufoOn {
		if g.ufoT--; g.ufoT <= 0 {
			g.ufoT = 25 * game.TickRate
			g.ufoOn = true
			if g.rng.Intn(2) == 0 {
				g.ufoX, g.ufoDir = -16, 0.75
			} else {
				g.ufoX, g.ufoDir = width, -0.75
			}
		}
		return
	}
	g.ufoX += g.ufoDir
	if g.ufoX < -16 || g.ufoX > width {
		g.ufoOn = false
	}
}

func (g *Invaders) killPlayer() {
	g.dying = dieTicks
	g.bombs = nil
	g.shot.on = false
	g.booms = append(g.booms, boom{g.px, playerY, dieTicks, "playerboom", "#7dff6a"})
}

func (g *Invaders) shieldAt(x, y int) (int, int, int, bool) {
	if y < shieldY || y >= shieldY+shieldH {
		return 0, 0, 0, false
	}
	for i, sx := range shieldXs {
		if x >= sx && x < sx+shieldW {
			return i, x - sx, y - shieldY, true
		}
	}
	return 0, 0, 0, false
}

func (g *Invaders) clearShieldPx(x, y int) {
	if i, sx, sy, ok := g.shieldAt(x, y); ok {
		g.shield[i][sy][sx] = false
	}
}

// hitShield erodes the shield around (x, y) if a pixel there is solid.
func (g *Invaders) hitShield(x, y, radius int) bool {
	i, sx, sy, ok := g.shieldAt(x, y)
	if !ok || !g.shield[i][sy][sx] {
		return false
	}
	for dy := -radius - 1; dy <= radius+1; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radius*radius+1 || g.rng.Intn(3) == 0 {
				xx, yy := sx+dx, sy+dy
				if xx >= 0 && xx < shieldW && yy >= 0 && yy < shieldH {
					g.shield[i][yy][xx] = false
				}
			}
		}
	}
	return true
}

func (g *Invaders) Status() game.Status {
	s := game.Status{Score: g.score, Lives: g.lives, Level: g.level, Over: g.over}
	if g.over {
		s.Message = "GAME OVER"
	}
	return s
}

func (g *Invaders) Frame() game.Frame {
	f := game.Frame{W: width, H: height, Bg: "#05060a"}
	S := func(id string, x, y float64, c string) {
		f.Sprites = append(f.Sprites, game.Sprite{ID: id, X: x, Y: y, C: c})
	}
	R := func(x, y, w, h float64, c string) {
		f.Rects = append(f.Rects, game.Rect{X: x, Y: y, W: w, H: h, C: c})
	}
	T := func(x, y float64, s, c string, size float64, align string) {
		f.Texts = append(f.Texts, game.Text{X: x, Y: y, S: s, C: c, Size: size, Align: align})
	}
	T(8, 16, fmt.Sprintf("SCORE %04d", g.score), "#fff", 10, "")
	T(width-8, 16, fmt.Sprintf("WAVE %d", g.level+1), "#fff", 10, "right")

	frames := [2]string{"a", "b"}
	for r := range g.alive {
		for c := range g.alive[r] {
			if g.alive[r][c] {
				x, y, _, _ := g.invRect(r, c)
				S(rowTypes[r].sprite+"_"+frames[g.anim], float64(x), float64(y), rowTypes[r].color)
			}
		}
	}
	for i, sx := range shieldXs {
		for y := 0; y < shieldH; y++ {
			for x := 0; x < shieldW; {
				if !g.shield[i][y][x] {
					x++
					continue
				}
				start := x
				for x < shieldW && g.shield[i][y][x] {
					x++
				}
				R(float64(sx+start), float64(shieldY+y), float64(x-start), 1, "#3cff5a")
			}
		}
	}
	if g.ufoOn {
		S("ufo", g.ufoX, 40, "#ff3040")
	}
	if g.ufoScoreT > 0 {
		T(g.ufoScoreX+8, 47, fmt.Sprint(g.ufoScore), "#ff3040", 8, "center")
	}
	if g.shot.on {
		R(g.shot.x, g.shot.y, 1, 4, "#ffffff")
	}
	for _, b := range g.bombs {
		S("bomb_"+frames[(int(b.y)/4)%2], b.x-1, b.y, "#ffffff")
	}
	if g.dying == 0 && !g.over {
		S("player", g.px, playerY, "#7dff6a")
	}
	for _, b := range g.booms {
		id := b.id
		if id == "playerboom" {
			id = "playerboom_" + frames[(b.t/6)%2]
		}
		S(id, b.x, b.y, b.c)
	}
	R(0, groundY, width, 1, "#3cff5a")
	for i := 0; i < g.lives-1; i++ {
		S("player", float64(24+i*16), groundY+4, "#7dff6a")
	}
	T(8, groundY+12, fmt.Sprint(max(g.lives, 0)), "#fff", 9, "")
	if g.over {
		R(0, 100, width, 50, "#000000cc")
		T(width/2, 124, "GAME OVER", "#ff5060", 16, "center")
		T(width/2, 140, "press Enter", "#aab", 8, "center")
	}
	return f
}

func (g *Invaders) Observe() map[string]any {
	center := g.px + playerW/2
	minX, maxX, bottom := g.bounds()
	dir := "right"
	if g.dir < 0 {
		dir = "left"
	}
	type target struct {
		DX   int    `json:"dx"`
		Kind string `json:"kind"`
		Pts  int    `json:"points"`
	}
	var targets []target
	for c := 0; c < gridC; c++ {
		if r, ok := g.shooter(c); ok {
			x, _, w, _ := g.invRect(r, c)
			targets = append(targets, target{int(float64(x+w/2) - center), rowTypes[r].sprite, rowTypes[r].points})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return abs(targets[i].DX) < abs(targets[j].DX) })
	if len(targets) > 4 {
		targets = targets[:4]
	}
	type threat struct {
		DX int `json:"dx"`
		DY int `json:"dy"`
	}
	var threats []threat
	danger := false
	for _, b := range g.bombs {
		dy := int(playerY - (b.y + 7))
		dx := int(b.x - center)
		if dy < 0 {
			continue
		}
		threats = append(threats, threat{dx, dy})
		if abs(dx) <= playerW/2+2 && dy < 70 {
			danger = true
		}
	}
	sort.Slice(threats, func(i, j int) bool { return threats[i].DY < threats[j].DY })
	if len(threats) > 4 {
		threats = threats[:4]
	}
	underShield := false
	for _, sx := range shieldXs {
		if center >= float64(sx) && center < float64(sx+shieldW) {
			underShield = true
		}
	}
	obs := map[string]any{
		"cannon_x":      int(center),
		"cannon_min_x":  margin + playerW/2,
		"cannon_max_x":  width - margin - playerW/2,
		"can_fire":      !g.shot.on && g.dying == 0,
		"under_shield":  underShield,
		"in_danger":     danger,
		"threats":       threats,
		"targets":       targets,
		"invaders_left": g.aliveCount(),
		"formation":     map[string]any{"left": minX, "right": maxX, "bottom": bottom, "moving": dir, "rows_until_landing": max(0, (playerY-bottom)/8)},
		"exploding":     g.dying > 0,
		"lives":         g.lives,
		"score":         g.score,
		"wave":          g.level + 1,
		"game_over":     g.over,
	}
	if g.ufoOn {
		obs["ufo_dx"] = int(g.ufoX + 8 - center)
	}
	return obs
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

var sprites = map[string][]string{
	"squid_a": {
		"...XX...",
		"..XXXX..",
		".XXXXXX.",
		"XX.XX.XX",
		"XXXXXXXX",
		"..X..X..",
		".X.XX.X.",
		"X.X..X.X",
	},
	"squid_b": {
		"...XX...",
		"..XXXX..",
		".XXXXXX.",
		"XX.XX.XX",
		"XXXXXXXX",
		".X.XX.X.",
		"X......X",
		".X....X.",
	},
	"crab_a": {
		"..X.....X..",
		"...X...X...",
		"..XXXXXXX..",
		".XX.XXX.XX.",
		"XXXXXXXXXXX",
		"X.XXXXXXX.X",
		"X.X.....X.X",
		"...XX.XX...",
	},
	"crab_b": {
		"..X.....X..",
		"X..X...X..X",
		"X.XXXXXXX.X",
		"XXX.XXX.XXX",
		"XXXXXXXXXXX",
		".XXXXXXXXX.",
		"..X.....X..",
		".X.......X.",
	},
	"octopus_a": {
		"....XXXX....",
		".XXXXXXXXXX.",
		"XXXXXXXXXXXX",
		"XXX..XX..XXX",
		"XXXXXXXXXXXX",
		"...XX..XX...",
		"..XX.XX.XX..",
		"XX........XX",
	},
	"octopus_b": {
		"....XXXX....",
		".XXXXXXXXXX.",
		"XXXXXXXXXXXX",
		"XXX..XX..XXX",
		"XXXXXXXXXXXX",
		"..XXX..XXX..",
		".XX..XX..XX.",
		"..XX....XX..",
	},
	"player": {
		"......X......",
		".....XXX.....",
		".....XXX.....",
		".XXXXXXXXXXX.",
		"XXXXXXXXXXXXX",
		"XXXXXXXXXXXXX",
		"XXXXXXXXXXXXX",
		"XXXXXXXXXXXXX",
	},
	"playerboom_a": {
		".....X.......",
		"..X....X..X..",
		".....X.X.....",
		"..X.XX...X...",
		"X..XXXXX...X.",
		".XXXXXXXX.X..",
		"XXXXXXXXXXXX.",
		".XXXXXXXXXXX.",
	},
	"playerboom_b": {
		"X.......X...X",
		"...X.X....X..",
		".X.....X.....",
		"....X.X...X..",
		"..X.XXX.X....",
		"X.XXXXXXX..X.",
		".XXXXXXXXXX..",
		"XXXXXXXXXXXXX",
	},
	"ufo": {
		".....XXXXXX.....",
		"...XXXXXXXXXX...",
		"..XXXXXXXXXXXX..",
		".XX.XX.XX.XX.XX.",
		"XXXXXXXXXXXXXXXX",
		"..XXX..XX..XXX..",
		"...X........X...",
	},
	"explode": {
		"....X...X....",
		".X...X.X...X.",
		"..X.......X..",
		"...X.....X...",
		"XX.........XX",
		"...X.....X...",
		"..X..X.X..X..",
		".X..X...X..X.",
	},
	"shotboom": {
		"X...X..X",
		"..X...X.",
		".XXXXXX.",
		"XXXXXXXX",
		".XXXXXX.",
		"..X..X..",
		"X..X...X",
		"...X....",
	},
	"bomb_a": {
		".X.",
		"X..",
		".X.",
		"..X",
		".X.",
		"X..",
		".X.",
	},
	"bomb_b": {
		".X.",
		"..X",
		".X.",
		"X..",
		".X.",
		"..X",
		".X.",
	},
}
