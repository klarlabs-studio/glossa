//go:build integration

package app_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klarlabs-studio/auth-go/aesgcm"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/passkey"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

var env *dbtest.Env

func TestMain(m *testing.M) {
	var err error
	env, err = dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	env.Close()
	os.Exit(code)
}

// outbox captures sent mail.
type outbox struct {
	mu   sync.Mutex
	sent []app.Message
}

func (o *outbox) Send(_ context.Context, m app.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sent = append(o.sent, m)
	return nil
}

var linkToken = regexp.MustCompile(`#token=([A-Za-z0-9_-]+)`)

// lastToken returns the token in the last mail to addr.
func (o *outbox) lastToken(t *testing.T, addr string) string {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := len(o.sent) - 1; i >= 0; i-- {
		if strings.EqualFold(o.sent[i].To, addr) {
			if m := linkToken.FindStringSubmatch(o.sent[i].Text); m != nil {
				return m[1]
			}
		}
	}
	t.Fatalf("no link mailed to %s", addr)
	return ""
}

type harness struct {
	svc   *app.Service
	mail  *outbox
	clock *clock
	uow   *db.UnitOfWork
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fastArgon keeps tests quick; production uses auth-go's defaults.
var fastArgon = authgo.Argon2idParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func newHarness(t *testing.T) *harness { return newHarnessMailing(t, true) }

// newHarnessMailing builds the service with or without a mailer (a
// deployment without email).
func newHarnessMailing(t *testing.T, mailing bool, with ...func(*app.Deps)) *harness {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cipher, err := aesgcm.New(key)
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	h := &harness{mail: &outbox{}, clock: &clock{now: time.Now().UTC()}, uow: uow}
	cfg := app.DefaultConfig("https://studio.test")
	cfg.Argon2 = fastArgon
	passkeys, err := passkey.New(passkey.Config{
		RPID: "studio.test", RPName: "Glossa", Origins: []string{"https://studio.test"},
		StateKey: []byte(strings.Repeat("s", 32)),
	}, postgres.NewPasskeyRepo(uow))
	if err != nil {
		t.Fatal(err)
	}
	deps := app.Deps{
		Passkeys:       passkeys,
		Tx:             postgres.NewTransactor(uow, cipher),
		Sessions:       postgres.NewSessionRepo(uow),
		DeviceSessions: postgres.NewDeviceSessionRepo(uow),
		SignInLinks:    postgres.NewLinkRepo(uow, postgres.PurposeSignIn),
		ResetLinks:     postgres.NewLinkRepo(uow, postgres.PurposePasswordReset),
		TOTP:           postgres.NewTOTPRepo(uow, cipher),
		LoginAttempts:  postgres.NewLoginAttemptRepo(uow),
		Clock:          h.clock.Now,
	}
	if mailing {
		deps.Mailer = h.mail
	}
	for _, w := range with {
		w(&deps)
	}
	h.svc, err = app.New(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// signUp registers addr through a magic link and returns the session.
func (h *harness) signUp(t *testing.T, addr string) app.SignedIn {
	t.Helper()
	ctx := context.Background()
	if err := h.svc.RequestSignInLink(ctx, addr); err != nil {
		t.Fatal(err)
	}
	in, err := h.svc.RedeemSignInLink(ctx, h.mail.lastToken(t, addr))
	if err != nil {
		t.Fatalf("redeem link for %s: %v", addr, err)
	}
	return in
}

// as returns a context acting as a signed-in person in tenant, the way
// the HTTP edge builds it.
func (h *harness) as(t *testing.T, in app.SignedIn, tenant tenancy.ID) context.Context {
	t.Helper()
	ctx := context.Background()
	a, err := h.svc.AuthenticateSession(ctx, in.SessionToken)
	if err != nil {
		t.Fatalf("authenticate session: %v", err)
	}
	if tenant.IsZero() {
		return authz.WithPrincipal(ctx, a.Principal())
	}
	p, err := h.svc.Authorize(ctx, a, tenant)
	if err != nil {
		t.Fatalf("authorize in %s: %v", tenant, err)
	}
	return authz.WithPrincipal(tenancy.ContextWithTenant(ctx, tenant), p)
}

// asToken is as for a bearer token.
func (h *harness) asToken(t *testing.T, secret string) context.Context {
	t.Helper()
	ctx := context.Background()
	a, err := h.svc.AuthenticateToken(ctx, secret)
	if err != nil {
		t.Fatalf("authenticate token: %v", err)
	}
	p, err := h.svc.Authorize(ctx, a, a.Token.Tenant)
	if err != nil {
		t.Fatalf("authorize token: %v", err)
	}
	return authz.WithPrincipal(tenancy.ContextWithTenant(ctx, a.Token.Tenant), p)
}

// count counts rows past RLS (superuser), for assertions.
func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Super.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
