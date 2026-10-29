//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func firstPage() pagination.Page { return pagination.Page{Size: pagination.DefaultPageSize} }

func ptr[T any](v T) *T { return &v }

func TestMagicLinkRegistrationCreatesIndividualTenantAndOwner(t *testing.T) {
	h := newHarness(t)
	in := h.signUp(t, "Ada@Example.com")

	if in.Person.Email.String() != "ada@example.com" || !in.Person.EmailVerified() {
		t.Errorf("person = %+v", in.Person)
	}
	tenant := in.Person.IndividualTenantID
	if n := count(t, "SELECT count(*) FROM tenants WHERE id = $1 AND kind = 'individual'", tenant.UUID()); n != 1 {
		t.Fatalf("individual tenant rows = %d", n)
	}
	if n := count(t, `SELECT count(*) FROM identity_members WHERE tenant_id = $1 AND person_id = $2
		AND status = 'active' AND roles = '{owner}'`, tenant.UUID(), in.Person.ID.UUID()); n != 1 {
		t.Errorf("owner memberships = %d", n)
	}
	if n := count(t, `SELECT count(*) FROM outbox_events WHERE tenant_id = $1
		AND event_type IN ('identity.tenant.created', 'identity.member.added')`, tenant.UUID()); n != 2 {
		t.Errorf("events = %d, want tenant.created + member.added", n)
	}

	// A second sign-in reuses the person and the tenant.
	again := h.signUp(t, "ada@example.com")
	if again.Person.ID != in.Person.ID {
		t.Error("second sign-in created a second person")
	}
	if n := count(t, "SELECT count(*) FROM tenants"); n != 1 {
		t.Errorf("tenants = %d after a second sign-in, want 1", n)
	}
}

// The tenant and its owner membership commit together or not at all; a
// registration interrupted between the person and the tenant heals at
// the next sign-in.
func TestIndividualTenantAndOwnerAreAtomic(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	mustExec(t, `CREATE FUNCTION fail_member() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'injected failure'; END $$`)
	mustExec(t, `CREATE TRIGGER fail_member BEFORE INSERT ON identity_members
		FOR EACH ROW EXECUTE FUNCTION fail_member()`)
	t.Cleanup(func() {
		mustExec(t, "DROP TRIGGER IF EXISTS fail_member ON identity_members")
		mustExec(t, "DROP FUNCTION IF EXISTS fail_member()")
	})

	err := h.svc.Register(ctx, "grace@example.com", "correct horse battery", "Grace")
	if err == nil {
		t.Fatal("registration succeeded despite the failing membership insert")
	}
	if n := count(t, "SELECT count(*) FROM identity_people"); n != 1 {
		t.Fatalf("people = %d; the person is written before the tenant", n)
	}
	if n := count(t, "SELECT count(*) FROM tenants"); n != 0 {
		t.Errorf("tenants = %d: the tenant committed without its owner", n)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events"); n != 0 {
		t.Errorf("events = %d: events committed without their state change", n)
	}

	mustExec(t, "DROP TRIGGER fail_member ON identity_members")
	in := h.signUp(t, "grace@example.com")
	if n := count(t, `SELECT count(*) FROM tenants t JOIN identity_members m ON m.tenant_id = t.id
		WHERE t.id = $1 AND m.person_id = $2`, in.Person.IndividualTenantID.UUID(), in.Person.ID.UUID()); n != 1 {
		t.Errorf("sign-in did not heal the missing individual tenant")
	}
}

