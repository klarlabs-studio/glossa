// Package aitranslator hosts the outbound HTTP clients for the
// supported AI translator providers (OpenAI, Anthropic, Gemini,
// custom). The Translator dispatches by Provider.Kind and shares the
// same prompt across implementations so behavior is consistent.
package aitranslator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	domain "go.klarlabs.de/glossa/apps/api/internal/domain/aitranslator"
)

const (
	defaultTimeout = 30 * time.Second

	systemPrompt = `You translate UI copy for a software product.
- Preserve ICU MessageFormat placeholders verbatim: {name}, {n, plural, ...}, {date, ...}.
- Preserve markup tags like <b> and </b>.
- Do not translate brand names, code identifiers, or values that look like enum keys.
- Match the source register: casual stays casual, formal stays formal.
- Return ONLY the translated string. No quotes, no explanation, no preamble.`
)

// Translator is the multiplexing HTTP client. Implements
// domain.Translator.
type Translator struct {
	httpc *http.Client
}

// New returns a Translator using a shared HTTP client with the
// default timeout.
func New() *Translator {
	return &Translator{httpc: &http.Client{Timeout: defaultTimeout}}
}

// WithHTTPClient swaps the underlying client. Used by tests to point
// at httptest stubs.
func (t *Translator) WithHTTPClient(c *http.Client) *Translator {
	t.httpc = c
	return t
}

// Translate dispatches on provider kind.
func (t *Translator) Translate(ctx context.Context, p domain.Provider, plainKey []byte, req domain.TranslateRequest) (domain.TranslateResult, error) {
	user := buildUserPrompt(req)
	var (
		out string
		err error
	)
	switch p.Kind {
	case domain.KindOpenAI:
		out, err = t.callOpenAI(ctx, p, plainKey, user)
	case domain.KindAnthropic:
		out, err = t.callAnthropic(ctx, p, plainKey, user)
	case domain.KindGemini:
		out, err = t.callGemini(ctx, p, plainKey, user)
	case domain.KindCustom:
		out, err = t.callOpenAI(ctx, p, plainKey, user)
	default:
		return domain.TranslateResult{}, fmt.Errorf("aitranslator: unsupported kind %q", p.Kind)
	}
	if err != nil {
		return domain.TranslateResult{}, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return domain.TranslateResult{}, errors.New("aitranslator: provider returned empty translation")
	}
	return domain.TranslateResult{Translation: out, Provider: string(p.Kind)}, nil
}

func buildUserPrompt(req domain.TranslateRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Key: %s\n", req.Key)
	fmt.Fprintf(&b, "From: %s\n", req.SourceLocale)
	fmt.Fprintf(&b, "To: %s\n\n", req.TargetLocale)
	b.WriteString("Source:\n")
	b.WriteString(req.Source)
	return b.String()
}

// ── OpenAI (chat-completions) ─────────────────────────────────────

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (t *Translator) callOpenAI(ctx context.Context, p domain.Provider, key []byte, user string) (string, error) {
	url := p.BaseURL
	if url == "" {
		url = "https://api.openai.com"
	}
	body, _ := json.Marshal(openAIRequest{
		Model: p.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: user},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(key))
	resp, err := t.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("openai: %d %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out openAIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("openai: decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("openai: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("openai: no choices in response")
	}
	return out.Choices[0].Message.Content, nil
}

// ── Anthropic (messages) ──────────────────────────────────────────

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (t *Translator) callAnthropic(ctx context.Context, p domain.Provider, key []byte, user string) (string, error) {
	url := p.BaseURL
	if url == "" {
		url = "https://api.anthropic.com"
	}
	body, _ := json.Marshal(anthropicRequest{
		Model:     p.Model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages:  []anthropicMessage{{Role: "user", Content: user}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", string(key))
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := t.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("anthropic: %d %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out anthropicResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("anthropic: decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("anthropic: %s", out.Error.Message)
	}
	for _, c := range out.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", errors.New("anthropic: no text content")
}

// ── Gemini (generateContent) ──────────────────────────────────────

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (t *Translator) callGemini(ctx context.Context, p domain.Provider, key []byte, user string) (string, error) {
	url := p.BaseURL
	if url == "" {
		url = "https://generativelanguage.googleapis.com"
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", url, p.Model, string(key))
	body, _ := json.Marshal(geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: systemPrompt}}},
		Contents:          []geminiContent{{Role: "user", Parts: []geminiPart{{Text: user}}}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gemini: %d %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out geminiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("gemini: decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("gemini: %s", out.Error.Message)
	}
	for _, c := range out.Candidates {
		for _, part := range c.Content.Parts {
			if part.Text != "" {
				return part.Text, nil
			}
		}
	}
	return "", errors.New("gemini: no text in candidates")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
