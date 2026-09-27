package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	laya "github.com/brennanMKE/laya-go"
	"github.com/brennanMKE/laya-go/engine"
	"github.com/brennanMKE/laya-go/hub"

	"system1/internal/game"
)

// The parity tests replay laya-go's golden fixtures (testdata/golden in the
// laya-go module): 343 real Tetris, Frogger and Space Invaders requests with
// the answers the Python reference gave (laya 0.3.6, PyTorch fp32 CPU),
// exactly as agents/laya_server.py returns them.

type f32 float32

func (f *f32) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseFloat(string(b), 32)
	*f = f32(v)
	return err
}

type goldenSeq struct {
	IDs             []int32 `json:"ids"`
	Markers         []int32 `json:"markers"`
	Logits          []f32   `json:"logits"`
	ActLogits       []f32   `json:"act_logits"`
	LogitsSingle    []f32   `json:"logits_single"`
	ActLogitsSingle []f32   `json:"act_logits_single"`
}

type goldenRecord struct {
	ID          string                     `json:"id"`
	Request     json.RawMessage            `json:"request"`
	Sequences   []goldenSeq                `json:"sequences"`
	PredictMany map[string]json.RawMessage `json:"predict_many"`
	Predict     map[string]struct {
		Answers map[string]json.RawMessage `json:"answers"`
	} `json:"predict"`
	Error string `json:"error"`
}

