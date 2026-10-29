package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// secretScanPattern is what we register with secret scanners (GitHub
// push protection, gitleaks). Keep it in sync with README.md.
var secretScanPattern = regexp.MustCompile(`^glossa_api_[A-Za-z0-9_-]{43}$`)

var now = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func TestTokenSecretFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		s, err := domain.NewTokenSecret()
		if err != nil {
			t.Fatal(err)
		}
		if !secretScanPattern.MatchString(s.String()) {
			t.Fatalf("secret %q does not match the secret-scanning pattern", s)
		}
		if seen[s.String()] {
			t.Fatal("two secrets collided")
		}
		seen[s.String()] = true
	}
}

func TestTokenSecretHashIsSHA256OfTheWholeSecret(t *testing.T) {
	s, err := domain.NewTokenSecret()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(s.String()))
	if s.Hash() != hex.EncodeToString(sum[:]) {
		t.Errorf("Hash() = %s, want the hex SHA-256 of the secret", s.Hash())
	}
	parsed, err := domain.ParseTokenSecret(s.String())
	if err != nil || parsed.Hash() != s.Hash() {
		t.Fatalf("ParseTokenSecret round trip = %v, %v", parsed, err)
	}
}

func TestTokenHintRevealsOnlyThePrefixAndFourCharacters(t *testing.T) {
	s, _ := domain.NewTokenSecret()
	hint := s.Hint()
	if !strings.HasPrefix(hint, "glossa_api_") || len(hint) != len("glossa_api_")+4 {
		t.Errorf("hint = %q", hint)
	}
	if !strings.HasPrefix(s.String(), hint) {
		t.Error("hint must be a prefix of the secret")
	}
}

func TestParseTokenSecretRejectsMalformedInput(t *testing.T) {
	valid, _ := domain.NewTokenSecret()
	for _, in := range []string{
		"",
		"glossa_api_",
		"ghp_" + strings.Repeat("a", 43),
		"glossa_api_" + strings.Repeat("a", 42),
		"glossa_api_" + strings.Repeat("a", 44),
		"glossa_api_" + strings.Repeat("!", 43),
		strings.ToUpper(valid.String()),
	} {
		if _, err := domain.ParseTokenSecret(in); !errors.Is(err, domain.ErrInvalidTokenSecret) {
			t.Errorf("ParseTokenSecret(%q) err = %v, want ErrInvalidTokenSecret", in, err)
		}
	}
}

func TestNewAPIToken(t *testing.T) {
	scopes, _ := domain.ParseScopes([]string{"read"})
	creator := domain.PersonActor(domain.NewPersonID())
	tenant := tenancy.NewID()

	tok, secret, err := domain.NewAPIToken(tenant, " CI deploy ", scopes, nil, creator, now)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "CI deploy" || tok.TenantID != tenant || tok.Hash != secret.Hash() || tok.Hint != secret.Hint() {
		t.Errorf("token = %+v", tok)
	}
	if tok.CreatedBy != creator || !tok.CreatedAt.Equal(now) {
		t.Errorf("provenance = %v at %v", tok.CreatedBy, tok.CreatedAt)
	}

	past := now.Add(-time.Minute)
	if _, _, err := domain.NewAPIToken(tenant, "old", scopes, &past, creator, now); !errors.Is(err, domain.ErrInvalidTokenExpiry) {
		t.Errorf("expiry in the past err = %v", err)
	}
	if _, _, err := domain.NewAPIToken(tenant, "  ", scopes, nil, creator, now); !errors.Is(err, domain.ErrInvalidTokenName) {
		t.Errorf("blank name err = %v", err)
	}
	if _, _, err := domain.NewAPIToken(tenant, strings.Repeat("n", 101), scopes, nil, creator, now); !errors.Is(err, domain.ErrInvalidTokenName) {
		t.Errorf("overlong name err = %v", err)
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	scopes, _ := domain.ParseScopes([]string{"read"})
	exp := now.Add(time.Hour)
	tok, _, err := domain.NewAPIToken(tenancy.NewID(), "ci", scopes, &exp, domain.PersonActor(domain.NewPersonID()), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tok.CheckUsable(now); err != nil {
		t.Errorf("fresh token unusable: %v", err)
	}
	if err := tok.CheckUsable(exp); !errors.Is(err, domain.ErrTokenExpired) {
		t.Errorf("at expiry err = %v, want ErrTokenExpired", err)
	}
	if err := tok.Revoke(now); err != nil {
		t.Fatal(err)
	}
	if err := tok.CheckUsable(now); !errors.Is(err, domain.ErrTokenRevoked) {
		t.Errorf("revoked err = %v, want ErrTokenRevoked", err)
	}
	if err := tok.Revoke(now); !errors.Is(err, domain.ErrTokenRevoked) {
		t.Errorf("second revoke err = %v, want ErrTokenRevoked", err)
	}
}

func TestActorRoundTrip(t *testing.T) {
	for _, a := range []domain.Actor{domain.PersonActor(domain.NewPersonID()), domain.TokenActor(domain.NewTokenID())} {
		got, err := domain.ParseActor(a.String())
		if err != nil || got != a {
			t.Errorf("ParseActor(%q) = %v, %v", a, got, err)
		}
	}
	if _, err := domain.ParseActor("robot:1"); err == nil {
		t.Error("unknown actor kind accepted")
	}
}
