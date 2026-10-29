//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
)

type messagePreview struct {
	Valid     bool           `json:"valid"`
	Message   map[string]any `json:"message"`
	MF2       *string        `json:"mf2"`
	Arguments []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"arguments"`
	Markup    []any   `json:"markup"`
	Formatted *string `json:"formatted"`
	Errors    []struct {
		Stage, Code, Message string
	} `json:"errors"`
}

// Studio previews MF1 through the server's one converter; the CLI's
// token can too. Nothing is stored and the caller must be known.
func TestMessagePreviewOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var me struct {
		Person struct {
			IndividualTenantID string `json:"individual_tenant_id"`
		} `json:"person"`
	}
	s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie}).decode(t, &me)
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: "/v1/tenants/" + me.Person.IndividualTenantID + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "cli", "scopes": []string{"read"}}}).decode(t, &tok)

	body := map[string]any{
		"source": "{count, plural, one {# Artikel} other {# Artikel}} für {name}", "locale": "de",
		"values": map[string]any{"count": 1, "name": "Ada"}, "bidi_isolation": false,
	}
	r := s.do(call{method: "POST", path: "/v1/message-previews", cookie: ada.cookie, csrf: ada.csrf, body: body})
	r.want(t, http.StatusOK, "")
	var p messagePreview
	r.decode(t, &p)
	if !p.Valid || p.Message["type"] != "select" || p.MF2 == nil || !strings.HasPrefix(*p.MF2, ".input {$count :number}") ||
		len(p.Arguments) != 2 || p.Markup == nil || p.Formatted == nil || *p.Formatted != "1 Artikel für Ada" || len(p.Errors) != 0 {
		t.Fatalf("preview = %s", r.body)
	}

	// Source that doesn't parse is a result, not a failure.
	p = messagePreview{}
	r = s.do(call{method: "POST", path: "/v1/message-previews", bearer: tok.Secret,
		body: map[string]any{"source": "{count, plural, one {x}", "locale": "en"}})
	r.want(t, http.StatusOK, "")
	r.decode(t, &p)
	if p.Valid || p.MF2 != nil || p.Message != nil || len(p.Errors) != 1 || p.Errors[0].Stage != "parse" ||
		p.Errors[0].Code != "mf1-syntax-error" || p.Arguments == nil {
		t.Errorf("invalid preview = %s", r.body)
	}
	// Formatting problems come back with the fallback text.
	r = s.do(call{method: "POST", path: "/v1/message-previews", bearer: tok.Secret,
		body: map[string]any{"source": "Hello {$name}", "syntax": "mf2", "locale": "en", "values": map[string]any{}, "bidi_isolation": false}})
	p = messagePreview{}
	r.decode(t, &p)
	if !p.Valid || p.Formatted == nil || *p.Formatted != "Hello {$name}" || len(p.Errors) != 1 || p.Errors[0].Code != "unresolved-variable" {
		t.Errorf("format problem = %s", r.body)
	}

	for _, tc := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"source": strings.Repeat("a", 20001), "locale": "en"}, "message_too_long"},
		{map[string]any{"source": "x", "locale": "not a locale"}, "invalid_locale"},
		{map[string]any{"source": "x", "locale": "en", "syntax": "xliff"}, "invalid_syntax"},
		{map[string]any{"source": "x", "locale": "en", "values": map[string]any{"a": []int{1}}}, "invalid_values"},
	} {
		s.do(call{method: "POST", path: "/v1/message-previews", bearer: tok.Secret, body: tc.body}).want(t, http.StatusBadRequest, tc.code)
	}
	s.do(call{method: "POST", path: "/v1/message-previews", body: body}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "POST", path: "/v1/message-previews", cookie: ada.cookie, body: body}).want(t, http.StatusForbidden, "csrf_invalid")
}

// Previews cost CPU, so each caller gets a budget: bursts of 120, then
// 10 a second. Another caller is unaffected.
func TestMessagePreviewIsRateLimited(t *testing.T) {
	s := startServer(t)
	ada, bob := s.signIn("ada@example.com"), s.signIn("bob@example.com")
	body := map[string]any{"source": "Hi", "locale": "en"}
	// The burst, plus whatever refilled while the loop ran, succeeds;
	// then requests are refused (unless each took over 100 ms).
	allowed := 0
	for range 400 {
		r := s.do(call{method: "POST", path: "/v1/message-previews", cookie: ada.cookie, csrf: ada.csrf, body: body})
		if r.status == http.StatusTooManyRequests {
			r.want(t, http.StatusTooManyRequests, "rate_limited")
			break
		}
		r.want(t, http.StatusOK, "")
		allowed++
	}
	if allowed < 120 || allowed == 400 {
		t.Errorf("%d requests allowed before the first 429", allowed)
	}
	s.do(call{method: "POST", path: "/v1/message-previews", cookie: bob.cookie, csrf: bob.csrf, body: body}).want(t, http.StatusOK, "")
}
