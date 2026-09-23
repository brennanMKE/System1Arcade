package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"system1/internal/game"
)

// fakeEndpoint answers every choice question with its first criterion key
// ("clean") and counts requests.
func fakeEndpoint(t *testing.T, calls *int32, nested bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer token")
		}
		var body struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
			Batch     map[string]game.Prompt     `json:"batch"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		ans := map[string]any{}
		one := map[string]any{"type": "choice", "choice": "clean", "probabilities": map[string]float64{"clean": 0.9, "messy": 0.1}}
		for k, p := range body.Batch {
			for q := range p.Questions {
				if nested {
					ans[k] = map[string]any{q: one}
				} else {
					ans[k+"."+q] = one
				}
			}
		}
		for q := range body.Questions {
			ans[q] = one
		}
		json.NewEncoder(w).Encode(map[string]any{"answers": ans})
	}))
}

func batchPrompt() map[string]any {
	q := map[string]any{"look": game.Choice("How does it look?", map[string]string{"clean": "clean", "messy": "messy"})}
	return map[string]any{"batch": map[string]game.Prompt{
		"s1": {State: "one", Questions: q}, "s2": {State: "two", Questions: q}, "s3": {State: "three", Questions: q},
	}}
}

func TestBatchModes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		batch, nested bool
		wantCalls     int32
	}{
		{"one request", true, false, 1},
		{"one request, nested answers", true, true, 1},
		{"request per state", false, false, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			srv := fakeEndpoint(t, &calls, tc.nested)
			defer srv.Close()
			c := &Client{URL: srv.URL, APIKey: "k", Batch: tc.batch}
			ans, err := c.Ask(context.Background(), batchPrompt())
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.wantCalls {
				t.Errorf("%d requests, want %d", calls, tc.wantCalls)
			}
			for _, k := range []string{"s1.look", "s2.look", "s3.look"} {
				if ans[k].Probabilities["clean"] != 0.9 {
					t.Errorf("answer %s = %+v", k, ans[k])
				}
			}
		})
	}
}

func TestErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result": 1}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL}
	if _, _, err := c.Test(context.Background()); err == nil {
		t.Fatal("a response without answers should be an error")
	}
}
