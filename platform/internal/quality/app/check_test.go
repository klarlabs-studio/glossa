package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// RunCheck is the server's side of `glossa check`: it reads the project
// through the Snapshot port and hands it to the same layers, and it
// stores nothing — which is what lets a reader run one.

type snapshotPort struct {
	project uuid.UUID
	snap    app.ProjectSnapshot
	calls   int
}

func (s *snapshotPort) Snapshot(_ context.Context, project uuid.UUID) (app.ProjectSnapshot, error) {
	if project != s.project {
		return app.ProjectSnapshot{}, app.ErrProjectNotFound
	}
	s.calls++
	return s.snap, nil
}

// checkCatalog is Quality's Catalog port: one project exists.
type checkCatalog struct{ id uuid.UUID }

func (c checkCatalog) Project(_ context.Context, project uuid.UUID) error {
	if project != c.id {
		return app.ErrProjectNotFound
	}
	return nil
}

// The rest of Quality's Catalog port is the policy side, which RunCheck
// never reaches: a check grades against the policy its caller passes,
// not one it fetches. These answer the zero policy so the fake
// satisfies the port without pretending to a behaviour under test.

func (c checkCatalog) MessageIDs(
	_ context.Context, project uuid.UUID, _ []string,
) (map[string]uuid.UUID, error) {
	if project != c.id {
		return nil, app.ErrProjectNotFound
	}
	return nil, nil
}

func (c checkCatalog) CheckPolicy(_ context.Context, project uuid.UUID) (app.StoredPolicy, error) {
	if project != c.id {
		return app.StoredPolicy{}, app.ErrProjectNotFound
	}
	return app.StoredPolicy{}, nil
}

func (c checkCatalog) SaveCheckPolicy(
	_ context.Context, project uuid.UUID, _ int, _ checkpolicy.Policy,
) error {
	if project != c.id {
		return app.ErrProjectNotFound
	}
	return nil
}

func (c checkCatalog) OpenPullRequests(_ context.Context, project uuid.UUID) (map[string]int, error) {
	if project != c.id {
		return nil, app.ErrProjectNotFound
	}
	return nil, nil
}

// failingStore is Quality's persistence, and every method on it panics:
// RunCheck must never reach storage, because it records nothing.
type failingStore struct{ app.Store }

type checkTransactor struct{ t *testing.T }

func (tx checkTransactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	tx.t.Fatal("RunCheck opened a transaction; it stores nothing")
	return fn(ctx, failingStore{})
}

func newCheckService(t *testing.T, opts ...app.Option) (*app.Service, *snapshotPort, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	port := &snapshotPort{project: id, snap: app.ProjectSnapshot{
		// The fixture of run_test.go, read as if from the server.
		Project: project(t), Policy: checkpolicy.Policy{},
	}}
	opts = append([]app.Option{app.WithSnapshot(port)}, opts...)
	return app.NewService(checkTransactor{t: t}, checkCatalog{id: id}, opts...), port, id
}

// A read-only token runs a check: it needs `catalog.read` and nothing
// more (RFC 0005 §7.3, §12.6 case 6).
func TestRunCheckNeedsOnlyCatalogRead(t *testing.T) {
	svc, port, project := newCheckService(t)
	ctx := authztest.Token(context.Background(), tenancy.NewID(), "read")

	rep, err := svc.RunCheck(ctx, project, app.CheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if port.calls != 1 {
		t.Fatalf("the snapshot was read %d times", port.calls)
	}
	if rep.Conclusion != domain.ConclusionFailure || len(rep.Findings) == 0 {
		t.Fatalf("report = %s with %d findings", rep.Conclusion, len(rep.Findings))
	}
	if len(rep.Layers) != len(layers.Default()) {
		t.Fatalf("layers = %v, want every deterministic layer", rep.Layers)
	}
}

// A caller without even that is refused, and the refusal is
// authorization's, not a 404.
func TestRunCheckRefusesACallerWithoutCatalogRead(t *testing.T) {
	svc, _, project := newCheckService(t)
	ctx := authz.WithPrincipal(tenancy.ContextWithTenant(context.Background(), tenancy.NewID()), authz.Principal{})
	if _, err := svc.RunCheck(ctx, project, app.CheckRequest{}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

// A project this caller cannot see is not found, whether Catalog or the
// snapshot says so.
func TestRunCheckOnAnUnknownProjectIsNotFound(t *testing.T) {
	svc, _, _ := newCheckService(t)
	ctx := authztest.Token(context.Background(), tenancy.NewID(), "read")
	if _, err := svc.RunCheck(ctx, uuid.New(), app.CheckRequest{}); !errors.Is(err, app.ErrProjectNotFound) {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

// Naming layers runs those layers, and only those: a caller that asked
// for one must not be told the others are clean.
func TestRunCheckRunsOnlyTheLayersNamed(t *testing.T) {
	svc, _, project := newCheckService(t)
	ctx := authztest.Token(context.Background(), tenancy.NewID(), "read")

	rep, err := svc.RunCheck(ctx, project, app.CheckRequest{Layers: []string{"completeness"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Layers) != 1 || rep.Layers[0] != domain.LayerCompleteness {
		t.Fatalf("layers = %v, want completeness alone", rep.Layers)
	}
	for _, f := range rep.Findings {
		if f.Layer != domain.LayerCompleteness {
			t.Fatalf("a %s finding came from a layer that was not asked for", f.Layer)
		}
	}
}

// A layer this check does not compute is an invalid query, not a
// silently smaller check.
func TestRunCheckRefusesAnUnknownLayer(t *testing.T) {
	svc, _, project := newCheckService(t)
	ctx := authztest.Token(context.Background(), tenancy.NewID(), "read")
	_, err := svc.RunCheck(ctx, project, app.CheckRequest{Layers: []string{"completeness", "visual"}})
	if !errors.Is(err, app.ErrInvalidQuery) {
		t.Fatalf("err = %v, want ErrInvalidQuery", err)
	}
}

// Without the snapshot port a deployment cannot check on the server,
// and says so rather than reporting an empty project as clean.
func TestRunCheckWithoutASnapshotSaysSo(t *testing.T) {
	project := uuid.New()
	svc := app.NewService(checkTransactor{t: t}, checkCatalog{id: project})
	ctx := authztest.Token(context.Background(), tenancy.NewID(), "read")
	if _, err := svc.RunCheck(ctx, project, app.CheckRequest{}); !errors.Is(err, app.ErrNoSnapshot) {
		t.Fatalf("err = %v, want ErrNoSnapshot", err)
	}
}
