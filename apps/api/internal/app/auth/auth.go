// Package auth holds the JWT login + signing flow. The HTTP layer
// exposes POST /api/v1/auth/login; the JWT middleware in
// interfaces/httpgin consumes the issued token.
//
// Tokens are HS256 with the signing key from config (JWT_SIGNING_KEY).
// Claims: standard sub/iss/iat/exp plus custom tenant_id, role,
// locales — exactly what the gin middleware needs to populate
// per-request context without a DB hit.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"go.klarlabs.de/glossa/apps/api/internal/domain/user"
)

// TokenIssuer signs claims into a compact JWT.
type TokenIssuer interface {
	Issue(claims Claims) (string, error)
	Verify(token string) (Claims, error)
}

// Claims is the public shape carried in every Glossa JWT. The
// struct mirrors what handlers + middleware need to authorise a
// request, so verification populates the gin context in one step.
type Claims struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Email    string
	Role     user.Role
	Locales  []string
	IssuedAt time.Time
	Expires  time.Time
}

// HMACIssuer signs/verifies with HS256.
type HMACIssuer struct {
	key      []byte
	issuer   string
	tokenTTL time.Duration
	parser   *jwt.Parser
}

// NewHMACIssuer constructs an issuer. ttl is the access-token
// lifetime — 24h is a balance between refresh churn and the blast
// radius of a stolen token.
func NewHMACIssuer(key []byte, issuer string, ttl time.Duration) (*HMACIssuer, error) {
	if len(key) < 32 {
		return nil, errors.New("auth: signing key must be at least 32 bytes")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &HMACIssuer{
		key:      key,
		issuer:   issuer,
		tokenTTL: ttl,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{"HS256"}),
			jwt.WithIssuer(issuer),
			jwt.WithExpirationRequired(),
			// Only the canonical base64url encoding verifies: a lenient
			// decoder ignores the signature's padding bits.
			jwt.WithStrictDecoding(),
		),
	}, nil
}

type jwtBody struct {
	jwt.RegisteredClaims
	TenantID string   `json:"tenant_id"`
	Email    string   `json:"email"`
	Role     string   `json:"role"`
	Locales  []string `json:"locales,omitempty"`
}

// Issue returns a signed compact JWT carrying c.
func (h *HMACIssuer) Issue(c Claims) (string, error) {
	now := time.Now().UTC()
	if c.IssuedAt.IsZero() {
		c.IssuedAt = now
	}
	if c.Expires.IsZero() {
		c.Expires = now.Add(h.tokenTTL)
	}
	body := jwtBody{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    h.issuer,
			Subject:   c.UserID.String(),
			IssuedAt:  jwt.NewNumericDate(c.IssuedAt),
			ExpiresAt: jwt.NewNumericDate(c.Expires),
		},
		TenantID: c.TenantID.String(),
		Email:    c.Email,
		Role:     string(c.Role),
		Locales:  c.Locales,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, body)
	return tok.SignedString(h.key)
}

// Verify parses + validates a token. Returns ErrInvalidToken on
// any failure — never reveals the specific cause so a probing
// caller can't distinguish expired vs malformed vs wrong-signature.
func (h *HMACIssuer) Verify(token string) (Claims, error) {
	body := &jwtBody{}
	parsed, err := h.parser.ParseWithClaims(token, body, func(t *jwt.Token) (interface{}, error) {
		return h.key, nil
	})
	if err != nil || !parsed.Valid {
		return Claims{}, ErrInvalidToken
	}
	userID, err := uuid.Parse(body.Subject)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	tenantID, err := uuid.Parse(body.TenantID)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	return Claims{
		UserID:   userID,
		TenantID: tenantID,
		Email:    body.Email,
		Role:     user.Role(body.Role),
		Locales:  body.Locales,
		IssuedAt: body.IssuedAt.Time,
		Expires:  body.ExpiresAt.Time,
	}, nil
}

// ErrInvalidToken is the only verification error callers see.
var ErrInvalidToken = errors.New("auth: invalid token")

// ErrInvalidCredentials is the only login error callers see —
// same response shape for "no such user" and "wrong password" to
// deny user-enumeration attacks.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// Login authenticates an email/password pair against a tenant and
// issues a JWT. The use case is intentionally tenant-scoped — the
// caller (handler) maps host/header to a tenant before invoking.
type Login struct {
	users  user.Repository
	issuer TokenIssuer
}

func NewLogin(users user.Repository, issuer TokenIssuer) *Login {
	return &Login{users: users, issuer: issuer}
}

type LoginInput struct {
	TenantID uuid.UUID
	Email    string
	Password string
}

type LoginOutput struct {
	Token   string
	User    user.User
	Expires time.Time
}

func (l *Login) Execute(ctx context.Context, in LoginInput) (LoginOutput, error) {
	email, err := user.NormalizeEmail(in.Email)
	if err != nil {
		return LoginOutput{}, ErrInvalidCredentials
	}
	u, err := l.users.FindByEmail(ctx, in.TenantID, email)
	if err != nil {
		// Compare against a dummy hash even on miss so the
		// timing channel doesn't leak "user exists".
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$abcdefghijklmnopqrstuv"), []byte(in.Password))
		return LoginOutput{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword(u.PasswordHash, []byte(in.Password)); err != nil {
		return LoginOutput{}, ErrInvalidCredentials
	}
	expires := time.Now().UTC().Add(24 * time.Hour)
	tok, err := l.issuer.Issue(Claims{
		UserID:   u.ID,
		TenantID: u.TenantID,
		Email:    u.Email,
		Role:     u.Role,
		Locales:  u.Locales,
		Expires:  expires,
	})
	if err != nil {
		return LoginOutput{}, fmt.Errorf("issue token: %w", err)
	}
	return LoginOutput{Token: tok, User: u, Expires: expires}, nil
}

// HashPassword runs bcrypt at the default cost. Centralised so
// every user-creation path uses the same algorithm + cost.
func HashPassword(plain string) ([]byte, error) {
	if len(plain) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	return bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
}
