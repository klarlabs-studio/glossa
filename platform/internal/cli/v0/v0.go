// Package v0 reads a Glossa v0.3 deployment through its read API and
// plans the import into the new platform (RFC 0002 §12.3, "Migrate
// v0.3"): v0.3 keys become messages (their source-locale text parsed as
// ICU MessageFormat 1 by the kernel), the other locales' values become
// translations with provenance import, and v0.3 statuses become review
// states.
//
// v0.3 API (frozen): GET {api}/projects/{slug}/locales and
// GET {api}/projects/{slug}/locales/{locale}/messages →
// {project, locale, messages: {key: value}, statuses: {key: status}},
// with a project API key as bearer token. Keys without a translation come
// back with an empty value and no status.
//
// It also reads a restored v0.3 backup directly (ReadRestore, RFC 0006
// §7.2), which carries what the API doesn't expose: see db.go for the
// read, restore.go for the restore marker it insists on, and dbplan.go
// for the plan, including what it deliberately does not carry.
package v0

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/fortify/retry"

	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Client reads one v0.3 deployment.
type Client struct {
	base  string
	key   string
	http  *http.Client
	ua    string
	retry retry.Retry[[]byte]
}

// New returns a client for the v0.3 API at base (…/api/v1; appended when
// missing) with a project API key.
func New(base, key string, hc *http.Client, userAgent string) *Client {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/api/v1") {
		base += "/api/v1"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: base, key: key, http: hc, ua: userAgent,
		retry: retry.New[[]byte](retry.Config{MaxAttempts: 3, InitialDelay: 200 * time.Millisecond, Jitter: true,
			IsRetryable: func(err error) bool {
				var se *StatusError
				return !errors.As(err, &se) || se.Status >= 500 || se.Status == http.StatusTooManyRequests
			}})}
}

// Base is the API base URL.
func (c *Client) Base() string { return c.base }

// StatusError is a v0.3 API refusal.
type StatusError struct {
	URL    string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GET %s: %d %s", e.URL, e.Status, strings.TrimSpace(e.Body))
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	u := c.base + path
	body, err := c.retry.Execute(ctx, func(ctx context.Context) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Accept", "application/json")
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, &StatusError{URL: u, Status: resp.StatusCode, Body: truncate(string(b), 300)}
		}
		return b, nil
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("GET %s: not the v0.3 JSON shape: %w", u, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Locales lists the project's locale codes.
func (c *Client) Locales(ctx context.Context, project string) ([]string, error) {
	var ls []struct {
		Code string `json:"code"`
	}
	if err := c.get(ctx, "/projects/"+url.PathEscape(project)+"/locales", &ls); err != nil {
		return nil, err
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Code
	}
	return out, nil
}

// Bundle is one locale of a v0.3 project.
type Bundle struct {
	Project  string            `json:"project"`
	Locale   string            `json:"locale"`
	Messages map[string]string `json:"messages"`
	Statuses map[string]string `json:"statuses"`
}

// Bundle reads one locale's messages and statuses.
func (c *Client) Bundle(ctx context.Context, project, locale string) (Bundle, error) {
	var b Bundle
	err := c.get(ctx, "/projects/"+url.PathEscape(project)+"/locales/"+url.PathEscape(locale)+"/messages", &b)
	return b, err
}

// ── mapping ─────────────────────────────────────────────────────────

// ReviewState maps a v0.3 status to a review state: approved stays
// approved; needs_review and ai_translated (machine output nobody
// reviewed) need review; pending is a draft.
func ReviewState(status string) string {
	switch status {
	case "approved":
		return "approved"
	case "needs_review", "ai_translated":
		return "needs_review"
	default:
		return "draft"
	}
}

// MessageItem is a v0.3 key as a message.
type MessageItem struct {
	Key  string
	Text string
	// Description is v0.3's keys.description; only a restore (--v0-db)
	// has it, the read API doesn't expose it.
	Description string
	Invalid     *snapshot.Invalid
}

// TranslationItem is a v0.3 value as a translation.
type TranslationItem struct {
	Key      string
	Locale   string
	Text     string
	V0Status string
	State    string
	// Detail is the v0.3 row's provenance for the import revision's
	// origin_detail (v0_status, v0_updated_by, v0_updated_at,
	// v0_translation_id); only a restore has it.
	Detail  map[string]any
	Invalid *snapshot.Invalid
}

// Skip is a v0.3 entry that isn't imported.
type Skip struct {
	Key    string
	Locale string
	Reason string
}

// Plan is what an import writes.
type Plan struct {
	SourceLocale string
	// Locales are the canonical target locales, sorted.
	Locales      []string
	Messages     []MessageItem
	Translations []TranslationItem
	Skipped      []Skip
}

// BuildPlan turns bundles (canonical locale → bundle) into a plan for a
// project whose source locale is source. Every text is parsed as ICU
// MessageFormat 1 for its locale; what doesn't parse is marked Invalid.
func BuildPlan(source string, bundles map[string]Bundle) (Plan, error) {
	src, ok := bundles[source]
	if !ok {
		have := make([]string, 0, len(bundles))
		for l := range bundles {
			have = append(have, l)
		}
		sort.Strings(have)
		return Plan{}, fmt.Errorf("the v0.3 project has no %s locale (it has %s); the new project's source locale must be one of them",
			source, strings.Join(have, ", "))
	}
	p := Plan{SourceLocale: source}
	keys := sortedKeys(src.Messages)
	present := map[string]bool{}
	for _, k := range keys {
		text := src.Messages[k]
		if strings.TrimSpace(text) == "" {
			p.Skipped = append(p.Skipped, Skip{Key: k, Locale: source, Reason: "no source text in v0.3"})
			continue
		}
		_, _, invalid := snapshot.Parse("mf1", text, source)
		p.Messages = append(p.Messages, MessageItem{Key: k, Text: text, Invalid: invalid})
		present[k] = invalid == nil
	}
	for _, l := range sortedKeys(bundles) {
		if l == source {
			continue
		}
		p.Locales = append(p.Locales, l)
		b := bundles[l]
		for _, k := range sortedKeys(b.Messages) {
			text := b.Messages[k]
			switch {
			case strings.TrimSpace(text) == "":
				continue // untranslated in v0.3: nothing to import
			case !present[k]:
				p.Skipped = append(p.Skipped, Skip{Key: k, Locale: l, Reason: "its message isn't imported (no or invalid source text)"})
				continue
			}
			status := b.Statuses[k]
			_, _, invalid := snapshot.Parse("mf1", text, l)
			p.Translations = append(p.Translations, TranslationItem{Key: k, Locale: l, Text: text, V0Status: status,
				State: ReviewState(status), Invalid: invalid})
		}
	}
	return p, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Canonical canonicalizes a v0.3 locale code.
func Canonical(code string) (string, error) {
	tag, err := bcp47.Parse(code)
	if err != nil {
		return "", err
	}
	return tag.String(), nil
}
