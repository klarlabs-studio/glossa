package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

const minPasswordLen = 12

// SignedIn is a new session. SessionToken is the raw cookie value; it is
// never stored and can't be recovered later.
type SignedIn struct {
	Person       PersonRecord
	SessionToken string
	ExpiresAt    time.Time
}

// RequestSignInLink emails a magic link. It says nothing about whether
// the address has an account: redeeming the link creates one.
func (s *Service) RequestSignInLink(ctx context.Context, email string) error {
	if !s.EmailEnabled() {
		return ErrEmailDisabled
	}
	e, err := parseEmail(email)
	if err != nil {
		return err
	}
	return s.sendSignInLink(ctx, e, "Sign in to Glossa",
		"Use this link to sign in to Glossa. It works once, for 15 minutes:")
}

func (s *Service) sendSignInLink(ctx context.Context, e authgo.Email, subject, intro string) error {
	raw, err := s.signInLinks.Issue(ctx, e, realm)
	if err != nil {
		return fmt.Errorf("identity: issue sign-in link: %w", err)
	}
	return s.mail(ctx, e, subject, intro, "/auth/sign-in#token="+raw.String())
}

func (s *Service) mail(ctx context.Context, to authgo.Email, subject, intro, path string) error {
	msg := Message{
		To:      to.String(),
		Subject: subject,
		Text: intro + "\n\n" + s.cfg.LinkBaseURL + path +
			"\n\nIf you didn't ask for this, you can ignore this email.\n",
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("identity: send %q: %w", subject, err)
	}
	return nil
}

// RedeemSignInLink spends a magic link and signs its recipient in,
// registering them first if the address is new. Either way the address
// is now verified.
func (s *Service) RedeemSignInLink(ctx context.Context, token string) (SignedIn, error) {
	if !s.EmailEnabled() {
		return SignedIn{}, ErrEmailDisabled
	}
	raw, err := authgo.TokenFromString(token)
	if err != nil {
		return SignedIn{}, ErrLinkInvalid
	}
	link, err := s.signInLinks.Consume(ctx, raw)
	if err != nil {
		return SignedIn{}, linkError(err)
	}
	person, err := s.personForVerifiedEmail(ctx, link.Email())
	if errors.Is(err, ErrEmailTaken) { // registered concurrently; it exists now
		person, err = s.personForVerifiedEmail(ctx, link.Email())
	}
	if err != nil {
		return SignedIn{}, err
	}
	return s.completeSignIn(ctx, person, MethodMagicLink)
}

func linkError(err error) error {
	if errors.Is(err, authgo.ErrNotFound) || errors.Is(err, authgo.ErrExpired) || errors.Is(err, authgo.ErrConsumed) {
		return ErrLinkInvalid
	}
	return fmt.Errorf("identity: redeem link: %w", err)
}

// personForVerifiedEmail finds or registers the person who just proved
// they own email, and marks the address verified.
func (s *Service) personForVerifiedEmail(ctx context.Context, email authgo.Email) (PersonRecord, error) {
	now := s.now()
	var rec PersonRecord
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		rec, err = st.PersonByEmail(ctx, email)
		if errors.Is(err, ErrNotFound) {
			p, perr := domain.NewPerson(email, "", now)
			if perr != nil {
				return perr
			}
			p.EmailVerifiedAt = &now
			if err := st.CreatePerson(ctx, p, nil); err != nil {
				return err
			}
			rec = PersonRecord{Person: p}
			return nil
		}
		if err != nil {
			return err
		}
		if !rec.EmailVerified() {
			if err := st.MarkEmailVerified(ctx, rec.ID, now); err != nil {
				return err
			}
			rec.EmailVerifiedAt = &now
		}
		return nil
	})
	return rec, err
}

