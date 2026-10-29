package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Device sign-in (RFC 0006 §7.2; the OAuth 2.0 device authorization
// grant, RFC 8628): how a person signs the CLI in as themselves, which
// `glossa approve` and `glossa import --history` need and an API token
// can never be.
//
// The device asks for a device code and a user code; the person, signed
// in to Studio in a browser, looks the user code up and approves or
// denies it; the device polls with its device code and, once approved,
// receives the person's own session as a `glossa_dev_` bearer — one row
// of the same sessions table, kept apart from browser sessions by kind,
// so "sign out everywhere" ends it and it acts with exactly the
// person's memberships, scope and visibility.

// Device-flow errors. Each is a problem code of the same name
// (api/openapi.yaml, /v1/auth/device-sessions), so the CLI branches on
// RFC 8628 §3.5's vocabulary unchanged.
var (
	// ErrDeviceSignInUnavailable means this server does not run the
	// device flow.
	ErrDeviceSignInUnavailable = errors.New("identity: device sign-in is not available on this server")
	// ErrDeviceAuthorizationNotFound covers a user code that is unknown,
	// expired or already decided, without saying which.
	ErrDeviceAuthorizationNotFound = errors.New("identity: no pending device authorization with this code")
	ErrAuthorizationPending        = domain.ErrAuthorizationPending
	ErrSlowDown                    = domain.ErrSlowDown
	ErrAccessDenied                = domain.ErrAccessDenied
	ErrExpiredToken                = domain.ErrExpiredToken
	ErrInvalidClientName           = domain.ErrInvalidClientName
	// ErrDeviceRateLimited means a client address started, or a person
	// looked up or decided, too many device sign-ins too fast.
	ErrDeviceRateLimited = errors.New("identity: too many device sign-in attempts; slow down")
)

// DeviceAuthorization is what the device shows and polls with.
type DeviceAuthorization struct {
	// DeviceCode is the device's polling secret, never shown.
	DeviceCode string
	// UserCode is shown to the person as XXXX-XXXX.
	UserCode string
	// VerificationURI is Studio's page for entering a code,
	// LinkBaseURL + "/device"; VerificationURIComplete adds
	// "?code=" + UserCode.
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
}

// DeviceAuthorizationView is what Studio shows before the person decides.
type DeviceAuthorizationView struct {
	UserCode    string
	ClientName  string
	RequestedAt time.Time
	ExpiresAt   time.Time
}

// DeviceSession is the bearer an approved device receives: the person's
// session, presented as `Authorization: Bearer glossa_dev_…`.
type DeviceSession struct {
	AccessToken string
	ExpiresAt   time.Time
	Person      domain.PersonID
}

// Limiter is a token bucket per key (the kernel's ratelimit.Limiter).
type Limiter interface {
	Allow(ctx context.Context, key string) bool
}

type clientAddressKey struct{}

// WithClientAddress records the address a request came from, which the
// HTTP edge establishes (behind a trusted proxy, from its
// X-Forwarded-For). Starting a device sign-in is limited per address.
func WithClientAddress(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, clientAddressKey{}, addr)
}

func clientAddress(ctx context.Context) string {
	if a, _ := ctx.Value(clientAddressKey{}).(string); a != "" {
		return a
	}
	return "unknown"
}

// DeviceSignInEnabled reports whether this server runs the device flow.
func (s *Service) DeviceSignInEnabled() bool { return s.deviceSessions != nil }

// deviceRetention is how long an expired authorization is kept before
// a later start purges it.
const deviceRetention = time.Hour

// userCodeAttempts bounds the draws of a fresh user code; a collision
// with a pending code is a one-in-billions event.
const userCodeAttempts = 3

func (s *Service) allowDevice(ctx context.Context, key string) error {
	if s.deviceLimits != nil && !s.deviceLimits.Allow(ctx, "device:"+key) {
		return ErrDeviceRateLimited
	}
	return nil
}

