//go:build integration

package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Device sign-in (RFC 0006 §7.2) against real Postgres: the codes are
// stored as hashes only, the poll follows RFC 8628 §3.5, the session the
// device receives is the person's own — of its own kind, ended by
// "sign out everywhere" — and the decisions reach the outbox of every
// tenant the person is in.

func TestDeviceSignInFromStartToSession(t *testing.T) {
	h := newHarness(t)
	ctx := app.WithClientAddress(context.Background(), "198.51.100.7")
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}

	start, err := h.svc.StartDeviceAuthorization(ctx, "glossa CLI on build-01")
	if err != nil {
		t.Fatal(err)
	}
	if start.Interval != 5*time.Second || start.ExpiresIn != 15*time.Minute ||
		start.VerificationURI != "https://studio.test/device" ||
		start.VerificationURIComplete != "https://studio.test/device?code="+start.UserCode {
		t.Errorf("start = %+v", start)
	}
	for _, secret := range []string{start.DeviceCode, start.UserCode, strings.ReplaceAll(start.UserCode, "-", "")} {
		if n := count(t, `SELECT count(*) FROM identity_device_authorizations
			WHERE device_code_hash = $1 OR user_code_hash = $1 OR client_name = $1`, secret); n != 0 {
			t.Errorf("a code is stored as it was handed out: %q", secret)
		}
	}

	// Pending until the person decides.
	if _, err := h.svc.RedeemDeviceAuthorization(ctx, start.DeviceCode); !errors.Is(err, app.ErrAuthorizationPending) {
		t.Fatalf("first poll: %v", err)
	}
	// Polling again at once is too fast.
	if _, err := h.svc.RedeemDeviceAuthorization(ctx, start.DeviceCode); !errors.Is(err, app.ErrSlowDown) {
		t.Fatalf("an immediate second poll: %v", err)
	}
	if n := count(t, `SELECT interval_seconds FROM identity_device_authorizations`); n != 10 {
		t.Errorf("interval after slow_down = %d s, want 10", n)
	}

	// The person, signed in, sees what is asking — the code typed any
	// way — and approves.
	typed := strings.ToLower(strings.ReplaceAll(start.UserCode, "-", ""))
	view, err := h.svc.DeviceAuthorizationFor(ctx, ada.Person.ID, typed)
	if err != nil {
		t.Fatal(err)
	}
	if view.ClientName != "glossa CLI on build-01" || view.UserCode != start.UserCode {
		t.Errorf("view = %+v", view)
	}
	if err := h.svc.DecideDeviceAuthorization(ctx, ada.Person.ID, start.UserCode, true); err != nil {
		t.Fatal(err)
	}
	// A decided code is not found, whoever asks and however.
	if _, err := h.svc.DeviceAuthorizationFor(ctx, ada.Person.ID, start.UserCode); !errors.Is(err, app.ErrDeviceAuthorizationNotFound) {
		t.Errorf("looking up a decided code: %v", err)
	}
	if err := h.svc.DecideDeviceAuthorization(ctx, ada.Person.ID, start.UserCode, false); !errors.Is(err, app.ErrDeviceAuthorizationNotFound) {
		t.Errorf("deciding twice: %v", err)
	}

	h.clock.Advance(11 * time.Second)
	sess, err := h.svc.RedeemDeviceAuthorization(ctx, start.DeviceCode)
	if err != nil {
		t.Fatalf("poll after approval: %v", err)
	}
	if !strings.HasPrefix(sess.AccessToken, "glossa_dev_") || sess.Person != ada.Person.ID {
		t.Fatalf("session = %+v", sess)
	}
	h.clock.Advance(11 * time.Second)
	if _, err := h.svc.RedeemDeviceAuthorization(ctx, start.DeviceCode); !errors.Is(err, app.ErrExpiredToken) {
		t.Errorf("second redemption: %v, want expired_token", err)
	}

	// The bearer is the person's session, of the device kind only.
	a, err := h.svc.AuthenticateDeviceSession(ctx, sess.AccessToken)
	if err != nil || a.Person != ada.Person.ID || a.Token != nil {
		t.Fatalf("device session authenticates as %+v, %v", a, err)
	}
	raw, _ := domain.DeviceSessionToken(sess.AccessToken)
	if _, err := h.svc.AuthenticateSession(ctx, raw); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("the device's session works as a cookie: %v", err)
	}
	if _, err := h.svc.AuthenticateDeviceSession(ctx, domain.DeviceSessionBearer(ada.SessionToken)); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("a browser's session works as a device bearer: %v", err)
	}
	if n := count(t, `SELECT count(*) FROM identity_sessions WHERE kind = 'device' AND person_id = $1`, ada.Person.ID.UUID()); n != 1 {
		t.Errorf("%d device sessions stored", n)
	}
	if n := count(t, `SELECT count(*) FROM identity_sessions WHERE token_hash = $1 OR token_hash = $2`, raw, sess.AccessToken); n != 0 {
		t.Error("the device's raw token is stored")
	}
	// It carries the person's memberships: Acme admits it as Ada.
	if p, err := h.svc.Authorize(ctx, a, acme.ID); err != nil || p.Member.IsZero() {
		t.Errorf("Acme refuses Ada's device: %+v, %v", p, err)
	}

	// The decisions are in the outbox of each of Ada's tenants, with
	// her as the actor.
	for _, tenant := range []tenancy.ID{acme.ID, ada.Person.IndividualTenantID} {
		for _, typ := range []string{domain.EventDeviceApproved, domain.EventDeviceRedeemed} {
			if n := count(t, `SELECT count(*) FROM outbox_events WHERE tenant_id = $1 AND event_type = $2 AND actor = $3`,
				tenant.UUID(), typ, "person:"+ada.Person.ID.String()); n != 1 {
				t.Errorf("%s in %s: %d events", typ, tenant, n)
			}
		}
	}

	// Signing out on the device ends it alone; signing out everywhere
	// ends every device.
	if err := h.svc.SignOutDevice(ctx, sess.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AuthenticateDeviceSession(ctx, sess.AccessToken); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("a signed-out device: %v", err)
	}
	if _, err := h.svc.AuthenticateSession(ctx, ada.SessionToken); err != nil {
		t.Errorf("the device's sign-out ended the browser's session: %v", err)
	}
	second := h.signInDevice(t, ada)
	if err := h.svc.SignOutEverywhere(ctx, ada.Person.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AuthenticateDeviceSession(ctx, second); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("a device after sign-out-everywhere: %v", err)
	}
}

