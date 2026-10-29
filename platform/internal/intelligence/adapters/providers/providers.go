// Package providers builds callable providers from tenants' provider
// configuration (RFC 0003 §3.1): the Anthropic, OpenAI-compatible or
// Gemini adapter with the tenant's key, over an HTTP client that refuses
// private and loopback addresses when it dials (SSRF), wrapped by
// fortify (timeout, retry with backoff, a circuit breaker per provider)
// and a per-provider concurrency cap. Built providers are cached per
// configuration version, so a provider's breaker sees all its traffic.
package providers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/anthropic"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/gemini"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/openaicompat"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/resilient"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// Config tunes the factory.
type Config struct {
	// AllowPrivate lets providers reach loopback and private addresses
	// (self-hosted models in the cluster).
	AllowPrivate bool
	// Concurrency caps the calls in flight per configured provider in
	// this process (default 4).
	Concurrency int
	Resilience  resilient.Config
	Logger      *slog.Logger
}

// Factory implements app.ProviderFactory.
type Factory struct {
	cfg    Config
	client *http.Client
	mu     sync.Mutex
	cache  map[string]cached
}

type cached struct {
	version int
	key     string
	p       domain.Provider
}

var _ app.ProviderFactory = (*Factory)(nil)

// New returns a factory.
func New(cfg Config) *Factory {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	return &Factory{cfg: cfg, client: SafeClient(cfg.AllowPrivate), cache: map[string]cached{}}
}

// Provider implements app.ProviderFactory.
func (f *Factory) Provider(cfg domain.ProviderConfig, apiKey string) (domain.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := cfg.ID.String()
	if c, ok := f.cache[id]; ok && c.version == cfg.Version && c.key == apiKey {
		return c.p, nil
	}
	var (
		p   domain.Provider
		err error
	)
	switch cfg.Kind {
	case domain.KindAnthropic:
		p, err = anthropic.New(anthropic.Config{Name: cfg.Name, APIKey: apiKey, BaseURL: cfg.BaseURL, HTTPClient: f.client})
	case domain.KindOpenAICompatible:
		p, err = openaicompat.New(openaicompat.Config{Name: cfg.Name, APIKey: apiKey, BaseURL: cfg.BaseURL, HTTPClient: f.client})
	case domain.KindGemini:
		p, err = gemini.New(gemini.Config{Name: cfg.Name, APIKey: apiKey, BaseURL: cfg.BaseURL, HTTPClient: f.client})
	default:
		err = fmt.Errorf("unknown provider kind %q", cfg.Kind)
	}
	if err != nil {
		return nil, err
	}
	rc := f.cfg.Resilience
	rc.Logger = f.cfg.Logger
	built := Limit(resilient.Wrap(p, rc), f.cfg.Concurrency)
	f.cache[id] = cached{version: cfg.Version, key: apiKey, p: built}
	return built, nil
}

// Limit caps the calls in flight through p (a bulkhead): a call waits
// for a slot or its context.
func Limit(p domain.Provider, n int) domain.Provider {
	return &limited{next: p, slots: make(chan struct{}, n)}
}

type limited struct {
	next  domain.Provider
	slots chan struct{}
}

func (l *limited) Name() string { return l.next.Name() }

func (l *limited) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return domain.Completion{}, ctx.Err()
	}
	defer func() { <-l.slots }()
	return l.next.Complete(ctx, req)
}

// ErrPrivateAddress is a refused dial to a private or loopback address.
var ErrPrivateAddress = errors.New("providers: the endpoint resolves to a private or loopback address")

// SafeClient is the HTTP client provider calls use: unless allowPrivate,
// it refuses to connect to loopback, private, link-local and CGNAT
// addresses — checked on the resolved address at dial time, so neither
// a hostname nor a redirect can reach them.
func SafeClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || domain.PrivateIP(ip) {
				return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
			}
			return nil
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the default transport is an *http.Transport
	transport.DialContext = dialer.DialContext
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = 16
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("providers: too many redirects")
			}
			return nil
		},
	}
}
