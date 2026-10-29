package providers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/providers"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// A tenant can't point a provider at the deployment's own network: the
// client refuses private and loopback addresses when it dials.
func TestSafeClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()

	_, err := providers.SafeClient(false).Get(srv.URL)
	if !errors.Is(err, providers.ErrPrivateAddress) {
		t.Fatalf("err = %v, want the private-address refusal", err)
	}
	resp, err := providers.SafeClient(true).Get(srv.URL)
	if err != nil {
		t.Fatalf("with private endpoints allowed: %v", err)
	}
	_ = resp.Body.Close()
}

type slowProvider struct {
	inFlight, peak atomic.Int32
}

func (s *slowProvider) Name() string { return "slow" }

func (s *slowProvider) Complete(context.Context, domain.CompletionRequest) (domain.Completion, error) {
	n := s.inFlight.Add(1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	s.inFlight.Add(-1)
	return domain.Completion{Text: "ok"}, nil
}

func TestLimitCapsConcurrency(t *testing.T) {
	slow := &slowProvider{}
	p := providers.Limit(slow, 2)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = p.Complete(context.Background(), domain.CompletionRequest{}) })
	}
	wg.Wait()
	if peak := slow.peak.Load(); peak != 2 {
		t.Errorf("peak concurrency = %d, want 2", peak)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := providers.Limit(slow, 0+1).Complete(ctx, domain.CompletionRequest{}); err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestFactoryCachesPerVersionAndKey(t *testing.T) {
	f := providers.New(providers.Config{})
	cfg := domain.ProviderConfig{ID: uuid.New(), Name: "anthropic", Kind: domain.KindAnthropic, Version: 1}
	a, err := f.Provider(cfg, "k1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.Provider(cfg, "k1")
	if a != b {
		t.Error("the same configuration reuses its provider (one circuit breaker)")
	}
	cfg.Version = 2
	if c, _ := f.Provider(cfg, "k1"); c == a {
		t.Error("a new version builds a new provider")
	}
	if _, err := f.Provider(domain.ProviderConfig{ID: uuid.New(), Name: "g", Kind: domain.KindGemini}, ""); err == nil {
		t.Error("gemini needs a key")
	}
	if p, err := f.Provider(domain.ProviderConfig{ID: uuid.New(), Name: "local", Kind: domain.KindOpenAICompatible, BaseURL: "https://llm.example.com/v1"}, ""); err != nil || p.Name() != "local" {
		t.Errorf("an OpenAI-compatible server may need no key: %v", err)
	}
}
