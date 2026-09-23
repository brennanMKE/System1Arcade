package game

// Advisor is implemented by games that describe themselves to a System 1
// model. Such models classify well but do not reason across steps, so a game
// describes what a player would perceive in plain English, asks a few simple
// questions about it, and turns the answers into controller input itself.
type Advisor interface {
	// Situation is a plain-English description of what a player sees right
	// now, including motion. It states facts, not advice. The UI shows it.
	Situation() string
	// Prompt is what the model is asked.
	Prompt() Prompt
	// Decide maps the model's answers to a plan of actions.
	Decide(a Answers) Decision
}

// Prompt is a request for a Laya/Jev style model. Either State and
// Questions are set, or Batch holds several independent prompts. Laya reads
// short states best, so a game can split a decision into one small state per
// fact. Answers to a batch are keyed "<batch key>.<question>".
type Prompt struct {
	State     string            `json:"state,omitempty"`
	Questions map[string]any    `json:"questions,omitempty"`
	Batch     map[string]Prompt `json:"batch,omitempty"`
}

// Answer is one answered question, in Laya's result format.
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Noul          float64            `json:"noul"`
	Score         float64            `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
}

// Answers maps question names to answers.
type Answers map[string]Answer

// Yes reports whether a noul question was answered true.
func (a Answers) Yes(q string) bool { return a[q].Noul > 0.5 }

// Decision is what a game does with the model's answers.
type Decision struct {
	// Actions are pressed in order, one per tick, like a player tapping.
	Actions []string `json:"actions"`
	// Wait is how many more ticks lockstep mode advances once the actions are
	// done, e.g. until the frog can hop again.
	Wait int `json:"wait"`
	// Note explains the decision for the UI, e.g. "bomb incoming → cover left".
	Note string `json:"note"`
	// Keep leaves the plan already in progress untouched.
	Keep bool `json:"keep,omitempty"`
}

// Choice builds a choice question.
func Choice(instructions string, criteria map[string]string) map[string]any {
	return map[string]any{"type": "choice", "instructions": instructions, "criteria": criteria}
}

// Noul builds a yes/no question.
func Noul(instructions string) map[string]any {
	return map[string]any{"type": "noul", "instructions": instructions}
}

// Oracle is implemented by games that can answer their own questions
// perfectly. It measures the ceiling of a game's descriptions and Decide
// logic, separately from how well a model answers.
type Oracle interface {
	Oracle() Answers
}

// Yes/no and pick helpers for oracles.
func Truth(b bool) Answer {
	if b {
		return Answer{Type: "noul", Noul: 1}
	}
	return Answer{Type: "noul", Noul: 0}
}

func Pick(choice string) Answer {
	return Answer{Type: "choice", Choice: choice, Probabilities: map[string]float64{choice: 1}}
}
