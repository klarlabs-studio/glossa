package githubtest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"strings"
)

//go:embed testdata/webhooks/*.json
var fixtures embed.FS

// Fixtures lists the recorded webhook deliveries by name
// ("pull_request.opened", …): the event, a dot, the action.
func Fixtures() []string {
	entries, _ := fixtures.ReadDir("testdata/webhooks")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names
}

// Fixture returns a recorded delivery's body; it panics for an unknown
// name, as a test typo should fail loudly.
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile(path.Join("testdata/webhooks", name+".json"))
	if err != nil {
		panic(fmt.Sprintf("githubtest: no webhook fixture %q", name))
	}
	return b
}

// FixtureEvent is the X-GitHub-Event of a fixture name.
func FixtureEvent(name string) string {
	event, _, _ := strings.Cut(name, ".")
	return event
}

// Sign returns body's X-Hub-Signature-256 value under secret.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// WebhookRequest builds a signed delivery as GitHub sends it.
func WebhookRequest(url string, secret []byte, event, delivery string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHub-Hookshot/fake")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-GitHub-Hook-ID", "1")
	req.Header.Set("X-Hub-Signature-256", Sign(secret, body))
	return req, nil
}

// FixtureRequest builds the signed delivery of a recorded fixture.
func FixtureRequest(url string, secret []byte, name, delivery string) (*http.Request, error) {
	return WebhookRequest(url, secret, FixtureEvent(name), delivery, Fixture(name))
}
