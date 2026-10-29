package github_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"sync"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// appKey is a test-time App key: no key material lives in the repo.
func appKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

func newKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadConfigDisabledWithoutAppID(t *testing.T) {
	cfg, enabled, err := github.LoadConfig(env(nil))
	if err != nil || enabled || cfg.AppID != 0 {
		t.Fatalf("got %v, %v, %v", cfg, enabled, err)
	}
}

func TestLoadConfig(t *testing.T) {
	k := appKey(t)
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	pkcs8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	for name, keyText := range map[string]string{
		"pkcs1":           pkcs1,
		"pkcs8":           pkcs8,
		"escaped newline": strings.ReplaceAll(pkcs1, "\n", `\n`),
	} {
		t.Run(name, func(t *testing.T) {
			cfg, enabled, err := github.LoadConfig(env(map[string]string{
				github.EnvAppID:         "99001",
				github.EnvAppSlug:       "glossa",
				github.EnvPrivateKey:    keyText,
				github.EnvWebhookSecret: "hook-secret",
				github.EnvClientID:      "Iv1.abc",
				github.EnvClientSecret:  "client-secret",
				github.EnvAPIURL:        "https://ghe.acme.test/api/v3/",
			}))
			if err != nil || !enabled {
				t.Fatalf("LoadConfig: %v, %v", enabled, err)
			}
			if cfg.AppID != 99001 || !cfg.PrivateKey.Equal(k) || string(cfg.WebhookSecret) != "hook-secret" {
				t.Fatalf("unexpected config %v", cfg)
			}
			if cfg.APIURL != "https://ghe.acme.test/api/v3" || cfg.WebURL != github.DefaultWebURL {
				t.Fatalf("urls %q %q", cfg.APIURL, cfg.WebURL)
			}
			if got, want := cfg.InstallURL("st te"), github.DefaultWebURL+"/apps/glossa/installations/new?state=st+te"; got != want {
				t.Fatalf("InstallURL = %q, want %q", got, want)
			}
			for _, s := range []string{cfg.String(), fmt.Sprint(cfg.LogValue())} {
				if strings.Contains(s, "hook-secret") || strings.Contains(s, "client-secret") || strings.Contains(s, "PRIVATE") {
					t.Fatalf("a secret leaked: %s", s)
				}
			}
		})
	}
}

// A pull request's address is the web host's, not the API's: on GitHub
// Enterprise Server the two differ, and a link to the API would open a
// JSON document.
func TestPullRequestURLIsOnTheWebHost(t *testing.T) {
	for _, c := range []struct {
		web, repo string
		number    int
		want      string
	}{
		{github.DefaultWebURL, "acme/shop", 11, "https://github.com/acme/shop/pull/11"},
		{"https://ghe.acme.test", "platform/web app", 7, "https://ghe.acme.test/platform/web%20app/pull/7"},
		// Nothing to point at: no repository, or no pull request.
		{github.DefaultWebURL, "", 11, ""},
		{github.DefaultWebURL, "acme/shop", 0, ""},
		// A label that is not owner/name is not a path to invent.
		{github.DefaultWebURL, "shop", 11, ""},
	} {
		cfg := github.Config{WebURL: c.web}
		if got := cfg.PullRequestURL(c.repo, c.number); got != c.want {
			t.Errorf("PullRequestURL(%q, %d) on %s = %q, want %q", c.repo, c.number, c.web, got, c.want)
		}
	}
}

func TestLoadConfigReportsEveryProblem(t *testing.T) {
	_, enabled, err := github.LoadConfig(env(map[string]string{
		github.EnvAppID:      "abc",
		github.EnvPrivateKey: "-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----",
		github.EnvAPIURL:     "ftp://nope",
	}))
	if !enabled || err == nil {
		t.Fatalf("want an error, got %v %v", enabled, err)
	}
	for _, want := range []string{github.EnvAppID, github.EnvPrivateKey, github.EnvAppSlug, github.EnvWebhookSecret, github.EnvClientID, github.EnvClientSecret, github.EnvAPIURL} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "AAAA") {
		t.Fatal("the key text leaked into the error")
	}
}
