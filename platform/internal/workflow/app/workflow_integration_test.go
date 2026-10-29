//go:build integration

package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/identity"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/defaults"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
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

type harness struct {
	svc    *app.Service
	uow    *db.UnitOfWork
	tenant tenancy.ID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	return harnessFor(t, "acme")
}

func harnessFor(t *testing.T, slug string) *harness {
	t.Helper()
	tenant, err := env.SeedTenant(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	svc := app.New(postgres.NewTransactor(uow), identity.Permissions{}, app.WithClock(func() time.Time { return clock }))
	return &harness{svc: svc, uow: uow, tenant: tenant}
}

// manager acts with the permissions RFC 0006 §4.2 gives owners and
// admins for workflows. Until Identity's role matrix grants them (the
// wave-1 identity slice), a background grant is how a test holds them.
func (h *harness) manager(t *testing.T) context.Context {
	t.Helper()
	ctx, err := authz.Background(tenancy.ContextWithTenant(context.Background(), h.tenant), "test.manager",
		app.PermWorkflowsManage, app.PermWorkflowsRead)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func doc(name, subject string) []byte {
	return fmt.Appendf(nil, `{"schema":"glossa.workflow/v1","name":%q,"subject":%q,
	  "chart":{"id":%q,"initial":"waiting","states":{
	    "waiting":{"transitions":[{"event":"approval.granted","target":"done","guard":"enough"}]},
	    "done":{"type":"final"}}},
	  "guards":{"enough":{"use":"approvals_at_least","n":1}}}`, name, subject, name)
}

func TestVersionsAreAppendOnly(t *testing.T) {
	h := newHarness(t)
	ctx := h.manager(t)

	first, err := h.svc.CreateDefinition(ctx, app.NewDefinition{Document: defaults.Review()})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version.Number != 1 || first.Definition.Latest != 1 || first.Definition.CreatedBy == "" {
		t.Fatalf("first = %+v", first)
	}

	edited := []byte(`{"schema":"glossa.workflow/v1","name":"review","subject":"translation",
	  "chart":{"id":"review","initial":"awaiting_review","states":{
	    "awaiting_review":{"transitions":[{"event":"translation.reviewed","target":"reviewed"},
	                                      {"event":"translation.outdated","target":"reviewed"}]},
	    "reviewed":{"type":"final"}}}}`)
	second, err := h.svc.SaveVersion(ctx, first.Definition.ID, 1, edited)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version.Number != 2 || second.Definition.Latest != 2 {
		t.Fatalf("second = %+v", second)
	}

	// Saving on top of a version someone else has replaced is a
	// conflict, not an overwrite.
	if _, err := h.svc.SaveVersion(ctx, first.Definition.ID, 1, edited); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("stale save: err = %v, want ErrConflict", err)
	}
	// A refused document stores nothing.
	if _, err := h.svc.SaveVersion(ctx, first.Definition.ID, 2, []byte(`{"schema":"glossa.workflow/v1"}`)); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("invalid save: err = %v", err)
	}
	// Renaming is a new definition, not a version.
	if _, err := h.svc.SaveVersion(ctx, first.Definition.ID, 2, doc("other", "translation")); !errors.Is(err, domain.ErrRenamed) {
		t.Fatalf("rename: err = %v", err)
	}

	vs, err := h.svc.Versions(ctx, first.Definition.ID)
	if err != nil || len(vs) != 2 || vs[0].Number != 2 || vs[1].Number != 1 {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	v1, err := h.svc.Version(ctx, first.Definition.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Version 1 is the default as it was saved: it loads to the same
	// chart, state for state.
	want, err := domain.Compile(defaults.Review())
	if err != nil {
		t.Fatal(err)
	}
	if loaded, err := v1.Load(); err != nil || !slices.Equal(loaded.States(), want.States()) {
		t.Fatalf("version 1 no longer loads as saved: %v", err)
	}

	// The application role cannot rewrite history, whatever the code
	// above it does.
	err = h.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tx.Exec(ctx, `UPDATE workflow_definition_versions SET document = '{}'`)
		return err
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("UPDATE of a version: err = %v, want permission denied", err)
	}
}

func TestARefusedDefinitionIsNotStored(t *testing.T) {
	h := newHarness(t)
	ctx := h.manager(t)
	legal := []byte(`{"schema":"glossa.workflow/v1","name":"legal","subject":"translation",
	  "chart":{"id":"legal","initial":"a","states":{
	    "a":{"transitions":[{"event":"approval.granted","target":"b","guard":"signed"}]},
	    "b":{"type":"final"}}},
	  "guards":{"signed":{"use":"legal_signed_off"}}}`)
	if _, err := h.svc.CreateDefinition(ctx, app.NewDefinition{Document: legal}); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("err = %v, want ErrInvalidWorkflow", err)
	}
	// A permission Identity does not have is refused too.
	perm := []byte(`{"schema":"glossa.workflow/v1","name":"perm","subject":"translation",
	  "chart":{"id":"perm","initial":"a","states":{
	    "a":{"transitions":[{"event":"approval.granted","target":"b","guard":"may"}]},
	    "b":{"type":"final"}}},
	  "guards":{"may":{"use":"actor_has_permission","permission":"legal.signoff"}}}`)
	if _, err := h.svc.CreateDefinition(ctx, app.NewDefinition{Document: perm}); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("unknown permission: err = %v, want ErrInvalidWorkflow", err)
	}
	if ds, err := h.svc.Definitions(ctx, uuid.Nil); err != nil || len(ds) != 0 {
		t.Fatalf("definitions = %+v, %v; want none", ds, err)
	}
}

