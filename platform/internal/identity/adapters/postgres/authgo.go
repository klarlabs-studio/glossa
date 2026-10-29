package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

// auth-go's repository ports over Identity's tables. Glossa uses
// auth-go's domain services (sessions, magic links, TOTP, lockout) and
// its WebAuthn adapter as they are; only persistence is Glossa's, so it
// runs under the kernel's system-scope unit of work and row-level
// security instead of auth-go's pgstore (database/sql, its own schema,
// one tenant per user).
//
// auth-go keys users and links by a TenantID. Glossa's people are global,
// so every object belongs to app.Realm and the tenant column isn't stored.

var realm = func() authgo.TenantID {
	id, err := authgo.NewTenantID(app.Realm)
	if err != nil {
		panic(err)
	}
	return id
}()

// system runs fn in its own system-scope transaction.
func system(ctx context.Context, uow *db.UnitOfWork, fn func(context.Context, *identitysql.Queries) error) error {
	return uow.InSystemTx(ctx, systemScope, func(ctx context.Context, tx *db.SystemTx) error {
		return fn(ctx, identitysql.New(tx))
	})
}

// authError maps storage errors to auth-go's.
func authError(err error) error {
	if errors.Is(storeError(err), app.ErrNotFound) {
		return authgo.ErrNotFound
	}
	return err
}

func personID(u authgo.UserID) (domain.PersonID, error) {
	p, err := domain.ParsePersonID(u.String())
	if err != nil {
		return domain.PersonID{}, fmt.Errorf("identity: auth-go user %q is not a person id: %w", u, err)
	}
	return p, nil
}

// SessionRepo implements authgo.SessionRepository and
// authgo.AtomicSessionRotator for one kind of session (migration 0054):
// a browser's, sent as the cookie, or a device's, sent as a glossa_dev_
// bearer. It finds and ends only sessions of its own kind, so neither
// credential is ever accepted as the other; DeleteByUser (sign out
// everywhere) ends both kinds. Keys are auth-go's SHA-256 token hashes.
type SessionRepo struct {
	uow  *db.UnitOfWork
	kind string
}

// Session kinds (migration 0054).
const (
	SessionBrowser = "browser"
	SessionDevice  = "device"
)

// NewSessionRepo returns the browser sessions' repository.
func NewSessionRepo(uow *db.UnitOfWork) *SessionRepo {
	return &SessionRepo{uow: uow, kind: SessionBrowser}
}

// NewDeviceSessionRepo returns the device sessions' repository (RFC 0006
// §7.2).
func NewDeviceSessionRepo(uow *db.UnitOfWork) *SessionRepo {
	return &SessionRepo{uow: uow, kind: SessionDevice}
}

var (
	_ authgo.SessionRepository    = (*SessionRepo)(nil)
	_ authgo.AtomicSessionRotator = (*SessionRepo)(nil)
)

// Save stores a new session.
func (r *SessionRepo) Save(ctx context.Context, s authgo.Session) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		return r.insert(ctx, q, s)
	})
}

func (r *SessionRepo) insert(ctx context.Context, q *identitysql.Queries, s authgo.Session) error {
	snap := s.Snapshot()
	uid, err := authgo.NewUserID(snap.UserID)
	if err != nil {
		return err
	}
	person, err := personID(uid)
	if err != nil {
		return err
	}
	return q.InsertSession(ctx, identitysql.InsertSessionParams{
		TokenHash: snap.Token, PersonID: person.UUID(), CreatedAt: snap.CreatedAt, ExpiresAt: snap.ExpiresAt,
		Kind: r.kind,
	})
}

// FindByToken loads a session of this kind by its hash.
func (r *SessionRepo) FindByToken(ctx context.Context, key authgo.Token) (authgo.Session, error) {
	var out authgo.Session
	err := system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		row, err := q.GetSession(ctx, identitysql.GetSessionParams{TokenHash: key.String(), Kind: r.kind})
		if err != nil {
			return authError(err)
		}
		out = authgo.SessionFromSnapshot(authgo.SessionSnapshot{
			Token: row.TokenHash, UserID: row.PersonID.String(), TenantID: realm.String(),
			CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
		})
		return nil
	})
	return out, err
}

