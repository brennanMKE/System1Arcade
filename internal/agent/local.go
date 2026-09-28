package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	laya "github.com/brennanMKE/laya-go"
	"github.com/brennanMKE/laya-go/answercache"

	"system1/internal/game"
)

// Local is the built-in agent: Laya running in this process through laya-go,
// with the same answer cache the Python server (agents/laya_server.py) keeps.
// It answers exactly what that server would for the same prompt: the prompt
// is encoded as the HTTP client would send it (Go's JSON, so map keys such as
// criteria are sorted) and decoded the way the server reads it, every
// decision is one PredictMany call, and answers are rounded to 4 decimals and
// shaped as the server returns them.
//
// A Local is safe for concurrent use.
type Local struct {
	model *laya.Model
	cache *answercache.Cache[game.Answer]
}

// NewLocal answers with m, caching up to cacheSize answers (0 turns the
// cache off). Local owns m and closes it in Close.
func NewLocal(m *laya.Model, cacheSize int) *Local {
	return &Local{model: m, cache: answercache.New[game.Answer](cacheSize)}
}

// LoadLocal loads Laya (downloading it into the Hugging Face cache on first
// use; see laya.Options.Progress) with the answer cache sized by
// $SYSTEM1_LAYA_CACHE, default 10000.
func LoadLocal(ctx context.Context, opts laya.Options) (*Local, error) {
	size, err := answercache.SizeFromEnv(answercache.DefaultSize)
	if err != nil {
		return nil, err
	}
	m, err := laya.Load(ctx, opts)
	if err != nil {
		return nil, err
	}
	return NewLocal(m, size), nil
}

// Engine says in plain words where the model runs: "the GPU (Metal)" or
// "the CPU". laya-go picks the engine ("auto": Metal on Apple Silicon when the
// GPU passes its self-test, else the CPU); LAYA_ENGINE=native or metal
// overrides it.
func (l *Local) Engine() string { return EngineLabel(l.model.Info().Engine) }

// EngineDetail is laya-go's own description of the engine, such as
// "metal fp16 weights (Apple M4 Pro)" or "native (accelerate)", and why
// Metal wasn't used when "auto" tried it and fell back to the CPU.
func (l *Local) EngineDetail() (name, fallback string) {
	info := l.model.Info()
	return info.Engine, info.EngineFallback
}

// EngineLabel describes a laya.Info.Engine name for people.
func EngineLabel(name string) string {
	if strings.HasPrefix(name, "metal") {
		return "the GPU (Metal)"
	}
	return "the CPU"
}

// Close frees the model. Ask fails afterwards.
func (l *Local) Close() error { return l.model.Close() }

// CacheStats reports the answer cache's hits and misses, as the Python
// server's GET /stats does.
func (l *Local) CacheStats() answercache.Stats { return l.cache.Stats() }

// rawPrompt is one state and its questions as JSON, for the cache keys.
type rawPrompt struct {
	State     json.RawMessage            `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
}

// Ask answers prompt (as built by engine.LayaRequest) in at most one forward
// pass: {"state", "questions"} gives answers by question name,
// {"batch": {key: …}} gives them as "key.question".
func (l *Local) Ask(ctx context.Context, prompt map[string]any) (game.Answers, error) {
	body, err := json.Marshal(prompt)
	if err != nil {
		return nil, err
	}
	var raw struct {
		rawPrompt
		Batch map[string]rawPrompt `json:"batch"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	_, batch := prompt["batch"]

	// The prompts in request order (Go's JSON sorts keys: batch keys, question
	// names and criteria alike), with each question's cache key.
	type entry struct {
		key   string
		names []string
	}
	var entries []entry
	var keys [][]string
	add := func(key string, rp rawPrompt) error {
		e := entry{key: key}
		var ks []string
		if rp.Questions == nil {
			return fmt.Errorf("prompt %q has no questions", key)
		}
		for _, name := range sortedKeys(rp.Questions) {
			if rp.State == nil {
				return fmt.Errorf("prompt %q has no state", key)
			}
			k, err := answercache.Key(rp.State, rp.Questions[name])
			if err != nil {
				return err
			}
			e.names = append(e.names, name)
			ks = append(ks, k)
		}
		entries = append(entries, e)
		keys = append(keys, ks)
		return nil
	}
	if batch {
		for _, k := range sortedKeys(raw.Batch) {
			if err := add(k, raw.Batch[k]); err != nil {
				return nil, err
			}
		}
	} else if err := add("", raw.rawPrompt); err != nil {
		return nil, err
	}

	answers, err := l.cache.Do(keys, func(miss [][]bool) ([][]game.Answer, error) {
		prompts, err := laya.DecodeRequest(body)
		if err != nil {
			return nil, err
		}
		if len(prompts) != len(entries) {
			return nil, fmt.Errorf("decoded %d prompts from %d", len(prompts), len(entries))
		}
		// Only the questions the cache can't answer go to the model.
		var sub []laya.Prompt
		var from []int
		for i, p := range prompts {
			if p.Key != entries[i].key || len(p.Questions) != len(entries[i].names) {
				return nil, fmt.Errorf("prompt %q decoded out of order", entries[i].key)
			}
			q := laya.Prompt{Key: p.Key, State: p.State}
			for j, n := range p.Questions {
				if miss[i][j] {
					q.Questions = append(q.Questions, n)
				}
			}
			if len(q.Questions) > 0 {
				sub = append(sub, q)
				from = append(from, i)
			}
		}
		out := make([][]game.Answer, len(prompts))
		for i, p := range prompts {
			out[i] = make([]game.Answer, len(p.Questions))
		}
		if len(sub) == 0 {
			return out, nil
		}
		results, err := l.model.PredictMany(ctx, sub)
		if err != nil {
			return nil, err
		}
		for k, r := range results {
			i := from[k]
			for j, n := range prompts[i].Questions {
				if miss[i][j] {
					out[i][j] = gameAnswer(r.Answers[n.Name], batch)
				}
			}
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out := make(game.Answers)
	for i, e := range entries {
		for j, name := range e.names {
			if batch {
				name = e.key + "." + name
			}
			out[name] = answers[i][j]
		}
	}
	return out, nil
}

// gameAnswer is what the HTTP client decodes from the Python server's JSON
// for a: every number rounded to 4 decimals; score answers in a batch carry
// no confidence (predict_many's shape), single-state ones do (Agent.predict's).
func gameAnswer(a laya.Answer, batch bool) game.Answer {
	g := game.Answer{Type: a.Type.String()}
	probs := func() map[string]float64 {
		m := make(map[string]float64, len(a.Probabilities))
		for _, p := range a.Probabilities {
			m[p.Label] = laya.Round4(p.P)
		}
		return m
	}
	switch a.Type {
	case laya.TypeChoice:
		g.Choice = a.Choice
		g.Probabilities = probs()
		g.Confidence = laya.Round4(a.Confidence)
	case laya.TypeScore:
		g.Score = laya.Round4(a.Score)
		g.Probabilities = probs()
		if !batch {
			g.Confidence = laya.Round4(a.Confidence)
		}
	case laya.TypeNoul:
		g.Noul = laya.Round4(a.Noul)
		g.Confidence = laya.Round4(a.Confidence)
	}
	return g
}

// Test asks Client.Test's question and describes the answer.
func (l *Local) Test(ctx context.Context) (string, error) {
	ans, err := l.Ask(ctx, testPrompt())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("answered %q (expected \"green\")", ans["light"].Choice), nil
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
