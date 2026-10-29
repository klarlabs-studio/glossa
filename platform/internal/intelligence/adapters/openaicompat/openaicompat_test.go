package openaicompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/openaicompat"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func request() domain.CompletionRequest {
	return domain.CompletionRequest{
		Model:     "llama-3.3",
		System:    []domain.SystemBlock{{Text: "You translate.", Cacheable: true}, {Text: "Be brief."}},
		Messages:  []domain.Message{{Role: domain.RoleUser, Text: "Hello"}},
		Output:    &domain.OutputSchema{Name: "draft", Schema: map[string]any{"type": "object"}},
		MaxTokens: 500,
	}
}

func serve(t *testing.T, status int, header map[string]string, response string, body *map[string]any, auth *string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		raw, _ := io.ReadAll(r.Body)
		if body != nil {
			_ = json.Unmarshal(raw, body)
		}
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1/"
}

const ok = `{"model":"llama-3.3","choices":[{"message":{"content":"{\"message\":\"Hallo\",\"notes\":\"\"}"},"finish_reason":"stop"}],
 "usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":60}}}`

func TestComplete(t *testing.T) {
	var body map[string]any
	var auth string
	url := serve(t, 200, nil, ok, &body, &auth)
	p, err := openaicompat.New(openaicompat.Config{Name: "local", BaseURL: url, APIKey: "k", StrictSchema: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Complete(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != `{"message":"Hallo","notes":""}` || out.Stop != domain.StopEnd || out.Provider != "local" {
		t.Errorf("out = %+v", out)
	}
	if out.Usage != (domain.Usage{InputTokens: 40, OutputTokens: 10, CacheReadTokens: 60}) {
		t.Errorf("usage = %+v", out.Usage)
	}
	if auth != "Bearer k" {
		t.Errorf("auth = %q", auth)
	}
	msgs := body["messages"].([]any)
	if first := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "You translate.\n\nBe brief." {
		t.Errorf("system message = %v", first)
	}
	if body["max_tokens"] != float64(500) {
		t.Errorf("max_tokens = %v", body["max_tokens"])
	}
	if _, ok := body["temperature"]; ok {
		t.Error("temperature sent without being set")
	}
	rf := body["response_format"].(map[string]any)
	if rf["type"] != "json_schema" || rf["json_schema"].(map[string]any)["strict"] != true {
		t.Errorf("response_format = %v", rf)
	}
}

func TestMaxCompletionTokensAndNoKey(t *testing.T) {
	var body map[string]any
	var auth string
	url := serve(t, 200, nil, ok, &body, &auth)
	p, _ := openaicompat.New(openaicompat.Config{Name: "openai", BaseURL: url, MaxCompletionTokens: true})
	req := request()
	req.Output = nil
	if _, err := p.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if body["max_completion_tokens"] != float64(500) || body["max_tokens"] != nil || auth != "" || body["response_format"] != nil {
		t.Errorf("body = %v, auth = %q", body, auth)
	}
}

func TestFailures(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		header     map[string]string
		response   string
		kind       domain.ErrorKind
		retryAfter time.Duration
	}{
		{"rate limited", 429, map[string]string{"Retry-After": "3"}, `{"error":{}}`, domain.KindRateLimited, 3 * time.Second},
		{"server error", 502, nil, `bad gateway`, domain.KindUnavailable, 0},
		{"bad request", 400, nil, `{"error":{}}`, domain.KindInvalidRequest, 0},
		{"unauthorized", 401, nil, `{}`, domain.KindAuth, 0},
		{"not json", 200, nil, `<html>`, domain.KindBadResponse, 0},
		{"no choices", 200, nil, `{"choices":[]}`, domain.KindBadResponse, 0},
		{"truncated", 200, nil, `{"choices":[{"message":{"content":"{"},"finish_reason":"length"}]}`, domain.KindTruncated, 0},
		{"refused", 200, nil, `{"choices":[{"message":{"content":"","refusal":"no"},"finish_reason":"stop"}]}`, domain.KindRefused, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := openaicompat.New(openaicompat.Config{Name: "x", BaseURL: serve(t, tc.status, tc.header, tc.response, nil, nil)})
			_, err := p.Complete(context.Background(), request())
			var pe *domain.ProviderError
			if !errors.As(err, &pe) || pe.Kind != tc.kind || pe.RetryAfter != tc.retryAfter {
				t.Errorf("err = %#v", err)
			}
		})
	}
}

func TestConfig(t *testing.T) {
	if _, err := openaicompat.New(openaicompat.Config{Name: "x"}); err == nil {
		t.Error("base URL required")
	}
	p, _ := openaicompat.New(openaicompat.Config{Name: "x", BaseURL: "http://127.0.0.1:1"})
	if _, err := p.Complete(context.Background(), request()); !domain.IsRetryable(err) {
		t.Errorf("transport failure: %v", err)
	}
	if _, err := p.Complete(context.Background(), domain.CompletionRequest{}); domain.IsRetryable(err) || err == nil {
		t.Errorf("invalid request: %v", err)
	}
}
