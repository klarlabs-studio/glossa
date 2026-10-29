// Package app is the Identity context's application layer: sign-in and
// credential flows built on auth-go's domain services, and the tenant,
// member and token use cases. It enforces authorization itself (package
// authz), so every adapter — REST today, MCP later — gets the same
// rules.
package app

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// Config holds the flows' tunables.
type Config struct {
	// LinkBaseURL is Studio's origin. Emailed links point at
	// {LinkBaseURL}/auth/sign-in#token=… and /auth/reset-password#token=…;
	// the token travels in the fragment so it never reaches server logs.
	LinkBaseURL string
	// SessionTTL is a session's lifetime.
	SessionTTL time.Duration
	// SignInLinkTTL is the magic link lifetime (standard: 15 minutes).
	SignInLinkTTL time.Duration
	// ResetLinkTTL is the password reset link lifetime.
	ResetLinkTTL time.Duration
	// TOTPIssuer names the product in authenticator apps.
	TOTPIssuer string
	// Argon2 parameterizes password hashing; zero means auth-go's defaults.
	Argon2 authgo.Argon2idParams
}

// DefaultConfig returns the standard's values.
func DefaultConfig(linkBaseURL string) Config {
	return Config{
		LinkBaseURL:   strings.TrimRight(linkBaseURL, "/"),
		SessionTTL:    14 * 24 * time.Hour,
		SignInLinkTTL: 15 * time.Minute,
		ResetLinkTTL:  30 * time.Minute,
		TOTPIssuer:    "Glossa",
		Argon2:        authgo.DefaultArgon2idParams(),
	}
}

// Deps are the service's collaborators. The auth-go repositories are
// Glossa's adapters over the kernel's system-scope unit of work.
type Deps struct {
	Tx            Transactor
	Sessions      authgo.SessionRepository
	SignInLinks   authgo.MagicLinkRepository
	ResetLinks    authgo.MagicLinkRepository
	TOTP          authgo.TOTPRepository
	LoginAttempts authgo.LoginAttemptStore
	// Passkeys is nil when no WebAuthn relying party is configured.
	Passkeys authgo.PasskeyAuthenticator
	// Mailer is nil when the deployment sends no email: magic links,
	// verification and password reset by email are then unavailable
	// (ErrEmailDisabled), and password sign-in doesn't wait for a
	// verified address.
	Mailer Mailer
	// DeviceSessions stores the sessions approved devices receive
	// (RFC 0006 §7.2): the same sessions as Sessions, kept apart by
	// kind so a device bearer is never a cookie and a cookie never a
	// bearer. Nil turns device sign-in off (ErrDeviceSignInUnavailable).
	DeviceSessions authgo.SessionRepository
	// DeviceLimits rate-limits device sign-in: starts per client
	// address, look-ups and decisions per person. Nil limits nothing,
	// which is a configuration only tests have.
	DeviceLimits Limiter
	// Audit records sign-in attempts in the audit trail (RFC 0006 §6.1);
	// nil records nothing, which is a configuration only tests have.
	Audit  SignInAudit
	Logger *slog.Logger
	// Clock defaults to time.Now.
	Clock func() time.Time
}

// Service implements Identity's use cases.
type Service struct {
	cfg         Config
	tx          Transactor
	sessions    *authgo.SessionService
	signInLinks *authgo.MagicLinkService
	resetLinks  *authgo.MagicLinkService
	totp        *authgo.TOTPService
	totpCfg     authgo.TOTPConfig
	totpStore   authgo.TOTPRepository
	lockout     *authgo.LockoutService
	passkeys    authgo.PasskeyAuthenticator
	// deviceSessions issues device sessions; nil when device sign-in is
	// off.
	deviceSessions *authgo.SessionService
	deviceLimits   Limiter
	mailer         Mailer
	audit          SignInAudit
	// auditing tracks failed attempts being recorded in the background.
	auditing sync.WaitGroup
	logger   *slog.Logger
	now      func() time.Time
	// decoy is verified when an email has no password, so a failed
	// sign-in takes as long whether or not the account exists.
	decoy authgo.PasswordHash
	// oidc is the GitHub Actions exchange (RFC 0004 §6.3). It is set
	// after construction, by SetGitHubOIDC, because it needs the
	// Integration context; zero means this deployment has no GitHub App
	// and every exchange is refused.
	oidc GitHubOIDC
	// coverage answers which units a member whose visibility is
	// `assigned` may see (RFC 0006 §3.3). It is set after construction,
	// by SetCoverage, because Workflow implements it; nil means an
	// `assigned` member sees nothing.
	coverage authz.Coverage
}