// Delete removes one session of this kind by its hash.
func (r *SessionRepo) Delete(ctx context.Context, key authgo.Token) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		_, err := q.DeleteSession(ctx, identitysql.DeleteSessionParams{TokenHash: key.String(), Kind: r.kind})
		return err
	})
}

// DeleteByUser removes every session of a person, of both kinds (sign
// out everywhere, and a password reset, end the person's devices too).
func (r *SessionRepo) DeleteByUser(ctx context.Context, u authgo.UserID) error {
	person, err := personID(u)
	if err != nil {
		return err
	}
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		return q.DeleteSessionsOfPerson(ctx, person.UUID())
	})
}

// RotateAtomically swaps a session for a new one in one transaction.
func (r *SessionRepo) RotateAtomically(ctx context.Context, oldKey authgo.Token, s authgo.Session) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		n, err := q.DeleteSession(ctx, identitysql.DeleteSessionParams{TokenHash: oldKey.String(), Kind: r.kind})
		if err != nil {
			return err
		}
		if n == 0 {
			return authgo.ErrNotFound
		}
		return r.insert(ctx, q, s)
	})
}

// Link purposes, one authgo.MagicLinkService each.
const (
	PurposeSignIn        = "sign_in"
	PurposePasswordReset = "password_reset"
)

// LinkRepo implements authgo.MagicLinkRepository (and the atomic issuer)
// for one purpose, so a reset link can never sign anyone in.
type LinkRepo struct {
	uow     *db.UnitOfWork
	purpose string
}

// NewLinkRepo returns the repository for purpose.
func NewLinkRepo(uow *db.UnitOfWork, purpose string) *LinkRepo {
	return &LinkRepo{uow: uow, purpose: purpose}
}

var (
	_ authgo.MagicLinkRepository   = (*LinkRepo)(nil)
	_ authgo.AtomicMagicLinkIssuer = (*LinkRepo)(nil)
)

// Save stores a link.
func (r *LinkRepo) Save(ctx context.Context, m authgo.MagicLink) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		return r.insert(ctx, q, m)
	})
}

func (r *LinkRepo) insert(ctx context.Context, q *identitysql.Queries, m authgo.MagicLink) error {
	snap := m.Snapshot()
	return q.InsertEmailLink(ctx, identitysql.InsertEmailLinkParams{
		Hash: snap.Hash, Purpose: r.purpose, Email: snap.Email, ExpiresAt: snap.ExpiresAt,
	})
}

// FindByHash loads a link of this purpose.
func (r *LinkRepo) FindByHash(ctx context.Context, hash string) (authgo.MagicLink, error) {
	var out authgo.MagicLink
	err := system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		row, err := q.GetEmailLink(ctx, identitysql.GetEmailLinkParams{Hash: hash, Purpose: r.purpose})
		if err != nil {
			return authError(err)
		}
		out = authgo.MagicLinkFromSnapshot(authgo.MagicLinkSnapshot{
			Hash: row.Hash, Email: row.Email, TenantID: realm.String(), ExpiresAt: row.ExpiresAt, Consumed: row.Consumed,
		})
		return nil
	})
	return out, err
}

// MarkConsumed spends the link; false if it was already spent.
func (r *LinkRepo) MarkConsumed(ctx context.Context, hash string) (bool, error) {
	var n int64
	err := system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		var err error
		n, err = q.ConsumeEmailLink(ctx, identitysql.ConsumeEmailLinkParams{Hash: hash, Purpose: r.purpose})
		return err
	})
	return n > 0, err
}

// InvalidateOutstanding spends every open link of this purpose for email.
func (r *LinkRepo) InvalidateOutstanding(ctx context.Context, email authgo.Email, _ authgo.TenantID) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		return q.InvalidateEmailLinks(ctx, identitysql.InvalidateEmailLinksParams{Email: email.String(), Purpose: r.purpose})
	})
}

// IssueAtomically invalidates open links and stores the new one together.
func (r *LinkRepo) IssueAtomically(ctx context.Context, m authgo.MagicLink) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		if err := q.InvalidateEmailLinks(ctx, identitysql.InvalidateEmailLinksParams{
			Email: m.Email().String(), Purpose: r.purpose,
		}); err != nil {
			return err
		}
		return r.insert(ctx, q, m)
	})
}

