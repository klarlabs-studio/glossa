package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.klarlabs.de/fortify/bulkhead"
	"go.klarlabs.de/fortify/circuitbreaker"
	"go.klarlabs.de/fortify/ferrors"
	"go.klarlabs.de/fortify/retry"
	"go.klarlabs.de/fortify/timeout"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// Options tunes the client. Zero values take the defaults.
type Options struct {
	// HTTPClient sends the requests (default: a client without its own
	// timeout; Timeout bounds each attempt).
	HTTPClient *http.Client
	// Timeout bounds one attempt (default 10s).
	Timeout time.Duration
	// MaxAttempts counts the first attempt (default 4).
	MaxAttempts  int
	InitialDelay time.Duration // default 500ms
	MaxDelay     time.Duration // default 8s
	// MaxRetryAfter is the longest rate-limit wait honoured in place
	// (default 60s). A longer one fails the call with
	// app.ErrGitHubRateLimited and its wait, for the caller to reschedule.
	MaxRetryAfter time.Duration
	// BreakerFailures consecutive outages open an installation's circuit
	// (default 5); it half-opens after BreakerCooldown (default 30s).
	BreakerFailures int
	BreakerCooldown time.Duration
	// TenantConcurrency caps a tenant's calls in flight (default 4);
	// TenantQueue more wait up to TenantQueueWait (defaults 32, 30s).
	TenantConcurrency int
	TenantQueue       int
	TenantQueueWait   time.Duration
	// Logger receives operation names, IDs and statuses (default: none).
	Logger *slog.Logger
	// Now is the clock (default time.Now).
	Now func() time.Time
	// OnCall observes every HTTP exchange, for metrics (GitHub calls by
	// status, the remaining rate limit). It must not block.
	OnCall func(CallInfo)
}

func (o *Options) defaults() {
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 4
	}
	if o.InitialDelay <= 0 {
		o.InitialDelay = 500 * time.Millisecond
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = 8 * time.Second
	}
	if o.MaxRetryAfter <= 0 {
		o.MaxRetryAfter = time.Minute
	}
	if o.BreakerFailures <= 0 {
		o.BreakerFailures = 5
	}
	if o.BreakerCooldown <= 0 {
		o.BreakerCooldown = 30 * time.Second
	}
	if o.TenantConcurrency <= 0 {
		o.TenantConcurrency = 4
	}
	if o.TenantQueue <= 0 {
		o.TenantQueue = 32
	}
	if o.TenantQueueWait <= 0 {
		o.TenantQueueWait = 30 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// RateLimit is GitHub's primary rate limit as last reported.
type RateLimit struct {
	Limit     int
	Remaining int
	Reset     time.Time
	Resource  string
}

// CallInfo describes one HTTP exchange with GitHub.
type CallInfo struct {
	Op             string
	InstallationID int64
	// Status is 0 when no answer came back.
	Status   int
	Duration time.Duration
	// RateLimit is set when GitHub reported it (Limit > 0).
	RateLimit RateLimit
}

// Client is the GitHub App adapter. It is safe for concurrent use; keep
// one per process so breakers, bulkheads and the token cache see all
// traffic. Close it on shutdown.
type Client struct {
	cfg    Config
	opts   Options
	base   string
	log    *slog.Logger
	tokens *tokenCache

	retry   retry.Retry[struct{}]
	timeout timeout.Timeout[struct{}]

	mu        sync.Mutex
	breakers  map[int64]circuitbreaker.CircuitBreaker[struct{}]
	bulkheads map[uuid.UUID]bulkhead.Bulkhead[struct{}]
	limits    map[int64]RateLimit
}

var _ app.GitHub = (*Client)(nil)

// New returns a client for cfg.
func New(cfg Config, opts Options) (*Client, error) {
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}
	if cfg.WebURL == "" {
		cfg.WebURL = DefaultWebURL
	}
	var errs []error
	if cfg.AppID <= 0 {
		errs = append(errs, errors.New("github: the App ID is required"))
	}
	if cfg.PrivateKey == nil {
		errs = append(errs, errors.New("github: the App's private key is required"))
	}
	if u, err := url.Parse(cfg.APIURL); err != nil || u.Host == "" {
		errs = append(errs, fmt.Errorf("github: API URL %q is not absolute", cfg.APIURL))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	opts.defaults()
	c := &Client{
		cfg:       cfg,
		opts:      opts,
		base:      strings.TrimRight(cfg.APIURL, "/"),
		log:       opts.Logger.With("component", "github"),
		breakers:  map[int64]circuitbreaker.CircuitBreaker[struct{}]{},
		bulkheads: map[uuid.UUID]bulkhead.Bulkhead[struct{}]{},
		limits:    map[int64]RateLimit{},
	}
	c.tokens = newTokenCache(c)
	c.retry = retry.New[struct{}](retry.Config{
		MaxAttempts:   opts.MaxAttempts,
		InitialDelay:  opts.InitialDelay,
		MaxDelay:      opts.MaxDelay,
		Multiplier:    2,
		BackoffPolicy: retry.BackoffExponential,
		Jitter:        true,
		IsRetryable:   isRetryable,
		Logger:        opts.Logger,
	})
	c.timeout = timeout.New[struct{}](timeout.Config{DefaultTimeout: opts.Timeout})
	return c, nil
}

// Close stops the tenants' bulkheads. Calls in flight must have ended.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, b := range c.bulkheads {
		_ = b.Close()
		delete(c.bulkheads, id)
	}
	return nil
}

