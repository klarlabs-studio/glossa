// Package openaicompat is the Provider adapter for OpenAI-compatible chat
// completion endpoints: OpenAI itself and the many self-hosted and hosted
// servers that speak its wire format (vLLM, Ollama, LM Studio, Mistral,
// gateways). It speaks plain net/http so it depends on no vendor SDK and
// works with any base URL.
package openaicompat

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/httpjson"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// Config configures the adapter.
type Config struct {
	// Name is the provider name used in routes and provenance.
	Name string
	// BaseURL is the API root, e.g. "https://api.openai.com/v1" or
	// "http://localhost:11434/v1". Required.
	BaseURL string
	// APIKey is sent as a bearer token; self-hosted servers may need none.
	APIKey string
	// MaxCompletionTokens sends max_completion_tokens instead of the
	// legacy max_tokens (OpenAI's reasoning models require it; many
	// compatible servers only know max_tokens).
	MaxCompletionTokens bool
	// StrictSchema asks for strict JSON-schema adherence (OpenAI); turn it
	// off for servers that reject the flag.
	StrictSchema bool
	HTTPClient   *http.Client
}

// Provider calls /chat/completions.
type Provider struct {
	cfg Config
}

// New validates cfg and returns the adapter.
func New(cfg Config) (*Provider, error) {
	if cfg.Name == "" || cfg.BaseURL == "" {
		return nil, errors.New("openaicompat: name and base URL are required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Provider{cfg: cfg}, nil
}

// Name implements domain.Provider.
func (p *Provider) Name() string { return p.cfg.Name }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict,omitempty"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ResponseFormat      *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// Complete implements domain.Provider.
func (p *Provider) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	if err := req.Validate(); err != nil {
		return domain.Completion{}, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindInvalidRequest, Err: err}
	}
	header := http.Header{}
	if p.cfg.APIKey != "" {
		header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	var resp chatResponse
	if err := httpjson.Post(ctx, p.cfg.HTTPClient, p.cfg.Name, p.cfg.BaseURL+"/chat/completions", header, p.body(req), &resp); err != nil {
		return domain.Completion{}, err
	}
	out := domain.Completion{
		Provider: p.cfg.Name,
		Model:    resp.Model,
		Usage: domain.Usage{
			// Cached prompt tokens are part of prompt_tokens here.
			InputTokens:     resp.Usage.PromptTokens - resp.Usage.PromptTokensDetails.CachedTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheReadTokens: resp.Usage.PromptTokensDetails.CachedTokens,
		},
	}
	if out.Model == "" {
		out.Model = req.Model
	}
	if len(resp.Choices) == 0 {
		return out, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindBadResponse, Err: errors.New("no choices")}
	}
	choice := resp.Choices[0]
	out.Text = choice.Message.Content
	switch {
	case choice.Message.Refusal != "" || choice.FinishReason == "content_filter":
		out.Stop = domain.StopRefusal
		return out, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindRefused, Err: errors.New("the model refused")}
	case choice.FinishReason == "length":
		out.Stop = domain.StopMaxTokens
		return out, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindTruncated, Err: errors.New("the answer hit the token limit")}
	case choice.FinishReason == "stop":
		out.Stop = domain.StopEnd
	default:
		out.Stop = domain.StopOther
	}
	return out, nil
}

func (p *Provider) body(req domain.CompletionRequest) chatRequest {
	body := chatRequest{Model: req.Model, Temperature: req.Temperature, ReasoningEffort: req.Effort}
	if p.cfg.MaxCompletionTokens {
		body.MaxCompletionTokens = req.MaxTokens
	} else {
		body.MaxTokens = req.MaxTokens
	}
	var system []string
	for _, b := range req.System {
		system = append(system, b.Text)
	}
	if len(system) > 0 {
		body.Messages = append(body.Messages, chatMessage{Role: "system", Content: strings.Join(system, "\n\n")})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, chatMessage{Role: string(m.Role), Content: m.Text})
	}
	if req.Output != nil {
		body.ResponseFormat = &responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchema{Name: req.Output.Name, Schema: req.Output.Schema, Strict: p.cfg.StrictSchema},
		}
	}
	return body
}
