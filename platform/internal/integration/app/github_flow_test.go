package app_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	identitydomain "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github/githubtest"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The GitHub install flow, the Git connections and the webhook inbox
// over in-memory ports (RFC 0004 §6). The Postgres adapter has its own
// integration tests; these pin the decisions: ownership is verified
// through the person's own token, a state is single-use, an
// installation belongs to one tenant, a duplicate delivery is a no-op,
// and nothing in Catalog depends on a webhook arriving.

const (
	webhookSecret = "hook-secret"
	instGitHubID  = int64(4242)
	repoGitHubID  = int64(9001)
)

var (
	tenantOne = uuid.MustParse("0192a1b2-0000-7000-8000-000000000001")
	tenantTwo = uuid.MustParse("0192a1b2-0000-7000-8000-000000000002")
	projectID = uuid.MustParse("0192a1b2-0000-7000-8000-000000000010")
	appID     = uuid.MustParse("0192a1b2-0000-7000-8000-000000000011")
)

// ── in-memory ports ──────────────────────────────────────────────────

type memStore struct {
	mu            sync.Mutex
	intents       map[string]*intentRow
	installations map[uuid.UUID]domain.Installation
	connections   map[uuid.UUID]domain.GitConnection
	// claimed is the global installation → tenant map, which is the
	// database's unique index in miniature.
	claimed map[int64]uuid.UUID
}

type intentRow struct {
	tenant   uuid.UUID
	id       uuid.UUID
	person   string
	expires  time.Time
	redeemed bool
}

func newMemStore() *memStore {
	return &memStore{
		intents: map[string]*intentRow{}, installations: map[uuid.UUID]domain.Installation{},
		connections: map[uuid.UUID]domain.GitConnection{}, claimed: map[int64]uuid.UUID{},
	}
}

// inTxKey marks a context already inside a unit of work.
type inTxKey struct{}

// InGitHub implements app.GitHubTransactor. The tenant comes from ctx,
// as row-level security would take it, and a nested unit of work is
// refused exactly as db.UnitOfWork refuses one — so a service method
// that calls GitHub or another context from inside a transaction fails
// here rather than in production.
func (m *memStore) InGitHub(ctx context.Context, fn func(context.Context, app.GitHubStore) error) error {
	t, ok := tenancy.FromContext(ctx)
	if !ok {
		return errors.New("no tenant on the context")
	}
	if ctx.Value(inTxKey{}) != nil {
		return errNestedTx
	}
	return fn(context.WithValue(ctx, inTxKey{}, true), &memScope{m: m, tenant: t.UUID()})
}

// errNestedTx mirrors db.ErrNestedTx.
var errNestedTx = errors.New("db: nested unit of work")

// ConnectionsForRepository implements app.RepositoryDirectory: the
// cross-tenant read the GitHub Actions OIDC exchange does before any
// tenant exists (RFC 0004 §6.3). It is on the store, not the scope,
// because it deliberately sees every tenant's connections — the
// repository is what names one.
func (m *memStore) ConnectionsForRepository(_ context.Context, repo int64) ([]app.RepositoryConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []app.RepositoryConnection
	for _, c := range m.connections {
		if c.RepositoryID != repo {
			continue
		}
		inst, ok := m.installations[c.InstallationID]
		if !ok {
			continue
		}
		out = append(out, app.RepositoryConnection{
			Tenant: tenancy.ID(m.claimed[inst.GitHubID]), Project: c.ProjectID,
			Application: c.ApplicationID, Path: c.Path,
		})
	}
	slices.SortFunc(out, func(a, b app.RepositoryConnection) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

type memScope struct {
	m      *memStore
	tenant uuid.UUID
}

func (s *memScope) InsertInstallIntent(_ context.Context, id uuid.UUID, person string, hash []byte, _, expires time.Time) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.intents[string(hash)] = &intentRow{tenant: s.tenant, id: id, person: person, expires: expires}
	return nil
}

func (s *memScope) RedeemInstallIntent(_ context.Context, hash []byte, now time.Time) (app.RedeemedIntent, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	r, ok := s.m.intents[string(hash)]
	// Row-level security scopes the redemption to the current tenant.
	if !ok || r.tenant != s.tenant || r.redeemed || !now.Before(r.expires) {
		return app.RedeemedIntent{}, app.ErrInstallStateInvalid
	}
	r.redeemed = true
	return app.RedeemedIntent{ID: r.id, TenantID: r.tenant, Person: r.person}, nil
}

func (s *memScope) InsertInstallation(_ context.Context, i domain.Installation) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, taken := s.m.claimed[i.GitHubID]; taken {
		return app.ErrInstallationClaimed
	}
	s.m.claimed[i.GitHubID] = s.tenant
	s.m.installations[i.ID] = i
	return nil
}

func (s *memScope) Installation(_ context.Context, id uuid.UUID) (domain.Installation, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	i, ok := s.m.installations[id]
	if !ok || s.m.claimed[i.GitHubID] != s.tenant {
		return domain.Installation{}, app.ErrNotFound
	}
	return i, nil
}

func (s *memScope) InstallationByGitHubID(_ context.Context, githubID int64) (domain.Installation, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.m.claimed[githubID] != s.tenant {
		return domain.Installation{}, app.ErrNotFound
	}
	for _, i := range s.m.installations {
		if i.GitHubID == githubID {
			return i, nil
		}
	}
	return domain.Installation{}, app.ErrNotFound
}