// Register creates an account with a password and emails a verification
// link. An address that already has an account gets a sign-in link
// instead, so the response is the same either way.
//
// Without email the account is created unverified and usable at once
// with its password; an address that already has an account gets
// nothing, and the response is still the same.
func (s *Service) Register(ctx context.Context, email, password, displayName string) error {
	e, err := parseEmail(email)
	if err != nil {
		return err
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	p, err := domain.NewPerson(e, displayName, s.now())
	if err != nil {
		return err
	}
	hash, err := authgo.HashPassword(password, s.cfg.Argon2)
	if err != nil {
		return fmt.Errorf("identity: hash password: %w", err)
	}
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.CreatePerson(ctx, p, &hash)
	})
	if errors.Is(err, ErrEmailTaken) {
		if !s.EmailEnabled() {
			return nil
		}
		return s.sendSignInLink(ctx, e, "Sign in to Glossa",
			"Someone tried to register with this address, which already has a Glossa account. "+
				"If it was you, sign in with this link (it works once, for 15 minutes):")
	}
	if err != nil {
		return err
	}
	if err := s.ensureIndividualTenant(ctx, PersonRecord{Person: p}); err != nil {
		return err
	}
	if !s.EmailEnabled() {
		return nil
	}
	return s.sendSignInLink(ctx, e, "Verify your email for Glossa",
		"Welcome to Glossa. Verify your address and sign in with this link (it works once, for 15 minutes):")
}

func checkPassword(pw string) error {
	if utf8.RuneCountInString(pw) < minPasswordLen || len(pw) > 1024 {
		return ErrWeakPassword
	}
	return nil
}

// SignInWithPassword checks email + password (+ TOTP when enrolled)
// under auth-go's lockout policy. The address must be verified, unless
// the deployment sends no email and so can't verify any.
func (s *Service) SignInWithPassword(ctx context.Context, email, password, totpCode string) (SignedIn, error) {
	e, err := parseEmail(email)
	if err != nil {
		return SignedIn{}, ErrInvalidCredentials
	}
	key := authgo.LockoutKeyFromEmail(e)
	if err := s.lockout.Guard(ctx, key); err != nil {
		if errors.Is(err, authgo.ErrAccountLocked) {
			s.auditLockedSignIn(ctx, e, MethodPassword)
			return SignedIn{}, ErrAccountLocked
		}
		return SignedIn{}, err
	}
	var rec PersonRecord
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		rec, err = st.PersonByEmail(ctx, e)
		return err
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return SignedIn{}, err
	}
	if err := s.checkPassword(rec, password); err != nil {
		s.auditSignInFailure(ctx, rec.ID, MethodPassword, FailureInvalidCredentials)
		return SignedIn{}, s.fail(ctx, key, err)
	}
	if !rec.EmailVerified() && s.EmailEnabled() {
		s.auditSignInFailure(ctx, rec.ID, MethodPassword, FailureEmailUnverified)
		return SignedIn{}, ErrEmailUnverified
	}
	if rec.TOTPEnabled {
		if totpCode == "" {
			return SignedIn{}, ErrTOTPRequired // the first of two steps, not a failure
		}
		if err := s.totp.Verify(ctx, userID(rec.ID), totpCode); err != nil {
			s.auditSignInFailure(ctx, rec.ID, MethodPassword, FailureTOTPInvalid)
			return SignedIn{}, s.fail(ctx, key, totpError(err))
		}
	}
	if err := s.lockout.Clear(ctx, key); err != nil {
		return SignedIn{}, err
	}
	return s.completeSignIn(ctx, rec, MethodPassword)
}

// checkPassword verifies against the decoy when there is no account or
// no password, so timing doesn't reveal which.
func (s *Service) checkPassword(rec PersonRecord, password string) error {
	hash := s.decoy
	if rec.PasswordHash != nil {
		hash = *rec.PasswordHash
	}
	err := hash.Verify(password)
	if rec.PasswordHash == nil || err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

func totpError(err error) error {
	if errors.Is(err, authgo.ErrInvalidTOTP) || errors.Is(err, authgo.ErrTOTPReused) || errors.Is(err, authgo.ErrNotFound) {
		return ErrTOTPInvalid
	}
	return err
}

// fail records a failed attempt and returns cause.
func (s *Service) fail(ctx context.Context, key string, cause error) error {
	if _, err := s.lockout.RecordFailure(ctx, key); err != nil {
		return err
	}
	return cause
}

// RequestPasswordReset emails a reset link if the address has an account.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if !s.EmailEnabled() {
		return ErrEmailDisabled
	}
	e, err := parseEmail(email)
	if err != nil {
		return err
	}
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		_, err := st.PersonByEmail(ctx, e)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := s.resetLinks.Issue(ctx, e, realm)
	if err != nil {
		return fmt.Errorf("identity: issue reset link: %w", err)
	}
	return s.mail(ctx, e, "Reset your Glossa password",
		"Use this link to choose a new password. It works once, for 30 minutes:",
		"/auth/reset-password#token="+raw.String())
}

