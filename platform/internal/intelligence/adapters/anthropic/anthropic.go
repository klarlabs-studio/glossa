// Package anthropic is the Provider adapter for the Claude Messages API.
//
// It uses the official Go SDK (github.com/anthropics/anthropic-sdk-go)
// rather than hand-rolled HTTP: the SDK tracks the API's request shapes
// (structured outputs, prompt caching, effort) and returns typed errors,
// so the adapter only translates between the provider-neutral port and
// the SDK. Two SDK behaviours are switched off on purpose:
//
//   - environment defaults (ANTHROPIC_API_KEY, profiles, base URL): a
//     tenant's calls use only the tenant's own sealed key (BYO
//     credentials, RFC 0003 §3.1), never the server's environment;
//   - SDK retries: resilience is fortify's job (adapters/resilient), so
//     retries, backoff and the circuit breaker are uniform across
//     providers.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/httpjson"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// DefaultBaseURL is the Claude API.
const DefaultBaseURL = "https://api.anthropic.com/"

// Config configures the adapter.
type Config struct {
	// Name is the provider name used in routes and provenance (default
	// "anthropic").
	Name string
	// APIKey is the tenant's key. Required.
	APIKey string
	// BaseURL overrides the API endpoint (tests, gateways).
	BaseURL    string
	HTTPClient *http.Client
}

// Provider calls the Messages API.
type Provider struct {
	name   string
	client sdk.Client
}

// New validates cfg and returns the adapter.
func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("anthropic: an API key is required")
	}
	if cfg.Name == "" {
		cfg.Name = "anthropic"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	client := sdk.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
		option.WithHTTPClient(cfg.HTTPClient),
		option.WithMaxRetries(0),
	)
	return &Provider{name: cfg.Name, client: client}, nil
}

// Name implements domain.Provider.
func (p *Provider) Name() string { return p.name }

// Complete implements domain.Provider.
func (p *Provider) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	if err := req.Validate(); err != nil {
		return domain.Completion{}, &domain.ProviderError{Provider: p.name, Kind: domain.KindInvalidRequest, Err: err}
	}
	msg, err := p.client.Messages.New(ctx, params(req))
	if err != nil {
		return domain.Completion{}, p.classify(ctx, err)
	}
	out := domain.Completion{
		Provider: p.name,
		Model:    string(msg.Model),
		Usage: domain.Usage{
			InputTokens:      msg.Usage.InputTokens,
			OutputTokens:     msg.Usage.OutputTokens,
			CacheReadTokens:  msg.Usage.CacheReadInputTokens,
			CacheWriteTokens: msg.Usage.CacheCreationInputTokens,
		},
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if b, ok := block.AsAny().(sdk.TextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	out.Text = text.String()
	switch msg.StopReason {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
		out.Stop = domain.StopEnd
	case sdk.StopReasonMaxTokens:
		out.Stop = domain.StopMaxTokens
		return out, &domain.ProviderError{Provider: p.name, Kind: domain.KindTruncated, Err: errors.New("the answer hit max_tokens")}
	case sdk.StopReasonRefusal:
		out.Stop = domain.StopRefusal
		return out, &domain.ProviderError{Provider: p.name, Kind: domain.KindRefused, Err: fmt.Errorf("refusal: %s", msg.StopDetails.Category)}
	default:
		out.Stop = domain.StopOther
	}
	return out, nil
}

func params(req domain.CompletionRequest) sdk.MessageNewParams {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
	}
	for _, b := range req.System {
		block := sdk.TextBlockParam{Text: b.Text}
		if b.Cacheable {
			block.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		params.System = append(params.System, block)
	}
	for _, m := range req.Messages {
		if m.Role == domain.RoleAssistant {
			params.Messages = append(params.Messages, sdk.NewAssistantMessage(sdk.NewTextBlock(m.Text)))
		} else {
			params.Messages = append(params.Messages, sdk.NewUserMessage(sdk.NewTextBlock(m.Text)))
		}
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(*req.Temperature)
	}
	if req.Output != nil {
		params.OutputConfig.Format = sdk.JSONOutputFormatParam{Schema: req.Output.Schema}
	}
	if req.Effort != "" {
		params.OutputConfig.Effort = sdk.OutputConfigEffort(req.Effort)
	}
	return params
}

// classify maps an SDK error onto the port's error kinds.
func (p *Provider) classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		// Transport failure: connection refused, reset, TLS.
		return &domain.ProviderError{Provider: p.name, Kind: domain.KindUnavailable, Err: err}
	}
	pe := &domain.ProviderError{Provider: p.name, Status: apiErr.StatusCode, Kind: httpjson.KindOf(apiErr.StatusCode), Err: err}
	if apiErr.Response != nil {
		pe.RetryAfter = httpjson.RetryAfter(apiErr.Response.Header)
	}
	return pe
}