func (s *memScope) Installations(_ context.Context) ([]domain.Installation, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	var out []domain.Installation
	for _, i := range s.m.installations {
		if s.m.claimed[i.GitHubID] == s.tenant {
			out = append(out, i)
		}
	}
	slices.SortFunc(out, func(a, b domain.Installation) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out, nil
}

func (s *memScope) SetInstallationState(_ context.Context, githubID int64, state domain.InstallationState, login string, now time.Time) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.m.claimed[githubID] != s.tenant {
		return app.ErrNotFound
	}
	for id, i := range s.m.installations {
		if i.GitHubID == githubID {
			i.State, i.AccountLogin, i.UpdatedAt = state, login, now
			s.m.installations[id] = i
			return nil
		}
	}
	return app.ErrNotFound
}

func (s *memScope) DeleteInstallation(_ context.Context, id uuid.UUID) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	i, ok := s.m.installations[id]
	if !ok || s.m.claimed[i.GitHubID] != s.tenant {
		return app.ErrNotFound
	}
	delete(s.m.installations, id)
	delete(s.m.claimed, i.GitHubID)
	for cid, c := range s.m.connections {
		if c.InstallationID == id {
			delete(s.m.connections, cid)
		}
	}
	return nil
}

func (s *memScope) InsertGitConnection(_ context.Context, c domain.GitConnection) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	for _, have := range s.m.connections {
		if have.RepositoryID == c.RepositoryID && have.Path == c.Path {
			return app.ErrConnectionExists
		}
	}
	s.m.connections[c.ID] = c
	return nil
}

func (s *memScope) GitConnection(_ context.Context, id uuid.UUID) (domain.GitConnection, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	c, ok := s.m.connections[id]
	if !ok {
		return domain.GitConnection{}, app.ErrNotFound
	}
	return c, nil
}

func (s *memScope) GitConnections(_ context.Context, f app.ConnectionFilter) ([]domain.GitConnection, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	var out []domain.GitConnection
	for _, c := range s.m.connections {
		if f.Installation != nil && c.InstallationID != *f.Installation {
			continue
		}
		if f.Project != nil && c.ProjectID != *f.Project {
			continue
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b domain.GitConnection) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out, nil
}

func (s *memScope) ConnectionsForRepository(_ context.Context, repo int64) ([]domain.GitConnection, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	var out []domain.GitConnection
	for _, c := range s.m.connections {
		if c.RepositoryID == repo {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b domain.GitConnection) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func (s *memScope) SaveGitConnection(_ context.Context, c domain.GitConnection) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.connections[c.ID]; !ok {
		return app.ErrNotFound
	}
	s.m.connections[c.ID] = c
	return nil
}

func (s *memScope) DeleteGitConnection(_ context.Context, id uuid.UUID) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.connections[id]; !ok {
		return app.ErrNotFound
	}
	delete(s.m.connections, id)
	return nil
}

// memInbox is the webhook inbox, keyed by delivery ID like the table.
type memInbox struct {
	store *memStore
	mu    sync.Mutex
	rows  map[string]*domain.Delivery
	order []string
	now   func() time.Time
}

func newMemInbox(store *memStore, now func() time.Time) *memInbox {
	return &memInbox{store: store, rows: map[string]*domain.Delivery{}, now: now}
}

func (i *memInbox) Store(_ context.Context, d domain.Delivery) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, seen := i.rows[d.ID]; seen {
		return false, nil
	}
	d.State = domain.DeliveryPending
	i.rows[d.ID] = &d
	i.order = append(i.order, d.ID)
	return true, nil
}

func (i *memInbox) Claim(context.Context, time.Duration) (app.ClaimedDelivery, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, id := range i.order {
		r := i.rows[id]
		if r.State != domain.DeliveryPending || r.ClaimToken != uuid.Nil {
			continue
		}
		r.Attempts++
		r.ClaimToken = uuid.New()
		return app.ClaimedDelivery{
			ID: r.ID, Event: r.Event, Action: r.Action, GitHubInstallationID: r.GitHubInstallationID,
			Payload: r.Payload, Attempts: r.Attempts, Token: r.ClaimToken,
		}, true, nil
	}
	return app.ClaimedDelivery{}, false, nil
}

func (i *memInbox) Settle(_ context.Context, id string, token uuid.UUID, state domain.DeliveryState,
	tenant *uuid.UUID, failure string, now time.Time,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	r, ok := i.rows[id]
	if !ok || r.ClaimToken != token {
		return nil // the lease was lost; the claimer that holds it settles
	}
	r.State, r.TenantID, r.Failure, r.Payload, r.ClaimToken, r.ProcessedAt = state, tenant, failure, nil, uuid.Nil, &now
	return nil
}

func (i *memInbox) Retry(_ context.Context, id string, token uuid.UUID, _ time.Duration, failure string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	r, ok := i.rows[id]
	if !ok || r.ClaimToken != token {
		return nil
	}
	r.Failure, r.ClaimToken = failure, uuid.Nil
	return nil
}

