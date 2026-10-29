package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// TokenPrefix starts every API token. It exists for secret scanners:
// the full pattern is glossa_api_[A-Za-z0-9_-]{43}. A future publishable
// delivery key (M1) gets its own prefix, so a leaked key's kind is
// obvious from the string alone.
const TokenPrefix = "glossa_api_"

const (
	tokenBodyLen = 43 // auth-go's 256-bit token, base64url without padding
	hintBodyLen  = 4
	maxTokenName = 100
)

// TokenSecret is the raw bearer credential. It is shown exactly once, at
// creation; only its hash is stored.
type TokenSecret struct{ v string }

// NewTokenSecret returns a fresh secret: the prefix plus 256 random bits
// from auth-go's token generator.
func NewTokenSecret() (TokenSecret, error) { return newSecret(TokenPrefix) }

// ParseTokenSecret validates an inbound bearer credential's shape before
// anything is hashed or looked up. It accepts API tokens only; an
// in-context grant (InContextGrantPrefix) has its own parser, so the two
// credentials can never be checked against the wrong table.
func ParseTokenSecret(s string) (TokenSecret, error) { return parseSecret(TokenPrefix, s) }

// newSecret mints a secret under prefix. Every Glossa bearer credential
// is prefix + 256 random bits, so a leaked string says what it opens.
func newSecret(prefix string) (TokenSecret, error) {
	raw, err := authgo.NewToken()
	if err != nil {
		return TokenSecret{}, fmt.Errorf("identity: generate token: %w", err)
	}
	return TokenSecret{v: prefix + raw.String()}, nil
}

// parseSecret validates a credential's shape under prefix.
func parseSecret(prefix, s string) (TokenSecret, error) {
	body, ok := strings.CutPrefix(s, prefix)
	if !ok || len(body) != tokenBodyLen || strings.IndexFunc(body, notBase64URL) >= 0 {
		return TokenSecret{}, ErrInvalidTokenSecret
	}
	return TokenSecret{v: s}, nil
}

func notBase64URL(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
}

// String returns the secret. Never log it.
func (s TokenSecret) String() string { return s.v }

// Hash is the at-rest lookup key: auth-go's hex SHA-256 of the whole
// secret. A fast hash is right here — the secret has 256 bits of
// entropy, so there is nothing to brute-force.
func (s TokenSecret) Hash() string {
	tok, err := authgo.TokenFromString(s.v)
	if err != nil {
		return ""
	}
	return authgo.HashToken(tok)
}

// Hint is the prefix plus four characters: enough for a person to tell
// tokens apart in a list, too little to matter as a secret.
func (s TokenSecret) Hint() string {
	return s.v[:len(TokenPrefix)+hintBodyLen]
}

// ActorKind says what kind of principal acted.
type ActorKind string

const (
	ActorPerson ActorKind = "person"
	ActorToken  ActorKind = "token"
	// ActorSystem is a bounded context's background process (an outbox
	// subscriber, a job) acting through another context's authorized
	// application ports. It never signs in and never holds a token.
	ActorSystem ActorKind = "system"
)

// Actor identifies who did something: a person, an API token or a
// background process.
type Actor struct {
	Kind ActorKind
	ID   uuid.UUID
}

// PersonActor names a person.
func PersonActor(id PersonID) Actor { return Actor{Kind: ActorPerson, ID: id.UUID()} }

// TokenActor names an API token.
func TokenActor(id TokenID) Actor { return Actor{Kind: ActorToken, ID: id.UUID()} }

// systemActors namespaces the stable IDs of background processes.
var systemActors = uuid.MustParse("5d0f6f64-2f8e-4c61-9c55-3f7d7a2b9e10")

// SystemActor names a background process by its stable name
// ("knowledge.derive_tm"); the same name is always the same actor.
func SystemActor(name string) Actor {
	return Actor{Kind: ActorSystem, ID: uuid.NewSHA1(systemActors, []byte(name))}
}

// String renders "person:<id>", "token:<id>" or "system:<id>".
func (a Actor) String() string { return string(a.Kind) + ":" + a.ID.String() }

// ParseActor parses Actor.String's output.
func ParseActor(s string) (Actor, error) {
	kind, id, _ := strings.Cut(s, ":")
	u, err := parseID(id)
	if err != nil || (ActorKind(kind) != ActorPerson && ActorKind(kind) != ActorToken) {
		return Actor{}, fmt.Errorf("%w: actor %q", ErrInvalidID, s)
	}
	return Actor{Kind: ActorKind(kind), ID: u}, nil
}

// APIToken is the aggregate for a tenant-owned bearer credential used by
// the CLI, CI and agents. Tokens belong to the tenant, not to the person
// who created them, and are revoked rather than deleted so their use
// stays auditable.
type APIToken struct {
	ID       TokenID
	TenantID tenancy.ID
	Name     string
	Scopes   Scopes
	// Projects is the projects the token may act on (RFC 0006 §4.1);
	// empty is every project. Package authz enforces it: a project
	// outside it does not exist to the token.
	Projects   ProjectScope
	Hint       string
	Hash       string
	CreatedBy  Actor
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// NewAPIToken creates a token and its one-time secret. expiresAt is
// optional; when set it must be in the future.
func NewAPIToken(tenant tenancy.ID, name string, scopes Scopes, expiresAt *time.Time, by Actor, now time.Time) (APIToken, TokenSecret, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxTokenName {
		return APIToken{}, TokenSecret{}, ErrInvalidTokenName
	}
	if len(scopes) == 0 {
		return APIToken{}, TokenSecret{}, ErrInvalidScope
	}
	if expiresAt != nil && !expiresAt.After(now) {
		return APIToken{}, TokenSecret{}, ErrInvalidTokenExpiry
	}
	secret, err := NewTokenSecret()
	if err != nil {
		return APIToken{}, TokenSecret{}, err
	}
	return APIToken{
		ID:        NewTokenID(),
		TenantID:  tenant,
		Name:      name,
		Scopes:    scopes,
		Hint:      secret.Hint(),
		Hash:      secret.Hash(),
		CreatedBy: by,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}, secret, nil
}

// CheckUsable returns ErrTokenRevoked or ErrTokenExpired, or nil.
func (t APIToken) CheckUsable(now time.Time) error {
	if t.RevokedAt != nil {
		return ErrTokenRevoked
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return ErrTokenExpired
	}
	return nil
}

// Grant is what the token may do in its tenant. It does not yet reflect
// t.Projects (RFC 0006 wave 2).
func (t APIToken) Grant() Grant { return GrantForScopes(t.Scopes) }

// Revoke ends the token for good.
func (t *APIToken) Revoke(now time.Time) error {
	if t.RevokedAt != nil {
		return ErrTokenRevoked
	}
	t.RevokedAt = &now
	return nil
}