// signInDevice runs the flow for in and returns the device's bearer.
func (h *harness) signInDevice(t *testing.T, in app.SignedIn) string {
	t.Helper()
	ctx := context.Background()
	start, err := h.svc.StartDeviceAuthorization(ctx, "glossa CLI")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DecideDeviceAuthorization(ctx, in.Person.ID, start.UserCode, true); err != nil {
		t.Fatal(err)
	}
	sess, err := h.svc.RedeemDeviceAuthorization(ctx, start.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	return sess.AccessToken
}

func TestDeniedAndExpiredDevices(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ada := h.signUp(t, "ada@example.com")

	denied, err := h.svc.StartDeviceAuthorization(ctx, "not mine")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DecideDeviceAuthorization(ctx, ada.Person.ID, denied.UserCode, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RedeemDeviceAuthorization(ctx, denied.DeviceCode); !errors.Is(err, app.ErrAccessDenied) {
		t.Errorf("polling a denied code: %v", err)
	}
	if n := count(t, `SELECT count(*) FROM outbox_events WHERE event_type = $1`, domain.EventDeviceDenied); n != 1 {
		t.Errorf("%d denial events", n)
	}

	late, err := h.svc.StartDeviceAuthorization(ctx, "too slow")
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(15 * time.Minute)
	if _, err := h.svc.DeviceAuthorizationFor(ctx, ada.Person.ID, late.UserCode); !errors.Is(err, app.ErrDeviceAuthorizationNotFound) {
		t.Errorf("looking up an expired code: %v", err)
	}
	if err := h.svc.DecideDeviceAuthorization(ctx, ada.Person.ID, late.UserCode, true); !errors.Is(err, app.ErrDeviceAuthorizationNotFound) {
		t.Errorf("approving an expired code: %v", err)
	}
	if _, err := h.svc.RedeemDeviceAuthorization(ctx, late.DeviceCode); !errors.Is(err, app.ErrExpiredToken) {
		t.Errorf("polling an expired code: %v", err)
	}
	for _, unknown := range []string{"", "nonsense", strings.Repeat("A", 43)} {
		if _, err := h.svc.RedeemDeviceAuthorization(ctx, unknown); !errors.Is(err, app.ErrExpiredToken) {
			t.Errorf("polling %q: %v", unknown, err)
		}
	}
	for _, code := range []string{"BCDF-GHJK", "AAAA-AAAA", ""} {
		if _, err := h.svc.DeviceAuthorizationFor(ctx, ada.Person.ID, code); !errors.Is(err, app.ErrDeviceAuthorizationNotFound) {
			t.Errorf("looking up %q: %v", code, err)
		}
	}
	if _, err := h.svc.StartDeviceAuthorization(ctx, strings.Repeat("x", 101)); !errors.Is(err, app.ErrInvalidClientName) {
		t.Errorf("a long client name: %v", err)
	}

	// An expired authorization is purged by a later start.
	h.clock.Advance(2 * time.Hour)
	if _, err := h.svc.StartDeviceAuthorization(ctx, "later"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT count(*) FROM identity_device_authorizations`); n != 1 {
		t.Errorf("%d authorizations after the purge, want 1", n)
	}
}

func TestSignOutEverywhereWithdrawsAnApprovalNotYetCollected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ada := h.signUp(t, "ada@example.com")
	start, err := h.svc.StartDeviceAuthorization(ctx, "glossa CLI")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DecideDeviceAuthorization(ctx, ada.Person.ID, start.UserCode, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SignOutEverywhere(ctx, ada.Person.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RedeemDeviceAuthorization(ctx, start.DeviceCode); !errors.Is(err, app.ErrAccessDenied) {
		t.Errorf("collecting an approval after sign-out-everywhere: %v", err)
	}
}

// Two polls racing on an approved code: exactly one gets the session,
// and only one device session exists afterwards.
func TestConcurrentRedemptionsSpendTheCodeOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ada := h.signUp(t, "ada@example.com")
	start, err := h.svc.StartDeviceAuthorization(ctx, "glossa CLI")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DecideDeviceAuthorization(ctx, ada.Person.ID, start.UserCode, true); err != nil {
		t.Fatal(err)
	}
	const n = 6
	var wg sync.WaitGroup
	results := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.RedeemDeviceAuthorization(ctx, start.DeviceCode)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, app.ErrSlowDown), errors.Is(err, app.ErrExpiredToken):
		default:
			t.Errorf("a racing poll: %v", err)
		}
	}
	if won != 1 {
		t.Errorf("%d polls got a session, want 1", won)
	}
	if c := count(t, `SELECT count(*) FROM identity_sessions WHERE kind = 'device'`); c != 1 {
		t.Errorf("%d device sessions exist, want 1", c)
	}
}

type countingLimiter struct {
	mu   sync.Mutex
	left map[string]int
}

func (l *countingLimiter) Allow(_ context.Context, key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.left[key]; !ok {
		l.left[key] = 2
	}
	l.left[key]--
	return l.left[key] >= 0
}

func TestDeviceSignInIsRateLimited(t *testing.T) {
	limits := &countingLimiter{left: map[string]int{}}
	h := newHarnessMailing(t, true, func(d *app.Deps) { d.DeviceLimits = limits })
	ada := h.signUp(t, "ada@example.com")
	bob := h.signUp(t, "bob@example.com")
	here := app.WithClientAddress(context.Background(), "198.51.100.7")
	for i := range 2 {
		if _, err := h.svc.StartDeviceAuthorization(here, "glossa CLI"); err != nil {
			t.Fatalf("start %d: %v", i+1, err)
		}
	}
	if _, err := h.svc.StartDeviceAuthorization(here, "glossa CLI"); !errors.Is(err, app.ErrDeviceRateLimited) {
		t.Errorf("a third start from one address: %v", err)
	}
	if _, err := h.svc.StartDeviceAuthorization(app.WithClientAddress(context.Background(), "203.0.113.1"), "glossa CLI"); err != nil {
		t.Errorf("another address shares the first one's budget: %v", err)
	}
	// Guessing codes: the person's budget runs out, wrong guesses and
	// right ones alike; another person's does not.
	for range 2 {
		if _, err := h.svc.DeviceAuthorizationFor(here, ada.Person.ID, "BCDF-GHJK"); !errors.Is(err, app.ErrDeviceAuthorizationNotFound) {
			t.Fatalf("a wrong guess: %v", err)
		}
	}
	if err := h.svc.DecideDeviceAuthorization(here, ada.Person.ID, "BCDF-GHJK", true); !errors.Is(err, app.ErrDeviceRateLimited) {
		t.Errorf("a third guess: %v", err)
	}
	if _, err := h.svc.DeviceAuthorizationFor(here, bob.Person.ID, "BCDF-GHJK"); !errors.Is(err, app.ErrDeviceAuthorizationNotFound) {
		t.Errorf("another person is limited by Ada's guesses: %v", err)
	}
}

func TestDeviceSignInOff(t *testing.T) {
	h := newHarnessMailing(t, true, func(d *app.Deps) { d.DeviceSessions = nil })
	ctx := context.Background()
	if _, err := h.svc.StartDeviceAuthorization(ctx, "glossa CLI"); !errors.Is(err, app.ErrDeviceSignInUnavailable) {
		t.Errorf("start without device sessions: %v", err)
	}
	if _, err := h.svc.AuthenticateDeviceSession(ctx, "glossa_dev_"+strings.Repeat("a", 43)); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("a bearer without device sessions: %v", err)
	}
}
