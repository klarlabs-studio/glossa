package gemini_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/gemini"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func request() domain.CompletionRequest {
	temp := 0.1
	return domain.CompletionRequest{
		Model:       "gemini-2.5-pro",
		System:      []domain.SystemBlock{{Text: "You translate."}},
		Messages:    []domain.Message{{Role: domain.RoleUser, Text: "Hello"}, {Role: domain.RoleAssistant, Text: "{}"}, {Role: domain.RoleUser, Text: "Fix"}},
		Output:      &domain.OutputSchema{Name: "draft", Schema: map[string]any{"type": "object"}},
		MaxTokens:   800,
		Temperature: &temp,
	}
}

func serve(t *testing.T, status int, response string, body *map[string]any) *gemini.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.5-pro:generateContent" || r.Header.Get("x-goog-api-key") != "key" {
			t.Errorf("path %s, key %q", r.URL.Path, r.Header.Get("x-goog-api-key"))
		}
		raw, _ := io.ReadAll(r.Body)
		if body != nil {
			_ = json.Unmarshal(raw, body)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	p, err := gemini.New(gemini.Config{APIKey: "key", BaseURL: srv.URL + "/v1beta/"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestComplete(t *testing.T) {
	var body map[string]any
	p := serve(t, 200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"message\":"},{"text":"\"Hallo\"}"}]},"finishReason":"STOP"}],
	 "usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":5,"cachedContentTokenCount":20}}`, &body)
	out, err := p.Complete(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != `{"message":"Hallo"}` || out.Stop != domain.StopEnd || out.Provider != "gemini" || out.Model != "gemini-2.5-pro" {
		t.Errorf("out = %+v", out)
	}
	if out.Usage != (domain.Usage{InputTokens: 80, OutputTokens: 15, CacheReadTokens: 20}) {
		t.Errorf("usage = %+v", out.Usage)
	}
	contents := body["contents"].([]any)
	if contents[1].(map[string]any)["role"] != "model" {
		t.Errorf("assistant turns are role model: %v", contents)
	}
	gc := body["generationConfig"].(map[string]any)
	if gc["maxOutputTokens"] != float64(800) || gc["temperature"] != 0.1 || gc["responseMimeType"] != "application/json" || gc["responseJsonSchema"] == nil {
		t.Errorf("generationConfig = %v", gc)
	}
	if body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"] != "You translate." {
		t.Errorf("systemInstruction = %v", body["systemInstruction"])
	}
}

func TestFailures(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		response string
		kind     domain.ErrorKind
	}{
		{"quota", 429, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, domain.KindRateLimited},
		{"unavailable", 503, `{"error":{}}`, domain.KindUnavailable},
		{"bad key", 403, `{"error":{}}`, domain.KindAuth},
		{"blocked prompt", 200, `{"promptFeedback":{"blockReason":"SAFETY"}}`, domain.KindRefused},
		{"safety finish", 200, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`, domain.KindRefused},
		{"truncated", 200, `{"candidates":[{"content":{"parts":[{"text":"{"}]},"finishReason":"MAX_TOKENS"}]}`, domain.KindTruncated},
		{"no candidates", 200, `{"candidates":[]}`, domain.KindBadResponse},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := serve(t, tc.status, tc.response, nil).Complete(context.Background(), request())
			var pe *domain.ProviderError
			if !errors.As(err, &pe) || pe.Kind != tc.kind {
				t.Errorf("err = %#v", err)
			}
		})
	}
	if _, err := gemini.New(gemini.Config{}); err == nil {
		t.Error("an API key is required")
	}
}
