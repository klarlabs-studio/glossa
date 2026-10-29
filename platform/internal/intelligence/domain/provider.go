// Package domain is the Intelligence context's model: the provider port,
// routing, budgets, pricing, the Knowledge ports, suggestions with their
// provenance, and confidence scoring. It does no I/O.
package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Task names what a model call is for. Routing picks provider and model
// by task and locale (RFC 0003 §3.1).
type Task string

// Tasks.
const (
	// TaskTranslate drafts and repairs translations.
	TaskTranslate Task = "translate"
	// TaskReview reviews a translation in depth.
	TaskReview Task = "review"
	// TaskExplain explains a translation or a finding to a person.
	TaskExplain Task = "explain"
	// TaskAssess is the cheap self-assessment check of a valid draft.
	TaskAssess Task = "assess"
)

// Role is who speaks a conversation message.
type Role string

// Roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one conversation turn.
type Message struct {
	Role Role   `json:"role"`
	Text string `json:"text"`
}

// SystemBlock is a part of the system prompt. Cacheable marks the end of
// a stable prefix a provider may cache (Anthropic prompt caching); other
// providers ignore it.
type SystemBlock struct {
	Text      string `json:"text"`
	Cacheable bool   `json:"cacheable,omitempty"`
}

// OutputSchema asks for a JSON answer matching Schema, a JSON Schema
// object in the subset every provider accepts (object, properties,
// required, additionalProperties false, string/number/array/boolean).
type OutputSchema struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
}

// CompletionRequest is a provider-neutral model call.
type CompletionRequest struct {
	Task     Task          `json:"task"`
	Model    string        `json:"model"`
	System   []SystemBlock `json:"system,omitempty"`
	Messages []Message     `json:"messages"`
	// Output, when set, constrains the answer to JSON of that schema.
	Output    *OutputSchema `json:"output,omitempty"`
	MaxTokens int           `json:"max_tokens"`
	// Temperature is sent only when set: some models reject sampling
	// parameters (Claude Sonnet 5).
	Temperature *float64 `json:"temperature,omitempty"`
	// Effort is a provider-specific reasoning effort ("low" … "max");
	// empty keeps the provider's default.
	Effort string `json:"effort,omitempty"`
}

// Validate checks the request is complete.
func (r CompletionRequest) Validate() error {
	switch {
	case r.Model == "":
		return errors.New("completion request: model is required")
	case r.MaxTokens <= 0:
		return errors.New("completion request: max_tokens must be positive")
	case len(r.Messages) == 0:
		return errors.New("completion request: at least one message is required")
	}
	return nil
}

// Usage is what a call consumed, in the provider's tokens.
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
}

// Add returns the sum of u and o.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
	}
}

// StopReason is why the model stopped.
type StopReason string

// Stop reasons, normalized across providers.
const (
	StopEnd       StopReason = "end"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopOther     StopReason = "other"
)

// Completion is a provider's answer.
type Completion struct {
	Provider string     `json:"provider"`
	Model    string     `json:"model"`
	Text     string     `json:"text"`
	Stop     StopReason `json:"stop"`
	Usage    Usage      `json:"usage"`
}

// Provider is the port every model backend implements (intent §21).
// Complete must honour ctx, return *ProviderError for provider failures,
// and never retry on its own: resilience is layered on top.
type Provider interface {
	// Name is the provider's configured name, e.g. "anthropic".
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (Completion, error)
}

// ErrorKind classifies provider failures for retry, circuit breaking and
// fallback decisions.
type ErrorKind string

// Error kinds.
const (
	// KindRateLimited: 429 or quota; retryable after backoff.
	KindRateLimited ErrorKind = "rate_limited"
	// KindUnavailable: 5xx, overloaded, timeouts, network; retryable.
	KindUnavailable ErrorKind = "unavailable"
	// KindInvalidRequest: the provider rejected the request (4xx).
	KindInvalidRequest ErrorKind = "invalid_request"
	// KindAuth: missing or rejected credentials.
	KindAuth ErrorKind = "auth"
	// KindRefused: the model declined to answer.
	KindRefused ErrorKind = "refused"
	// KindTruncated: the answer hit max_tokens.
	KindTruncated ErrorKind = "truncated"
	// KindBadResponse: the provider answered with something unusable.
	KindBadResponse ErrorKind = "bad_response"
)

// ProviderError is a classified provider failure.
type ProviderError struct {
	Provider string
	Kind     ErrorKind
	// Status is the HTTP status, if any.
	Status int
	// RetryAfter is the provider's requested delay, if it sent one.
	RetryAfter time.Duration
	Err        error
}

func (e *ProviderError) Error() string {
	msg := fmt.Sprintf("provider %s: %s", e.Provider, e.Kind)
	if e.Status != 0 {
		msg += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ProviderError) Unwrap() error { return e.Err }

// Retryable reports whether the same call may succeed later.
func (e *ProviderError) Retryable() bool {
	return e.Kind == KindRateLimited || e.Kind == KindUnavailable
}

// IsRetryable reports whether err is a retryable provider failure.
func IsRetryable(err error) bool {
	var pe *ProviderError
	return errors.As(err, &pe) && pe.Retryable()
}
