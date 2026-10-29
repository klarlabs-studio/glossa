package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// memStore is an in-memory audit_entries for one tenant, enough to run
// the use case without Postgres (the integration test runs it on the
// real table).
type memStore struct {
	tenant  uuid.UUID
	entries []domain.Entry
}

func (m *memStore) InChain(ctx context.Context, fn func(context.Context, app.Chain) error) error {
	snapshot := append([]domain.Entry(nil), m.entries...)
	if err := fn(ctx, m); err != nil {
		m.entries = snapshot // the transaction rolls back
		return err
	}
	return nil
}

func (m *memStore) Entries(context.Context, int64, int) ([]domain.Entry, error) {
	return m.entries, nil
}
func (m *memStore) OutboxEntries(context.Context) (int64, error) { return 0, nil }
func (m *memStore) Tenant() uuid.UUID                            { return m.tenant }

func (m *memStore) Head(context.Context) (domain.Head, error) {
	if len(m.entries) == 0 {
		return domain.Head{}, nil
	}
	return m.entries[len(m.entries)-1].Head(), nil
}

func (m *memStore) Recorded(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, e := range m.entries {
		for _, id := range ids {
			if e.EventID == id {
				out[id] = true
			}
		}
	}
	return out, nil
}

func (m *memStore) Insert(_ context.Context, e domain.Entry) error {
	m.entries = append(m.entries, e)
	return nil
}

func (m *memStore) InsertAll(_ context.Context, es []domain.Entry) error {
	m.entries = append(m.entries, es...)
	return nil
}

func digestOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var importProject = uuid.MustParse("0190a1b2-0000-7000-8000-000000000001")

func history(rows ...app.V0HistoryEntry) app.V0HistoryImport {
	return app.V0HistoryImport{Restore: "glossa-v03.sql.gz", RestoreSHA256: digestOf("dump"), Entries: rows}
}

func row(id string) app.V0HistoryEntry {
	return app.V0HistoryEntry{
		V0ID: id, Action: domain.ActionV0TranslationChanged, Actor: "v0:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d",
		OccurredAt: time.Date(2025, 3, 14, 15, 9, 0, 0, time.UTC), Project: importProject,
		Key: "greeting", Locale: "de", BeforeSHA256: digestOf("Hallo"), AfterSHA256: digestOf("Hallo!"),
	}
}

func newImporter() (*app.Service, *memStore, tenancy.ID) {
	tenant := tenancy.ID(uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"))
	store := &memStore{tenant: tenant.UUID()}
	return app.New(store), store, tenant
}

// Only an owner writes v0.3's history into the trail (RFC 0006 §7.2,
// amended in wave 4): an admin cannot, no token can whatever its
// scopes, CI cannot, and neither can an owner limited to some projects,
// because the trail is the tenant's.
func TestOnlyAnOwnerImportsHistory(t *testing.T) {
	svc, store, tenant := newImporter()
	ctx := context.Background()
	refused := map[string]context.Context{
		"an admin":                 authztest.Member(ctx, tenant, []string{"admin"}),
		"a developer":              authztest.Member(ctx, tenant, []string{"developer"}),
		"a token with every scope": authztest.Token(ctx, tenant, "read", "write", "publish", "admin", "workflows"),
		"a CI token":               authztest.CIToken(ctx, tenant),
		"an owner in one project":  authztest.ScopedMember(ctx, tenant, []uuid.UUID{importProject}, []string{"owner"}),
		"nobody":                   tenancy.ContextWithTenant(ctx, tenant),
	}
	for who, c := range refused {
		_, err := svc.ImportV0History(c, history(row("1")))
		if !errors.Is(err, authz.ErrForbidden) && !errors.Is(err, authz.ErrUnauthenticated) {
			t.Errorf("%s: err = %v, want a refusal", who, err)
		}
	}
	if len(store.entries) != 0 {
		t.Fatalf("a refused import recorded %d entries", len(store.entries))
	}
	owner := authztest.Member(ctx, tenant, []string{"owner"})
	r, err := svc.ImportV0History(owner, history(row("1")))
	if err != nil || r.Recorded != 1 {
		t.Fatalf("the owner's import: %+v, %v", r, err)
	}
	// Background work never holds it either.
	if _, err := authz.Background(tenancy.ContextWithTenant(ctx, tenant), "audit.import_job", authz.AuditImport); err == nil {
		t.Error("a background principal was given audit.import")
	}
}

func TestImportingTwiceRecordsEachRowOnce(t *testing.T) {
	svc, store, tenant := newImporter()
	owner := authztest.Member(context.Background(), tenant, []string{"owner"})
	r, err := svc.ImportV0History(owner, history(row("1"), row("2"), row("2")))
	if err != nil || r.Recorded != 2 || r.Existing != 0 {
		t.Fatalf("first import: %+v, %v", r, err)
	}
	// A second project's import of the same tenant carries the rows whose
	// translation is gone again, and a retried batch carries all of it.
	unresolved := row("3")
	unresolved.Key, unresolved.Locale, unresolved.Unresolved = "", "", "translation_deleted"
	r, err = svc.ImportV0History(owner, history(row("1"), row("2"), unresolved))
	if err != nil || r.Recorded != 1 || r.Existing != 2 {
		t.Fatalf("second import: %+v, %v", r, err)
	}
	r, err = svc.ImportV0History(owner, history(unresolved))
	if err != nil || r.Recorded != 0 || r.Existing != 1 {
		t.Fatalf("third import: %+v, %v", r, err)
	}
	if len(store.entries) != 3 {
		t.Fatalf("%d entries, want 3", len(store.entries))
	}
	if err := domain.Verify(tenant.UUID(), domain.Head{}, store.entries); err != nil {
		t.Errorf("the chain does not verify: %v", err)
	}
	for _, e := range store.entries {
		if e.Source != domain.SourceImport || e.Action != domain.ActionV0TranslationChanged {
			t.Errorf("entry %d: %s %s", e.Sequence, e.Source, e.Action)
		}
	}
}

// One bad row refuses the whole call, naming it; nothing is recorded.
func TestOneMalformedRowRefusesTheCall(t *testing.T) {
	svc, store, tenant := newImporter()
	owner := authztest.Member(context.Background(), tenant, []string{"owner"})
	bad := row("2")
	bad.AfterSHA256 = "Geht's gut?"
	other := row("3")
	other.Action = "v0.user.created"
	_, err := svc.ImportV0History(owner, history(row("1"), bad, other))
	if !errors.Is(err, domain.ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
	for _, want := range []string{"2 of 3", "entry 1:", "after_sha256", "entry 2:", "v0.user.created"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "Geht's") {
		t.Errorf("the refusal repeats the text it refused: %v", err)
	}
	if len(store.entries) != 0 {
		t.Errorf("%d entries recorded", len(store.entries))
	}
	rows := make([]app.V0HistoryEntry, app.MaxV0HistoryEntries+1)
	if _, err := svc.ImportV0History(owner, history(rows...)); !errors.Is(err, app.ErrTooManyV0Entries) {
		t.Errorf("an oversized call: %v", err)
	}
}
