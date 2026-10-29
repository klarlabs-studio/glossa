// Package httpjson is the small JSON-over-HTTP client the REST provider
// adapters (OpenAI-compatible, Gemini) share: one POST, status
// classification onto the port's error kinds, Retry-After parsing.
package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// maxResponse bounds a provider answer we are willing to read.
const maxResponse = 8 << 20

// Post sends body as JSON and decodes a 2xx answer into out. Failures
// come back as *domain.ProviderError (or ctx's error when the caller gave
// up).
func Post(ctx context.Context, client *http.Client, provider, url string, header http.Header, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return &domain.ProviderError{Provider: provider, Kind: domain.KindInvalidRequest, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return &domain.ProviderError{Provider: provider, Kind: domain.KindInvalidRequest, Err: err}
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &domain.ProviderError{Provider: provider, Kind: domain.KindUnavailable, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return &domain.ProviderError{Provider: provider, Kind: domain.KindUnavailable, Status: resp.StatusCode, Err: err}
	}
	if resp.StatusCode/100 != 2 {
		return &domain.ProviderError{
			Provider: provider, Kind: KindOf(resp.StatusCode), Status: resp.StatusCode,
			RetryAfter: RetryAfter(resp.Header),
			Err:        fmt.Errorf("%s", truncate(payload, 512)),
		}
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return &domain.ProviderError{Provider: provider, Kind: domain.KindBadResponse, Status: resp.StatusCode, Err: err}
	}
	return nil
}

// KindOf classifies an HTTP status.
func KindOf(status int) domain.ErrorKind {
	switch {
	case status == http.StatusTooManyRequests:
		return domain.KindRateLimited
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return domain.KindAuth
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status >= 500:
		return domain.KindUnavailable
	default:
		return domain.KindInvalidRequest
	}
}

// RetryAfter parses a Retry-After header in seconds.
func RetryAfter(h http.Header) time.Duration {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