func (i *memInbox) Owner(_ context.Context, githubID int64) (app.InstallationOwner, bool, error) {
	i.store.mu.Lock()
	defer i.store.mu.Unlock()
	tenant, ok := i.store.claimed[githubID]
	if !ok {
		return app.InstallationOwner{}, false, nil
	}
	state := domain.InstallationActive
	for _, inst := range i.store.installations {
		if inst.GitHubID == githubID {
			state = inst.State
		}
	}
	return app.InstallationOwner{Tenant: tenancy.ID(tenant), State: state}, true, nil
}

func (i *memInbox) Depth(context.Context) (map[string]int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := map[string]int{}
	for _, r := range i.rows {
		if r.State == domain.DeliveryPending {
			out[r.Event]++
		}
	}
	return out, nil
}

func (i *memInbox) Sweep(_ context.Context, before time.Time) (int, int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	var n int
	for id, r := range i.rows {
		if r.State != domain.DeliveryPending && r.ReceivedAt.Before(before) {
			delete(i.rows, id)
			n++
		}
	}
	i.order = slices.DeleteFunc(i.order, func(id string) bool { _, ok := i.rows[id]; return !ok })
	return n, 0, nil
}

func (i *memInbox) row(id string) domain.Delivery {
	i.mu.Lock()
	defer i.mu.Unlock()
	if r, ok := i.rows[id]; ok {
		return *r
	}
	return domain.Delivery{}
}

// memBranches records what the worker asked Catalog to do.
type memBranches struct {
	mu    sync.Mutex
	calls []string
	apps  map[uuid.UUID]bool
	fail  error
}

func (b *memBranches) record(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, s)
}

func (b *memBranches) seen() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

func (b *memBranches) UpsertBranch(ctx context.Context, project uuid.UUID, branch, head string, pr *int) error {
	if ctx.Value(inTxKey{}) != nil {
		return errNestedTx
	}
	n := 0
	if pr != nil {
		n = *pr
	}
	b.record("upsert " + project.String() + " " + branch + " " + head[:7] + " #" + itoa(n))
	return b.fail
}

func (b *memBranches) CloseBranch(ctx context.Context, project uuid.UUID, branch string) error {
	if ctx.Value(inTxKey{}) != nil {
		return errNestedTx
	}
	b.record("close " + project.String() + " " + branch)
	return b.fail
}

func (b *memBranches) MergeBranch(ctx context.Context, project uuid.UUID, branch string) error {
	if ctx.Value(inTxKey{}) != nil {
		return errNestedTx
	}
	b.record("merge " + project.String() + " " + branch)
	return b.fail
}

