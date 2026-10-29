package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxWebhookBytes caps a delivery's body (RFC 0004 §6.2).
const MaxWebhookBytes = 5 << 20

// Webhook errors. None of them carries payload content.
var (
	// ErrWebhookSignature: X-Hub-Signature-256 is missing, malformed or
	// does not match the body. Answer 401 and log nothing of the body.
	ErrWebhookSignature = errors.New("github: webhook signature missing or invalid")
	// ErrWebhookTooLarge: the body exceeds MaxWebhookBytes (answer 413).
	ErrWebhookTooLarge = errors.New("github: webhook body exceeds 5 MB")
	// ErrWebhookHeaders: X-GitHub-Event or X-GitHub-Delivery is missing.
	ErrWebhookHeaders = errors.New("github: webhook lacks X-GitHub-Event or X-GitHub-Delivery")
	// ErrWebhookIgnored: a verified event or action Glossa does not
	// handle; acknowledge and drop it.
	ErrWebhookIgnored = errors.New("github: webhook event not handled")
	// ErrWebhookPayload: a verified payload that is not what GitHub
	// sends for its event.
	ErrWebhookPayload = errors.New("github: malformed webhook payload")
)

// Delivery is a verified webhook delivery: the raw body, as signed.
type Delivery struct {
	// ID is X-GitHub-Delivery, the inbox key.
	ID string
	// Event is X-GitHub-Event.
	Event  string
	HookID string
	Body   []byte
}

// WebhookVerifier checks deliveries against the App's webhook secret.
type WebhookVerifier struct {
	secret []byte
}

// NewWebhookVerifier returns a verifier for secret.
func NewWebhookVerifier(secret []byte) (*WebhookVerifier, error) {
	if len(secret) == 0 {
		return nil, errors.New("github: the webhook secret is required")
	}
	return &WebhookVerifier{secret: append([]byte(nil), secret...)}, nil
}

// Verify reads at most MaxWebhookBytes of body and checks its
// X-Hub-Signature-256 (HMAC-SHA256, compared in constant time) on the raw
// bytes before anything is parsed; only then are the event headers read.
func (v *WebhookVerifier) Verify(h http.Header, body io.Reader) (Delivery, error) {
	raw, err := io.ReadAll(io.LimitReader(body, MaxWebhookBytes+1))
	if err != nil {
		return Delivery{}, fmt.Errorf("github: reading the webhook body: %w", err)
	}
	if len(raw) > MaxWebhookBytes {
		return Delivery{}, ErrWebhookTooLarge
	}
	if !v.valid(h.Get("X-Hub-Signature-256"), raw) {
		return Delivery{}, ErrWebhookSignature
	}
	d := Delivery{
		ID:     strings.TrimSpace(h.Get("X-GitHub-Delivery")),
		Event:  strings.TrimSpace(h.Get("X-GitHub-Event")),
		HookID: strings.TrimSpace(h.Get("X-GitHub-Hook-ID")),
		Body:   raw,
	}
	if d.ID == "" || d.Event == "" || len(d.ID) > 128 || len(d.Event) > 64 {
		return Delivery{}, ErrWebhookHeaders
	}
	return d, nil
}

func (v *WebhookVerifier) valid(header string, body []byte) bool {
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
