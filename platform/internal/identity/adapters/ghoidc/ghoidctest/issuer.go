// Package ghoidctest is a fake OpenID Connect issuer standing in for
// GitHub Actions, so the CI exchange (RFC 0004 §6.3) can be tested
// against a real verifier rather than a stubbed one.
//
// It serves a discovery document and a JWKS over httptest and signs
// compact RS256 tokens with a generated key, which is what lets a test
// say "this token is expired", "this one is for another audience" or
// "this one is signed by a key the issuer never published" and see the
// production verifier refuse each for its own reason.
package ghoidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Claims is one GitHub Actions ID token as a test wants to describe it.
// The zero value is a usable token for Repository/RepositoryID once
// those are set; Issuer, Audience and the times default to a token the
// verifier accepts.
type Claims struct {
	// Issuer defaults to the fake issuer's own URL. Set it to forge a
	// token that claims to come from somewhere else.
	Issuer string
	// Audience defaults to "glossa".
	Audience string
	// Subject defaults to "repo:acme/shop:ref:refs/heads/main".
	Subject string

	RepositoryID      int64
	RepositoryOwnerID int64
	Repository        string
	Ref               string
	SHA               string
	EventName         string
	JobWorkflowRef    string
	RunID             string
	RunnerEnvironment string

	// IssuedAt defaults to now, Expiry to five minutes from now.
	IssuedAt time.Time
	Expiry   time.Time
	// NotBefore is omitted when zero.
	NotBefore time.Time
	// Extra overwrites or adds raw claims, for shapes the fields above
	// cannot express (a numeric repository_id sent as a string, say).
	Extra map[string]any
}

// Issuer is a running fake issuer.
type Issuer struct {
	// URL is the issuer value and the discovery base.
	URL string

	key   *rsa.PrivateKey
	kid   string
	other *rsa.PrivateKey
	srv   *httptest.Server
	now   func() time.Time

	mu      sync.Mutex
	fetches int
}

// New starts an issuer and stops it when the test ends. Keys are 2048
// bits, generated once per issuer.
func New(t *testing.T) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate the issuer key: %v", err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate the second key: %v", err)
	}
	iss := &Issuer{key: key, kid: "test-key-1", other: other, now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                iss.URL,
			"jwks_uri":                              iss.URL + "/.well-known/jwks",
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public", "pairwise"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /.well-known/jwks", func(w http.ResponseWriter, _ *http.Request) {
		iss.mu.Lock()
		iss.fetches++
		iss.mu.Unlock()
		pub := key.Public().(*rsa.PublicKey)
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": iss.kid,
			"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	iss.srv = httptest.NewServer(mux)
	iss.URL = iss.srv.URL
	t.Cleanup(iss.srv.Close)
	return iss
}

// SetClock makes the issuer date tokens by now, so a test can issue one
// that is already expired without waiting.
func (i *Issuer) SetClock(now func() time.Time) { i.now = now }

// KeySetFetches counts how often the key set was served, so a test can
// say that verifying many tokens does not fetch many key sets.
func (i *Issuer) KeySetFetches() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fetches
}

// Token signs an ID token with the issuer's published key.
func (i *Issuer) Token(t *testing.T, c Claims) string {
	t.Helper()
	return i.sign(t, i.key, i.kid, c)
}

// TokenFromAnotherKey signs a token with a key the issuer never
// published, keeping the published `kid` — the shape of a forgery that
// a verifier trusting the token's own header would accept.
func (i *Issuer) TokenFromAnotherKey(t *testing.T, c Claims) string {
	t.Helper()
	return i.sign(t, i.other, i.kid, c)
}

func (i *Issuer) sign(t *testing.T, key *rsa.PrivateKey, kid string, c Claims) string {
	t.Helper()
	now := i.now()
	claims := map[string]any{
		"iss": orString(c.Issuer, i.URL),
		"aud": orString(c.Audience, "glossa"),
		"sub": orString(c.Subject, "repo:acme/shop:ref:refs/heads/main"),
		"jti": "jti-" + strconv.FormatInt(now.UnixNano(), 10),
		"iat": orTime(c.IssuedAt, now).Unix(),
		"exp": orTime(c.Expiry, now.Add(5*time.Minute)).Unix(),

		// GitHub sends both IDs as decimal strings, so the fake does too.
		"repository_id":       strconv.FormatInt(c.RepositoryID, 10),
		"repository_owner_id": strconv.FormatInt(orInt(c.RepositoryOwnerID, 1), 10),
		"repository":          c.Repository,
		"ref":                 c.Ref,
		"sha":                 c.SHA,
		"event_name":          c.EventName,
		"job_workflow_ref":    c.JobWorkflowRef,
		"runner_environment":  c.RunnerEnvironment,
	}
	if c.RunID != "" {
		claims["run_id"] = json.Number(c.RunID)
	}
	if !c.NotBefore.IsZero() {
		claims["nbf"] = c.NotBefore.Unix()
	}
	for k, v := range c.Extra {
		claims[k] = v
	}
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatalf("encode the header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encode the claims: %v", err)
	}
	signing := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign the token: %v", err)
	}
	return signing + "." + b64(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// No caching, so a test that rotates keys sees the change at once.
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func orString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orTime(v, def time.Time) time.Time {
	if v.IsZero() {
		return def
	}
	return v
}

func orInt(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}