// StartDeviceAuthorization issues a device code and a user code for a
// device that names itself.
func (s *Service) StartDeviceAuthorization(ctx context.Context, clientName string) (DeviceAuthorization, error) {
	if !s.DeviceSignInEnabled() {
		return DeviceAuthorization{}, ErrDeviceSignInUnavailable
	}
	if err := s.allowDevice(ctx, "start:"+clientAddress(ctx)); err != nil {
		return DeviceAuthorization{}, err
	}
	for range userCodeAttempts {
		now := s.now()
		d, dc, uc, err := domain.NewDeviceAuthorization(clientName, now)
		if err != nil {
			return DeviceAuthorization{}, err
		}
		err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
			if _, err := st.PurgeDeviceAuthorizations(ctx, now.Add(-deviceRetention)); err != nil {
				return err
			}
			return st.InsertDeviceAuthorization(ctx, d)
		})
		if errors.Is(err, ErrUserCodeTaken) {
			continue
		}
		if err != nil {
			return DeviceAuthorization{}, err
		}
		uri := s.cfg.LinkBaseURL + "/device"
		return DeviceAuthorization{
			DeviceCode: dc.String(), UserCode: uc.String(),
			VerificationURI: uri, VerificationURIComplete: uri + "?code=" + uc.String(),
			ExpiresIn: d.ExpiresAt.Sub(d.RequestedAt), Interval: d.Interval,
		}, nil
	}
	return DeviceAuthorization{}, fmt.Errorf("identity: no free user code after %d draws", userCodeAttempts)
}

// DeviceAuthorizationFor looks a pending user code up for the signed-in
// person who is about to decide it.
func (s *Service) DeviceAuthorizationFor(ctx context.Context, person domain.PersonID, userCode string) (DeviceAuthorizationView, error) {
	if !s.DeviceSignInEnabled() {
		return DeviceAuthorizationView{}, ErrDeviceSignInUnavailable
	}
	if err := s.allowDevice(ctx, "person:"+person.String()); err != nil {
		return DeviceAuthorizationView{}, err
	}
	uc, err := domain.ParseUserCode(userCode)
	if err != nil {
		return DeviceAuthorizationView{}, ErrDeviceAuthorizationNotFound
	}
	var d domain.DeviceAuthorization
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		d, err = st.LockPendingDeviceAuthorization(ctx, uc.Hash(), s.now())
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return DeviceAuthorizationView{}, ErrDeviceAuthorizationNotFound
	}
	if err != nil {
		return DeviceAuthorizationView{}, err
	}
	return DeviceAuthorizationView{
		UserCode: uc.String(), ClientName: d.ClientName, RequestedAt: d.RequestedAt, ExpiresAt: d.ExpiresAt,
	}, nil
}

// DecideDeviceAuthorization approves (signing the device in as person)
// or denies a pending user code.
func (s *Service) DecideDeviceAuthorization(ctx context.Context, person domain.PersonID, userCode string, approve bool) error {
	if !s.DeviceSignInEnabled() {
		return ErrDeviceSignInUnavailable
	}
	if err := s.allowDevice(ctx, "person:"+person.String()); err != nil {
		return err
	}
	uc, err := domain.ParseUserCode(userCode)
	if err != nil {
		return ErrDeviceAuthorizationNotFound
	}
	var d domain.DeviceAuthorization
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		now := s.now()
		var err error
		if d, err = st.LockPendingDeviceAuthorization(ctx, uc.Hash(), now); err != nil {
			return err
		}
		if err := d.Decide(person, approve, now); err != nil {
			return err
		}
		return st.UpdateDeviceAuthorization(ctx, d)
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, domain.ErrDeviceNotPending) {
		return ErrDeviceAuthorizationNotFound
	}
	if err != nil {
		return err
	}
	event := domain.EventDeviceDenied
	if approve {
		event = domain.EventDeviceApproved
	}
	s.publishDeviceEvent(ctx, d, event)
	return nil
}

