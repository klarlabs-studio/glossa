package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// scopeStore is the slice of Store the restriction checks reach: one
// tenant's definitions and bindings in memory. Methods it does not
// implement panic through the nil embedded interface, which would mean
// a check read something this test did not set up.
type scopeStore struct {
	app.Store
	definitions map[uuid.UUID]domain.DefinitionRecord
	bindings    map[uuid.UUID]domain.Binding
}

func (m *scopeStore) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return fn(ctx, m)
}

func (m *scopeStore) GetDefinition(_ context.Context, id uuid.UUID) (domain.DefinitionRecord, error) {
	d, ok := m.definitions[id]
	if !ok {
		return domain.DefinitionRecord{}, app.ErrNotFound
	}
	return d, nil
}

func (m *scopeStore) LockDefinition(ctx context.Context, id uuid.UUID) (domain.DefinitionRecord, error) {
	return m.GetDefinition(ctx, id)
}

func (m *scopeStore) ListDefinitions(context.Context, uuid.UUID) ([]domain.DefinitionRecord, error) {
	return nil, nil
}

func (m *scopeStore) ListVersions(context.Context, uuid.UUID) ([]domain.Version, error) {
	return nil, nil
}

func (m *scopeStore) GetBinding(_ context.Context, id uuid.UUID) (domain.Binding, error) {
	b, ok := m.bindings[id]
	if !ok {
		return domain.Binding{}, app.ErrNotFound
	}
	return b, nil
}

func (m *scopeStore) ListBindings(context.Context, uuid.UUID, domain.SubjectKind) ([]domain.Binding, error) {
	return nil, nil
}

func (m *scopeStore) DeleteBinding(context.Context, uuid.UUID) error { return nil }

func outcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, app.ErrNotFound):
		return "not found"
	case errors.Is(err, authz.ErrForbidden):
		return "denied"
	}
	return err.Error()
}

// TestWorkflowRestrictions: a project's definitions and bindings are not
// found outside a principal's scope, the tenant's own definitions are
// read by anyone who may read workflows and changed only by someone
// limited to no project, and an assigned member reads no workflow (RFC
// 0006 §3.3, §4.1).
func TestWorkflowRestrictions(t *testing.T) {
	tenant := tenancy.NewID()
	a, b := uuid.New(), uuid.New()
	tenantDef := domain.DefinitionRecord{ID: uuid.New()}
	bDef := domain.DefinitionRecord{ID: uuid.New(), ProjectID: b}
	bBinding := domain.Binding{ID: uuid.New(), ProjectID: b}
	store := &scopeStore{
		definitions: map[uuid.UUID]domain.DefinitionRecord{tenantDef.ID: tenantDef, bDef.ID: bDef},
		bindings:    map[uuid.UUID]domain.Binding{bBinding.ID: bBinding},
	}
	svc := app.New(store, nil)
	bg := context.Background()
	scoped := authztest.ScopedMember(bg, tenant, []uuid.UUID{a}, []string{"admin"})
	vendor, _ := authztest.Assigned(bg, tenant, &authztest.Coverage{}, "de")

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"scoped: its project's bindings", func() error { _, err := svc.Bindings(scoped, a); return err }, "ok"},
		{"scoped: another project's bindings", func() error { _, err := svc.Bindings(scoped, b); return err }, "not found"},
		{"scoped: another project's definitions", func() error { _, err := svc.Definitions(scoped, b); return err }, "not found"},
		{"scoped: another project's definition", func() error { _, err := svc.Definition(scoped, bDef.ID); return err }, "not found"},
		{"scoped: its versions", func() error { _, err := svc.Versions(scoped, bDef.ID); return err }, "not found"},
		{"scoped: the tenant's definition", func() error { _, err := svc.Definition(scoped, tenantDef.ID); return err }, "ok"},
		{"scoped: binds another project", func() error {
			_, err := svc.Bind(scoped, app.NewBinding{ProjectID: b, DefinitionID: tenantDef.ID})
			return err
		}, "not found"},
		{"scoped: unbinds another project's", func() error { return svc.Unbind(scoped, b, bBinding.ID) }, "not found"},
		{"scoped: deletes the tenant's definition", func() error { return svc.DeleteDefinition(scoped, tenantDef.ID) }, "denied"},
		{"scoped: creates a tenant definition", func() error {
			_, err := svc.CreateDefinition(scoped, app.NewDefinition{Document: []byte(`{}`)})
			return err
		}, "denied"},
		{"assigned: reads bindings", func() error { _, err := svc.Bindings(vendor, a); return err }, "denied"},
		{"assigned: reads a definition", func() error { _, err := svc.Definition(vendor, tenantDef.ID); return err }, "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcome(tc.call()); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
