//go:build integration

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestSignInMethods(t *testing.T) {
	mailing := newHarness(t)
	if got := mailing.svc.SignInMethods(); !slices.Equal(got, []string{"passkey", "password", "magic_link"}) || !mailing.svc.EmailEnabled() {
		t.Errorf("with email: %v", got)
	}
	silent := newHarnessMailing(t, false)
	if got := silent.svc.SignInMethods(); !slices.Equal(got, []string{"passkey", "password"}) || silent.svc.EmailEnabled() {
		t.Errorf("without email: %v", got)
	}
}

// Without a mailer, email flows are unavailable rather than silently
// dropped, and password accounts work without a verified address.
func TestWithoutEmail(t *testing.T) {
	h := newHarnessMailing(t, false)
	ctx := context.Background()
	const addr, pw = "linus@example.com", "correct horse battery"

	for name, err := range map[string]error{
		"magic link":          h.svc.RequestSignInLink(ctx, addr),
		"password reset":      h.svc.RequestPasswordReset(ctx, addr),
		"reset redemption":    h.svc.ResetPassword(ctx, "whatever-token", "a new long password"),
		"magic link redeemed": func() error { _, err := h.svc.RedeemSignInLink(ctx, "whatever-token"); return err }(),
	} {
		if !errors.Is(err, app.ErrEmailDisabled) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if err := h.svc.Register(ctx, addr, pw, "Linus"); err != nil {
		t.Fatal(err)
	}
	in, err := h.svc.SignInWithPassword(ctx, addr, pw, "")
	if err != nil {
		t.Fatalf("an unverified account can't sign in without email: %v", err)
	}
	if in.Person.EmailVerified() || in.Person.DisplayName != "Linus" {
		t.Errorf("person %+v", in.Person)
	}
	me, err := h.svc.GetMe(h.as(t, in, tenancy.ID{}))
	if err != nil || len(me.Memberships) != 1 {
		t.Fatalf("individual tenant: %v %+v", err, me.Memberships)
	}
	// Registering the address again changes nothing and says nothing.
	if err := h.svc.Register(ctx, addr, "another long password", ""); err != nil {
		t.Errorf("re-registration: %v", err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, "another long password", ""); !errors.Is(err, app.ErrInvalidCredentials) {
		t.Errorf("re-registration replaced the password: %v", err)
	}
	if len(h.mail.sent) != 0 {
		t.Errorf("mail sent without a mailer: %+v", h.mail.sent)
	}

	// TOTP still guards the password.
	enr, err := h.svc.BeginTOTP(ctx, in.Person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ConfirmTOTP(ctx, in.Person.ID, totpCode(t, enr.Secret, h.clock.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); !errors.Is(err, app.ErrTOTPRequired) {
		t.Errorf("TOTP not required: %v", err)
	}
}

// An unverified address never accepts an invitation: without email no
// account proves its address, so invitations wait until email works.
func TestWithoutEmailInvitationsWait(t *testing.T) {
	h := newHarnessMailing(t, false)
	ctx := context.Background()
	if err := h.svc.Register(ctx, "ada@example.com", "correct horse battery", ""); err != nil {
		t.Fatal(err)
	}
	ada, err := h.svc.SignInWithPassword(ctx, "ada@example.com", "correct horse battery", "")
	if err != nil {
		t.Fatal(err)
	}
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.AddMember(h.as(t, ada, acme.ID), "mallory@example.com", []string{"admin"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Register(ctx, "mallory@example.com", "correct horse battery", ""); err != nil {
		t.Fatal(err)
	}
	mallory, err := h.svc.SignInWithPassword(ctx, "mallory@example.com", "correct horse battery", "")
	if err != nil {
		t.Fatal(err)
	}
	me, err := h.svc.GetMe(h.as(t, mallory, tenancy.ID{}))
	if err != nil || len(me.Memberships) != 1 {
		t.Errorf("an unverified address joined an organization: %v %+v", err, me.Memberships)
	}
}
