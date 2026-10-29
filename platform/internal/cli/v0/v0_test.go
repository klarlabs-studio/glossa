package v0_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
)

func TestReviewStateMapping(t *testing.T) {
	for status, want := range map[string]string{
		"approved": "approved", "needs_review": "needs_review", "ai_translated": "needs_review", "pending": "draft", "": "draft",
	} {
		if got := v0.ReviewState(status); got != want {
			t.Errorf("%q → %q, want %q", status, got, want)
		}
	}
}

func TestBuildPlanConvertsMF1AndSkipsGaps(t *testing.T) {
	plan, err := v0.BuildPlan("de", map[string]v0.Bundle{
		"de": {Messages: map[string]string{
			"cart.items": "{count, plural, one {# Artikel} other {# Artikel}}",
			"cart.empty": "",
			"broken":     "{count, plural, one {x}",
			"hello":      "Hallo {name}",
		}},
		"en": {Messages: map[string]string{
			"cart.items": "{count, plural, one {# item} other {# items}}",
			"hello":      "",
			"broken":     "x",
			"cart.empty": "Empty",
		}, Statuses: map[string]string{"cart.items": "ai_translated", "broken": "approved", "cart.empty": "approved"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Messages) != 3 || plan.Messages[0].Key != "broken" || plan.Messages[0].Invalid == nil {
		t.Fatalf("messages = %+v", plan.Messages)
	}
	if len(plan.Translations) != 1 {
		t.Fatalf("translations = %+v", plan.Translations)
	}
	tr := plan.Translations[0]
	if tr.Key != "cart.items" || tr.Locale != "en" || tr.State != "needs_review" || tr.V0Status != "ai_translated" || tr.Invalid != nil {
		t.Errorf("translation = %+v", tr)
	}
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		reasons[s.Locale+" "+s.Key] = s.Reason
	}
	if reasons["de cart.empty"] == "" || reasons["en broken"] == "" || reasons["en cart.empty"] == "" {
		t.Errorf("skipped = %+v", plan.Skipped)
	}
	if strings.Join(plan.Locales, ",") != "en" {
		t.Errorf("locales = %v", plan.Locales)
	}
}

func TestBuildPlanNeedsTheSourceLocale(t *testing.T) {
	_, err := v0.BuildPlan("fr", map[string]v0.Bundle{"de": {}, "en": {}})
	if err == nil || !strings.Contains(err.Error(), "de, en") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientReadsTheV03API(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer glossa_abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/projects/site/locales":
			_, _ = io.WriteString(w, `[{"id":"1","code":"de","label":"Deutsch","enabled":true},{"id":"2","code":"en","label":"English","enabled":true}]`)
		case "/api/v1/projects/site/locales/de/messages":
			_, _ = io.WriteString(w, `{"project":"site","locale":"de","messages":{"a":"A"},"statuses":{"a":"approved"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := v0.New(srv.URL, "glossa_abc", nil, "test")
	ls, err := c.Locales(context.Background(), "site")
	if err != nil || strings.Join(ls, ",") != "de,en" {
		t.Fatalf("locales = %v, %v", ls, err)
	}
	b, err := c.Bundle(context.Background(), "site", "de")
	if err != nil || b.Messages["a"] != "A" || b.Statuses["a"] != "approved" {
		t.Fatalf("bundle = %+v, %v", b, err)
	}
	calls.Store(0)
	_, err = v0.New(srv.URL+"/api/v1", "wrong", nil, "test").Locales(context.Background(), "site")
	var se *v0.StatusError
	if !errors.As(err, &se) || se.Status != 401 || calls.Load() != 1 {
		t.Errorf("err = %v after %d calls; a 401 must not be retried", err, calls.Load())
	}
}
