// Package gemini is the Provider adapter for the Gemini API
// (generateContent), over plain net/http.
package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/httpjson"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// DefaultBaseURL is the Gemini API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Config configures the adapter.
type Config struct {
	// Name is the provider name (default "gemini").
	Name string
	// APIKey is the tenant's key. Required.
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider calls models/{model}:generateContent.
type Provider struct {
	cfg Config
}

// New validates cfg and returns the adapter.
func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("gemini: an API key is required")
	}
	if cfg.Name == "" {
		cfg.Name = "gemini"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Provider{cfg: cfg}, nil
}

// Name implements domain.Provider.
func (p *Provider) Name() string { return p.cfg.Name }

type part struct {
	Text string `json:"text"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type generationConfig struct {
	MaxOutputTokens    int            `json:"maxOutputTokens"`
	Temperature        *float64       `json:"temperature,omitempty"`
	ResponseMimeType   string         `json:"responseMimeType,omitempty"`
	ResponseJSONSchema map[string]any `json:"responseJsonSchema,omitempty"`
}

type generateRequest struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type generateResponse struct {
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount        int64 `json:"promptTokenCount"`
		CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
		ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
		CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
}

// Complete implements domain.Provider.
func (p *Provider) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	if err := req.Validate(); err != nil {
		return domain.Completion{}, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindInvalidRequest, Err: err}
	}
	header := http.Header{}
	header.Set("x-goog-api-key", p.cfg.APIKey)
	endpoint := p.cfg.BaseURL + "/models/" + url.PathEscape(req.Model) + ":generateContent"
	var resp generateResponse
	if err := httpjson.Post(ctx, p.cfg.HTTPClient, p.cfg.Name, endpoint, header, body(req), &resp); err != nil {
		return domain.Completion{}, err
	}
	u := resp.UsageMetadata
	out := domain.Completion{
		Provider: p.cfg.Name,
		Model:    req.Model,
		Usage: domain.Usage{
			InputTokens:     u.PromptTokenCount - u.CachedContentTokenCount,
			OutputTokens:    u.CandidatesTokenCount + u.ThoughtsTokenCount, // thoughts are billed as output
			CacheReadTokens: u.CachedContentTokenCount,
		},
	}
	if resp.PromptFeedback.BlockReason != "" {
		out.Stop = domain.StopRefusal
		return out, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindRefused, Err: errors.New("prompt blocked: " + resp.PromptFeedback.BlockReason)}
	}
	if len(resp.Candidates) == 0 {
		return out, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindBadResponse, Err: errors.New("no candidates")}
	}
	c := resp.Candidates[0]
	var text strings.Builder
	for _, pt := range c.Content.Parts {
		text.WriteString(pt.Text)
	}
	out.Text = text.String()
	switch c.FinishReason {
	case "STOP":
		out.Stop = domain.StopEnd
	case "MAX_TOKENS":
		out.Stop = domain.StopMaxTokens
		return out, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindTruncated, Err: errors.New("the answer hit maxOutputTokens")}
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		out.Stop = domain.StopRefusal
		return out, &domain.ProviderError{Provider: p.cfg.Name, Kind: domain.KindRefused, Err: errors.New("finish reason " + c.FinishReason)}
	default:
		out.Stop = domain.StopOther
	}
	return out, nil
}

func body(req domain.CompletionRequest) generateRequest {
	g := generateRequest{GenerationConfig: generationConfig{MaxOutputTokens: req.MaxTokens, Temperature: req.Temperature}}
	if len(req.System) > 0 {
		sys := &content{}
		for _, b := range req.System {
			sys.Parts = append(sys.Parts, part{Text: b.Text})
		}
		g.SystemInstruction = sys
	}
	for _, m := range req.Messages {
		role := "user"
		if m.Role == domain.RoleAssistant {
			role = "model"
		}
		g.Contents = append(g.Contents, content{Role: role, Parts: []part{{Text: m.Text}}})
	}
	if req.Output != nil {
		g.GenerationConfig.ResponseMimeType = "application/json"
		g.GenerationConfig.ResponseJSONSchema = req.Output.Schema
	}
	return g
}
