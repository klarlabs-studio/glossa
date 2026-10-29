// Package github is Glossa's GitHub App adapter (RFC 0004 §6): it
// implements app.GitHub over GitHub's REST API and verifies webhook
// deliveries.
//
//   - App auth: an RS256 App JWT (issued 60 s in the past, valid for
//     9 minutes) mints installation tokens on demand; they are cached in
//     memory until 5 minutes before they expire, never stored, and minted
//     once per installation however many calls wait for one.
//   - Checks and the sticky PR comment, idempotent: a retry never creates
//     a second check run or comment.
//   - Every call runs through fortify: a bulkhead per tenant, a circuit
//     breaker per installation, retries with backoff that honour
//     Retry-After and GitHub's secondary rate limits, and a timeout per
//     attempt.
//   - Repositories are addressed by numeric ID (/repositories/{id}/…),
//     so a rename or transfer does not break a Git connection.
//
// Logs carry operation names, installation IDs, check-run and comment
// IDs and statuses; never tokens, keys or payload bodies.
package github

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Environment variables LoadConfig reads.
const (
	EnvAppID         = "GLOSSA_GITHUB_APP_ID"
	EnvAppSlug       = "GLOSSA_GITHUB_APP_SLUG"        // the App's URL name, for the install link
	EnvPrivateKey    = "GLOSSA_GITHUB_APP_PRIVATE_KEY" // PEM (PKCS#1 or PKCS#8)
	EnvWebhookSecret = "GLOSSA_GITHUB_WEBHOOK_SECRET"
	EnvClientID      = "GLOSSA_GITHUB_CLIENT_ID"
	EnvClientSecret  = "GLOSSA_GITHUB_CLIENT_SECRET"
	EnvAPIURL        = "GLOSSA_GITHUB_API_URL" // default https://api.github.com
	EnvWebURL        = "GLOSSA_GITHUB_WEB_URL" // default https://github.com

	// CI's OIDC exchange (RFC 0004 §6.3). github.com needs none of
	// these; GitHub Enterprise Server sets its own issuer, and an
	// operator who knows their issuer's availability can widen how long
	// a cached key set keeps verifying while that issuer is unreachable.
	EnvOIDCIssuer   = "GLOSSA_GITHUB_OIDC_ISSUER"    // default https://token.actions.githubusercontent.com
	EnvOIDCAudience = "GLOSSA_GITHUB_OIDC_AUDIENCE"  // default glossa
	EnvOIDCMaxStale = "GLOSSA_GITHUB_OIDC_MAX_STALE" // default 24h (auth-go's)
)

// Defaults for github.com; GitHub Enterprise Server uses
// https://HOST/api/v3 and https://HOST.
const (
	DefaultAPIURL = "https://api.github.com"
	DefaultWebURL = "https://github.com"
	// DefaultOIDCIssuer is where github.com's Actions ID tokens come
	// from; DefaultOIDCAudience is the `aud` a run must ask for.
	DefaultOIDCIssuer   = "https://token.actions.githubusercontent.com"
	DefaultOIDCAudience = "glossa"
)

// Config is the GitHub App's platform configuration (not tenant data).
// Its String and LogValue redact the secrets.
type Config struct {
	AppID int64
	// AppSlug is the App's name in its own URL ("glossa"), which the
	// install link needs: <WebURL>/apps/<slug>/installations/new.
	AppSlug       string
	PrivateKey    *rsa.PrivateKey
	WebhookSecret []byte
	// ClientID and ClientSecret are the App's OAuth credentials, for the
	// install flow's one-time user token.
	ClientID     string
	ClientSecret string
	// APIURL is the REST base (GHES: https://HOST/api/v3).
	APIURL string
	// WebURL is where people authorize the App (GHES: https://HOST).
	WebURL string

	// OIDCIssuer is the exact `iss` of the Actions ID tokens CI presents
	// (GHES: https://HOST/_services/token). OIDCAudience is the `aud` a
	// run must ask for — Glossa's own name, never GitHub's default
	// audience, which any other service could also be handed.
	OIDCIssuer   string
	OIDCAudience string
	// OIDCMaxStale bounds how long a cached key set keeps verifying when
	// refetches fail. Zero means auth-go's default (24 h).
	OIDCMaxStale time.Duration
}

// String implements fmt.Stringer without the secrets.
func (c Config) String() string {
	return fmt.Sprintf(
		"github.Config{AppID: %d, AppSlug: %q, ClientID: %q, APIURL: %q, WebURL: %q, OIDCIssuer: %q, OIDCAudience: %q, secrets: [redacted]}",
		c.AppID, c.AppSlug, c.ClientID, c.APIURL, c.WebURL, c.OIDCIssuer, c.OIDCAudience)
}