// RedeemDeviceAuthorization is the device's poll: ErrAuthorizationPending,
// ErrSlowDown, ErrAccessDenied or ErrExpiredToken until the code is
// approved, then the session, once.
//
// The session is issued before the code is spent, and the code is
// spent under its row lock: of two polls racing on an approved code
// exactly one spends it, and the other's session is revoked before
// anyone has seen it. A code is never spent without its session
// existing.
func (s *Service) RedeemDeviceAuthorization(ctx context.Context, deviceCode string) (DeviceSession, error) {
	if !s.DeviceSignInEnabled() {
		return DeviceSession{}, ErrDeviceSignInUnavailable
	}
	code, err := domain.ParseDeviceCode(deviceCode)
	if err != nil {
		return DeviceSession{}, ErrExpiredToken
	}
	var d domain.DeviceAuthorization
	var outcome error
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		if d, err = st.LockDeviceAuthorization(ctx, code.Hash()); err != nil {
			return err
		}
		// The poll is recorded whatever it gets: slow_down's longer
		// interval and the time of this poll both count for the next.
		outcome = d.Poll(s.now())
		return st.UpdateDeviceAuthorization(ctx, d)
	})
	if errors.Is(err, ErrNotFound) {
		return DeviceSession{}, ErrExpiredToken
	}
	if err != nil {
		return DeviceSession{}, err
	}
	if outcome != nil {
		return DeviceSession{}, outcome
	}
	sess, err := s.deviceSessions.Issue(ctx, userID(d.Person), realm)
	if err != nil {
		return DeviceSession{}, fmt.Errorf("identity: issue device session: %w", err)
	}
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		if d, err = st.LockDeviceAuthorization(ctx, code.Hash()); err != nil {
			return err
		}
		if err := d.Redeem(s.now()); err != nil {
			return err
		}
		return st.UpdateDeviceAuthorization(ctx, d)
	})
	if err != nil {
		if rerr := s.deviceSessions.Revoke(ctx, sess.Token()); rerr != nil {
			s.logger.ErrorContext(ctx, "identity: an unspent device session was not revoked", slog.Any("error", rerr))
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, domain.ErrExpiredToken) {
			return DeviceSession{}, ErrExpiredToken
		}
		return DeviceSession{}, err
	}
	s.publishDeviceEvent(ctx, d, domain.EventDeviceRedeemed)
	return DeviceSession{
		AccessToken: domain.DeviceSessionBearer(sess.Token().String()), ExpiresAt: sess.ExpiresAt(), Person: d.Person,
	}, nil
}

// AuthenticateDeviceSession resolves a glossa_dev_ bearer to its
// person. Only a device session answers it: a browser's session cookie
// presented as a bearer is unknown here, as a device bearer presented as
// the cookie is unknown to AuthenticateSession.
func (s *Service) AuthenticateDeviceSession(ctx context.Context, bearer string) (Authn, error) {
	if !s.DeviceSignInEnabled() {
		return Authn{}, ErrUnauthenticated
	}
	raw, ok := domain.DeviceSessionToken(bearer)
	if !ok {
		return Authn{}, ErrUnauthenticated
	}
	return s.authenticateSessionWith(ctx, s.deviceSessions, raw)
}

// SignOutDevice ends the device session behind a glossa_dev_ bearer
// (`glossa logout`).
func (s *Service) SignOutDevice(ctx context.Context, bearer string) error {
	raw, ok := domain.DeviceSessionToken(bearer)
	if !ok || !s.DeviceSignInEnabled() {
		return ErrUnauthenticated
	}
	return s.signOutWith(ctx, s.deviceSessions, raw)
}

// publishDeviceEvent records a decision or a redemption in every tenant
// the person belongs to: the device acts in each of them as the person,
// so each tenant's trail says so. The authorization is the person's,
// not a tenant's, so the change itself committed in system scope
// before this; a publish that fails is logged at error level and does
// not undo it — a gap in a trail is loud, never silent, as with
// sign-ins (audit.go).
func (s *Service) publishDeviceEvent(ctx context.Context, d domain.DeviceAuthorization, eventType string) {
	var ms []MembershipView
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		ms, err = st.MembershipsOf(ctx, d.Person, tenancy.ID{}, auditTenantsLimit)
		return err
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "identity: the device sign-in was not published: listing the person's tenants failed",
			slog.String("authorization_id", d.ID.String()), slog.Any("error", err))
		return
	}
	by := domain.PersonActor(d.Person).String()
	for _, m := range ms {
		err := s.tx.InTenant(tenancy.ContextWithTenant(ctx, m.Tenant.ID), func(ctx context.Context, st TenantStore) error {
			return st.Publish(ctx, outbox.Event{
				Type: eventType, AggregateType: domain.AggregateDeviceAuthorization, AggregateID: d.ID.String(),
				Actor: outbox.Actor(by),
				Payload: domain.DeviceAuthorizationChanged{
					AuthorizationID: d.ID.String(), PersonID: d.Person.String(), Status: string(d.Status),
					ClientName: d.ClientName, By: by,
				},
			})
		})
		if err != nil {
			s.logger.ErrorContext(ctx, "identity: the device sign-in was not published",
				slog.String("authorization_id", d.ID.String()), slog.String("tenant_id", m.Tenant.ID.String()),
				slog.Any("error", err))
		}
	}
}