// ResetPassword spends a reset link, sets the password, verifies the
// address (the link proved ownership) and signs the person out
// everywhere.
func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	if !s.EmailEnabled() {
		return ErrEmailDisabled
	}
	if err := checkPassword(password); err != nil {
		return err // before spending the link, so a retry can succeed
	}
	raw, err := authgo.TokenFromString(token)
	if err != nil {
		return ErrLinkInvalid
	}
	hash, err := authgo.HashPassword(password, s.cfg.Argon2)
	if err != nil {
		return fmt.Errorf("identity: hash password: %w", err)
	}
	link, err := s.resetLinks.Consume(ctx, raw)
	if err != nil {
		return linkError(err)
	}
	now := s.now()
	var person domain.PersonID
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		rec, err := st.PersonByEmail(ctx, link.Email())
		if errors.Is(err, ErrNotFound) {
			return ErrLinkInvalid
		}
		if err != nil {
			return err
		}
		person = rec.ID
		if err := st.SetPassword(ctx, rec.ID, hash, now); err != nil {
			return err
		}
		return st.MarkEmailVerified(ctx, rec.ID, now)
	})
	if err != nil {
		return err
	}
	if err := s.sessions.RevokeAll(ctx, userID(person)); err != nil {
		return fmt.Errorf("identity: revoke sessions: %w", err)
	}
	return s.lockout.Clear(ctx, authgo.LockoutKeyFromEmail(link.Email()))
}

// SignOut revokes one session by its raw cookie value.
func (s *Service) SignOut(ctx context.Context, sessionToken string) error {
	return s.signOutWith(ctx, s.sessions, sessionToken)
}

func (s *Service) signOutWith(ctx context.Context, sessions *authgo.SessionService, raw string) error {
	tok, err := authgo.TokenFromString(raw)
	if err != nil {
		return ErrUnauthenticated
	}
	return sessions.Revoke(ctx, tok)
}

// SignOutEverywhere revokes all of a person's sessions, the sessions of
// the devices they signed in included (RFC 0006 §7.2), and withdraws
// any device they approved that has not yet taken its session, so no
// device signs in after the person signed out everywhere.
func (s *Service) SignOutEverywhere(ctx context.Context, person domain.PersonID) error {
	// Approvals first: a device redeeming meanwhile either took its
	// session before this (and RevokeAll ends it) or finds its
	// approval withdrawn.
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.WithdrawDeviceApprovals(ctx, person)
	})
	if err != nil {
		return err
	}
	return s.sessions.RevokeAll(ctx, userID(person))
}

// completeSignIn runs after any successful authentication of a person:
// it makes sure their individual tenant exists, accepts their open
// invitations, issues the session, and records the sign-in in the audit
// trail of each tenant it opens.
func (s *Service) completeSignIn(ctx context.Context, rec PersonRecord, method string) (SignedIn, error) {
	if err := s.ensureIndividualTenant(ctx, rec); err != nil {
		return SignedIn{}, err
	}
	s.acceptInvitations(ctx, rec)
	sess, err := s.sessions.Issue(ctx, userID(rec.ID), realm)
	if err != nil {
		return SignedIn{}, fmt.Errorf("identity: issue session: %w", err)
	}
	s.auditSignIn(ctx, rec.ID, method)
	return SignedIn{Person: rec, SessionToken: sess.Token().String(), ExpiresAt: sess.ExpiresAt()}, nil
}

