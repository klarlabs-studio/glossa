package evals_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unicode/utf8"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/cassette"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/evals"
)

var record = flag.String("record", "", `re-record the cassettes: "scripted" (hand-crafted answers from testdata/scripted) or "live" (real providers; build with -tags=live)`)

// liveSetup is set by live_test.go (build tag live). It returns the real
// providers and routing, or skips when no API key is configured.
var liveSetup func(t *testing.T) evals.Setup

// TestRecordCassettes rewrites testdata/cassettes. With -record=scripted
// the "model" answers come from testdata/scripted/<pair>.json — the
// hand-crafted cassettes CI replays. With -tags=live -record=live they
// come from the real provider (ANTHROPIC_API_KEY, …). Review the diff:
// the recorded prompts show exactly what changed.
func TestRecordCassettes(t *testing.T) {
	if *record == "" {
		t.Skip("set -record=scripted or -record=live to re-record cassettes")
	}
	var outcomes []evals.Outcome
	for _, c := range loadCases(t) {
		var setup evals.Setup
		switch *record {
		case "scripted":
			answers, err := scriptedAnswers(c)
			if err != nil {
				t.Fatal(err)
			}
			setup = evals.Setup{
				Providers: map[string]domain.Provider{"anthropic": &scriptedProvider{answers: answers}},
				Routing:   app.DefaultRouting(), Prices: app.DefaultPrices(),
			}
		case "live":
			if liveSetup == nil {
				t.Fatal("-record=live needs -tags=live")
			}
			setup = liveSetup(t)
		default:
			t.Fatalf("unknown -record=%s", *record)
		}
		rec := cassette.NewRecorder(*record)
		for name, p := range setup.Providers {
			setup.Providers[name] = rec.Wrap(p)
		}
		o, err := evals.RunCase(context.Background(), c, setup)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		outcomes = append(outcomes, o)
		if err := rec.Cassette().Save(cassettePath(c)); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("eval report (%s, recorded):\n%s", *record, evals.Summarize(outcomes))
}

func scriptedAnswers(c evals.Case) (map[domain.Task][]string, error) {
	raw, err := os.ReadFile(filepath.Join(testdata, "scripted", c.Pair()+".json"))
	if err != nil {
		return nil, err
	}
	var all map[string]map[domain.Task][]string
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	answers, ok := all[c.ID]
	if !ok {
		return nil, fmt.Errorf("%s: no scripted answers", c.ID)
	}
	return answers, nil
}

// scriptedProvider plays hand-crafted answers in order per task, with a
// token usage estimated from the text lengths so costs are plausible.
type scriptedProvider struct {
	mu      sync.Mutex
	answers map[domain.Task][]string
}

func (s *scriptedProvider) Name() string { return "anthropic" }

func (s *scriptedProvider) Complete(_ context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.answers[req.Task]
	if len(q) == 0 {
		return domain.Completion{}, errors.New("scripted: no answer left for " + string(req.Task))
	}
	s.answers[req.Task] = q[1:]
	in := 0
	for _, b := range req.System {
		in += utf8.RuneCountInString(b.Text)
	}
	for _, m := range req.Messages {
		in += utf8.RuneCountInString(m.Text)
	}
	return domain.Completion{
		Provider: "anthropic", Model: req.Model, Text: q[0], Stop: domain.StopEnd,
		Usage: domain.Usage{InputTokens: tokens(in), OutputTokens: tokens(utf8.RuneCountInString(q[0]))},
	}, nil
}

func tokens(chars int) int64 { return int64(math.Ceil(float64(chars) / 3.5)) }