// TOTPRepo implements authgo.TOTPRepository and authgo.AtomicTOTPConsumer.
// Secrets are sealed with an authgo.SecretCipher (AES-256-GCM).
//
// GetSecret returns pending as well as confirmed secrets, so auth-go's
// replay-safe TOTPService can confirm an enrollment; the application only
// requires TOTP at sign-in once the secret is confirmed.
type TOTPRepo struct {
	uow    *db.UnitOfWork
	cipher authgo.SecretCipher
}

// NewTOTPRepo returns a TOTP repository.
func NewTOTPRepo(uow *db.UnitOfWork, cipher authgo.SecretCipher) *TOTPRepo {
	return &TOTPRepo{uow: uow, cipher: cipher}
}

var (
	_ authgo.TOTPRepository     = (*TOTPRepo)(nil)
	_ authgo.AtomicTOTPConsumer = (*TOTPRepo)(nil)
)

// GetSecret loads and unseals a person's secret.
func (r *TOTPRepo) GetSecret(ctx context.Context, u authgo.UserID) (authgo.TOTPSecret, error) {
	person, err := personID(u)
	if err != nil {
		return authgo.TOTPSecret{}, err
	}
	var sealed string
	err = system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		row, err := q.GetTOTP(ctx, person.UUID())
		sealed = row.SecretCiphertext
		return authError(err)
	})
	if err != nil {
		return authgo.TOTPSecret{}, err
	}
	return unseal(r.cipher, sealed)
}

// SetSecret stores a pending secret (never replacing a confirmed one).
func (r *TOTPRepo) SetSecret(ctx context.Context, u authgo.UserID, secret authgo.TOTPSecret) error {
	person, err := personID(u)
	if err != nil {
		return err
	}
	sealed, err := seal(r.cipher, secret)
	if err != nil {
		return err
	}
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		_, err := q.UpsertPendingTOTP(ctx, identitysql.UpsertPendingTOTPParams{
			PersonID: person.UUID(), SecretCiphertext: sealed, At: time.Now().UTC(),
		})
		return err
	})
}

// DeleteSecret removes a person's secret.
func (r *TOTPRepo) DeleteSecret(ctx context.Context, u authgo.UserID) error {
	person, err := personID(u)
	if err != nil {
		return err
	}
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		n, err := q.DeleteTOTP(ctx, person.UUID())
		if err == nil && n == 0 {
			return authgo.ErrNotFound
		}
		return err
	})
}

// ConsumeStep records step as used; false on a replay.
func (r *TOTPRepo) ConsumeStep(ctx context.Context, u authgo.UserID, step int64) (bool, error) {
	person, err := personID(u)
	if err != nil {
		return false, err
	}
	var n int64
	err = system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		n, err = q.ConsumeTOTPStep(ctx, identitysql.ConsumeTOTPStepParams{PersonID: person.UUID(), Step: pgInt8(step)})
		return err
	})
	return n > 0, err
}

func seal(c authgo.SecretCipher, s authgo.TOTPSecret) (string, error) {
	ct, err := c.Encrypt([]byte(s.String()))
	if err != nil {
		return "", fmt.Errorf("identity: seal TOTP secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

func unseal(c authgo.SecretCipher, sealed string) (authgo.TOTPSecret, error) {
	ct, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return authgo.TOTPSecret{}, fmt.Errorf("identity: sealed TOTP secret: %w", err)
	}
	pt, err := c.Decrypt(ct)
	if err != nil {
		return authgo.TOTPSecret{}, fmt.Errorf("identity: unseal TOTP secret: %w", err)
	}
	return authgo.TOTPSecretFromString(string(pt))
}

// PasskeyRepo implements authgo.PasskeyRepository.
type PasskeyRepo struct{ uow *db.UnitOfWork }

// NewPasskeyRepo returns a passkey repository.
func NewPasskeyRepo(uow *db.UnitOfWork) *PasskeyRepo { return &PasskeyRepo{uow: uow} }

var _ authgo.PasskeyRepository = (*PasskeyRepo)(nil)

// Add stores a credential.
func (r *PasskeyRepo) Add(ctx context.Context, c authgo.PasskeyCredential) error {
	person, err := personID(c.UserID)
	if err != nil {
		return err
	}
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		return (&systemStore{q: q}).AddPasskey(ctx, person, c, time.Now().UTC())
	})
}

