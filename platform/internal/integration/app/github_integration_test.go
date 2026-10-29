//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	integrationpg "go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The GitHub store and the webhook inbox against a real Postgres with
// row-level security on (RFC 0004 §6). The in-memory tests pin the
// flow; these pin what only the database can enforce: an installation
// belongs to one tenant across the whole deployment, a delivery is
// written with no tenant at all and moves to one, a duplicate delivery
// id is a no-op, a claim is fenced by its token, and the sweep keeps
// ids for the replay window.

// githubHarness wires the GitHub store and inbox on the test database.
type githubHarness struct {
	tx      *integrationpg.GitHubTransactor
	inbox   *integrationpg.Inbox
	catalog *catalogapp.Service
	one     tenancy.ID
	two     tenancy.ID
}

func newGitHubHarness(t *testing.T) *githubHarness {
	t.Helper()
	ctx := context.Background()
	if err := env.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	one, err := env.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	two, err := env.SeedTenant(ctx, "globex")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	return &githubHarness{
		tx: integrationpg.NewGitHubTransactor(uow), inbox: integrationpg.NewInbox(uow),
		catalog: catalogapp.New(catalogpg.NewTransactor(uow)), one: one, two: two,
	}
}

// admin returns a context acting in tenant with the GitHub permissions.
func (h *githubHarness) admin(tenant tenancy.ID) context.Context {
	return authztest.Member(context.Background(), tenant, []string{"admin"})
}

