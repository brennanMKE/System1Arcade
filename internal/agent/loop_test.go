package agent

import (
	"testing"
	"time"
)

func TestLatencySummary(t *testing.T) {
	var l latencies
	for i := 0; i < 197; i++ {
		l.add(100 * time.Microsecond)
	}
	l.add(30 * time.Millisecond)
	l.add(45 * time.Millisecond)
	l.add(120 * time.Millisecond)
	want := "200 decisions, median 0.10 ms, p99 45.0 ms, slowest 120.0 ms, 3 over 20 ms"
	if got := l.summary(); got != want {
		t.Errorf("summary = %q\nwant       %q", got, want)
	}
}