// ApplicationExists refuses a call from inside a unit of work: Catalog
// opens its own, and nesting one is what db.UnitOfWork forbids.
func (b *memBranches) ApplicationExists(ctx context.Context, _, application uuid.UUID) (bool, error) {
	if ctx.Value(inTxKey{}) != nil {
		return false, errNestedTx
	}
	return b.apps[application], nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// ── the fixture ──────────────────────────────────────────────────────

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// appKey is a test-time App key: no key material lives in the repo, and
// one 2048-bit key is generated for the whole package.
func appKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

type fixture struct {
	svc      *app.GitHubService
	worker   *app.InboxWorker
	checker  *app.CheckWorker
	store    *memStore
	inbox    *memInbox
	branches *memBranches
	checks   *memChecks
	sources  *memSources
	fake     *githubtest.Server
	clock    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key := appKey(t)
	fake := githubtest.New(t, githubtest.Options{AppID: 99001, PublicKey: &key.PublicKey})
	fake.AddInstallation(instGitHubID, "acme", repoGitHubID)
	fake.AddRepository(githubtest.Repository{
		ID: repoGitHubID, Name: "shop", FullName: "acme/shop", DefaultBranch: "main",
	})
	fake.AddUser("gho_owner", instGitHubID)
	fake.AddOAuthCode("code-1", "gho_owner")

	client, err := github.New(github.Config{
		AppID: 99001, AppSlug: "glossa", PrivateKey: key, WebhookSecret: []byte(webhookSecret),
		ClientID: "Iv1.abc", ClientSecret: "client-secret", APIURL: fake.URL, WebURL: fake.WebURL,
	}, github.Options{HTTPClient: fake.Client(), Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	hooks, err := github.NewWebhooks([]byte(webhookSecret))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		store: newMemStore(), branches: &memBranches{apps: map[uuid.UUID]bool{appID: true}}, fake: fake,
		clock: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}
	f.inbox = newMemInbox(f.store, func() time.Time { return f.clock })
	f.checks = newMemChecks(func() time.Time { return f.clock })
	f.sources = newMemSources()
	f.svc, err = app.NewGitHubService(app.GitHubDeps{
		Tx: f.store, Inbox: f.inbox, Repositories: f.store, GitHub: client, Verifier: hooks, Events: hooks,
		Branches: f.branches,
		Checks:   f.checks, Sources: f.sources, StudioURL: "https://studio.example/",
		Now: func() time.Time { return f.clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.worker = app.NewInboxWorker(f.svc, app.InboxConfig{})
	f.checker = app.NewCheckWorker(f.svc, app.CheckConfig{})
	return f
}

// as returns a context acting as a named person in tenant with exactly
// perms, the way Identity's edge would put one on a request. The name
// is stable, because an install intent is bound to the person who
// started it.
func as(tenant uuid.UUID, person string, perms ...authz.Permission) context.Context {
	id := identitydomain.PersonID(uuid.NewSHA1(tenant, []byte(person)))
	ctx := tenancy.ContextWithTenant(context.Background(), tenancy.ID(tenant))
	return authz.WithPrincipal(ctx, authz.Principal{
		Actor: identitydomain.PersonActor(id), Person: id, Tenant: tenancy.ID(tenant),
		Grant: identitydomain.GrantOf(perms...),
	})
}

func manager(tenant uuid.UUID, person string) context.Context {
	return as(tenant, person, authz.IntegrationManage, authz.IntegrationRead)
}

// connect runs the whole install flow and returns the installation.
func (f *fixture) connect(t *testing.T, ctx context.Context, code string) domain.Installation {
	t.Helper()
	intent, err := f.svc.StartInstall(ctx)
	if err != nil {
		t.Fatalf("StartInstall: %v", err)
	}
	if !strings.Contains(intent.InstallURL, "/apps/glossa/installations/new") ||
		!strings.Contains(intent.InstallURL, "state="+intent.State) {
		t.Fatalf("install URL %q does not carry the state to our App", intent.InstallURL)
	}
	inst, err := f.svc.CompleteInstall(ctx, app.CompleteInstall{
		State: intent.State, Code: code, InstallationID: instGitHubID, SetupAction: "install",
	})
	if err != nil {
		t.Fatalf("CompleteInstall: %v", err)
	}
	return inst
}

// deliver signs a fixture as GitHub would and posts it at the endpoint.
func (f *fixture) deliver(t *testing.T, name, deliveryID string) (bool, error) {
	t.Helper()
	return f.deliverBody(t, githubtest.FixtureEvent(name), deliveryID, githubtest.Fixture(name))
}

func (f *fixture) deliverBody(t *testing.T, event, deliveryID string, body []byte) (bool, error) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	h := http.Header{
		"X-Github-Event":      {event},
		"X-Github-Delivery":   {deliveryID},
		"X-Hub-Signature-256": {"sha256=" + hex.EncodeToString(mac.Sum(nil))},
	}
	return f.svc.ReceiveWebhook(context.Background(), h, bytes.NewReader(body))
}

// drain runs the worker until the inbox is empty.
func (f *fixture) drain(t *testing.T) {
	t.Helper()
	for range 50 {
		worked, err := f.worker.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("the worker never drained the inbox")
}

// fixtureWith rewrites a recorded delivery's installation and repository
// ids to the fixture's, so the recorded payloads resolve to our tenant.
func retarget(t *testing.T, name string, edits map[string]any) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(githubtest.Fixture(name), &doc); err != nil {
		t.Fatal(err)
	}
	for path, v := range edits {
		node := doc
		parts := strings.Split(path, ".")
		for _, p := range parts[:len(parts)-1] {
			next, ok := node[p].(map[string]any)
			if !ok {
				t.Fatalf("fixture %s has no object at %s", name, p)
			}
			node = next
		}
		node[parts[len(parts)-1]] = v
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ── the install flow ─────────────────────────────────────────────────

func TestInstallFlowVerifiesOwnershipAndClaimsTheInstallation(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	inst := f.connect(t, ctx, "code-1")

	if inst.GitHubID != instGitHubID || inst.AccountLogin != "acme" || inst.State != domain.InstallationActive {
		t.Fatalf("installation = %+v", inst)
	}
	// The ownership check went to GitHub with the person's own token,
	// which the code was exchanged for.
	req := f.fake.Requests(githubtest.RouteUserInstallations)
	if len(req) != 1 || req[0].Header.Get("Authorization") != "Bearer gho_owner" {
		t.Fatalf("ownership was not checked with the person's token: %+v", req)
	}
	if n := len(f.fake.Requests(githubtest.RouteOAuthToken)); n != 1 {
		t.Fatalf("%d code exchanges, want 1", n)
	}
}

func TestInstallStateIsSingleUseAndBoundToItsPerson(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")

	intent, err := f.svc.StartInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Someone else in the same workspace cannot finish it.
	other := manager(tenantOne, "person:two")
	_, err = f.svc.CompleteInstall(other, app.CompleteInstall{
		State: intent.State, Code: "code-1", InstallationID: instGitHubID, SetupAction: "install",
	})
	if !errors.Is(err, app.ErrInstallStateNotYours) {
		t.Fatalf("another person's callback: err = %v", err)
	}
	// And the state is burned even so, which is what single-use means.
	_, err = f.svc.CompleteInstall(ctx, app.CompleteInstall{
		State: intent.State, Code: "code-1", InstallationID: instGitHubID, SetupAction: "install",
	})
	if !errors.Is(err, app.ErrInstallStateInvalid) {
		t.Fatalf("replayed state: err = %v", err)
	}
	// An unknown state, an expired one and setup_action=request all
	// answer the same way.
	for name, in := range map[string]app.CompleteInstall{
		"unknown state":  {State: "nope", Code: "code-1", InstallationID: instGitHubID, SetupAction: "install"},
		"no code":        {State: intent.State, InstallationID: instGitHubID, SetupAction: "install"},
		"request action": {State: intent.State, Code: "code-1", InstallationID: instGitHubID, SetupAction: "request"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.svc.CompleteInstall(ctx, in); !errors.Is(err, app.ErrInstallStateInvalid) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestStateExpires(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	intent, err := f.svc.StartInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(domain.InstallIntentTTL + time.Second)
	_, err = f.svc.CompleteInstall(ctx, app.CompleteInstall{
		State: intent.State, Code: "code-1", InstallationID: instGitHubID, SetupAction: "install",
	})
	if !errors.Is(err, app.ErrInstallStateInvalid) {
		t.Fatalf("expired state: err = %v", err)
	}
}

func TestAnInstallationCannotBeClaimedByASecondTenant(t *testing.T) {
	f := newFixture(t)
	f.connect(t, manager(tenantOne, "person:one"), "code-1")

	// The second workspace's own person, their own state, their own
	// code — and GitHub agrees they can see the installation. It is
	// still not theirs to claim.
	f.fake.AddUser("gho_two", instGitHubID)
	f.fake.AddOAuthCode("code-2", "gho_two")
	ctx := manager(tenantTwo, "person:two")
	intent, err := f.svc.StartInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CompleteInstall(ctx, app.CompleteInstall{
		State: intent.State, Code: "code-2", InstallationID: instGitHubID, SetupAction: "install",
	})
	if !errors.Is(err, app.ErrInstallationClaimed) {
		t.Fatalf("second tenant: err = %v", err)
	}
	if !strings.Contains(err.Error(), "already connected") || strings.Contains(err.Error(), tenantOne.String()) {
		t.Fatalf("the error names the other workspace: %v", err)
	}
}

func TestOwnershipIsRefusedForSomeoneElsesInstallation(t *testing.T) {
	f := newFixture(t)
	f.fake.AddUser("gho_stranger") // sees nothing
	f.fake.AddOAuthCode("code-x", "gho_stranger")
	ctx := manager(tenantOne, "person:one")
	intent, err := f.svc.StartInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CompleteInstall(ctx, app.CompleteInstall{
		State: intent.State, Code: "code-x", InstallationID: instGitHubID, SetupAction: "install",
	})
	if !errors.Is(err, app.ErrInstallationNotVisible) {
		t.Fatalf("stranger: err = %v", err)
	}
}

func TestReconnectingAnInstallationThisWorkspaceHoldsRefreshesIt(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	first := f.connect(t, ctx, "code-1")

	f.fake.AddOAuthCode("code-again", "gho_owner")
	f.clock = f.clock.Add(time.Hour)
	again := f.connect(t, ctx, "code-again")
	if again.ID != first.ID || !again.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("reconnect made a second installation: %+v then %+v", first, again)
	}
}

func TestPermissionsAreRequired(t *testing.T) {
	f := newFixture(t)
	reader := as(tenantOne, "person:one", authz.IntegrationRead)
	if _, err := f.svc.StartInstall(reader); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("StartInstall without integration.manage: err = %v", err)
	}
	if _, err := f.svc.Connections(as(tenantOne, "p"), app.ConnectionFilter{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("Connections without integration.read: err = %v", err)
	}
}

// ── installations and connections ────────────────────────────────────

func TestInstallationsListTheirRepositories(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	f.connect(t, ctx, "code-1")

	views, err := f.svc.Installations(ctx)
	if err != nil || len(views) != 1 {
		t.Fatalf("Installations = %+v, %v", views, err)
	}
	if len(views[0].Repositories) != 1 || views[0].Repositories[0].FullName != "acme/shop" {
		t.Fatalf("repositories = %+v", views[0].Repositories)
	}
	if views[0].RepositoriesUnavailable {
		t.Fatal("the repositories were read, so nothing is unavailable")
	}
	// Another workspace sees none of it.
	if views, err := f.svc.Installations(manager(tenantTwo, "person:two")); err != nil || len(views) != 0 {
		t.Fatalf("another tenant sees %+v, %v", views, err)
	}
}

func TestConnectValidatesTheRepositoryAndTheApplication(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	inst := f.connect(t, ctx, "code-1")

	c, err := f.svc.Connect(ctx, app.ConnectRepository{
		Installation: inst.ID,
		ConnectionInput: domain.ConnectionInput{
			RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: appID, Path: "/apps/web/",
		},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// The default branch and the label came from GitHub; the path was
	// normalized.
	if c.DefaultBranch != "main" || c.RepositoryName != "acme/shop" || c.Path != "apps/web" {
		t.Fatalf("connection = %+v", c)
	}
	// The same repository and path twice is refused, a second path is
	// not: one repository feeds several projects, one per path.
	_, err = f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
		RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: appID, Path: "apps/web",
	}})
	if !errors.Is(err, app.ErrConnectionExists) {
		t.Fatalf("duplicate path: err = %v", err)
	}
	if _, err := f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
		RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: appID, Path: "apps/admin",
	}}); err != nil {
		t.Fatalf("a second path: %v", err)
	}
	// A repository the installation cannot see, and an application the
	// project does not have, are both refused here.
	if _, err := f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
		RepositoryID: 777777, ProjectID: projectID, ApplicationID: appID,
	}}); !errors.Is(err, app.ErrRepositoryNotVisible) {
		t.Fatalf("unseen repository: err = %v", err)
	}
	if _, err := f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
		RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: uuid.New(), Path: "apps/other",
	}}); !errors.Is(err, app.ErrApplicationNotFound) {
		t.Fatalf("unknown application: err = %v", err)
	}
}