// SetCoverage wires the read port assignment-scoped visibility filters
// through (authz.Coverage, implemented by Workflow's assignments). The
// composition root calls it once Workflow is built. Until it does —
// and in a deployment that never does — every principal Identity
// builds for an `assigned` member carries no Coverage, and authz then
// shows that member nothing: the restriction fails closed.
func (s *Service) SetCoverage(c authz.Coverage) { s.coverage = c }

// Realm is the auth-go TenantID of every auth-go object. auth-go ties a
// user, session or link to one tenant; Glossa's people are global and
// choose a tenant per request, so auth-go sees a single realm.
const Realm = "glossa"

var realm = mustTenantID(Realm)

func mustTenantID(s string) authgo.TenantID {
	id, err := authgo.NewTenantID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// New builds the service.
func New(cfg Config, d Deps) (*Service, error) {
	if d.Tx == nil || d.Sessions == nil || d.SignInLinks == nil || d.ResetLinks == nil ||
		d.TOTP == nil || d.LoginAttempts == nil {
		return nil, errors.New("identity: incomplete dependencies")
	}
	if cfg.Argon2 == (authgo.Argon2idParams{}) {
		cfg.Argon2 = authgo.DefaultArgon2idParams()
	}
	now := d.Clock
	if now == nil {
		now = time.Now
	}
	clock := func() time.Time { return now() }
	logger := d.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	totpCfg := authgo.DefaultTOTPConfig(cfg.TOTPIssuer)
	totp, err := authgo.NewTOTPService(d.TOTP, totpCfg, clock)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	decoy, err := authgo.HashPassword("decoy-"+time.Now().String(), cfg.Argon2)
	if err != nil {
		return nil, fmt.Errorf("identity: decoy hash: %w", err)
	}
	var deviceSessions *authgo.SessionService
	if d.DeviceSessions != nil {
		deviceSessions = authgo.NewSessionService(d.DeviceSessions, cfg.SessionTTL, clock)
	}
	return &Service{
		deviceSessions: deviceSessions,
		deviceLimits:   d.DeviceLimits,
		cfg:            cfg,
		tx:             d.Tx,
		sessions:       authgo.NewSessionService(d.Sessions, cfg.SessionTTL, clock),
		signInLinks:    authgo.NewMagicLinkService(d.SignInLinks, cfg.SignInLinkTTL, clock),
		resetLinks:     authgo.NewMagicLinkService(d.ResetLinks, cfg.ResetLinkTTL, clock),
		totp:           totp,
		totpCfg:        totpCfg,
		totpStore:      d.TOTP,
		lockout:        authgo.NewLockoutService(d.LoginAttempts, authgo.DefaultLockoutPolicy(), clock),
		passkeys:       d.Passkeys,
		mailer:         d.Mailer,
		audit:          d.Audit,
		logger:         logger,
		now:            func() time.Time { return now().UTC() },
		decoy:          decoy,
	}, nil
}

// Sign-in methods, as GET /v1/meta names them.
const (
	MethodPasskey   = "passkey"
	MethodPassword  = "password"
	MethodMagicLink = "magic_link"
)

// EmailEnabled reports whether the deployment sends email.
func (s *Service) EmailEnabled() bool { return s.mailer != nil }

// SignInMethods lists how people can sign in here, strongest first:
// passkeys when a relying party is configured, passwords always, magic
// links when email is.
func (s *Service) SignInMethods() []string {
	var out []string
	if s.PasskeysEnabled() {
		out = append(out, MethodPasskey)
	}
	out = append(out, MethodPassword)
	if s.EmailEnabled() {
		out = append(out, MethodMagicLink)
	}
	return out
}

func userID(p domain.PersonID) authgo.UserID {
	id, _ := authgo.NewUserID(p.String()) // a UUID is always a valid UserID
	return id
}

func personOf(u authgo.UserID) (domain.PersonID, error) {
	return domain.ParsePersonID(u.String())
}

func parseEmail(s string) (authgo.Email, error) {
	e, err := authgo.NewEmail(s)
	if err != nil {
		return authgo.Email{}, ErrInvalidEmail
	}
	return e, nil
}
