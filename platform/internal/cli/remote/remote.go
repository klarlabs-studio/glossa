// Package remote is the CLI's view of glossa-server: the generated /v1
// client (internal/apiclient) behind a small API that pages through
// lists, batches bulk writes, retries what is safe to retry (fortify),
// and turns problem details into *APIError values with their stable
// codes.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/fortify/retry"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// MaxBatch is the server's limit for message-upserts and
// translation-imports.
const MaxBatch = 500

// pageSize is the largest page the API serves.
const pageSize = 100

// Client talks to one glossa-server with one API token.
type Client struct {
	api    *apiclient.ClientWithResponses
	server string
	// doer is the API's retrying transport, for calls outside the
	// generated client.
	doer *retryingDoer
	// transfer moves import and export files: the same transport
	// without the client's overall timeout (a file may take longer than
	// a request; the context bounds it).
	transfer  *http.Client
	editor    apiclient.RequestEditorFn
	userAgent string
}

// Options configure a Client.
type Options struct {
	// HTTP is the transport; nil means a client with a 30 s timeout.
	HTTP *http.Client
	// UserAgent identifies the CLI and its version.
	UserAgent string
	// Retry configures retries; zero means 3 attempts from 200 ms.
	Retry retry.Config
}

// New returns a client for server (the base URL, without /v1) that
// authenticates with token.
func New(server, token string, opts Options) (*Client, error) {
	server = strings.TrimRight(server, "/")
	hc := opts.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: httpTimeout()}
	}
	rc := opts.Retry
	if rc.MaxAttempts == 0 {
		rc = retry.Config{MaxAttempts: 3, InitialDelay: 200 * time.Millisecond, Multiplier: 2,
			BackoffPolicy: retry.BackoffExponential, Jitter: true}
	}
	rc.IsRetryable = isRetryable
	doer := &retryingDoer{client: hc, retry: retry.New[*http.Response](rc)}
	editor := func(_ context.Context, req *http.Request) error {
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if opts.UserAgent != "" {
			req.Header.Set("User-Agent", opts.UserAgent)
		}
		return nil
	}
	api, err := apiclient.NewClientWithResponses(server, apiclient.WithHTTPClient(doer), apiclient.WithRequestEditorFn(editor))
	if err != nil {
		return nil, err
	}
	transfer := *hc
	transfer.Timeout = 0
	return &Client{api: api, server: server, doer: doer, transfer: &transfer, editor: editor, userAgent: opts.UserAgent}, nil
}

// Server is the base URL.
func (c *Client) Server() string { return c.server }

// APIError is a failed request: the problem details the server sent, or
// the transport failure.
type APIError struct {
	Method string
	URL    string
	// Status is 0 when the server wasn't reached.
	Status int
	// Code is the problem's stable code (unauthenticated, not_found, …).
	Code   string
	Title  string
	Detail string
	// Errors are the problem's field errors, when it sent any: what the
	// server could say about the request beyond one sentence, such as
	// the projects an ambiguous repository feeds.
	Errors []FieldError
	Err    error
}

// FieldError points at one member of the request body.
type FieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s %s: %v", e.Method, e.URL, e.Err)
	}
	msg := fmt.Sprintf("%s %s: %d %s", e.Method, e.URL, e.Status, e.Code)
	if e.Detail != "" {
		msg += ": " + e.Detail
	} else if e.Title != "" {
		msg += ": " + e.Title
	}
	return msg
}

func (e *APIError) Unwrap() error { return e.Err }

// response is what every generated *Response has.
type response interface {
	StatusCode() int
}

// check returns nil for a 2xx response and an *APIError otherwise. r is
// a generated *…Response; it is nil when err is set.
func check[R response](r R, err error, method, url string) error {
	if err != nil {
		return &APIError{Method: method, URL: url, Err: err}
	}
	status := r.StatusCode()
	if status >= 200 && status < 300 {
		return nil
	}
	return problem(status, rawBody(r), method, url)
}

// rawBody reads the Body field every generated response has.
func rawBody(r any) []byte {
	v := reflect.ValueOf(r)
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		if f := v.Elem().FieldByName("Body"); f.IsValid() && f.Kind() == reflect.Slice {
			return f.Bytes()
		}
	}
	return nil
}

func problem(status int, body []byte, method, url string) *APIError {
	e := &APIError{Method: method, URL: url, Status: status}
	var p struct {
		Code   string       `json:"code"`
		Title  string       `json:"title"`
		Detail string       `json:"detail"`
		Errors []FieldError `json:"errors"`
	}
	if json.Unmarshal(body, &p) == nil && p.Code != "" {
		e.Code, e.Title, e.Detail, e.Errors = p.Code, p.Title, p.Detail, p.Errors
	} else {
		e.Code = http.StatusText(status)
		e.Detail = strings.TrimSpace(truncate(string(body), 200))
	}
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── retries ─────────────────────────────────────────────────────────

type idempotentKey struct{}

// idempotent marks a POST as safe to repeat (the contract makes
// message-upserts and translation-imports idempotent).
func idempotent(ctx context.Context) context.Context {
	return context.WithValue(ctx, idempotentKey{}, true)
}

// retryingDoer retries network failures and 429/502/503/504 for GETs and
// for requests marked idempotent.
type retryingDoer struct {
	client *http.Client
	retry  retry.Retry[*http.Response]
}

type statusError struct {
	resp *http.Response
	body []byte
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.resp.StatusCode) }

func retryableStatus(s int) bool {
	return s == http.StatusTooManyRequests || s == http.StatusBadGateway ||
		s == http.StatusServiceUnavailable || s == http.StatusGatewayTimeout
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

func (d *retryingDoer) Do(req *http.Request) (*http.Response, error) {
	replayable := req.Method == http.MethodGet || req.Context().Value(idempotentKey{}) != nil
	if !replayable || (req.Body != nil && req.GetBody == nil) {
		return d.client.Do(req)
	}
	resp, err := d.retry.Execute(req.Context(), func(ctx context.Context) (*http.Response, error) {
		r := req.Clone(ctx)
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			r.Body = body
		}
		resp, err := d.client.Do(r)
		if err != nil {
			return nil, err
		}
		if retryableStatus(resp.StatusCode) {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			return nil, &statusError{resp: resp, body: body}
		}
		return resp, nil
	})
	var se *statusError
	if errors.As(err, &se) {
		// Out of attempts: hand back the last response as it was.
		se.resp.Body = io.NopCloser(strings.NewReader(string(se.body)))
		return se.resp, nil
	}
	return resp, err
}

// batchSize is how many items one message-upserts or translation-upserts
// request carries: MaxBatch, or GLOSSA_BATCH_SIZE (1..MaxBatch) when the
// server answers a full batch slower than the client waits (#77).
func batchSize() int {
	if n, err := strconv.Atoi(os.Getenv("GLOSSA_BATCH_SIZE")); err == nil && n >= 1 && n <= MaxBatch {
		return n
	}
	return MaxBatch
}

// httpTimeout is how long one request may take: 30 s, or
// GLOSSA_HTTP_TIMEOUT (a Go duration, at least 1 s).
func httpTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("GLOSSA_HTTP_TIMEOUT")); err == nil && d >= time.Second {
		return d
	}
	return 30 * time.Second
}