func TestNamesAreUniquePerScope(t *testing.T) {
	h := newHarness(t)
	ctx := h.manager(t)
	if _, err := h.svc.CreateDefinition(ctx, app.NewDefinition{Document: doc("four-eyes", "translation")}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateDefinition(ctx, app.NewDefinition{Document: doc("four-eyes", "translation")}); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("second tenant-wide four-eyes: err = %v, want ErrConflict", err)
	}
	// The same name scoped to a project is another definition.
	if _, err := h.svc.CreateDefinition(ctx, app.NewDefinition{ProjectID: uuid.New(), Document: doc("four-eyes", "translation")}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingsResolveWithThePolicyPrecedence(t *testing.T) {
	h := newHarness(t)
	ctx := h.manager(t)
	project := uuid.New()
	general, err := h.svc.CreateDefinition(ctx, app.NewDefinition{Document: doc("general", "translation")})
	if err != nil {
		t.Fatal(err)
	}
	german, err := h.svc.CreateDefinition(ctx, app.NewDefinition{ProjectID: project, Document: doc("german", "translation")})
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.svc.CreateDefinition(ctx, app.NewDefinition{Document: doc("release", "release_request")})
	if err != nil {
		t.Fatal(err)
	}

	// Bound most specific first, so "later wins" cannot be what picks it.
	if _, err := h.svc.Bind(ctx, app.NewBinding{ProjectID: project, Locales: []string{"de"}, DefinitionID: german.Definition.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Bind(ctx, app.NewBinding{ProjectID: project, DefinitionID: general.Definition.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Bind(ctx, app.NewBinding{ProjectID: project, DefinitionID: release.Definition.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Bind(ctx, app.NewBinding{ProjectID: project, Locales: []string{"de"}, DefinitionID: general.Definition.ID}); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("a second binding with the same selector: err = %v, want ErrConflict", err)
	}
	if _, err := h.svc.Bind(ctx, app.NewBinding{ProjectID: uuid.New(), DefinitionID: german.Definition.ID}); !errors.Is(err, app.ErrOutOfScope) {
		t.Fatalf("binding a project's definition elsewhere: err = %v, want ErrOutOfScope", err)
	}

	resolve := func(subject domain.SubjectKind, locale string) uuid.UUID {
		t.Helper()
		r, ok, err := h.svc.Resolve(ctx, domain.Target{ProjectID: project, Subject: subject, Locale: locale})
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return uuid.Nil
		}
		return r.Version.DefinitionID
	}
	if got := resolve(domain.SubjectTranslation, "de"); got != german.Definition.ID {
		t.Errorf("de resolved to %s, want the German definition", got)
	}
	if got := resolve(domain.SubjectTranslation, "fr"); got != general.Definition.ID {
		t.Errorf("fr resolved to %s, want the general definition", got)
	}
	if got := resolve(domain.SubjectReleaseRequest, ""); got != release.Definition.ID {
		t.Errorf("a release request resolved to %s, want the release definition", got)
	}

	// A new version is what new instances start on.
	if _, err := h.svc.SaveVersion(ctx, general.Definition.ID, 1, doc("general", "translation")); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := h.svc.Resolve(ctx, domain.Target{ProjectID: project, Subject: domain.SubjectTranslation, Locale: "fr"}); r.Version.Number != 2 {
		t.Errorf("resolved version %d, want 2", r.Version.Number)
	}

	// Deleting a definition drops its bindings; its versions stay.
	if err := h.svc.DeleteDefinition(ctx, german.Definition.ID); err != nil {
		t.Fatal(err)
	}
	if got := resolve(domain.SubjectTranslation, "de"); got != general.Definition.ID {
		t.Errorf("after deleting the German definition, de resolved to %s", got)
	}
	if _, err := h.svc.Version(ctx, german.Definition.ID, 1); err != nil {
		t.Errorf("a deleted definition's version: %v", err)
	}
	if bs, _ := h.svc.Bindings(ctx, project); len(bs) != 2 {
		t.Errorf("bindings = %d, want 2", len(bs))
	}
}

func TestAnotherTenantSeesNothing(t *testing.T) {
	h := newHarness(t)
	other := harnessFor(t, "other")
	saved, err := h.svc.CreateDefinition(h.manager(t), app.NewDefinition{Document: doc("mine", "translation")})
	if err != nil {
		t.Fatal(err)
	}
	octx := other.manager(t)
	if _, err := other.svc.Definition(octx, saved.Definition.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant read the definition: err = %v", err)
	}
	if _, err := other.svc.Bind(octx, app.NewBinding{ProjectID: uuid.New(), DefinitionID: saved.Definition.ID}); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant bound the definition: err = %v", err)
	}
}

func TestPermissions(t *testing.T) {
	h := newHarness(t)
	translator := authztest.Member(context.Background(), h.tenant, []string{"translator"}, "de")
	if _, err := h.svc.CreateDefinition(translator, app.NewDefinition{Document: defaults.Review()}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a translator saved a workflow: err = %v", err)
	}
	ci := authztest.CIToken(context.Background(), h.tenant)
	if _, err := h.svc.Definitions(ci, uuid.Nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a CI token read workflows: err = %v", err)
	}
}