// ListByUser loads a person's credentials.
func (r *PasskeyRepo) ListByUser(ctx context.Context, u authgo.UserID) ([]authgo.PasskeyCredential, error) {
	person, err := personID(u)
	if err != nil {
		return nil, err
	}
	var out []authgo.PasskeyCredential
	err = system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		rows, err := q.ListPasskeysOfPerson(ctx, person.UUID())
		for _, row := range rows {
			out = append(out, authgo.PasskeyCredential{
				ID: row.CredentialID, UserID: u, PublicKey: row.PublicKey,
				SignCount: uint32(row.SignCount), Name: row.Name, //nolint:gosec // CHECKed to uint32 range
			})
		}
		return err
	})
	return out, err
}

// UpdateSignCount advances a credential's counter after an assertion.
func (r *PasskeyRepo) UpdateSignCount(ctx context.Context, id []byte, count uint32) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		n, err := q.UpdatePasskeySignCount(ctx, identitysql.UpdatePasskeySignCountParams{
			CredentialID: id, SignCount: int64(count), At: timestamptzNow(),
		})
		if err == nil && n == 0 {
			return authgo.ErrNotFound
		}
		return err
	})
}

// Delete removes a credential.
func (r *PasskeyRepo) Delete(ctx context.Context, id []byte) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		_, err := q.DeletePasskey(ctx, id)
		return err
	})
}

// LoginAttemptRepo implements authgo.LoginAttemptStore and the atomic
// failure recorder.
type LoginAttemptRepo struct{ uow *db.UnitOfWork }

// NewLoginAttemptRepo returns a lockout counter repository.
func NewLoginAttemptRepo(uow *db.UnitOfWork) *LoginAttemptRepo { return &LoginAttemptRepo{uow: uow} }

var (
	_ authgo.LoginAttemptStore       = (*LoginAttemptRepo)(nil)
	_ authgo.AtomicLoginAttemptStore = (*LoginAttemptRepo)(nil)
)

// Get loads a key's counters.
func (r *LoginAttemptRepo) Get(ctx context.Context, key string) (authgo.LoginAttemptSnapshot, error) {
	var out authgo.LoginAttemptSnapshot
	err := system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		row, err := q.GetLoginAttempt(ctx, key)
		if err != nil {
			return authError(err)
		}
		out = authgo.LoginAttemptSnapshot{Key: row.Key, FailureCount: int(row.FailureCount)}
		if row.LockedUntil.Valid {
			out.LockedUntil = row.LockedUntil.Time
		}
		return nil
	})
	return out, err
}

// Save stores a key's counters.
func (r *LoginAttemptRepo) Save(ctx context.Context, s authgo.LoginAttemptSnapshot) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		var until *time.Time
		if !s.LockedUntil.IsZero() {
			until = &s.LockedUntil
		}
		return q.SaveLoginAttempt(ctx, identitysql.SaveLoginAttemptParams{
			Key: s.Key, FailureCount: int32(s.FailureCount), LockedUntil: timestamptz(until), //nolint:gosec // small counts
		})
	})
}

// Delete clears a key's counters.
func (r *LoginAttemptRepo) Delete(ctx context.Context, key string) error {
	return system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		return q.DeleteLoginAttempt(ctx, key)
	})
}

// RecordFailureAtomically counts a failure and applies the lock in one
// statement.
func (r *LoginAttemptRepo) RecordFailureAtomically(ctx context.Context, key string, now time.Time, maxFailures int, window time.Duration) (authgo.LoginAttemptSnapshot, bool, error) {
	var out authgo.LoginAttemptSnapshot
	var justLocked bool
	err := system(ctx, r.uow, func(ctx context.Context, q *identitysql.Queries) error {
		row, err := q.RecordLoginFailure(ctx, identitysql.RecordLoginFailureParams{
			Key: key, MaxFailures: int32(maxFailures), Now: now, LockSeconds: window.Seconds(), //nolint:gosec // small counts
		})
		if err != nil {
			return err
		}
		out = authgo.LoginAttemptSnapshot{Key: key, FailureCount: int(row.FailureCount)}
		if row.LockedUntil.Valid {
			out.LockedUntil = row.LockedUntil.Time
		}
		justLocked = int(row.FailureCount) == maxFailures
		return nil
	})
	return out, justLocked, err
}
