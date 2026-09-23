// Package agent drives a game with an external decision endpoint: it sends
// each prompt from /v1/laya to a URL and hands the answers to the game.
//
// The endpoint contract (what a custom agent implements):
//
//	POST <url>
//	{"state": "...", "questions": {name: {type, instructions, criteria?}}}
//	-> {"answers": {name: {type, choice?, probabilities?, noul?, score?}}}
//
// That is the shape Laya's and Jev's predict endpoints use. Games that split a
// decision into several short states send {"batch": {key: {state, questions}}}
// and expect {"answers": {"key.name": answer}}; endpoints that do not support
// batches can be sent one request per state instead.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"system1/internal/game"
)

// Client talks to one decision endpoint.
type Client struct {
	URL    string
	APIKey string
	// Model, when set, is sent as "model" in every request, as TypeSafe's
	// /v1/systemone API requires (e.g. "jev-latest").
	Model string
	// Batch sends a whole batch in one request. When false, each state in a
	// batch is its own request, sent concurrently.
	Batch bool
	HTTP  *http.Client
}

// Ask sends prompt (as built by engine.LayaRequest) and returns the answers,
// keyed the way the game's Decide expects.
func (c *Client) Ask(ctx context.Context, prompt map[string]any) (game.Answers, error) {
	batch, isBatch := prompt["batch"].(map[string]game.Prompt)
	if !isBatch || c.Batch {
		return c.post(ctx, prompt)
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		out  = game.Answers{}
		errs []error
	)
	for key, p := range batch {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ans, err := c.post(ctx, map[string]any{"state": p.State, "questions": p.Questions})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			for q, a := range ans {
				out[key+"."+q] = a
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return nil, errs[0]
	}
	return out, nil
}

func (c *Client) post(ctx context.Context, body map[string]any) (game.Answers, error) {
	if c.Model != "" {
		body["model"] = c.Model
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var parsed struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Answers == nil {
		return nil, fmt.Errorf("response has no \"answers\" object: %.200s", data)
	}
	return flatten("", parsed.Answers)
}

// flatten accepts answers flat ({"key.q": answer}) or nested by batch key
// ({"key": {"q": answer}}).
func flatten(prefix string, raw map[string]json.RawMessage) (game.Answers, error) {
	out := game.Answers{}
	for k, v := range raw {
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(v, &probe) == nil && probe.Type != "" {
			var a game.Answer
			if err := json.Unmarshal(v, &a); err != nil {
				return nil, fmt.Errorf("answer %q: %w", name, err)
			}
			out[name] = a
			continue
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(v, &nested); err != nil {
			return nil, fmt.Errorf("answer %q is neither an answer nor a group of answers", name)
		}
		sub, err := flatten(name, nested)
		if err != nil {
			return nil, err
		}
		for sk, sv := range sub {
			out[sk] = sv
		}
	}
	return out, nil
}

// Test sends a tiny question and returns the answer and round-trip time.
func (c *Client) Test(ctx context.Context) (string, time.Duration, error) {
	start := time.Now()
	ans, err := c.post(ctx, map[string]any{
		"state": "The traffic light is green.",
		"questions": map[string]any{"light": game.Choice("What color is the traffic light?", map[string]string{
			"green": "green", "red": "red"})},
	})
	if err != nil {
		return "", 0, err
	}
	a := ans["light"]
	return fmt.Sprintf("answered %q (expected \"green\")", a.Choice), time.Since(start), nil
}