// ensureIndividualTenant creates the person's own tenant and their owner
// membership, in one tenant-scoped transaction, unless it exists. The
// person row is written first in its own (system) transaction, so this
// runs on every sign-in to heal a registration interrupted in between.
func (s *Service) ensureIndividualTenant(ctx context.Context, rec PersonRecord) error {
	tctx := tenancy.ContextWithTenant(ctx, rec.IndividualTenantID)
	const attempts = 3 // the slug has a random suffix; retry the rare collision
	for range attempts {
		err := s.tx.InTenant(tctx, func(ctx context.Context, st TenantStore) error {
			if _, err := st.CurrentTenant(ctx); err == nil {
				return nil
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			t, err := rec.IndividualTenant()
			if err != nil {
				return err
			}
			owner := domain.NewOwner(t.ID, rec.ID, rec.Email, s.now())
			return s.createTenantWithOwner(ctx, st, t, owner)
		})
		if !errors.Is(err, ErrSlugTaken) {
			return err
		}
	}
	return fmt.Errorf("identity: no free slug for %s's individual tenant", rec.ID)
}

// createTenantWithOwner stores a new tenant, its first owner and the two
// events, in the caller's tenant transaction.
func (s *Service) createTenantWithOwner(ctx context.Context, st TenantStore, t tenancy.Tenant, owner domain.Member) error {
	t, err := st.CreateTenant(ctx, t)
	if err != nil {
		return err
	}
	by := domain.PersonActor(owner.PersonID)
	if _, err := st.InsertMember(ctx, owner, by); err != nil {
		return err
	}
	if err := st.Publish(ctx, outbox.Event{
		Type: domain.EventTenantCreated, AggregateType: domain.AggregateTenant, AggregateID: t.ID.String(),
		Actor: outbox.Actor(by.String()),
		Payload: domain.TenantCreated{
			TenantID: t.ID.String(), Kind: string(t.Kind), Slug: string(t.Slug), Name: t.Name, CreatedBy: by.String(),
		},
	}); err != nil {
		return err
	}
	return st.Publish(ctx, memberAdded(owner, by))
}

func memberAdded(m domain.Member, by domain.Actor) outbox.Event {
	e := domain.MemberAdded{
		MemberID: m.ID.String(), Email: m.Email.String(), Status: string(m.Status),
		Roles: m.Roles.Strings(), Locales: m.Locales.Strings(), AddedBy: by.String(),
	}
	if !m.PersonID.IsZero() {
		e.PersonID = m.PersonID.String()
	}
	if r := m.Restriction; !r.Projects.All() || !r.Vendor.IsZero() || r.Visibility == domain.VisibilityAssigned {
		e.Projects, e.Visibility = r.Projects.Strings(), string(r.Visibility)
		if !r.Vendor.IsZero() {
			e.VendorID = r.Vendor.String()
		}
	}
	return outbox.Event{
		Type: domain.EventMemberAdded, AggregateType: domain.AggregateMember, AggregateID: m.ID.String(),
		Actor: outbox.Actor(by.String()), Payload: e,
	}
}

// acceptInvitations turns open invitations to the person's verified
// address into active memberships, one tenant transaction each. A
// failure is logged, not returned: it must not block signing in, and the
// next sign-in (or GET /v1/me) retries.
func (s *Service) acceptInvitations(ctx context.Context, rec PersonRecord) {
	if !rec.EmailVerified() {
		return
	}
	var refs []InvitationRef
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		refs, err = st.OpenInvitations(ctx, rec.Email)
		return err
	})
	if err != nil {
		s.logger.WarnContext(ctx, "list open invitations", slog.Any("error", err))
		return
	}
	for _, ref := range refs {
		if err := s.acceptInvitation(ctx, rec, ref); err != nil {
			s.logger.WarnContext(ctx, "accept invitation",
				slog.String("member_id", ref.Member.String()), slog.Any("error", err))
		}
	}
}

func (s *Service) acceptInvitation(ctx context.Context, rec PersonRecord, ref InvitationRef) error {
	tctx := tenancy.ContextWithTenant(ctx, ref.Tenant)
	return s.tx.InTenant(tctx, func(ctx context.Context, st TenantStore) error {
		m, err := st.LockMember(ctx, ref.Member)
		if errors.Is(err, ErrNotFound) {
			return nil // withdrawn meanwhile
		}
		if err != nil {
			return err
		}
		if err := m.Activate(rec.ID, s.now()); err != nil {
			return nil //nolint:nilerr // accepted meanwhile
		}
		if err := st.ActivateMember(ctx, m); err != nil {
			return err
		}
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventMemberActivated, AggregateType: domain.AggregateMember, AggregateID: m.ID.String(),
			Actor:   outbox.Actor(domain.PersonActor(rec.ID).String()),
			Payload: domain.MemberActivated{MemberID: m.ID.String(), PersonID: rec.ID.String()},
		})
	})
}