// RateLimit returns installation's primary rate limit as GitHub last
// reported it; ok is false before its first call.
func (c *Client) RateLimit(installationID int64) (RateLimit, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rl, ok := c.limits[installationID]
	return rl, ok
}

// request is one REST call.
type request struct {
	op string
	// tenant scopes the bulkhead (uuid.Nil: none).
	tenant uuid.UUID
	// installation scopes the breaker and authenticates with its token
	// (0: none).
	installation int64
	// userToken authenticates as a person instead (install flow).
	userToken string
	// appAuth authenticates as the App with a fresh JWT.
	appAuth bool
	method  string
	path    string
	query   url.Values
	body    any
	out     any
	// idempotent says the request may be repeated after an ambiguous
	// failure; a POST is not (a timeout may hide a created resource).
	idempotent bool
}

// do runs r through the bulkhead, the breaker and the retries.
func (c *Client) do(ctx context.Context, r request) error {
	run := func(ctx context.Context) (struct{}, error) { return struct{}{}, c.withRetry(ctx, r) }
	if r.installation != 0 {
		breaker, inner := c.breaker(r.installation), run
		run = func(ctx context.Context) (struct{}, error) { return breaker.Execute(ctx, inner) }
	}
	if r.tenant != uuid.Nil {
		bh, inner := c.bulkhead(r.tenant), run
		run = func(ctx context.Context) (struct{}, error) { return bh.Execute(ctx, inner) }
	}
	_, err := run(ctx)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ferrors.ErrCircuitOpen):
		c.log.WarnContext(ctx, "github circuit open", "op", r.op, "installation_id", r.installation)
		return &APIError{Op: r.op, Kind: app.ErrGitHubUnavailable, Err: err}
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, ferrors.ErrBulkheadFull), errors.Is(err, context.DeadlineExceeded):
		// A full queue, or the queue wait ran out (not the caller's ctx).
		c.log.WarnContext(ctx, "github calls saturated", "op", r.op, "tenant_id", r.tenant)
		return &APIError{Op: r.op, Kind: app.ErrGitHubBusy, Err: err}
	}
	return err
}

// withRetry retries r, first waiting out any Retry-After the previous
// attempt was given: fortify's retry has no hook for a server-sent
// delay, so the wait happens at the start of the next attempt.
func (c *Client) withRetry(ctx context.Context, r request) error {
	var notBefore time.Time
	_, err := c.retry.Execute(ctx, func(ctx context.Context) (struct{}, error) {
		if err := sleepUntil(ctx, notBefore, c.opts.Now); err != nil {
			return struct{}{}, err
		}
		err := c.attempt(ctx, r)
		var ae *APIError
		if errors.As(err, &ae) && ae.Wait > 0 {
			notBefore = c.opts.Now().Add(ae.Wait)
		}
		return struct{}{}, err
	})
	return err
}

func sleepUntil(ctx context.Context, t time.Time, now func() time.Time) error {
	if t.IsZero() {
		return nil
	}
	d := t.Sub(now())
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// attempt authenticates and sends r once, within the timeout.
func (c *Client) attempt(ctx context.Context, r request) error {
	auth, err := c.authorization(ctx, r)
	if err != nil {
		return err
	}
	_, err = c.timeout.Execute(ctx, c.opts.Timeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, c.send(ctx, r, auth)
	})
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		// The attempt timed out, not the caller: an outage.
		return &APIError{Op: r.op, Kind: app.ErrGitHubUnavailable, Err: err, retryable: r.idempotent}
	}
	return err
}

func (c *Client) authorization(ctx context.Context, r request) (string, error) {
	switch {
	case r.appAuth:
		jwt, err := appJWT(c.cfg.PrivateKey, c.cfg.AppID, c.opts.Now())
		if err != nil {
			return "", &APIError{Op: r.op, Kind: app.ErrGitHubRejected, Err: err}
		}
		return "Bearer " + jwt, nil
	case r.userToken != "":
		return "Bearer " + r.userToken, nil
	case r.installation != 0:
		tok, err := c.tokens.get(ctx, r.installation)
		if err != nil {
			return "", err
		}
		return "Bearer " + tok, nil
	}
	return "", nil
}