func mustExec(t *testing.T, sql string) {
	t.Helper()
	if _, err := env.Super.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func TestPasswordRegistrationNeedsVerification(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const addr, pw = "linus@example.com", "correct horse battery"

	if err := h.svc.Register(ctx, addr, "short", ""); !errors.Is(err, app.ErrWeakPassword) {
		t.Errorf("weak password err = %v", err)
	}
	if err := h.svc.Register(ctx, addr, pw, "Linus"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); !errors.Is(err, app.ErrEmailUnverified) {
		t.Fatalf("unverified sign-in err = %v", err)
	}
	// Registering again doesn't fail or reveal anything; it mails a link.
	if err := h.svc.Register(ctx, addr, "another long password", ""); err != nil {
		t.Fatalf("re-registration err = %v", err)
	}
	if _, err := h.svc.RedeemSignInLink(ctx, h.mail.lastToken(t, addr)); err != nil {
		t.Fatal(err)
	}
	in, err := h.svc.SignInWithPassword(ctx, addr, pw, "")
	if err != nil {
		t.Fatalf("verified sign-in: %v", err)
	}
	if in.Person.DisplayName != "Linus" {
		t.Errorf("display name = %q; the re-registration must not have replaced the account", in.Person.DisplayName)
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, "wrong password!", ""); !errors.Is(err, app.ErrInvalidCredentials) {
		t.Errorf("wrong password err = %v", err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, "nobody@example.com", pw, ""); !errors.Is(err, app.ErrInvalidCredentials) {
		t.Errorf("unknown account err = %v", err)
	}
}

func TestSignInLinksAreSingleUse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.RequestSignInLink(ctx, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	tok := h.mail.lastToken(t, "ada@example.com")
	if _, err := h.svc.RedeemSignInLink(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RedeemSignInLink(ctx, tok); !errors.Is(err, app.ErrLinkInvalid) {
		t.Errorf("second redemption err = %v", err)
	}
	if err := h.svc.RequestSignInLink(ctx, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(16 * time.Minute)
	if _, err := h.svc.RedeemSignInLink(ctx, h.mail.lastToken(t, "ada@example.com")); !errors.Is(err, app.ErrLinkInvalid) {
		t.Errorf("expired link err = %v", err)
	}
}

func TestSessionsSignOutAndSignOutEverywhere(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	laptop := h.signUp(t, "ada@example.com")
	phone := h.signUp(t, "ada@example.com")
	other := h.signUp(t, "bob@example.com")

	for _, s := range []app.SignedIn{laptop, phone, other} {
		if _, err := h.svc.AuthenticateSession(ctx, s.SessionToken); err != nil {
			t.Fatalf("fresh session rejected: %v", err)
		}
	}
	if n := count(t, "SELECT count(*) FROM identity_sessions WHERE token_hash = $1", laptop.SessionToken); n != 0 {
		t.Error("a raw session token is stored")
	}

	if err := h.svc.SignOut(ctx, laptop.SessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AuthenticateSession(ctx, laptop.SessionToken); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("signed-out session err = %v", err)
	}
	if _, err := h.svc.AuthenticateSession(ctx, phone.SessionToken); err != nil {
		t.Errorf("signing out one session ended another: %v", err)
	}

	if err := h.svc.SignOutEverywhere(ctx, phone.Person.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AuthenticateSession(ctx, phone.SessionToken); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("session after sign-out-everywhere err = %v", err)
	}
	if _, err := h.svc.AuthenticateSession(ctx, other.SessionToken); err != nil {
		t.Errorf("another person's session was revoked: %v", err)
	}

	h.clock.Advance(15 * 24 * time.Hour)
	if _, err := h.svc.AuthenticateSession(ctx, other.SessionToken); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("expired session err = %v", err)
	}
}

func TestPasswordLockout(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const addr, pw = "ada@example.com", "correct horse battery"
	if err := h.svc.Register(ctx, addr, pw, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RedeemSignInLink(ctx, h.mail.lastToken(t, addr)); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := h.svc.SignInWithPassword(ctx, addr, "wrong password!", ""); !errors.Is(err, app.ErrInvalidCredentials) {
			t.Fatalf("attempt %d err = %v", i+1, err)
		}
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); !errors.Is(err, app.ErrAccountLocked) {
		t.Fatalf("after 5 failures err = %v, want ErrAccountLocked", err)
	}
	h.clock.Advance(16 * time.Minute)
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); err != nil {
		t.Fatalf("after the lock window: %v", err)
	}
}

func TestTOTPGuardsPasswordSignIn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const addr, pw = "ada@example.com", "correct horse battery"
	if err := h.svc.Register(ctx, addr, pw, ""); err != nil {
		t.Fatal(err)
	}
	in, err := h.svc.RedeemSignInLink(ctx, h.mail.lastToken(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	enr, err := h.svc.BeginTOTP(ctx, in.Person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, "SELECT count(*) FROM identity_totp WHERE secret_ciphertext = $1", enr.Secret); n != 0 {
		t.Error("the TOTP secret is stored in plaintext")
	}
	// Pending: not required yet.
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); err != nil {
		t.Fatalf("a pending enrollment must not gate sign-in: %v", err)
	}
	code := totpCode(t, enr.Secret, h.clock.Now())
	if err := h.svc.ConfirmTOTP(ctx, in.Person.ID, "000000"); !errors.Is(err, app.ErrTOTPInvalid) && code != "000000" {
		t.Errorf("wrong confirmation code err = %v", err)
	}
	if err := h.svc.ConfirmTOTP(ctx, in.Person.ID, code); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BeginTOTP(ctx, in.Person.ID); !errors.Is(err, app.ErrTOTPAlreadyEnabled) {
		t.Errorf("re-enrolling err = %v", err)
	}

	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); !errors.Is(err, app.ErrTOTPRequired) {
		t.Fatalf("no code err = %v", err)
	}
	h.clock.Advance(30 * time.Second)
	code = totpCode(t, enr.Secret, h.clock.Now())
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, code); err != nil {
		t.Fatalf("with code: %v", err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, code); !errors.Is(err, app.ErrTOTPInvalid) {
		t.Errorf("replayed code err = %v", err)
	}

	h.clock.Advance(30 * time.Second)
	if err := h.svc.DisableTOTP(ctx, in.Person.ID, totpCode(t, enr.Secret, h.clock.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); err != nil {
		t.Errorf("after disabling TOTP: %v", err)
	}
}

func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	s, err := authgo.TOTPSecretFromString(secret)
	if err != nil {
		t.Fatal(err)
	}
	code, err := authgo.DefaultTOTPConfig("Glossa").Generate(s, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestPasswordResetRevokesSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	in := h.signUp(t, "ada@example.com")

	if err := h.svc.RequestPasswordReset(ctx, "nobody@example.com"); err != nil {
		t.Fatalf("reset for an unknown address err = %v", err)
	}
	if err := h.svc.RequestPasswordReset(ctx, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	tok := h.mail.lastToken(t, "ada@example.com")
	if err := h.svc.ResetPassword(ctx, tok, "short"); !errors.Is(err, app.ErrWeakPassword) {
		t.Fatalf("weak password err = %v", err)
	}
	// A sign-in link can't reset a password (separate purposes).
	if err := h.svc.RequestSignInLink(ctx, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ResetPassword(ctx, h.mail.lastToken(t, "ada@example.com"), "a brand new password"); !errors.Is(err, app.ErrLinkInvalid) {
		t.Errorf("sign-in link used for reset err = %v", err)
	}
	if err := h.svc.ResetPassword(ctx, tok, "a brand new password"); err != nil {
		t.Fatalf("reset (the weak attempt must not have spent the link): %v", err)
	}
	if _, err := h.svc.AuthenticateSession(ctx, in.SessionToken); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("session survived a password reset: %v", err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, "ada@example.com", "a brand new password", ""); err != nil {
		t.Errorf("sign in with the new password: %v", err)
	}
}

func TestCreateOrganization(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	ctx := h.as(t, ada, tenancy.ID{})

	org, replayed, err := h.svc.CreateOrganization(ctx, "acme", "Acme", "key-1")
	if err != nil || replayed {
		t.Fatalf("create = %v, replayed %t", err, replayed)
	}
	if org.Kind != tenancy.KindOrganization || org.Slug != "acme" || org.CreatedAt.IsZero() {
		t.Errorf("org = %+v", org)
	}
	principal, _ := authz.From(h.as(t, ada, org.ID))
	if !principal.Grant.Allows(authz.OwnersManage) {
		t.Error("the creator must be the organization's owner")
	}

	again, replayed, err := h.svc.CreateOrganization(ctx, "acme", "Acme", "key-1")
	if err != nil || !replayed || again.ID != org.ID {
		t.Errorf("replay = %+v, %t, %v", again, replayed, err)
	}
	if _, _, err := h.svc.CreateOrganization(ctx, "acme-2", "Acme", "key-1"); !errors.Is(err, app.ErrIdempotencyReuse) {
		t.Errorf("key reused for another slug err = %v", err)
	}
	if _, _, err := h.svc.CreateOrganization(ctx, "acme", "Acme again", ""); !errors.Is(err, app.ErrSlugTaken) {
		t.Errorf("taken slug err = %v", err)
	}
	if n := count(t, "SELECT count(*) FROM tenants WHERE kind = 'organization'"); n != 1 {
		t.Errorf("organizations = %d, want 1", n)
	}

	list, next, err := h.svc.ListTenants(ctx, firstPage())
	if err != nil || next != nil || len(list) != 2 {
		t.Errorf("ListTenants = %d tenants, next %v, err %v; want individual + acme", len(list), next, err)
	}
}

func TestCrossTenantIsolationOfMembersAndTokens(t *testing.T) {
	h := newHarness(t)
	ada, bob := h.signUp(t, "ada@example.com"), h.signUp(t, "bob@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	bolt, _, err := h.svc.CreateOrganization(h.as(t, bob, tenancy.ID{}), "bolt", "Bolt", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme, inBolt := h.as(t, ada, acme.ID), h.as(t, bob, bolt.ID)

	invite, _, err := h.svc.AddMember(inAcme, "carol@example.com", []string{"translator"}, []string{"de"}, "")
	if err != nil {
		t.Fatal(err)
	}
	acmeToken, err := h.svc.CreateToken(inAcme, "ci", []string{"read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// Bob, in his tenant, sees only his tenant's members and tokens…
	members, _, err := h.svc.ListMembers(inBolt, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if m.TenantID != bolt.ID {
			t.Errorf("bolt lists a member of %s", m.TenantID)
		}
	}
	tokens, _, err := h.svc.ListTokens(inBolt, firstPage())
	if err != nil || len(tokens) != 0 {
		t.Errorf("bolt lists %d tokens, err %v", len(tokens), err)
	}
	// …and can't reach Acme's by ID…
	if _, err := h.svc.GetMember(inBolt, invite.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("bolt reading acme's member err = %v", err)
	}
	if err := h.svc.RevokeToken(inBolt, acmeToken.Token.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("bolt revoking acme's token err = %v", err)
	}
	// …nor act in Acme at all.
	bobAuthn, _ := h.svc.AuthenticateSession(context.Background(), bob.SessionToken)
	if _, err := h.svc.Authorize(context.Background(), bobAuthn, acme.ID); !errors.Is(err, app.ErrForbidden) {
		t.Errorf("bob authorized in acme: %v", err)
	}
	tokAuthn, err := h.svc.AuthenticateToken(context.Background(), acmeToken.Secret.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authorize(context.Background(), tokAuthn, bolt.ID); !errors.Is(err, app.ErrForbidden) {
		t.Errorf("acme's token authorized in bolt: %v", err)
	}
	// A grant doesn't travel: Ada's acme principal on a bolt-scoped context.
	adaPrincipal, _ := authz.From(inAcme)
	crossed := authz.WithPrincipal(tenancy.ContextWithTenant(context.Background(), bolt.ID), adaPrincipal)
	if _, _, err := h.svc.ListMembers(crossed, firstPage()); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("acme grant used in bolt err = %v", err)
	}

	// Beneath the app: glossa_app scoped to bolt sees no acme rows, and
	// only the people who are bolt members.
	err = db.NewUnitOfWork(env.App).InTenantTx(tenancy.ContextWithTenant(context.Background(), bolt.ID),
		func(ctx context.Context, tx *db.TenantTx) error {
			for query, want := range map[string]int{
				"SELECT count(*) FROM identity_members WHERE tenant_id <> app_current_tenant()":    0,
				"SELECT count(*) FROM identity_api_tokens WHERE tenant_id <> app_current_tenant()": 0,
				"SELECT count(*) FROM identity_people":                                             1,
			} {
				var n int
				if err := tx.QueryRow(ctx, query).Scan(&n); err != nil {
					return err
				}
				if n != want {
					t.Errorf("%s = %d, want %d", query, n, want)
				}
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

// Tenant scope can't reach credentials at all, and can't read more than
// a person's name and email.
func TestTenantScopeCannotReachCredentials(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	ctx := tenancy.ContextWithTenant(context.Background(), ada.Person.IndividualTenantID)
	for _, query := range []string{
		"SELECT count(*) FROM identity_sessions",
		"SELECT count(*) FROM identity_email_links",
		"SELECT count(*) FROM identity_totp",
		"SELECT count(*) FROM identity_passkeys",
		"SELECT count(*) FROM identity_login_attempts",
		"SELECT password_hash FROM identity_people",
	} {
		err := db.NewUnitOfWork(env.App).InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
			_, err := tx.Exec(ctx, query)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: err = %v, want permission denied", query, err)
		}
	}
}

func TestTokenAuthResolvesTenantAndScopes(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)
	created, err := h.svc.CreateToken(inAcme, "deploy", []string{"read", "publish"}, nil, "idem-1")
	if err != nil {
		t.Fatal(err)
	}
	if created.Secret == nil || created.Token.Hint != created.Secret.Hint() {
		t.Fatalf("created = %+v", created)
	}
	if n := count(t, "SELECT count(*) FROM identity_api_tokens WHERE token_hash = $1", created.Secret.Hash()); n != 1 {
		t.Error("the token isn't stored by its hash")
	}
	if n := count(t, "SELECT count(*) FROM identity_api_tokens WHERE hint = $1 OR name = $1", created.Secret.String()); n != 0 {
		t.Error("the raw secret is stored")
	}

	replay, err := h.svc.CreateToken(inAcme, "deploy", []string{"read", "publish"}, nil, "idem-1")
	if err != nil || !replay.Replayed || replay.Secret != nil || replay.Token.ID != created.Token.ID {
		t.Errorf("replay = %+v, %v; want the same token without its secret", replay, err)
	}

	a, err := h.svc.AuthenticateToken(context.Background(), created.Secret.String())
	if err != nil {
		t.Fatal(err)
	}
	if a.Token.Tenant != acme.ID || a.Actor != domain.TokenActor(created.Token.ID) {
		t.Errorf("authn = %+v", a)
	}
	ctx := h.asToken(t, created.Secret.String())
	if err := authz.Require(ctx, authz.ReleasesPublish); err != nil {
		t.Errorf("publish token can't publish: %v", err)
	}
	if err := authz.Require(ctx, authz.CatalogWrite); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("publish token writing the catalog err = %v", err)
	}
	if n := count(t, "SELECT count(*) FROM identity_api_tokens WHERE id = $1 AND last_used_at IS NOT NULL", created.Token.ID.UUID()); n != 1 {
		t.Error("last_used_at not recorded")
	}
	// GET /v1/tenants is tenantless: the token lists its own tenant.
	list, _, err := h.svc.ListTenants(authz.WithPrincipal(context.Background(), a.Principal()), firstPage())
	if err != nil || len(list) != 1 || list[0].ID != acme.ID {
		t.Errorf("a token's tenants = %+v, %v", list, err)
	}

	if err := h.svc.RevokeToken(inAcme, created.Token.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RevokeToken(inAcme, created.Token.ID); !errors.Is(err, domain.ErrTokenRevoked) {
		t.Errorf("second revoke err = %v", err)
	}
	if _, err := h.svc.AuthenticateToken(context.Background(), created.Secret.String()); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("revoked token err = %v", err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'identity.token.revoked'"); n != 1 {
		t.Errorf("token.revoked events = %d", n)
	}
	revoker, _ := authz.From(inAcme)
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'identity.token.revoked' AND actor = $1",
		revoker.Actor.String()); n != 1 {
		t.Errorf("token.revoked does not name the person who revoked it (%s)", revoker.Actor)
	}

	exp := h.clock.Now().Add(time.Hour)
	short, err := h.svc.CreateToken(inAcme, "short-lived", []string{"read"}, &exp, "")
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(2 * time.Hour)
	if _, err := h.svc.AuthenticateToken(context.Background(), short.Secret.String()); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("expired token err = %v", err)
	}
	if _, err := h.svc.AuthenticateToken(context.Background(), "glossa_api_not-a-real-token-at-all-but-43-chars"); !errors.Is(err, app.ErrUnauthenticated) {
		t.Errorf("unknown token err = %v", err)
	}
}

func TestTokenScopesCannotExceedTheCreator(t *testing.T) {
	h := newHarness(t)
	ada, dev := h.signUp(t, "ada@example.com"), h.signUp(t, "dev@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.AddMember(h.as(t, ada, acme.ID), "dev@example.com", []string{"developer"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	dev = h.signUp(t, "dev@example.com") // accepts the invitation
	inAcme := h.as(t, dev, acme.ID)
	if _, err := h.svc.CreateToken(inAcme, "ci", []string{"read", "write", "publish"}, nil, ""); err != nil {
		t.Errorf("developer CI token: %v", err)
	}
	if _, err := h.svc.CreateToken(inAcme, "root", []string{"admin"}, nil, ""); !errors.Is(err, domain.ErrScopeExceedsGrant) {
		t.Errorf("developer admin token err = %v", err)
	}
}

func TestInvitationBecomesMembershipOnSignIn(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)
	invite, replayed, err := h.svc.AddMember(inAcme, "Carol@Example.com", []string{"translator"}, []string{"de", "fr-ca"}, "inv-1")
	if err != nil || replayed || invite.Status != domain.MemberInvited {
		t.Fatalf("invite = %+v, %t, %v", invite, replayed, err)
	}
	if got := invite.Locales.Strings(); len(got) != 2 || got[1] != "fr-CA" {
		t.Errorf("locales = %v, want canonical tags", got)
	}
	again, replayed, err := h.svc.AddMember(inAcme, "carol@example.com", []string{"translator"}, []string{"de"}, "inv-1")
	if err != nil || !replayed || again.ID != invite.ID {
		t.Errorf("replay = %+v, %t, %v", again, replayed, err)
	}
	if _, _, err := h.svc.AddMember(inAcme, "carol@example.com", []string{"reviewer"}, nil, ""); !errors.Is(err, app.ErrDuplicate) {
		t.Errorf("second invitation for one address err = %v", err)
	}

	carol := h.signUp(t, "carol@example.com")
	p := h.as(t, carol, acme.ID)
	principal, _ := authz.From(p)
	de, _ := domain.ParseLocale("de-AT")
	ja, _ := domain.ParseLocale("ja")
	if !principal.Grant.AllowsFor(authz.TranslationsWrite, de) || principal.Grant.AllowsFor(authz.TranslationsWrite, ja) {
		t.Error("carol's grant must be a translator scoped to de and fr-CA")
	}
	m, err := h.svc.GetMember(inAcme, invite.ID)
	if err != nil || m.Status != domain.MemberActive || m.PersonID != carol.Person.ID || m.Version != 2 {
		t.Errorf("member after sign-in = %+v, %v", m, err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'identity.member.activated'"); n != 1 {
		t.Errorf("member.activated events = %d", n)
	}
	// Accepting an invitation is the invitee's act, not the inviter's.
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'identity.member.activated' AND actor = $1",
		domain.PersonActor(carol.Person.ID).String()); n != 1 {
		t.Error("member.activated does not name the person who accepted the invitation")
	}
	me, err := h.svc.GetMe(h.as(t, carol, tenancy.ID{}))
	if err != nil || len(me.Memberships) != 2 {
		t.Errorf("carol's memberships = %d, %v; want her own tenant and acme", len(me.Memberships), err)
	}
}

func TestMemberAccessChanges(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)
	me, _ := authz.From(inAcme)

	// The last owner can't demote or remove themselves.
	demote := []string{"admin"}
	if _, err := h.svc.UpdateMember(inAcme, me.Member, 1, app.MemberChange{Roles: &demote}); !errors.Is(err, domain.ErrLastOwner) {
		t.Errorf("demoting the last owner err = %v", err)
	}
	if err := h.svc.RemoveMember(inAcme, me.Member, nil); !errors.Is(err, domain.ErrLastOwner) {
		t.Errorf("removing the last owner err = %v", err)
	}

	m, _, err := h.svc.AddMember(inAcme, "tr@example.com", []string{"translator"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	locales := []string{"ja"}
	updated, err := h.svc.UpdateMember(inAcme, m.ID, m.Version, app.MemberChange{Locales: &locales})
	if err != nil || updated.Version != m.Version+1 || updated.Locales.Strings()[0] != "ja" || !updated.Roles.Has(domain.RoleTranslator) {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	if _, err := h.svc.UpdateMember(inAcme, m.ID, m.Version, app.MemberChange{Locales: &locales}); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale If-Match err = %v", err)
	}
	stale := m.Version
	if err := h.svc.RemoveMember(inAcme, m.ID, &stale); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale remove err = %v", err)
	}
	if err := h.svc.RemoveMember(inAcme, m.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GetMember(inAcme, m.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("removed member err = %v", err)
	}

	// An individual tenant takes no members.
	mine := h.as(t, ada, ada.Person.IndividualTenantID)
	if _, _, err := h.svc.AddMember(mine, "x@example.com", []string{"admin"}, nil, ""); !errors.Is(err, domain.ErrIndividualTenant) {
		t.Errorf("inviting into an individual tenant err = %v", err)
	}
}

// Passkey ceremonies live server-side: single-use, and bound to the
// person who started them.
func TestPasskeyCeremonies(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ada, bob := h.signUp(t, "ada@example.com"), h.signUp(t, "bob@example.com")

	if _, err := h.svc.BeginPasskeySignIn(ctx, "ada@example.com"); !errors.Is(err, app.ErrNoPasskeys) {
		t.Errorf("sign-in without passkeys err = %v", err)
	}
	if _, err := h.svc.BeginPasskeySignIn(ctx, "nobody@example.com"); !errors.Is(err, app.ErrNoPasskeys) {
		t.Errorf("sign-in for an unknown address err = %v", err)
	}
	ch, err := h.svc.BeginPasskeyRegistration(ctx, ada.Person.ID)
	if err != nil || len(ch.Options) == 0 || ch.Key == "" {
		t.Fatalf("challenge = %+v, %v", ch, err)
	}
	if n := count(t, "SELECT count(*) FROM identity_webauthn_ceremonies"); n != 1 {
		t.Fatalf("ceremonies = %d", n)
	}
	if _, err := h.svc.FinishPasskeyRegistration(ctx, bob.Person.ID, ch.Key, []byte(`{}`), "stolen"); !errors.Is(err, app.ErrPasskeyInvalid) {
		t.Errorf("finishing someone else's ceremony err = %v", err)
	}
	if n := count(t, "SELECT count(*) FROM identity_webauthn_ceremonies"); n != 0 {
		t.Errorf("a ceremony survived an attempt to answer it")
	}
	if _, err := h.svc.FinishPasskeyRegistration(ctx, ada.Person.ID, ch.Key, []byte(`{}`), "mac"); !errors.Is(err, app.ErrPasskeyInvalid) {
		t.Errorf("reusing a spent ceremony err = %v", err)
	}

	ch, err = h.svc.BeginPasskeyRegistration(ctx, ada.Person.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(app.CeremonyTTL + time.Second)
	if _, err := h.svc.FinishPasskeyRegistration(ctx, ada.Person.ID, ch.Key, []byte(`{}`), "mac"); !errors.Is(err, app.ErrPasskeyInvalid) {
		t.Errorf("expired ceremony err = %v", err)
	}
}

// A person sees and removes only their own passkeys, on every device.
func TestListAndDeletePasskeys(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ada, bob := h.signUp(t, "ada@example.com"), h.signUp(t, "bob@example.com")
	repo := postgres.NewPasskeyRepo(h.uow)
	for i, name := range []string{"mac", "phone", "key"} {
		uid, _ := authgo.NewUserID(ada.Person.ID.String())
		if err := repo.Add(ctx, authgo.PasskeyCredential{
			ID: []byte{0xfa, byte(i)}, UserID: uid, PublicKey: []byte{1}, Name: name,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.UpdateSignCount(ctx, []byte{0xfa, 1}, 7); err != nil {
		t.Fatal(err)
	}

	var all []app.Passkey
	page := pagination.Page{Size: 2}
	for i := 0; ; i++ {
		items, next, err := h.svc.ListPasskeys(ctx, ada.Person.ID, page)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, items...)
		if next == nil {
			break
		}
		if i > 3 {
			t.Fatal("pagination does not terminate")
		}
		if page, err = pagination.Parse(ptr(2), next); err != nil {
			t.Fatal(err)
		}
	}
	if len(all) != 3 || all[0].Name != "mac" || all[1].Name != "phone" || all[2].Name != "key" {
		t.Fatalf("passkeys = %+v", all)
	}
	if all[0].LastUsedAt != nil || all[1].LastUsedAt == nil || all[0].CreatedAt.IsZero() {
		t.Errorf("last used = %v, %v", all[0].LastUsedAt, all[1].LastUsedAt)
	}
	if _, _, err := h.svc.ListPasskeys(ctx, ada.Person.ID, pagination.Page{Size: 2, After: "garbage"}); err == nil {
		t.Error("accepted a malformed cursor")
	}
	if theirs, _, err := h.svc.ListPasskeys(ctx, bob.Person.ID, firstPage()); err != nil || len(theirs) != 0 {
		t.Errorf("bob's passkeys = %+v, %v", theirs, err)
	}

	if err := h.svc.DeletePasskey(ctx, bob.Person.ID, all[0].ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("deleting someone else's passkey err = %v", err)
	}
	if err := h.svc.DeletePasskey(ctx, ada.Person.ID, all[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeletePasskey(ctx, ada.Person.ID, all[0].ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("deleting twice err = %v", err)
	}
	if n := count(t, "SELECT count(*) FROM identity_passkeys WHERE person_id = $1", ada.Person.ID.UUID()); n != 2 {
		t.Errorf("passkeys left = %d", n)
	}
}

func TestPaginatedMembers(t *testing.T) {
	h := newHarness(t)
	ada := h.signUp(t, "ada@example.com")
	acme, _, err := h.svc.CreateOrganization(h.as(t, ada, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}
	inAcme := h.as(t, ada, acme.ID)
	for _, e := range []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com"} {
		if _, _, err := h.svc.AddMember(inAcme, e, []string{"reviewer"}, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[domain.MemberID]bool{}
	page := pagination.Page{Size: 2}
	for i := 0; ; i++ {
		items, next, err := h.svc.ListMembers(inAcme, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range items {
			if seen[m.ID] {
				t.Fatalf("member %s listed twice", m.ID)
			}
			seen[m.ID] = true
		}
		if next == nil {
			break
		}
		size := 2
		if page, err = pagination.Parse(&size, next); err != nil {
			t.Fatal(err)
		}
		if i > 5 {
			t.Fatal("pagination doesn't end")
		}
	}
	if len(seen) != 5 {
		t.Errorf("listed %d members, want 5 (owner + 4)", len(seen))
	}
}