// A project's pull requests are named by their address on the web host
// of the repository connected to it, so the impact preview (RFC 0005
// §4.3) can link to the one that would newly fail instead of printing a
// branch name somebody has to look up.
func TestAProjectsPullRequestsAreNamedByTheirAddress(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	// Nothing connected: nothing to point at, and no error — a project
	// without a repository still has a preview to show.
	got, err := f.svc.PullRequestURLs(ctx, projectID, []int{11})
	if err != nil || len(got) != 0 {
		t.Fatalf("unconnected: %v, %v", got, err)
	}

	inst := f.connect(t, ctx, "code-1")
	for _, path := range []string{"apps/web", "apps/admin"} {
		if _, err := f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
			RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: appID, Path: path,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	// Two paths of one repository are one place to point.
	got, err = f.svc.PullRequestURLs(ctx, projectID, []int{11, 12, 0})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{11: f.fake.WebURL + "/acme/shop/pull/11", 12: f.fake.WebURL + "/acme/shop/pull/12"}
	if !maps.Equal(got, want) {
		t.Fatalf("urls = %v, want %v", got, want)
	}
	// Another project's pull requests are not this repository's.
	if got, _ := f.svc.PullRequestURLs(ctx, uuid.New(), []int{11}); len(got) != 0 {
		t.Errorf("another project: %v", got)
	}
	// Reading them is reading the integration.
	if _, err := f.svc.PullRequestURLs(as(tenantOne, "person:one", authz.CatalogRead), projectID, []int{11}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("without integration.read: err = %v", err)
	}
}

func TestConnectionPathsAreNormalizedAndTraversalRefused(t *testing.T) {
	for in, want := range map[string]string{"": "", ".": "", "/apps/web/": "apps/web", " apps/web ": "apps/web"} {
		got, err := domain.NormalizeConnectionPath(in)
		if err != nil || got != want {
			t.Errorf("NormalizeConnectionPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"../etc", "apps/../..", `apps\web`, strings.Repeat("a", 600)} {
		if _, err := domain.NormalizeConnectionPath(bad); !errors.Is(err, domain.ErrInvalidConnection) {
			t.Errorf("NormalizeConnectionPath(%q) accepted it: %v", bad, err)
		}
	}
}

func TestForgettingAnInstallationTakesItsConnections(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	inst := f.connect(t, ctx, "code-1")
	if _, err := f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
		RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: appID,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ForgetInstallation(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	conns, err := f.svc.Connections(ctx, app.ConnectionFilter{})
	if err != nil || len(conns) != 0 {
		t.Fatalf("connections after forgetting = %+v, %v", conns, err)
	}
}

// ── the webhook endpoint ─────────────────────────────────────────────

func TestAWebhookIsVerifiedBeforeItIsStored(t *testing.T) {
	f := newFixture(t)
	body := githubtest.Fixture("pull_request.opened")

	// A body that was not signed with our secret is refused, and no row
	// is written.
	h := http.Header{
		"X-Github-Event":      {"pull_request"},
		"X-Github-Delivery":   {"d-forged"},
		"X-Hub-Signature-256": {"sha256=" + strings.Repeat("00", 32)},
	}
	if _, err := f.svc.ReceiveWebhook(context.Background(), h, bytes.NewReader(body)); !errors.Is(err, app.ErrWebhookSignature) {
		t.Fatalf("a forged signature: err = %v", err)
	}
	if f.inbox.row("d-forged").ID != "" {
		t.Fatal("a delivery that did not verify was stored")
	}
	// Changing one byte of a correctly signed body invalidates it: the
	// HMAC covers the raw bytes, which is what the endpoint reads.
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	tampered := append(slices.Clone(body[:len(body)-1]), ' ', '}')
	h.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	if _, err := f.svc.ReceiveWebhook(context.Background(), h, bytes.NewReader(tampered)); !errors.Is(err, app.ErrWebhookSignature) {
		t.Fatalf("a tampered body: err = %v", err)
	}
}

func TestABodyOverTheCapIsRefused(t *testing.T) {
	f := newFixture(t)
	big := bytes.Repeat([]byte("x"), github.MaxWebhookBytes+1)
	if _, err := f.deliverBody(t, "pull_request", "d-big", big); !errors.Is(err, app.ErrWebhookTooLarge) {
		t.Fatalf("an oversized body: err = %v", err)
	}
}

func TestADuplicateDeliveryIsANoOp(t *testing.T) {
	f := newFixture(t)
	accepted, err := f.deliver(t, "pull_request.opened", "d-1")
	if err != nil || !accepted {
		t.Fatalf("first delivery: %v, %v", accepted, err)
	}
	accepted, err = f.deliver(t, "pull_request.opened", "d-1")
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if accepted {
		t.Fatal("a delivery id seen inside the replay window was accepted again")
	}
}

func TestAnUnhandledEventIsAcknowledgedAndIgnored(t *testing.T) {
	f := newFixture(t)
	accepted, err := f.deliverBody(t, "star", "d-star", []byte(`{"action":"created"}`))
	if err != nil || !accepted {
		t.Fatalf("an unhandled event: %v, %v", accepted, err)
	}
	f.drain(t)
	if got := f.inbox.row("d-star").State; got != domain.DeliveryIgnored {
		t.Fatalf("state = %s, want ignored", got)
	}
}

func TestADeliveryNoWorkspaceClaimedIsIgnored(t *testing.T) {
	f := newFixture(t)
	if _, err := f.deliver(t, "pull_request.opened", "d-orphan"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	r := f.inbox.row("d-orphan")
	if r.State != domain.DeliveryIgnored || r.TenantID != nil {
		t.Fatalf("an unclaimed installation's delivery = %+v", r)
	}
	if calls := f.branches.seen(); len(calls) != 0 {
		t.Fatalf("Catalog was touched for an unclaimed installation: %v", calls)
	}
}

// ── the inbox worker ─────────────────────────────────────────────────

// connected sets up a workspace with the installation and one
// connection, and returns the delivery ids it will use.
func (f *fixture) connected(t *testing.T) {
	t.Helper()
	ctx := manager(tenantOne, "person:one")
	inst := f.connect(t, ctx, "code-1")
	if _, err := f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
		RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: appID,
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestPullRequestEventsMoveTheCatalogBranch(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	edits := map[string]any{
		"installation.id":           float64(instGitHubID),
		"repository.id":             float64(repoGitHubID),
		"pull_request.head.repo.id": float64(repoGitHubID),
	}
	for i, name := range []string{"pull_request.opened", "pull_request.synchronize", "pull_request.reopened"} {
		body := retarget(t, name, edits)
		if _, err := f.deliverBody(t, "pull_request", "d-pr-"+itoa(i), body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	f.drain(t)
	calls := f.branches.seen()
	if len(calls) != 3 {
		t.Fatalf("branch calls = %v", calls)
	}
	for _, c := range calls {
		if !strings.HasPrefix(c, "upsert "+projectID.String()+" ") {
			t.Fatalf("unexpected call %q", c)
		}
	}
	for _, id := range []string{"d-pr-0", "d-pr-1", "d-pr-2"} {
		r := f.inbox.row(id)
		if r.State != domain.DeliveryDone || r.TenantID == nil || *r.TenantID != tenantOne {
			t.Fatalf("%s = %+v", id, r)
		}
		if r.Payload != nil {
			t.Fatalf("%s kept its payload after processing", id)
		}
	}
}

func TestAClosedPullRequestClosesTheBranchAndAMergedOneMergesIt(t *testing.T) {
	for name, want := range map[string]string{"closed": "close", "merged": "merge"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.connected(t)
			edits := map[string]any{
				"installation.id":           float64(instGitHubID),
				"repository.id":             float64(repoGitHubID),
				"pull_request.head.repo.id": float64(repoGitHubID),
				"pull_request.merged":       name == "merged",
				"pull_request.head.ref":     "feat/copy",
			}
			body := retarget(t, "pull_request.closed", edits)
			if _, err := f.deliverBody(t, "pull_request", "d-close", body); err != nil {
				t.Fatal(err)
			}
			f.drain(t)
			calls := f.branches.seen()
			if len(calls) != 1 || !strings.HasPrefix(calls[0], want+" ") {
				t.Fatalf("calls = %v, want one %s", calls, want)
			}
		})
	}
}

// TestCorrectnessDoesNotDependOnTheWebhook is RFC 0004 §4.1 as a test:
// a merged pull request's webhook only marks the branch merged. It
// activates nothing, so a delivery GitHub never sends, or one that
// fails for good, costs a stale branch view and nothing else.
func TestCorrectnessDoesNotDependOnTheWebhook(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	// Catalog refuses everything the worker asks of it.
	f.branches.fail = errors.New("catalog is down")

	body := retarget(t, "pull_request.closed", map[string]any{
		"installation.id":           float64(instGitHubID),
		"repository.id":             float64(repoGitHubID),
		"pull_request.head.repo.id": float64(repoGitHubID),
		"pull_request.merged":       true,
	})
	if _, err := f.deliverBody(t, "pull_request", "d-merge", body); err != nil {
		t.Fatal(err)
	}
	// The delivery retries and eventually gives up; nothing else breaks.
	for range domain.MaxDeliveryAttempts + 2 {
		if _, err := f.worker.RunOnce(context.Background()); err != nil {
			t.Fatalf("worker: %v", err)
		}
	}
	r := f.inbox.row("d-merge")
	if r.State != domain.DeliveryFailed {
		t.Fatalf("state = %s after %d attempts, want failed", r.State, r.Attempts)
	}
	// The merge itself is the default branch's push, which never comes
	// through here: the only casualty is the branch's view.
	if !strings.Contains(r.Failure, "catalog is down") {
		t.Fatalf("failure = %q", r.Failure)
	}
	if got := f.inbox.row("d-merge").Payload; got != nil {
		t.Fatal("a failed delivery kept its payload")
	}
}

func TestInstallationEventsChangeTheInstallationsState(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	ctx := manager(tenantOne, "person:one")
	for i, tc := range []struct {
		action string
		want   domain.InstallationState
	}{
		{"suspend", domain.InstallationSuspended},
		{"unsuspend", domain.InstallationActive},
		{"deleted", domain.InstallationRevoked},
	} {
		body := retarget(t, "installation.deleted", map[string]any{
			"action": tc.action, "installation.id": float64(instGitHubID),
		})
		if _, err := f.deliverBody(t, "installation", "d-inst-"+itoa(i), body); err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		f.drain(t)
		views, err := f.svc.Installations(ctx)
		if err != nil || len(views) != 1 {
			t.Fatalf("%s: installations = %+v, %v", tc.action, views, err)
		}
		if views[0].State != tc.want {
			t.Fatalf("%s: state = %s, want %s", tc.action, views[0].State, tc.want)
		}
	}
	// A revoked installation lists without repositories, because GitHub
	// would refuse the call anyway.
	views, _ := f.svc.Installations(ctx)
	if len(views[0].Repositories) != 0 {
		t.Fatalf("a revoked installation listed repositories: %+v", views[0].Repositories)
	}
}

func TestRepositoriesLeavingTheInstallationDropTheirConnections(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	ctx := manager(tenantOne, "person:one")

	body := retarget(t, "installation_repositories.removed", map[string]any{
		"installation.id": float64(instGitHubID),
	})
	// The fixture's removed list is rewritten to name our repository.
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	doc["repositories_removed"] = []any{map[string]any{
		"id": float64(repoGitHubID), "name": "shop", "full_name": "acme/shop",
	}}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.deliverBody(t, "installation_repositories", "d-repos", body); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	conns, err := f.svc.Connections(ctx, app.ConnectionFilter{})
	if err != nil || len(conns) != 0 {
		t.Fatalf("connections = %+v, %v", conns, err)
	}
}

func TestTheSweepKeepsDeliveryIDsForTheReplayWindow(t *testing.T) {
	f := newFixture(t)
	f.connected(t)
	if _, err := f.deliverBody(t, "pull_request", "d-old", retarget(t, "pull_request.opened", map[string]any{
		"installation.id": float64(instGitHubID), "repository.id": float64(repoGitHubID),
		"pull_request.head.repo.id": float64(repoGitHubID),
	})); err != nil {
		t.Fatal(err)
	}
	f.drain(t)

	// Inside the window the id is still there, so a redelivery is a
	// no-op.
	f.clock = f.clock.Add(domain.DeliveryReplayWindow - time.Hour)
	if err := f.worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.deliverBody(t, "pull_request", "d-old", []byte(`{"action":"opened"}`))
	if err != nil || accepted {
		t.Fatalf("inside the window a redelivery was accepted: %v, %v", accepted, err)
	}
	// Past it the id is dropped.
	f.clock = f.clock.Add(2 * time.Hour)
	if err := f.worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.inbox.row("d-old").ID != "" {
		t.Fatal("the delivery survived the replay window")
	}
}

func TestInboxDepthCountsWhatIsWaiting(t *testing.T) {
	f := newFixture(t)
	if _, err := f.deliver(t, "pull_request.opened", "d-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.deliver(t, "installation.created", "d-b"); err != nil {
		t.Fatal(err)
	}
	d, err := f.inbox.Depth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(d, map[string]int{"pull_request": 1, "installation": 1}) {
		t.Fatalf("depth = %v", d)
	}
}

func TestRetryDelayBacksOffAndIsCapped(t *testing.T) {
	if got := domain.RetryDelay(1); got != time.Second {
		t.Fatalf("first retry = %v", got)
	}
	if got := domain.RetryDelay(3); got != 4*time.Second {
		t.Fatalf("third retry = %v", got)
	}
	if got := domain.RetryDelay(50); got != 5*time.Minute {
		t.Fatalf("a late retry = %v, want the cap", got)
	}
}

// A Git connection is its project's (RFC 0006 §3.3, §4.1): read by its
// id, it is not there for a caller who cannot see that project — the
// answer for an id that does not exist — even one who lacks
// integration.read and so would be refused it anyway (§12.2's sweep
// found the refusal: a 403 for another project's connection said it
// existed). Inside a project they work in, an assigned member is
// refused.
func TestAConnectionIsNotThereOutsideWhatTheCallerSees(t *testing.T) {
	f := newFixture(t)
	ctx := manager(tenantOne, "person:one")
	inst := f.connect(t, ctx, "code-1")
	c, err := f.svc.Connect(ctx, app.ConnectRepository{Installation: inst.ID, ConnectionInput: domain.ConnectionInput{
		RepositoryID: repoGitHubID, ProjectID: projectID, ApplicationID: appID, Path: "apps/web",
	}})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	bg := context.Background()
	elsewhere := uuid.New()
	cov := &authztest.Coverage{}
	inProject, member := authztest.Assigned(bg, tenancy.ID(tenantOne), cov, "de")
	cov.Assign(member, projectID, uuid.New(), "de")
	elsewhereCov := &authztest.Coverage{}
	outside, other := authztest.Assigned(bg, tenancy.ID(tenantOne), elsewhereCov, "de")
	elsewhereCov.Assign(other, elsewhere, uuid.New(), "de")

	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"the manager", ctx, nil},
		{"an assigned member working elsewhere", outside, authz.ErrNotVisible},
		{"an assigned member working in its project", inProject, authz.ErrForbidden},
		{"a member scoped to another project, without integration.read",
			authztest.ScopedMember(bg, tenancy.ID(tenantOne), []uuid.UUID{elsewhere}, []string{"translator"}), authz.ErrNotVisible},
		{"a member scoped to another project", authztest.ScopedMember(bg, tenancy.ID(tenantOne), []uuid.UUID{elsewhere}, []string{"admin"}), authz.ErrNotVisible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.Connection(tc.ctx, c.ID)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