// LogValue implements slog.LogValuer without the secrets.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("app_id", c.AppID),
		slog.String("app_slug", c.AppSlug),
		slog.String("client_id", c.ClientID),
		slog.String("api_url", c.APIURL),
		slog.String("web_url", c.WebURL),
		slog.String("oidc_issuer", c.OIDCIssuer),
		slog.String("oidc_audience", c.OIDCAudience),
	)
}

// Validate reports every problem at once.
func (c Config) Validate() error {
	var errs []error
	if c.AppID <= 0 {
		errs = append(errs, fmt.Errorf("%s: must be a positive integer", EnvAppID))
	}
	if !appSlugPattern.MatchString(c.AppSlug) {
		errs = append(errs, fmt.Errorf("%s: required; it is the App's URL name (lowercase letters, digits and hyphens)", EnvAppSlug))
	}
	if c.PrivateKey == nil {
		errs = append(errs, fmt.Errorf("%s: required", EnvPrivateKey))
	}
	if len(c.WebhookSecret) == 0 {
		errs = append(errs, fmt.Errorf("%s: required", EnvWebhookSecret))
	}
	if c.ClientID == "" {
		errs = append(errs, fmt.Errorf("%s: required", EnvClientID))
	}
	if c.ClientSecret == "" {
		errs = append(errs, fmt.Errorf("%s: required", EnvClientSecret))
	}
	if c.OIDCAudience == "" {
		errs = append(errs, fmt.Errorf("%s: must not be empty", EnvOIDCAudience))
	}
	for key, raw := range map[string]string{EnvAPIURL: c.APIURL, EnvWebURL: c.WebURL, EnvOIDCIssuer: c.OIDCIssuer} {
		if u, err := url.Parse(raw); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s: %q is not an absolute http(s) URL", key, raw))
		}
	}
	return errors.Join(errs...)
}

// LoadConfig reads the App's configuration from the environment
// (os.LookupEnv satisfies lookup). enabled is false when no App ID is
// set: the GitHub integration is then off and nothing else is read.
func LoadConfig(lookup func(string) (string, bool)) (cfg Config, enabled bool, err error) {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	rawID := get(EnvAppID)
	if rawID == "" {
		return Config{}, false, nil
	}
	cfg = Config{
		AppSlug:       strings.ToLower(get(EnvAppSlug)),
		WebhookSecret: []byte(get(EnvWebhookSecret)),
		ClientID:      get(EnvClientID),
		ClientSecret:  get(EnvClientSecret),
		APIURL:        strings.TrimRight(orDefault(get(EnvAPIURL), DefaultAPIURL), "/"),
		WebURL:        strings.TrimRight(orDefault(get(EnvWebURL), DefaultWebURL), "/"),
		OIDCIssuer:    strings.TrimRight(orDefault(get(EnvOIDCIssuer), DefaultOIDCIssuer), "/"),
		OIDCAudience:  orDefault(get(EnvOIDCAudience), DefaultOIDCAudience),
	}
	var errs []error
	if raw := get(EnvOIDCMaxStale); raw != "" {
		if cfg.OIDCMaxStale, err = time.ParseDuration(raw); err != nil || cfg.OIDCMaxStale <= 0 {
			errs = append(errs, fmt.Errorf("%s: %q is not a positive duration (e.g. 24h)", EnvOIDCMaxStale, raw))
			cfg.OIDCMaxStale = 0
		}
	}
	if cfg.AppID, err = strconv.ParseInt(rawID, 10, 64); err != nil {
		cfg.AppID = 0
	}
	if pemText := get(EnvPrivateKey); pemText != "" {
		if cfg.PrivateKey, err = ParsePrivateKey([]byte(pemText)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", EnvPrivateKey, err))
		}
	}
	if err := cfg.Validate(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, true, err
	}
	return cfg, true, nil
}

// ParsePrivateKey reads the App's RSA key from PEM, PKCS#1 (as GitHub
// downloads it) or PKCS#8. Escaped newlines ("\n" as two characters,
// common in env files) are accepted.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	text := strings.ReplaceAll(string(pemBytes), `\n`, "\n")
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not an RSA private key in PKCS#1 or PKCS#8")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the key is not RSA")
	}
	return rk, nil
}

// appSlugPattern is what GitHub allows in an App's URL name.
var appSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// InstallURL is where a person authorizes the App, carrying state
// through the round trip so the callback can prove who started it.
func (c Config) InstallURL(state string) string {
	u := c.WebURL + "/apps/" + url.PathEscape(c.AppSlug) + "/installations/new"
	if state != "" {
		u += "?" + url.Values{"state": {state}}.Encode()
	}
	return u
}

// PullRequestURL is where pull request number of repository ("owner/name")
// is on the web host, or "" when there is nothing to point at: no
// number, or a repository label that is not owner/name.
func (c Config) PullRequestURL(repository string, number int) string {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") || number <= 0 {
		return ""
	}
	return c.WebURL + "/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/pull/" + strconv.Itoa(number)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
