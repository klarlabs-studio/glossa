package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// APIError is a failed GitHub call. It matches one of the app.ErrGitHub*
// sentinels with errors.Is, and app.GitHubRetryAfter reads its wait.
type APIError struct {
	// Op names the call ("checks.update", …).
	Op string
	// Status is GitHub's HTTP status (0 when no answer came back).
	Status int
	// Message is GitHub's error message (never a request or payload).
	Message string
	// Kind is the app sentinel the error matches.
	Kind error
	// Wait is how long a rate limit asked to wait.
	Wait time.Duration
	// Err is the underlying transport error, if any.
	Err error

	retryable bool
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("github: ")
	b.WriteString(e.Op)
	if e.Status != 0 {
		fmt.Fprintf(&b, ": %d %s", e.Status, http.StatusText(e.Status))
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	if e.Wait > 0 {
		fmt.Fprintf(&b, " (retry after %s)", e.Wait)
	}
	return b.String()
}

// Unwrap returns the sentinel and the transport error.
func (e *APIError) Unwrap() []error {
	out := make([]error, 0, 2)
	if e.Kind != nil {
		out = append(out, e.Kind)
	}
	if e.Err != nil {
		out = append(out, e.Err)
	}
	return out
}

// RetryAfter is the wait a rate limit asked for (0 otherwise).
func (e *APIError) RetryAfter() time.Duration { return e.Wait }

// maxMessage bounds GitHub's error message we keep.
const maxMessage = 300

// fromResponse classifies a non-2xx answer. idempotent says whether the
// request may be repeated after an ambiguous failure (a 5xx may have
// been applied); a rate-limited request was refused, so it may always be
// repeated once the wait is acceptable.
func fromResponse(op string, status int, h http.Header, body []byte, now time.Time, idempotent bool, maxWait time.Duration) *APIError {
	e := &APIError{Op: op, Status: status, Message: errorMessage(body)}
	if wait, limited := rateLimitWait(status, h, e.Message, now); limited {
		e.Kind, e.Wait = app.ErrGitHubRateLimited, wait
		e.retryable = wait <= maxWait
		return e
	}
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		e.Kind = app.ErrGitHubNotFound
	case status >= 500 || status == http.StatusRequestTimeout:
		e.Kind, e.retryable = app.ErrGitHubUnavailable, idempotent
	default:
		e.Kind = app.ErrGitHubRejected
	}
	return e
}

// secondaryWait is GitHub's advice when a secondary rate limit names no
// wait: "wait for at least one minute before retrying".
const secondaryWait = time.Minute

// rateLimitWait reads GitHub's rate-limit signals on a 403 or 429: the
// Retry-After header (seconds or an HTTP date), else an exhausted
// primary limit's x-ratelimit-reset, else a minute for a secondary limit.
// A 403 without any of them is a permission error, not a rate limit.
func rateLimitWait(status int, h http.Header, msg string, now time.Time) (time.Duration, bool) {
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return 0, false
	}
	if ra := strings.TrimSpace(h.Get("Retry-After")); ra != "" {
		if s, err := strconv.Atoi(ra); err == nil && s >= 0 {
			return max(time.Duration(s)*time.Second, time.Second), true
		}
		if t, err := http.ParseTime(ra); err == nil {
			return max(t.Sub(now), time.Second), true
		}
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return max(time.Unix(reset, 0).Sub(now), time.Second), true
		}
	}
	if status == http.StatusTooManyRequests || strings.Contains(strings.ToLower(msg), "rate limit") {
		return secondaryWait, true
	}
	return 0, false
}

func errorMessage(body []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	if len(m.Message) > maxMessage {
		return strings.ToValidUTF8(m.Message[:maxMessage], "") + "…"
	}
	return m.Message
}

func isRetryable(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.retryable
}
