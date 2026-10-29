//go:build system

package m4_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The provider guard.
//
// RFC 0005 §14 decision 2: `glossa check` never calls an AI provider,
// and §12's exit test must not either. The nine layers of §12.2 are all
// deterministic — `linguistic` is not among them — so a correct run of
// this suite makes no model call at all.
//
// Rather than assume that, the test proves it. The tenant's only
// configured provider is this one: an OpenAI-compatible endpoint on
// loopback that answers every request with a 400 and counts it. A layer
// that reached for a model would fail its job loudly instead of quietly
// costing money, and the count below goes into REPORT.md so a future
// run that starts calling a provider is visible rather than silent.
//
// Nothing here reaches the network. The endpoint is a httptest server on
// 127.0.0.1 and the key is this file's.
const (
	fakeProviderName = "m4-guard"
	fakeAPIKey       = "sk-m4-guard"
	fakeProviderKind = "openai"
)

type fakeProvider struct {
	srv *httptest.Server
	mu  sync.Mutex
	// calls counts every request that reached it, refused or not.
	calls int
	paths []string
}

func startFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	p := &fakeProvider{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		p.mu.Lock()
		p.calls++
		p.paths = append(p.paths, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"the M4 exit test makes no model calls: nothing should ask this endpoint for anything","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeProvider) baseURL() string { return p.srv.URL + "/v1" }

// Calls is how many requests reached the provider guard. The exit
// criterion is zero.
func (p *fakeProvider) Calls() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]string(nil), p.paths...)
}
