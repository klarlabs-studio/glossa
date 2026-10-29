// Package cassette records provider calls and replays them, so the agent
// and the evals run deterministically and for free in CI (RFC 0003 §4).
//
// A cassette is a JSON file of interactions. Each is keyed by a hash of
// the provider name and the complete request — model, system prompt,
// messages, schema, limits — so any change to a prompt, the knowledge the
// tools returned, routing or the model misses the recording and fails
// loudly with the fields that changed and how to re-record. A miss is
// never retried and never falls back to another provider.
package cassette

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// FormatVersion is the cassette file format.
const FormatVersion = 1

// RecordedError is a provider failure as recorded.
type RecordedError struct {
	Kind    domain.ErrorKind `json:"kind"`
	Status  int              `json:"status,omitempty"`
	Message string           `json:"message"`
}

// Interaction is one recorded call.
type Interaction struct {
	Key      string                   `json:"key"`
	Provider string                   `json:"provider"`
	Request  domain.CompletionRequest `json:"request"`
	Response *domain.Completion       `json:"response,omitempty"`
	Error    *RecordedError           `json:"error,omitempty"`
}

// Cassette is a set of recorded interactions.
type Cassette struct {
	Version int `json:"version"`
	// Source says how it was recorded: "live" (a real provider) or
	// "scripted" (hand-crafted answers).
	Source       string        `json:"source"`
	Interactions []Interaction `json:"interactions"`

	path   string
	mu     sync.Mutex
	served map[string]int
}

// Key is the recording key of a request to provider.
func Key(provider string, req domain.CompletionRequest) string {
	raw, err := json.Marshal(req) // map keys sort, so this is canonical
	if err != nil {
		panic(fmt.Sprintf("cassette: encode request: %v", err))
	}
	sum := sha256.Sum256(append([]byte(provider+"\n"), raw...))
	return hex.EncodeToString(sum[:])
}

// Load reads a cassette file.
func Load(path string) (*Cassette, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cassette: %w (record it: go test ./internal/intelligence/evals -run 'TestRecord.*Cassettes' -record=scripted)", err)
	}
	var c Cassette
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("cassette %s: %w", path, err)
	}
	if c.Version != FormatVersion {
		return nil, fmt.Errorf("cassette %s: format version %d, want %d; re-record it", path, c.Version, FormatVersion)
	}
	c.path = path
	return &c, nil
}

