package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// SignInAudit is where sign-in attempts are recorded: the Audit
// context's Recorder, behind Identity's own port (RFC 0006 §6.1, "not
// only events"). A sign-in is not a domain event — it changes no
// aggregate — so it never reaches the outbox, and is written here
// instead. Each call records one attempt in the tenant on ctx.
type SignInAudit interface {
	RecordSignIn(ctx context.Context, a SignInAttempt) error
}

// SignInAttempt is what is recorded about one attempt: who, how, and
// whether it worked. Never the email, the password, a code, a session
// or a link.
type SignInAttempt struct {
	// ID identifies the attempt. It is the same in every tenant the
	// attempt is recorded in.
	ID      uuid.UUID
	Person  domain.PersonID
	Method  string
	Failure string // "" for a success; one of the Failure* values otherwise
	At      time.Time
}

// Why a sign-in by a known person failed, as the audit trail records it.
const (
	FailureInvalidCredentials = "invalid_credentials"
	FailureTOTPInvalid        = "totp_invalid"
	FailureLocked             = "locked"
	FailureEmailUnverified    = "email_unverified"
)

// auditTenantsLimit bounds how many of a person's tenants one attempt is
// recorded in; it matches what GET /v1/me lists.
const auditTenantsLimit = maxMemberships

// auditFailureTimeout bounds the background write of a failed attempt.
const auditFailureTimeout = 10 * time.Second

// auditSignIn records a successful sign-in in each tenant the person
// belongs to: a sign-in opens every one of them, so each tenant's trail
// says so. A write that fails is logged at error level and does not fail
// the sign-in — a gap in the trail is loud, never silent.
func (s *Service) auditSignIn(ctx context.Context, person domain.PersonID, method string) {
	if s.audit == nil {
		return
	}
	s.recordAttempt(ctx, SignInAttempt{ID: uuid.Must(uuid.NewV7()), Person: person, Method: method, At: s.now()})
}

// auditSignInFailure records a failed attempt on a known person's
// account, in the background: a failure that wrote synchronously would
// take longer only when the account exists, which is the very thing the
// decoy hash keeps a caller from learning.
func (s *Service) auditSignInFailure(ctx context.Context, person domain.PersonID, method, failure string) {
	if person.IsZero() {
		return
	}
	s.auditFailure(ctx, method, failure, func(context.Context) (domain.PersonID, error) { return person, nil })
}

// auditLockedSignIn records an attempt refused because the address is
// locked out. Who owns the address is looked up in the background too.
func (s *Service) auditLockedSignIn(ctx context.Context, email authgo.Email, method string) {
	s.auditFailure(ctx, method, FailureLocked, func(ctx context.Context) (domain.PersonID, error) {
		var rec PersonRecord
		err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
			var err error
			rec, err = st.PersonByEmail(ctx, email)
			return err
		})
		if errors.Is(err, ErrNotFound) {
			return domain.PersonID{}, nil
		}
		return rec.ID, err
	})
}

func (s *Service) auditFailure(
	ctx context.Context, method, failure string, who func(context.Context) (domain.PersonID, error),
) {
	if s.audit == nil {
		return
	}
	id, at := uuid.Must(uuid.NewV7()), s.now()
	s.auditing.Add(1)
	go func() {
		defer s.auditing.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditFailureTimeout)
		defer cancel()
		person, err := who(ctx)
		if err != nil {
			s.logger.ErrorContext(ctx, "identity: the failed sign-in was not audited", slog.Any("error", err))
			return
		}
		if person.IsZero() {
			return // no account: there is no tenant to record it in
		}
		s.recordAttempt(ctx, SignInAttempt{ID: id, Person: person, Method: method, Failure: failure, At: at})
	}()
}

// WaitAudits waits for background audit writes to finish (shutdown and
// tests).
func (s *Service) WaitAudits() { s.auditing.Wait() }

func (s *Service) recordAttempt(ctx context.Context, a SignInAttempt) {
	var ms []MembershipView
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		ms, err = st.MembershipsOf(ctx, a.Person, tenancy.ID{}, auditTenantsLimit)
		return err
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "identity: the sign-in was not audited: listing the person's tenants failed",
			slog.String("person_id", a.Person.String()), slog.Any("error", err))
		return
	}
	for _, m := range ms {
		if err := s.audit.RecordSignIn(tenancy.ContextWithTenant(ctx, m.Tenant.ID), a); err != nil {
			s.logger.ErrorContext(ctx, "identity: the sign-in was not audited",
				slog.String("person_id", a.Person.String()), slog.String("tenant_id", m.Tenant.ID.String()),
				slog.Any("error", err))
		}
	}
}
