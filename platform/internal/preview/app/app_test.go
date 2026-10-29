package app_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/preview/app"
)

// allow is a Limiter that records keys and allows until it's told not to.
type allow struct {
	keys []string
	deny bool
}

func (a *allow) Allow(_ context.Context, key string) bool {
	a.keys = append(a.keys, key)
	return !a.deny
}

func newService() (*app.Service, *allow) {
	l := &allow{}
	return app.New(l), l
}

func caller() context.Context { return authztest.Person(context.Background()) }

func TestPreviewConvertsMF1ToTheCanonicalModel(t *testing.T) {
	svc, _ := newService()
	res, err := svc.Preview(caller(), app.Input{
		Source: "{count, plural, one {# item for <b>{name}</b>} other {# items}}", Locale: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid() || res.Content.Model.Type != mf.SelectMessageType || len(res.Content.Arguments) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.HasPrefix(res.MF2, ".input {$count :number}") || res.Formatted != nil || len(res.Errors) != 0 {
		t.Errorf("mf2 = %q, formatted = %v, errors = %v", res.MF2, res.Formatted, res.Errors)
	}
	// The same model from MF2 source: one converter, one canonical form.
	again, err := svc.Preview(caller(), app.Input{Source: res.MF2, Syntax: "mf2", Locale: "en"})
	if err != nil || !again.Content.SameModel(res.Content) || again.MF2 != res.MF2 {
		t.Errorf("round trip = %+v, %v", again, err)
	}
}

func TestPreviewFormatsWithValues(t *testing.T) {
	svc, _ := newService()
	no := false
	res, err := svc.Preview(caller(), app.Input{
		Source: "{count, plural, one {# Artikel} other {# Artikel}} für {name}", Locale: "de",
		Values: map[string]any{"count": float64(1), "name": "Ada"}, BidiIsolation: &no,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Formatted == nil || *res.Formatted != "1 Artikel für Ada" || len(res.Errors) != 0 {
		t.Errorf("formatted = %v, errors = %+v", res.Formatted, res.Errors)
	}
	res, err = svc.Preview(caller(), app.Input{
		Source: "{n, plural, one {# day} other {# days}}", Locale: "en", Values: map[string]any{"n": 2.5}, BidiIsolation: &no,
	})
	if err != nil || res.Formatted == nil || *res.Formatted != "2.5 days" {
		t.Errorf("fractional = %v, %v", res.Formatted, err)
	}
	// Isolation is the MF2 default.
	res, err = svc.Preview(caller(), app.Input{Source: "Hi {name}", Locale: "en", Values: map[string]any{"name": "Ada"}})
	if err != nil || res.Formatted == nil || *res.Formatted != "Hi ⁨Ada⁩" {
		t.Errorf("isolated = %q, %v", *res.Formatted, err)
	}
}

func TestPreviewReportsFormatErrorsWithAFallback(t *testing.T) {
	svc, _ := newService()
	no := false
	res, err := svc.Preview(caller(), app.Input{
		Source: "Hello {name}", Locale: "en", Values: map[string]any{}, BidiIsolation: &no,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid() || res.Formatted == nil || *res.Formatted != "Hello {$name}" {
		t.Errorf("formatted = %v", res.Formatted)
	}
	if len(res.Errors) != 1 || res.Errors[0].Stage != app.StageFormat || res.Errors[0].Code != string(mf.CodeUnresolvedVariable) {
		t.Errorf("errors = %+v", res.Errors)
	}
}

func TestPreviewReportsParseErrorsInTheResult(t *testing.T) {
	svc, _ := newService()
	for _, in := range []app.Input{
		{Source: "{count, plural, one {x}", Locale: "en"},
		{Source: "{count, plural, few {x} other {y}}", Locale: "en", Values: map[string]any{"count": 1.0}},
		{Source: "{{unclosed", Syntax: "mf2", Locale: "en"},
	} {
		res, err := svc.Preview(caller(), in)
		if err != nil {
			t.Fatalf("%q: %v", in.Source, err)
		}
		if res.Valid() || res.Formatted != nil || res.MF2 != "" || len(res.Errors) != 1 || res.Errors[0].Stage != app.StageParse ||
			res.Errors[0].Code == "" || res.Errors[0].Message == "" {
			t.Errorf("%q: %+v", in.Source, res)
		}
	}
}

func TestPreviewRejectsBadInput(t *testing.T) {
	svc, _ := newService()
	many := map[string]any{}
	for i := range app.MaxValues + 1 {
		many[fmt.Sprintf("v%d", i)] = 1.0
	}
	for name, tc := range map[string]struct {
		in   app.Input
		want error
	}{
		"too long":     {app.Input{Source: strings.Repeat("a", mfcontent.MaxTextBytes+1), Locale: "en"}, mfcontent.ErrTooLong},
		"bad locale":   {app.Input{Source: "x", Locale: "not a locale"}, bcp47.ErrInvalid},
		"bad syntax":   {app.Input{Source: "x", Syntax: "xliff", Locale: "en"}, mfcontent.ErrInvalidSyntax},
		"object value": {app.Input{Source: "x", Locale: "en", Values: map[string]any{"a": map[string]any{}}}, app.ErrInvalidValues},
		"array value":  {app.Input{Source: "x", Locale: "en", Values: map[string]any{"a": []any{}}}, app.ErrInvalidValues},
		"null value":   {app.Input{Source: "x", Locale: "en", Values: map[string]any{"a": nil}}, app.ErrInvalidValues},
		"long value":   {app.Input{Source: "x", Locale: "en", Values: map[string]any{"a": strings.Repeat("a", app.MaxValueBytes+1)}}, app.ErrInvalidValues},
		"long name":    {app.Input{Source: "x", Locale: "en", Values: map[string]any{strings.Repeat("a", app.MaxValueNameLen+1): "x"}}, app.ErrInvalidValues},
		"too many":     {app.Input{Source: "x", Locale: "en", Values: many}, app.ErrInvalidValues},
		"empty name":   {app.Input{Source: "x", Locale: "en", Values: map[string]any{"": "x"}}, app.ErrInvalidValues},
	} {
		if _, err := svc.Preview(caller(), tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestPreviewIsRateLimitedPerCaller(t *testing.T) {
	svc, l := newService()
	ctx := caller()
	if _, err := svc.Preview(ctx, app.Input{Source: "x", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	p, _ := authz.From(ctx)
	if len(l.keys) != 1 || l.keys[0] != p.Actor.String() {
		t.Errorf("limiter keys = %v, want the actor", l.keys)
	}
	l.deny = true
	if _, err := svc.Preview(ctx, app.Input{Source: "x", Locale: "en"}); !errors.Is(err, app.ErrRateLimited) {
		t.Errorf("err = %v, want ErrRateLimited", err)
	}
	if _, err := svc.Preview(context.Background(), app.Input{Source: "x", Locale: "en"}); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("anonymous err = %v", err)
	}
}
