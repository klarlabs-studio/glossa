package glossa

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *clock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func TestReporterRateLimitsRepeats(t *testing.T) {
	var got []Error
	clk := &clock{t: time.Unix(0, 0)}
	r := newReporter(func(e Error) { got = append(got, e) }, time.Minute)
	r.now = clk.now

	e := Error{Type: ErrorMissingMessage, Detail: "missing", MessageID: "a", Locale: "de"}
	r.report(e)
	r.report(e)
	r.report(e)
	r.report(Error{Type: ErrorMissingMessage, Detail: "missing", MessageID: "b", Locale: "de"})
	if len(got) != 2 {
		t.Fatalf("reported %d errors, want 2 (repeats suppressed): %+v", len(got), got)
	}
	clk.advance(2 * time.Minute)
	r.report(e)
	if len(got) != 3 || got[2].Repeated != 2 {
		t.Fatalf("after the window: %+v; want the repeat count carried", got)
	}
}

func TestReporterSurvivesPanickingHandler(t *testing.T) {
	r := newReporter(func(Error) { panic("boom") }, time.Minute)
	r.report(Error{Type: ErrorFormat})
}

func TestReporterBoundsMemory(t *testing.T) {
	n := 0
	r := newReporter(func(Error) { n++ }, time.Hour)
	for i := range maxTrackedErrors + 10 {
		r.report(Error{Type: ErrorMissingMessage, MessageID: strings.Repeat("x", i%50) + string(rune('a'+i%26)) + string(rune(i))})
	}
	if len(r.seen) > maxTrackedErrors {
		t.Fatalf("tracking %d keys, cap is %d", len(r.seen), maxTrackedErrors)
	}
}

func TestLogHandler(t *testing.T) {
	var buf bytes.Buffer
	h := logHandler(slog.New(slog.NewTextHandler(&buf, nil)))
	h(Error{Type: ErrorIntegrity, Detail: "bad bytes", ReleaseID: "rel_1", Repeated: 3})
	out := buf.String()
	for _, want := range []string{"level=WARN", "type=integrity", "detail=\"bad bytes\"", "release=rel_1", "repeated=3"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}
}

func TestErrorString(t *testing.T) {
	e := Error{Type: ErrorFormat, Detail: "bad-operand", MessageID: "cart.total", Locale: "de"}
	if got := e.Error(); got != "glossa: format: cart.total (de): bad-operand" {
		t.Fatalf("Error() = %q", got)
	}
	if got := (Error{Type: ErrorNetwork, Detail: "down"}).Error(); got != "glossa: network: down" {
		t.Fatalf("Error() = %q", got)
	}
}