func installationFor(t *testing.T, githubID int64) domain.Installation {
	t.Helper()
	i, err := domain.NewInstallation(domain.InstallationInput{
		GitHubID: githubID, AccountID: githubID * 10, AccountLogin: "acme", AccountType: domain.AccountOrganization,
	}, "person:one", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestAnInstallationBelongsToExactlyOneTenant(t *testing.T) {
	h := newGitHubHarness(t)
	const githubID = int64(4242)

	if err := h.tx.InGitHub(h.admin(h.one), func(ctx context.Context, st app.GitHubStore) error {
		return st.InsertInstallation(ctx, installationFor(t, githubID))
	}); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	// The second tenant's claim hits the global unique index. Row-level
	// security hides the row that caused it, so nothing of the first
	// tenant leaks into the error.
	err := h.tx.InGitHub(h.admin(h.two), func(ctx context.Context, st app.GitHubStore) error {
		return st.InsertInstallation(ctx, installationFor(t, githubID))
	})
	if !errors.Is(err, app.ErrInstallationClaimed) {
		t.Fatalf("second claim: err = %v", err)
	}
	// And the second tenant cannot see it at all.
	if err := h.tx.InGitHub(h.admin(h.two), func(ctx context.Context, st app.GitHubStore) error {
		got, err := st.Installations(ctx)
		if err != nil || len(got) != 0 {
			t.Fatalf("the other tenant sees %d installations, %v", len(got), err)
		}
		if _, err := st.InstallationByGitHubID(ctx, githubID); !errors.Is(err, app.ErrNotFound) {
			t.Fatalf("lookup across tenants: err = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOneRepositoryFeedsSeveralProjectsOnePerPath(t *testing.T) {
	h := newGitHubHarness(t)
	ctx := h.admin(h.one)
	const repo = int64(9001)
	var inst domain.Installation

	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		inst = installationFor(t, 4242)
		return st.InsertInstallation(ctx, inst)
	}); err != nil {
		t.Fatal(err)
	}
	newConn := func(path string, project uuid.UUID) domain.GitConnection {
		c, err := domain.NewGitConnection(inst, domain.ConnectionInput{
			RepositoryID: repo, RepositoryName: "acme/shop", ProjectID: project,
			ApplicationID: uuid.Must(uuid.NewV7()), DefaultBranch: "main", Path: path,
		}, "person:one", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	web, admin := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		if err := st.InsertGitConnection(ctx, newConn("apps/web", web)); err != nil {
			return err
		}
		// A second path of the same repository is a different project.
		if err := st.InsertGitConnection(ctx, newConn("apps/admin", admin)); err != nil {
			return err
		}
		// The same path twice is not.
		if err := st.InsertGitConnection(ctx, newConn("apps/web", admin)); !errors.Is(err, app.ErrConnectionExists) {
			t.Fatalf("duplicate path: err = %v", err)
		}
		conns, err := st.ConnectionsForRepository(ctx, repo)
		if err != nil || len(conns) != 2 {
			t.Fatalf("connections = %d, %v", len(conns), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestADeliveryIsStoredWithoutATenantAndMovesToOne(t *testing.T) {
	h := newGitHubHarness(t)
	ctx := context.Background()
	const githubID = int64(4242)

	// The endpoint writes the delivery before any tenant is known.
	stored, err := h.inbox.Store(ctx, domain.Delivery{
		ID: "d-1", Event: "pull_request", Action: "opened", GitHubInstallationID: githubID,
		Payload: []byte(`{"action":"opened"}`), ReceivedAt: time.Now().UTC(),
	})
	if err != nil || !stored {
		t.Fatalf("store: %v, %v", stored, err)
	}
	// A duplicate id is a no-op, which is the replay protection.
	again, err := h.inbox.Store(ctx, domain.Delivery{
		ID: "d-1", Event: "pull_request", GitHubInstallationID: githubID, Payload: []byte(`{}`), ReceivedAt: time.Now().UTC(),
	})
	if err != nil || again {
		t.Fatalf("duplicate: %v, %v", again, err)
	}
	// No tenant has claimed the installation yet.
	if _, ok, err := h.inbox.Owner(ctx, githubID); err != nil || ok {
		t.Fatalf("owner before the claim: %v, %v", ok, err)
	}
	if err := h.tx.InGitHub(h.admin(h.one), func(ctx context.Context, st app.GitHubStore) error {
		return st.InsertInstallation(ctx, installationFor(t, githubID))
	}); err != nil {
		t.Fatal(err)
	}
	owner, ok, err := h.inbox.Owner(ctx, githubID)
	if err != nil || !ok || owner.Tenant != h.one || owner.State != domain.InstallationActive {
		t.Fatalf("owner = %+v, %v, %v", owner, ok, err)
	}

	// The worker claims it, and the claim is fenced by its token: a
	// settlement with the wrong one changes nothing.
	c, ok, err := h.inbox.Claim(ctx, time.Minute)
	if err != nil || !ok || c.ID != "d-1" || string(c.Payload) != `{"action":"opened"}` {
		t.Fatalf("claim = %+v, %v, %v", c, ok, err)
	}
	if err := h.inbox.Settle(ctx, c.ID, uuid.New(), domain.DeliveryDone, nil, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Nothing else is due while the lease holds.
	if _, ok, err := h.inbox.Claim(ctx, time.Minute); err != nil || ok {
		t.Fatalf("a second claim while leased: %v, %v", ok, err)
	}
	// The real settlement moves it to the tenant and drops the payload.
	id := h.one.UUID()
	if err := h.inbox.Settle(ctx, c.ID, c.Token, domain.DeliveryDone, &id, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := h.inbox.Claim(ctx, time.Minute); err != nil || ok {
		t.Fatalf("a settled delivery was claimed again: %v, %v", ok, err)
	}
	if d, err := h.inbox.Depth(ctx); err != nil || len(d) != 0 {
		t.Fatalf("depth after settling = %v, %v", d, err)
	}
}

func TestTheSweepKeepsSettledDeliveriesForTheReplayWindow(t *testing.T) {
	h := newGitHubHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := h.inbox.Store(ctx, domain.Delivery{
		ID: "d-old", Event: "installation", GitHubInstallationID: 1, Payload: []byte(`{}`),
		ReceivedAt: now.Add(-8 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.inbox.Store(ctx, domain.Delivery{
		ID: "d-pending", Event: "installation", GitHubInstallationID: 1, Payload: []byte(`{}`), ReceivedAt: now.Add(-8 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Settle the first; the second stays pending.
	c, _, err := h.inbox.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.inbox.Settle(ctx, c.ID, c.Token, domain.DeliveryIgnored, nil, "", now); err != nil {
		t.Fatal(err)
	}

	deliveries, _, err := h.inbox.Sweep(ctx, now.Add(-domain.DeliveryReplayWindow))
	if err != nil || deliveries != 1 {
		t.Fatalf("sweep removed %d, %v; want the settled one only", deliveries, err)
	}
	// The swept id is free again; the pending one was left alone, because
	// a delivery nobody has processed is not one to forget.
	stored, err := h.inbox.Store(ctx, domain.Delivery{
		ID: c.ID, Event: "installation", GitHubInstallationID: 1, Payload: []byte(`{}`), ReceivedAt: now,
	})
	if err != nil || !stored {
		t.Fatalf("after the window: %v, %v", stored, err)
	}
	if again, err := h.inbox.Store(ctx, domain.Delivery{
		ID: "d-pending", Event: "installation", GitHubInstallationID: 1, Payload: []byte(`{}`), ReceivedAt: now,
	}); err != nil || again {
		t.Fatalf("the pending delivery was swept: %v, %v", again, err)
	}
}

func TestAnInstallIntentIsRedeemedOnce(t *testing.T) {
	h := newGitHubHarness(t)
	ctx := h.admin(h.one)
	hash := make([]byte, 32)
	for i := range hash {
		hash[i] = byte(i)
	}
	now := time.Now().UTC()
	id := uuid.Must(uuid.NewV7())

	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		return st.InsertInstallIntent(ctx, id, "person:one", hash, now, now.Add(15*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		r, err := st.RedeemInstallIntent(ctx, hash, now)
		if err != nil || r.ID != id || r.Person != "person:one" || r.TenantID != h.one.UUID() {
			t.Fatalf("redeem = %+v, %v", r, err)
		}
		// A second redemption finds nothing: single-use.
		if _, err := st.RedeemInstallIntent(ctx, hash, now); !errors.Is(err, app.ErrInstallStateInvalid) {
			t.Fatalf("replay: err = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Another tenant's redemption of the same hash finds nothing either,
	// because row-level security scopes it.
	if err := h.tx.InGitHub(h.admin(h.two), func(ctx context.Context, st app.GitHubStore) error {
		if _, err := st.RedeemInstallIntent(ctx, hash, now); !errors.Is(err, app.ErrInstallStateInvalid) {
			t.Fatalf("across tenants: err = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAnExpiredIntentIsNeitherRedeemedNorKept(t *testing.T) {
	h := newGitHubHarness(t)
	ctx := h.admin(h.one)
	hash := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now().UTC()

	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		return st.InsertInstallIntent(ctx, uuid.Must(uuid.NewV7()), "person:one", hash, now.Add(-time.Hour), now.Add(-time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		_, err := st.RedeemInstallIntent(ctx, hash, now)
		if !errors.Is(err, app.ErrInstallStateInvalid) {
			t.Fatalf("expired: err = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, intents, err := h.inbox.Sweep(context.Background(), now); err != nil || intents != 1 {
		t.Fatalf("sweep removed %d intents, %v", intents, err)
	}
}

// TestForgettingAnInstallationCascadesInTheDatabase pins the cascade
// the migration declares, so a forgotten account leaves nothing behind
// and its installation id is free again.
func TestForgettingAnInstallationCascadesInTheDatabase(t *testing.T) {
	h := newGitHubHarness(t)
	ctx := h.admin(h.one)
	var inst domain.Installation

	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		inst = installationFor(t, 4242)
		if err := st.InsertInstallation(ctx, inst); err != nil {
			return err
		}
		c, err := domain.NewGitConnection(inst, domain.ConnectionInput{
			RepositoryID: 9001, RepositoryName: "acme/shop", ProjectID: uuid.Must(uuid.NewV7()),
			ApplicationID: uuid.Must(uuid.NewV7()), DefaultBranch: "main",
		}, "person:one", time.Now().UTC())
		if err != nil {
			return err
		}
		return st.InsertGitConnection(ctx, c)
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.tx.InGitHub(ctx, func(ctx context.Context, st app.GitHubStore) error {
		if err := st.DeleteInstallation(ctx, inst.ID); err != nil {
			return err
		}
		conns, err := st.GitConnections(ctx, app.ConnectionFilter{})
		if err != nil || len(conns) != 0 {
			t.Fatalf("connections after forgetting = %d, %v", len(conns), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The installation id is free again, for this tenant or another.
	if err := h.tx.InGitHub(h.admin(h.two), func(ctx context.Context, st app.GitHubStore) error {
		return st.InsertInstallation(ctx, installationFor(t, 4242))
	}); err != nil {
		t.Fatalf("re-claiming a forgotten installation: %v", err)
	}
}