// maxResponse bounds an answer we read (a page of 100 comments).
const maxResponse = 16 << 20

const apiVersion = "2022-11-28"

func (c *Client) send(ctx context.Context, r request, auth string) error {
	u := c.base + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}
	var body io.Reader
	if r.body != nil {
		raw, err := json.Marshal(r.body)
		if err != nil {
			return &APIError{Op: r.op, Kind: app.ErrGitHubRejected, Err: err}
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return &APIError{Op: r.op, Kind: app.ErrGitHubRejected, Err: err}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "glossa")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		c.observe(ctx, r, 0, time.Since(start), nil)
		// Drop the URL from the error: it is ours, and quiet logs are
		// easier to read.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return &APIError{Op: r.op, Kind: app.ErrGitHubUnavailable, Err: err, retryable: r.idempotent}
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	c.observe(ctx, r, resp.StatusCode, time.Since(start), resp.Header)
	if err != nil {
		return &APIError{Op: r.op, Status: resp.StatusCode, Kind: app.ErrGitHubUnavailable, Err: err, retryable: r.idempotent}
	}
	if resp.StatusCode/100 != 2 {
		e := fromResponse(r.op, resp.StatusCode, resp.Header, payload, c.opts.Now(), r.idempotent, c.opts.MaxRetryAfter)
		if resp.StatusCode == http.StatusUnauthorized && r.installation != 0 && !r.appAuth && r.userToken == "" {
			// A revoked or expired installation token: forget it, so the
			// next attempt mints a fresh one.
			c.tokens.forget(r.installation)
			e.retryable = true
		}
		c.log.WarnContext(ctx, "github call failed", "op", r.op, "installation_id", r.installation,
			"status", resp.StatusCode, "retry_after", e.Wait)
		return e
	}
	if r.out != nil {
		if err := json.Unmarshal(payload, r.out); err != nil {
			return &APIError{Op: r.op, Status: resp.StatusCode, Kind: app.ErrGitHubUnavailable, Message: "malformed response", Err: err}
		}
	}
	return nil
}

// observe records the rate limit and reports the exchange.
func (c *Client) observe(ctx context.Context, r request, status int, d time.Duration, h http.Header) {
	info := CallInfo{Op: r.op, InstallationID: r.installation, Status: status, Duration: d}
	if h != nil {
		info.RateLimit = parseRateLimit(h)
	}
	if info.RateLimit.Limit > 0 && r.installation != 0 && !r.appAuth && r.userToken == "" {
		c.mu.Lock()
		c.limits[r.installation] = info.RateLimit
		c.mu.Unlock()
	}
	c.log.DebugContext(ctx, "github call", "op", r.op, "installation_id", r.installation, "status", status,
		"duration", d, "ratelimit_remaining", info.RateLimit.Remaining)
	if c.opts.OnCall != nil {
		c.opts.OnCall(info)
	}
}

func parseRateLimit(h http.Header) RateLimit {
	limit, err := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	if err != nil {
		return RateLimit{}
	}
	rl := RateLimit{Limit: limit, Resource: h.Get("X-RateLimit-Resource")}
	rl.Remaining, _ = strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		rl.Reset = time.Unix(reset, 0)
	}
	return rl
}

// breaker returns installation's circuit breaker; only outages (5xx,
// timeouts, transport failures) count against it.
func (c *Client) breaker(installation int64) circuitbreaker.CircuitBreaker[struct{}] {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.breakers[installation]; ok {
		return b
	}
	b := circuitbreaker.New[struct{}](circuitbreaker.Config{
		MaxRequests:  1,
		Timeout:      c.opts.BreakerCooldown,
		ReadyToTrip:  circuitbreaker.TripOnConsecutiveFailures(c.opts.BreakerFailures),
		IsSuccessful: func(err error) bool { return !errors.Is(err, app.ErrGitHubUnavailable) },
		Logger:       c.opts.Logger,
	})
	c.breakers[installation] = b
	return b
}

// bulkhead returns tenant's concurrency cap.
func (c *Client) bulkhead(tenant uuid.UUID) bulkhead.Bulkhead[struct{}] {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.bulkheads[tenant]; ok {
		return b
	}
	b := bulkhead.New[struct{}](bulkhead.Config{
		MaxConcurrent: c.opts.TenantConcurrency,
		MaxQueue:      c.opts.TenantQueue,
		QueueTimeout:  c.opts.TenantQueueWait,
		Logger:        c.opts.Logger,
	})
	c.bulkheads[tenant] = b
	return b
}