func goldenRecords(t *testing.T) []goldenRecord {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/brennanMKE/laya-go").Output()
	if err != nil {
		t.Skipf("laya-go module not found: %v", err)
	}
	f, err := os.Open(filepath.Join(strings.TrimSpace(string(out)), "testdata", "golden", "games.jsonl"))
	if err != nil {
		t.Skipf("golden fixtures not found: %v", err)
	}
	defer f.Close()
	var recs []goldenRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		var r goldenRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Error == "" {
			recs = append(recs, r)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return recs
}

func checkpointDir(t *testing.T) string {
	t.Helper()
	dir, err := hub.Find(hub.Options{})
	if err != nil {
		t.Skipf("Laya checkpoint not in the Hugging Face cache: %v", err)
	}
	return dir
}

// appPrompt is the request as the engine builds it: engine.LayaRequest's
// {"batch": map[string]game.Prompt}.
func appPrompt(t *testing.T, r goldenRecord) (map[string]any, map[string]game.Prompt) {
	t.Helper()
	var req struct {
		Batch map[string]game.Prompt `json:"batch"`
	}
	if err := json.Unmarshal(r.Request, &req); err != nil || req.Batch == nil {
		t.Fatalf("%s: not a batch request: %v", r.ID, err)
	}
	return map[string]any{"batch": req.Batch}, req.Batch
}

// expected decodes Python's answers the way Client does.
func expected(t *testing.T, raw map[string]json.RawMessage) game.Answers {
	t.Helper()
	a, err := flatten("", raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// stubEngine returns the fixture's logits for each sequence, as PyTorch
// computed them, so the test checks everything but the forward pass exactly.
type stubEngine struct {
	logits map[string][2][]float32 // by ids and markers: logits, act logits
}

func seqKey(ids, markers []int32) string { return fmt.Sprint(ids, markers) }

func (s *stubEngine) set(r goldenRecord, single bool) {
	s.logits = map[string][2][]float32{}
	for _, q := range r.Sequences {
		l, a := q.Logits, q.ActLogits
		if single {
			l, a = q.LogitsSingle, q.ActLogitsSingle
		}
		s.logits[seqKey(q.IDs, q.Markers)] = [2][]float32{f32s(l), f32s(a)}
	}
}

func f32s(xs []f32) []float32 {
	out := make([]float32, len(xs))
	for i, x := range xs {
		out[i] = float32(x)
	}
	return out
}

func (s *stubEngine) Forward(_ context.Context, b *engine.Batch) (*engine.Output, error) {
	out := &engine.Output{}
	for i := range b.Len() {
		l, ok := s.logits[seqKey(b.Seq(i), b.Markers[i])]
		if !ok {
			return nil, fmt.Errorf("sequence %d is not in the fixture: the prompt was built differently", i)
		}
		out.Logits = append(out.Logits, l[0])
		out.ActLogits = append(out.ActLogits, [2]float32{l[1][0], l[1][1]})
	}
	return out, nil
}

func (s *stubEngine) Close() error { return nil }

// TestLocalMatchesPython checks, for every golden game request, that the
// local agent builds the same model input as the Python server (token ids
// and option markers) and turns Python's logits into exactly the answers the
// app decoded from laya_server.py: for batches (predict_many) and for each
// state asked alone (Agent.predict).
func TestLocalMatchesPython(t *testing.T) {
	recs := goldenRecords(t)
	dir := checkpointDir(t)
	stub := &stubEngine{}
	m, err := laya.New(dir, stub)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(m, 0) // no cache: every answer comes from its own logits
	ctx := context.Background()
	var batches, singles int
	for _, r := range recs {
		prompt, batch := appPrompt(t, r)
		body, _ := json.Marshal(prompt)
		var want bytes.Buffer
		json.Compact(&want, r.Request)
		if !bytes.Equal(body, want.Bytes()) {
			t.Errorf("%s: the app's JSON differs from the request the fixture recorded", r.ID)
		}

		stub.set(r, false)
		got, err := local.Ask(ctx, prompt)
		if err != nil {
			t.Fatalf("%s: %v", r.ID, err)
		}
		if w := expected(t, r.PredictMany); !reflect.DeepEqual(got, w) {
			t.Errorf("%s batch:\n got %v\nwant %v", r.ID, got, w)
		}
		batches++

		stub.set(r, true)
		for key, p := range batch {
			got, err := local.Ask(ctx, map[string]any{"state": p.State, "questions": p.Questions})
			if err != nil {
				t.Fatalf("%s %s: %v", r.ID, key, err)
			}
			if w := expected(t, r.Predict[key].Answers); !reflect.DeepEqual(got, w) {
				t.Errorf("%s %s single:\n got %v\nwant %v", r.ID, key, got, w)
			}
			singles++
		}
	}
	t.Logf("%d batch requests and %d single-state requests answered as laya_server.py did", batches, singles)
}

// TestLocalCache checks that cached answers are the ones the model gave, and
// that a repeated decision skips the model.
func TestLocalCache(t *testing.T) {
	recs := goldenRecords(t)
	dir := checkpointDir(t)
	stub := &stubEngine{}
	m, err := laya.New(dir, stub)
	if err != nil {
		t.Fatal(err)
	}
	local := NewLocal(m, 10000)
	ctx := context.Background()
	for _, r := range recs[:40] {
		prompt, _ := appPrompt(t, r)
		stub.set(r, false)
		first, err := local.Ask(ctx, prompt)
		if err != nil {
			t.Fatal(err)
		}
		stub.logits = nil // a second ask must not reach the model
		again, err := local.Ask(ctx, prompt)
		if err != nil {
			t.Fatalf("%s: repeat reached the model: %v", r.ID, err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Errorf("%s: cached answers differ", r.ID)
		}
	}
	s := local.CacheStats()
	if s.SkippedCalls < 40 || s.Hits == 0 {
		t.Errorf("stats %+v: want at least 40 skipped calls", s)
	}
}

// TestLocalModelParity runs the real forward pass on every golden request
// and compares with the Python reference, at the laya-go README's
// tolerance: answers equal at 4 decimals in at least 99% of fields, every
// choice the same. With SYSTEM1_LAYA_PARITY_URL set to a running
// laya_server.py (or laya-server) predict URL, it also compares with the
// answers the HTTP client gets from it. Slow (minutes on the CPU), so it
// runs only with SYSTEM1_LAYA_PARITY=1.
func TestLocalModelParity(t *testing.T) {
	if os.Getenv("SYSTEM1_LAYA_PARITY") == "" {
		t.Skip("set SYSTEM1_LAYA_PARITY=1 to run the model over the golden requests")
	}
	recs := goldenRecords(t)
	ctx := context.Background()
	m, err := laya.Load(ctx, laya.Options{Offline: true})
	if err != nil {
		t.Skipf("Laya checkpoint not available: %v", err)
	}
	local := NewLocal(m, 0)
	defer local.Close()
	var client *Client
	if u := os.Getenv("SYSTEM1_LAYA_PARITY_URL"); u != "" {
		client = &Client{URL: u, Batch: true, HTTP: &http.Client{Timeout: 30 * time.Second}}
	}
	var golden, server tally
	var elapsed time.Duration
	for _, r := range recs {
		prompt, _ := appPrompt(t, r)
		start := time.Now()
		got, err := local.Ask(ctx, prompt)
		elapsed += time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v", r.ID, err)
		}
		golden.add(got, expected(t, r.PredictMany))
		if client != nil {
			w, err := client.Ask(ctx, prompt)
			if err != nil {
				t.Fatalf("%s: server: %v", r.ID, err)
			}
			server.add(got, w)
		}
	}
	t.Logf("%d requests, %.0f ms per request on average", len(recs), float64(elapsed.Milliseconds())/float64(len(recs)))
	golden.check(t, "golden (PyTorch fp32 CPU)")
	if client != nil {
		server.check(t, "HTTP "+client.URL)
	}
}

// tally counts answer fields that agree at 4 decimals.
type tally struct {
	fields, equal, choices, sameChoice int
	maxDiff                            float64
}

func (c *tally) num(a, b float64) {
	c.fields++
	if a == b {
		c.equal++
	}
	c.maxDiff = math.Max(c.maxDiff, math.Abs(a-b))
}

func (c *tally) add(got, want game.Answers) {
	for name, w := range want {
		g := got[name]
		if g.Type != w.Type {
			c.fields++
			continue
		}
		if w.Type == "choice" {
			c.choices++
			if g.Choice == w.Choice {
				c.sameChoice++
			}
		}
		c.num(g.Noul, w.Noul)
		c.num(g.Score, w.Score)
		c.num(g.Confidence, w.Confidence)
		for l, p := range w.Probabilities {
			c.num(g.Probabilities[l], p)
		}
	}
}

func (c *tally) check(t *testing.T, what string) {
	t.Helper()
	t.Logf("vs %s: %d of %d answer fields equal at 4 decimals (%.2f%%), max difference %.4f; %d of %d choices agree",
		what, c.equal, c.fields, 100*float64(c.equal)/float64(c.fields), c.maxDiff, c.sameChoice, c.choices)
	if float64(c.equal) < 0.99*float64(c.fields) || c.sameChoice != c.choices {
		t.Errorf("vs %s: answers differ more than float noise", what)
	}
}
