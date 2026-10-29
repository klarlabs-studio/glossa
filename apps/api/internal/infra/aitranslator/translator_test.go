package aitranslator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	infra "go.klarlabs.de/glossa/apps/api/internal/infra/aitranslator"

	domain "go.klarlabs.de/glossa/apps/api/internal/domain/aitranslator"
)

func newTranslator(stub *httptest.Server) *infra.Translator {
	c := &http.Client{Timeout: 5 * time.Second}
	return infra.New().WithHTTPClient(c)
}

func TestOpenAITranslate(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("missing bearer; got %q", got)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("wrong path: %q", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-4o" {
			t.Errorf("wrong model: %v", body["model"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Hello"}}]}`))
	}))
	defer stub.Close()

	tr := newTranslator(stub)
	res, err := tr.Translate(context.Background(), domain.Provider{
		Kind: domain.KindOpenAI, BaseURL: stub.URL, Model: "gpt-4o",
	}, []byte("sk-test"), domain.TranslateRequest{
		Key: "hi", SourceLocale: "de", TargetLocale: "en", Source: "Hallo",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if res.Translation != "Hello" {
		t.Errorf("got %q want Hello", res.Translation)
	}
	if res.Provider != "openai" {
		t.Errorf("provider %q", res.Provider)
	}
}

func TestAnthropicTranslate(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ant-test" {
			t.Errorf("missing key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing version header")
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"Greetings"}]}`))
	}))
	defer stub.Close()
	tr := newTranslator(stub)
	res, err := tr.Translate(context.Background(), domain.Provider{
		Kind: domain.KindAnthropic, BaseURL: stub.URL, Model: "claude-3-5",
	}, []byte("ant-test"), domain.TranslateRequest{
		Key: "hi", SourceLocale: "de", TargetLocale: "en", Source: "Hallo",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if res.Translation != "Greetings" {
		t.Errorf("got %q", res.Translation)
	}
}

func TestGeminiTranslate(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "key=gem-test") {
			t.Errorf("missing api-key qs: %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Yo"}]}}]}`))
	}))
	defer stub.Close()
	tr := newTranslator(stub)
	res, err := tr.Translate(context.Background(), domain.Provider{
		Kind: domain.KindGemini, BaseURL: stub.URL, Model: "gemini-2.0",
	}, []byte("gem-test"), domain.TranslateRequest{
		Key: "hi", SourceLocale: "de", TargetLocale: "en", Source: "Hallo",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if res.Translation != "Yo" {
		t.Errorf("got %q", res.Translation)
	}
}

func TestProviderErrorSurfaces(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer stub.Close()
	tr := newTranslator(stub)
	_, err := tr.Translate(context.Background(), domain.Provider{
		Kind: domain.KindOpenAI, BaseURL: stub.URL, Model: "gpt-4o",
	}, []byte("nope"), domain.TranslateRequest{Source: "x"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("want 401 in err, got %v", err)
	}
}

func TestEmptyResponseRejected(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"   "}}]}`))
	}))
	defer stub.Close()
	tr := newTranslator(stub)
	_, err := tr.Translate(context.Background(), domain.Provider{
		Kind: domain.KindOpenAI, BaseURL: stub.URL, Model: "gpt-4o",
	}, []byte("k"), domain.TranslateRequest{Source: "x"})
	if err == nil {
		t.Fatal("expected error on empty translation")
	}
}