// Save writes the cassette as indented JSON.
func (c *Cassette) Save(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Version = FormatVersion
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// Unused lists interactions that were never served, so a test can insist
// a cassette holds nothing stale.
func (c *Cassette) Unused() []Interaction {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Interaction
	for _, in := range c.Interactions {
		if c.served[in.Key] == 0 {
			out = append(out, in)
		}
	}
	return out
}

// Provider returns a replaying provider named name.
func (c *Cassette) Provider(name string) domain.Provider { return &player{c: c, name: name} }

type player struct {
	c    *Cassette
	name string
}

func (p *player) Name() string { return p.name }

func (p *player) Complete(_ context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	return p.c.replay(p.name, req)
}

// replay serves the recordings of key in order, repeating the last.
func (c *Cassette) replay(provider string, req domain.CompletionRequest) (domain.Completion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := Key(provider, req)
	var matches []Interaction
	for _, in := range c.Interactions {
		if in.Key == key {
			matches = append(matches, in)
		}
	}
	if len(matches) == 0 {
		return domain.Completion{}, c.mismatch(provider, key, req)
	}
	if c.served == nil {
		c.served = map[string]int{}
	}
	in := matches[min(c.served[key], len(matches)-1)]
	c.served[key]++
	if in.Error != nil {
		return completionOf(in), &domain.ProviderError{
			Provider: provider, Kind: in.Error.Kind, Status: in.Error.Status, Err: errors.New(in.Error.Message),
		}
	}
	return completionOf(in), nil
}

func completionOf(in Interaction) domain.Completion {
	if in.Response == nil {
		return domain.Completion{}
	}
	return *in.Response
}

// ErrMismatch is wrapped by every cassette miss.
var ErrMismatch = errors.New("cassette mismatch")

// MismatchError explains a miss.
type MismatchError struct {
	Path     string
	Provider string
	Key      string
	Task     domain.Task
	Model    string
	// Changed names what differs from the closest recording of the same
	// task, if there is one.
	Changed []string
}

func (e *MismatchError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v: %s has no recording of this %s request to %s/%s (key %.12s).", ErrMismatch, e.Path, e.Task, e.Provider, e.Model, e.Key)
	if len(e.Changed) > 0 {
		fmt.Fprintf(&b, " Compared with the recorded %s request, these changed: %s.", e.Task, strings.Join(e.Changed, "; "))
	}
	b.WriteString(" A prompt, the knowledge the tools returned, routing or the model changed since the cassette was recorded." +
		" Re-record it: `go test ./internal/intelligence/evals -run 'TestRecord.*Cassettes' -record=scripted` for hand-crafted answers," +
		" or with -tags=live and -record=live (needs the provider's API key) against the real provider, then review the diff.")
	return b.String()
}

func (e *MismatchError) Unwrap() error { return ErrMismatch }

func (c *Cassette) mismatch(provider, key string, req domain.CompletionRequest) error {
	e := &MismatchError{Path: c.path, Provider: provider, Key: key, Task: req.Task, Model: req.Model}
	var best []string
	for _, in := range c.Interactions {
		if in.Provider != provider || in.Request.Task != req.Task {
			continue
		}
		if d := Diff(in.Request, req); best == nil || len(d) < len(best) {
			best = d
		}
	}
	e.Changed = best
	return e
}

// Diff lists the fields in which b differs from a, with the first
// differing line of changed texts.
func Diff(a, b domain.CompletionRequest) []string {
	var out []string
	field := func(name string, x, y any) {
		if !jsonEqual(x, y) {
			out = append(out, fmt.Sprintf("%s: %s → %s", name, short(x), short(y)))
		}
	}
	field("model", a.Model, b.Model)
	field("max_tokens", a.MaxTokens, b.MaxTokens)
	field("temperature", a.Temperature, b.Temperature)
	field("effort", a.Effort, b.Effort)
	field("output schema", a.Output, b.Output)
	texts := func(name string, x, y []string) {
		if len(x) != len(y) {
			out = append(out, fmt.Sprintf("%s: %d parts → %d", name, len(x), len(y)))
		}
		for i := range min(len(x), len(y)) {
			if x[i] != y[i] {
				out = append(out, fmt.Sprintf("%s[%d] %s", name, i, firstLineDiff(x[i], y[i])))
			}
		}
	}
	var sa, sb, ma, mb []string
	for _, s := range a.System {
		sa = append(sa, s.Text)
	}
	for _, s := range b.System {
		sb = append(sb, s.Text)
	}
	for _, m := range a.Messages {
		ma = append(ma, string(m.Role)+": "+m.Text)
	}
	for _, m := range b.Messages {
		mb = append(mb, string(m.Role)+": "+m.Text)
	}
	texts("system", sa, sb)
	texts("messages", ma, mb)
	return out
}

func firstLineDiff(x, y string) string {
	lx, ly := strings.Split(x, "\n"), strings.Split(y, "\n")
	for i := range max(len(lx), len(ly)) {
		var a, b string
		if i < len(lx) {
			a = lx[i]
		}
		if i < len(ly) {
			b = ly[i]
		}
		if a != b {
			return fmt.Sprintf("line %d: %q → %q", i+1, a, b)
		}
	}
	return "(whitespace)"
}

func jsonEqual(x, y any) bool {
	a, _ := json.Marshal(x)
	b, _ := json.Marshal(y)
	return slices.Equal(a, b)
}

func short(v any) string {
	raw, _ := json.Marshal(v)
	if len(raw) > 60 {
		return string(raw[:60]) + "…"
	}
	return string(raw)
}

// Recorder wraps real providers and records every call into a cassette.
type Recorder struct {
	c *Cassette
}

// NewRecorder starts an empty cassette of the given source ("live" or
// "scripted").
func NewRecorder(source string) *Recorder {
	return &Recorder{c: &Cassette{Version: FormatVersion, Source: source}}
}

// Wrap returns p recording into the cassette.
func (r *Recorder) Wrap(p domain.Provider) domain.Provider { return &recording{r: r, next: p} }

// Cassette is the recording so far.
func (r *Recorder) Cassette() *Cassette { return r.c }

type recording struct {
	r    *Recorder
	next domain.Provider
}

func (p *recording) Name() string { return p.next.Name() }

func (p *recording) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	out, err := p.next.Complete(ctx, req)
	in := Interaction{Key: Key(p.Name(), req), Provider: p.Name(), Request: req}
	if out != (domain.Completion{}) {
		o := out
		in.Response = &o
	}
	var pe *domain.ProviderError
	switch {
	case errors.As(err, &pe):
		in.Error = &RecordedError{Kind: pe.Kind, Status: pe.Status, Message: pe.Error()}
	case err != nil:
		return out, err // not a provider answer (cancellation); nothing to record
	}
	p.r.c.mu.Lock()
	p.r.c.Interactions = append(p.r.c.Interactions, in)
	p.r.c.mu.Unlock()
	return out, err
}
