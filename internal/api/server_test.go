package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"system1/internal/engine"
)

func call(t *testing.T, h http.Handler, method, path, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, rec.Body)
		}
	}
	return rec.Code
}

func TestLockstepAgentLoop(t *testing.T) {
	h := Handler(engine.New())
	var st engine.State
	if c := call(t, h, "POST", "/v1/load", `{"game":"frogger","seed":1}`, &st); c != 200 || st.Game != "frogger" {
		t.Fatalf("load: %d %+v", c, st)
	}
	if c := call(t, h, "POST", "/v1/step", `{"action":"up"}`, nil); c != 409 {
		t.Fatalf("step in realtime = %d, want 409", c)
	}
	call(t, h, "POST", "/v1/mode", `{"mode":"lockstep"}`, nil)
	if c := call(t, h, "POST", "/v1/step", `{"action":"up","meta":{"p":0.9}}`, &st); c != 200 {
		t.Fatalf("step: %d", c)
	}
	if st.Status.Score != 10 || st.Observation["frog_row"].(float64) != 11 {
		t.Fatalf("after hop: score %d row %v", st.Status.Score, st.Observation["frog_row"])
	}
	var laya map[string]any
	call(t, h, "GET", "/v1/laya", "", &laya)
	if laya["batch"] == nil && (laya["state"] == nil || laya["questions"] == nil) {
		t.Fatalf("laya request missing fields: %v", laya)
	}
	if c := call(t, h, "POST", "/v1/action", `{"action":"fly"}`, nil); c != 400 {
		t.Fatalf("unknown action = %d, want 400", c)
	}
}
