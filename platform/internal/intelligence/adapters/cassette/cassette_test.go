package cassette_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/cassette"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

type echo struct {
	name string
	fail bool
	n    int
}

func (e *echo) Name() string { return e.name }

func (e *echo) Complete(_ context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	e.n++
	if e.fail {
		return domain.Completion{Provider: e.name, Usage: domain.Usage{InputTokens: 1}},
			&domain.ProviderError{Provider: e.name, Kind: domain.KindUnavailable, Status: 529}
	}
	return domain.Completion{Provider: e.name, Model: req.Model, Text: req.Messages[0].Text + "#" + string(rune('0'+e.n)), Stop: domain.StopEnd}, nil
}

func req(text string) domain.CompletionRequest {
	return domain.CompletionRequest{
		Task: domain.TaskTranslate, Model: "m", MaxTokens: 10,
		System:   []domain.SystemBlock{{Text: "sys\nline two"}},
		Messages: []domain.Message{{Role: domain.RoleUser, Text: text}},
	}
}

func TestRecordAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	rec := cassette.NewRecorder("scripted")
	p := rec.Wrap(&echo{name: "a"})
	failing := rec.Wrap(&echo{name: "b", fail: true})
	ctx := context.Background()
	for _, text := range []string{"one", "two", "one"} {
		if _, err := p.Complete(ctx, req(text)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := failing.Complete(ctx, req("one")); err == nil {
		t.Fatal("want the recorded failure")
	}
	if err := rec.Cassette().Save(path); err != nil {
		t.Fatal(err)
	}

	c, err := cassette.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "scripted" || len(c.Interactions) != 4 {
		t.Fatalf("cassette = %+v", c)
	}
	a := c.Provider("a")
	// Repeated identical requests replay in recorded order, then repeat.
	for _, want := range []string{"one#1", "one#3", "one#3"} {
		out, err := a.Complete(ctx, req("one"))
		if err != nil || out.Text != want {
			t.Fatalf("replay = %q, %v; want %q", out.Text, err, want)
		}
	}
	if len(c.Unused()) != 2 {
		t.Errorf("unused = %d, want 2 (two and b's failure)", len(c.Unused()))
	}
	// A recorded failure replays as the same classified error.
	out, err := c.Provider("b").Complete(ctx, req("one"))
	var pe *domain.ProviderError
	if !errors.As(err, &pe) || pe.Kind != domain.KindUnavailable || pe.Status != 529 || out.Usage.InputTokens != 1 {
		t.Errorf("replayed failure = %+v, %v", out, err)
	}
}

func TestMismatchFailsLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	rec := cassette.NewRecorder("live")
	if _, err := rec.Wrap(&echo{name: "a"}).Complete(context.Background(), req("hello")); err != nil {
		t.Fatal(err)
	}
	if err := rec.Cassette().Save(path); err != nil {
		t.Fatal(err)
	}
	c, _ := cassette.Load(path)
	changed := req("hello")
	changed.System[0].Text = "sys\nline 2"
	changed.MaxTokens = 20
	_, err := c.Provider("a").Complete(context.Background(), changed)
	var me *cassette.MismatchError
	if !errors.As(err, &me) || !errors.Is(err, cassette.ErrMismatch) {
		t.Fatalf("err = %v", err)
	}
	if domain.IsRetryable(err) {
		t.Error("a mismatch must not be retried")
	}
	msg := err.Error()
	for _, want := range []string{"max_tokens: 10 → 20", `system[0] line 2: "line two" → "line 2"`, "TestRecord.*Cassettes", path} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	// Another provider name is another key.
	if _, err := c.Provider("b").Complete(context.Background(), req("hello")); !errors.Is(err, cassette.ErrMismatch) {
		t.Errorf("err = %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := cassette.Load(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "TestRecord.*Cassettes") {
		t.Errorf("err = %v", err)
	}
}

func TestKeyIsStable(t *testing.T) {
	a := domain.CompletionRequest{Model: "m", Output: &domain.OutputSchema{Schema: map[string]any{"b": 1, "a": 2}}}
	b := domain.CompletionRequest{Model: "m", Output: &domain.OutputSchema{Schema: map[string]any{"a": 2, "b": 1}}}
	if cassette.Key("p", a) != cassette.Key("p", b) {
		t.Error("map order changed the key")
	}
	if cassette.Key("p", a) == cassette.Key("q", a) {
		t.Error("provider is part of the key")
	}
}
