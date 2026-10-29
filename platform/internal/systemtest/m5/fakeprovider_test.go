//go:build system

package m5_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The fake AI provider: an OpenAI-compatible /chat/completions endpoint
// on loopback. It exists so §12.2's sweep has an AI fill, an AI job and
// an AI suggestion in each project to address — no criterion grades
// what a model says, so it answers every draft with the same German
// and every assessment with the same fluent, on-style score. Anything
// else is refused with a 400, so an unexpected request fails its job
// instead of being answered.
//
// Nothing here reaches the network: the endpoint is an httptest server
// on 127.0.0.1 and the key is this file's.
const (
	fakeProviderName = "m5-fake"
	fakeAPIKey       = "sk-m5-fake"
	translateModel   = "m5-translate"
	assessModel      = "m5-assess"
	// fakeDraft is the only translation the fake ever drafts. It holds
	// no canary and no key, so it can never be mistaken for a leak.
	fakeDraft = "Maschinenentwurf"
)

type fakeProvider struct {
	srv *httptest.Server
	mu  sync.Mutex
	// calls counts the requests that reached it, by response format;
	// refused lists the ones it refused.
	calls   map[string]int
	refused []string
}

func startFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	p := &fakeProvider{calls: map[string]int{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeProvider) baseURL() string { return p.srv.URL + "/v1" }

func (p *fakeProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+fakeAPIKey {
		p.refuse(w, r.Method+" "+r.URL.Path)
		return
	}
	var req struct {
		Model          string `json:"model"`
		ResponseFormat struct {
			JSONSchema struct {
				Name string `json:"name"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		p.refuse(w, "undecodable request: "+err.Error())
		return
	}
	var answer any
	switch format := req.ResponseFormat.JSONSchema.Name; format {
	case "translation":
		answer = map[string]string{"message": fakeDraft, "notes": ""}
	case "assessment":
		answer = map[string]any{"score": 0.93, "formality_ok": true, "issues": []string{}}
	default:
		p.refuse(w, "unknown response format "+format)
		return
	}
	p.mu.Lock()
	p.calls[req.ResponseFormat.JSONSchema.Name]++
	p.mu.Unlock()
	content, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-m5", "object": "chat.completion", "model": req.Model,
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": string(content)}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 600, "completion_tokens": len(content)/4 + 1},
	})
}

func (p *fakeProvider) refuse(w http.ResponseWriter, why string) {
	p.mu.Lock()
	p.refused = append(p.refused, why)
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "invalid_request_error", "message": "unscripted request: " + why}})
}
