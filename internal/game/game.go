// Package game defines the contract every playable game implements.
//
// Games are deterministic, fixed-timestep simulations. They never read the
// keyboard or clock directly: the engine feeds them an Input once per tick,
// whether that input came from a human or an agent. That shared input path is
// what lets an agent control a game exactly the way a user would.
package game

// TickRate is the fixed simulation rate in ticks per second.
const TickRate = 60

// Button is a virtual controller button. Keyboards and agents both press these.
type Button string

const (
	Left  Button = "left"
	Right Button = "right"
	Up    Button = "up"
	Down  Button = "down"
	A     Button = "a" // Space / X
	B     Button = "b" // Z
	Start Button = "start"
)

// Buttons lists every valid button.
var Buttons = []Button{Left, Right, Up, Down, A, B, Start}

// ValidButton reports whether b is a known button.
func ValidButton(b Button) bool {
	for _, x := range Buttons {
		if x == b {
			return true
		}
	}
	return false
}

// Input is the controller state for one tick.
type Input struct {
	Held    map[Button]bool // down this tick
	Pressed map[Button]bool // went down this tick (edge)
}

// Down reports whether b is held.
func (in Input) Down(b Button) bool { return in.Held[b] }

// Hit reports whether b was pressed this tick.
func (in Input) Hit(b Button) bool { return in.Pressed[b] }

// Action is a named, agent-facing input: a set of buttons held for some ticks.
type Action struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Buttons     []Button `json:"buttons"`
	HoldTicks   int      `json:"hold_ticks"`
}

// Status is the scoreboard.
type Status struct {
	Score   int    `json:"score"`
	Lives   int    `json:"lives"`
	Level   int    `json:"level"`
	Over    bool   `json:"over"`
	Message string `json:"message,omitempty"`
}

// Draw primitives, in the game's logical pixel space.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
	C string  `json:"c"`
}

type Sprite struct {
	ID    string  `json:"id"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	C     string  `json:"c"`
	Scale float64 `json:"s,omitempty"`
}

type Text struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	S     string  `json:"s"`
	C     string  `json:"c"`
	Size  float64 `json:"size"`
	Align string  `json:"align,omitempty"` // left (default), center, right
}

// Frame is a complete draw list for one tick.
type Frame struct {
	W       int      `json:"w"`
	H       int      `json:"h"`
	Bg      string   `json:"bg"`
	Rects   []Rect   `json:"rects"`
	Sprites []Sprite `json:"sprites"`
	Texts   []Text   `json:"texts"`
}

// Info describes a game. Sprites maps a sprite ID to rows of pixels where
// any non-'.' character is lit.
type Info struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Controls string              `json:"controls"`
	Width    int                 `json:"width"`
	Height   int                 `json:"height"`
	Sprites  map[string][]string `json:"sprites,omitempty"`
	Actions  []Action            `json:"actions"`
}

// Game is a deterministic, fixed-timestep game.
type Game interface {
	Info() Info
	// Reset starts a new game. The same seed always yields the same game.
	Reset(seed int64)
	// Tick advances the simulation by one step.
	Tick(in Input)
	Frame() Frame
	Status() Status
	// Observe returns a compact, structured description of the state, sized
	// for a System 1 model (Laya reads ~512 tokens).
	Observe() map[string]any
	// Instructions is the prompt for the "which action next?" choice question.
	Instructions() string
}

// Noop is the do-nothing action every game exposes.
var Noop = Action{Name: "noop", Description: "do nothing this turn", HoldTicks: 1}
