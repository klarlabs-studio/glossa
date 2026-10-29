package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/anthropic"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func request() domain.CompletionRequest {
	return domain.CompletionRequest{
		Task:  domain.TaskTranslate,
		Model: "claude-sonnet-5",
		System: []domain.SystemBlock{
			{Text: "You translate.", Cacheable: true},
		},
		Messages: []domain.Message{
			{Role: domain.RoleUser, Text: "Translate: Hello"},
			{Role: domain.RoleAssistant, Text: `{"message":"Hallo {","notes":""}`},
			{Role: domain.RoleUser, Text: "Fix it."},
		},
		Output:    &domain.OutputSchema{Name: "draft", Schema: map[string]any{"type": "object"}},
		MaxTokens: 4000,
		Effort:    "medium",
	}
}

type captured struct {
	header http.Header
	body   map[string]any
}

func server(t *testing.T, status int, headers map[string]string, response string, got *captured) *anthropic.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if got != nil {
			got.header = r.Header.Clone()
			if err := json.Unmarshal(raw, &got.body); err != nil {
				t.Errorf("request body: %v", err)
			}
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	p, err := anthropic.New(anthropic.Config{APIKey: "tenant-key", BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const okResponse = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5",
 "content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"text","text":"{\"message\":\"Hallo\",\"notes\":\"\"}"}],
 "stop_reason":"end_turn","stop_sequence":null,
 "usage":{"input_tokens":120,"output_tokens":30,"cache_read_input_tokens":800,"cache_creation_input_tokens":0}}`

func TestCompleteRequestAndResponse(t *testing.T) {
	var got captured
	p := server(t, 200, nil, okResponse, &got)
	out, err := p.Complete(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != `{"message":"Hallo","notes":""}` || out.Stop != domain.StopEnd || out.Model != "claude-sonnet-5" || out.Provider != "anthropic" {
		t.Errorf("completion = %+v", out)
	}
	if out.Usage != (domain.Usage{InputTokens: 120, OutputTokens: 30, CacheReadTokens: 800}) {
		t.Errorf("usage = %+v", out.Usage)
	}
	if got.header.Get("X-Api-Key") != "tenant-key" || got.header.Get("Anthropic-Version") == "" {
		t.Errorf("headers = %v", got.header)
	}
	b := got.body
	if b["model"] != "claude-sonnet-5" || b["max_tokens"] != float64(4000) {
		t.Errorf("model/max_tokens = %v/%v", b["model"], b["max_tokens"])
	}
	if _, ok := b["temperature"]; ok {
		t.Error("temperature must not be sent unless set (Sonnet 5 rejects it)")
	}
	system := b["system"].([]any)[0].(map[string]any)
	if system["text"] != "You translate." || system["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Errorf("system = %v", system)
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("messages = %v", msgs)
	}
	oc := b["output_config"].(map[string]any)
	if oc["effort"] != "medium" || oc["format"].(map[string]any)["type"] != "json_schema" {
		t.Errorf("output_config = %v", oc)
	}
}

func TestTemperatureSentWhenSet(t *testing.T) {
	var got captured
	p := server(t, 200, nil, okResponse, &got)
	req := request()
	temp := 0.2
	req.Temperature, req.Output, req.Effort = &temp, nil, ""
	if _, err := p.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got.body["temperature"] != 0.2 {
		t.Errorf("temperature = %v", got.body["temperature"])
	}
	if _, ok := got.body["output_config"]; ok {
		t.Error("no output_config without schema or effort")
	}
}

func TestErrorsAreClassified(t *testing.T) {
	errBody := func(typ string) string {
		return `{"type":"error","error":{"type":"` + typ + `","message":"nope"}}`
	}
	tests := []struct {
		status     int
		body       string
		headers    map[string]string
		kind       domain.ErrorKind
		retryAfter time.Duration
	}{
		{429, errBody("rate_limit_error"), map[string]string{"retry-after": "7"}, domain.KindRateLimited, 7 * time.Second},
		{529, errBody("overloaded_error"), nil, domain.KindUnavailable, 0},
		{500, errBody("api_error"), nil, domain.KindUnavailable, 0},
		{400, errBody("invalid_request_error"), nil, domain.KindInvalidRequest, 0},
		{401, errBody("authentication_error"), nil, domain.KindAuth, 0},
		{403, errBody("permission_error"), nil, domain.KindAuth, 0},
	}
	for _, tc := range tests {
		p := server(t, tc.status, tc.headers, tc.body, nil)
		_, err := p.Complete(context.Background(), request())
		var pe *domain.ProviderError
		if !errors.As(err, &pe) || pe.Kind != tc.kind || pe.Status != tc.status || pe.RetryAfter != tc.retryAfter {
			t.Errorf("%d: err = %#v", tc.status, err)
		}
	}
}

func TestStopReasons(t *testing.T) {
	refusal := `{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5","content":[],
	 "stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"},"usage":{"input_tokens":1,"output_tokens":0}}`
	truncated := `{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"{\"mess"}],
	 "stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":4000}}`
	for body, kind := range map[string]domain.ErrorKind{refusal: domain.KindRefused, truncated: domain.KindTruncated} {
		out, err := server(t, 200, nil, body, nil).Complete(context.Background(), request())
		var pe *domain.ProviderError
		if !errors.As(err, &pe) || pe.Kind != kind || pe.Retryable() {
			t.Errorf("err = %v, want non-retryable %s", err, kind)
		}
		if out.Usage.InputTokens != 1 {
			t.Error("usage of a failed answer is still reported, it was billed")
		}
	}
}

func TestTransportFailureIsOutage(t *testing.T) {
	p, err := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: "http://127.0.0.1:1/"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Complete(context.Background(), request())
	if !domain.IsRetryable(err) {
		t.Errorf("err = %v, want retryable outage", err)
	}
}

func TestConfigAndValidation(t *testing.T) {
	if _, err := anthropic.New(anthropic.Config{}); err == nil {
		t.Error("an API key is required")
	}
	p := server(t, 200, nil, okResponse, nil)
	_, err := p.Complete(context.Background(), domain.CompletionRequest{})
	var pe *domain.ProviderError
	if !errors.As(err, &pe) || pe.Kind != domain.KindInvalidRequest {
		t.Errorf("err = %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("Name = %q", p.Name())
	}
}
