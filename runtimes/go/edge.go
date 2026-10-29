package glossa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.klarlabs.de/fortify/circuitbreaker"
	"go.klarlabs.de/fortify/ferrors"
	"go.klarlabs.de/fortify/retry"
)

// The delivery endpoints of glossa-edge (runtimes/SPEC.md §2), behind
// retry with exponential backoff and a circuit breaker.

const (
	maxManifestBytes = 16 << 20
	userAgent        = "glossa-go/1"
	breakerFailures  = 5
	breakerOpenFor   = 30 * time.Second
)

// RetryPolicy configures retries of edge requests. Transport errors, 429
// and 5xx answers are retried with exponential backoff and jitter; other
// answers are final.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts per request (default 3).
	MaxAttempts int
	// InitialBackoff is the delay before the first retry (default 200ms).
	InitialBackoff time.Duration
	// MaxBackoff caps the delay between retries (default 5s).
	MaxBackoff time.Duration
}

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = 200 * time.Millisecond
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = 5 * time.Second
	}
	return p
}

type edge struct {
	manifestURL string
	artifactURL string // prefix; the digest and ".json" follow
	client      *http.Client
	retry       retry.Retry[fetched]
	breaker     circuitbreaker.CircuitBreaker[fetched]
}

type fetched struct {
	status int
	etag   string
	body   []byte
}

// manifestResult is the edge's answer to a manifest request.
type manifestResult struct {
	body        []byte
	etag        string
	notModified bool
}

// httpStatusError is a non-success edge answer.
type httpStatusError struct {
	status int
	url    string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("edge answered %d for %s", e.status, e.url)
}

func (e *httpStatusError) retryable() bool {
	return e.status == http.StatusTooManyRequests || e.status >= 500
}

func newEdge(edgeURL, deliveryKey, environment string, client *http.Client, policy RetryPolicy) (*edge, error) {
	base, err := url.Parse(strings.TrimRight(edgeURL, "/"))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return nil, fmt.Errorf("glossa: EdgeURL %q must be an absolute http(s) URL", edgeURL)
	}
	root := base.String() + "/v1/" + url.PathEscape(deliveryKey) + "/"
	return &edge{
		manifestURL: root + url.PathEscape(environment) + "/manifest.json",
		artifactURL: root + "a/",
		client:      client,
		retry: retry.New[fetched](retry.Config{
			MaxAttempts:   policy.MaxAttempts,
			InitialDelay:  policy.InitialBackoff,
			MaxDelay:      policy.MaxBackoff,
			BackoffPolicy: retry.BackoffExponential,
			Jitter:        true,
			IsRetryable:   isRetryable,
		}),
		breaker: circuitbreaker.New[fetched](circuitbreaker.Config{
			ReadyToTrip:  circuitbreaker.TripOnConsecutiveFailures(breakerFailures),
			Timeout:      breakerOpenFor,
			IsSuccessful: func(err error) bool { return err == nil || !isRetryable(err) },
		}),
	}, nil
}

func (e *edge) close() { _ = e.breaker.Close() }

// isRetryable reports whether a failed request is worth repeating: the
// edge being unreachable or overloaded, but not a final answer, a
// cancelled context or an open circuit.
func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ferrors.ErrCircuitOpen) {
		return false
	}
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.retryable()
	}
	var tooLarge *tooLargeError
	return !errors.As(err, &tooLarge)
}

func (e *edge) manifest(ctx context.Context, etag string) (manifestResult, error) {
	res, err := e.get(ctx, e.manifestURL, etag, maxManifestBytes)
	if err != nil {
		return manifestResult{}, err
	}
	if res.status == http.StatusNotModified {
		return manifestResult{notModified: true, etag: etag}, nil
	}
	return manifestResult{body: res.body, etag: res.etag}, nil
}

func (e *edge) artifact(ctx context.Context, ref artifactRef) ([]byte, error) {
	res, err := e.get(ctx, e.artifactURL+ref.SHA256+".json", "", ref.Size)
	if err != nil {
		return nil, err
	}
	return res.body, nil
}

// get fetches url through retry and the circuit breaker. A body longer
// than limit is an integrity failure.
func (e *edge) get(ctx context.Context, url, etag string, limit int64) (fetched, error) {
	res, err := e.retry.Execute(ctx, func(ctx context.Context) (fetched, error) {
		return e.breaker.Execute(ctx, func(ctx context.Context) (fetched, error) {
			return e.attempt(ctx, url, etag, limit)
		})
	})
	var tooLarge *tooLargeError
	if errors.As(err, &tooLarge) {
		return res, fmt.Errorf("%w: %v", errIntegrity, err)
	}
	if err != nil {
		return res, fmt.Errorf("%w: %v", errNetwork, err)
	}
	return res, nil
}

func (e *edge) attempt(ctx context.Context, url, etag string, limit int64) (fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetched{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fetched{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := readLimited(resp.Body, limit, url)
		return fetched{status: resp.StatusCode, etag: resp.Header.Get("ETag"), body: body}, err
	case http.StatusNotModified:
		return fetched{status: resp.StatusCode}, nil
	default:
		return fetched{}, &httpStatusError{status: resp.StatusCode, url: url}
	}
}

// tooLargeError is a response body longer than its declared size.
type tooLargeError struct {
	url   string
	limit int64
}

func (e *tooLargeError) Error() string {
	return fmt.Sprintf("%s is larger than %d bytes", e.url, e.limit)
}

func readLimited(r io.Reader, limit int64, url string) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, &tooLargeError{url: url, limit: limit}
	}
	return body, nil
}
