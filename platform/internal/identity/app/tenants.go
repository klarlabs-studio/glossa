package app

import (
	"context"
	"errors"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// ListTenants lists the tenants the caller can act in: a person's active
// memberships, or an API token's own tenant.
func (s *Service) ListTenants(ctx context.Context, page pagination.Page) ([]tenancy.Tenant, *string, error) {
	p, ok := authz.From(ctx)
	if !ok {
		return nil, nil, ErrUnauthenticated
	}
	id, err := parseAfter(page.After)
	if err != nil {
		return nil, nil, err
	}
	after := tenancy.ID(id)
	var out []tenancy.Tenant
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		if p.Actor.Kind == domain.ActorToken {
			if !after.IsZero() { // a token's list has one page
				return nil
			}
			t, err := st.Tenant(ctx, p.TokenTenant)
			out = append(out, t)
			return err
		}
		ms, err := st.MembershipsOf(ctx, p.Person, after, page.Limit())
		for _, m := range ms {
			out = append(out, m.Tenant)
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(out, page, func(t tenancy.Tenant) string { return t.ID.String() })
	return items, next, nil
}

// CreateOrganization creates an organization owned by the calling
// person. With an idempotency key, a retry returns the organization the
// first request created (replayed = true).
func (s *Service) CreateOrganization(ctx context.Context, slug, name, idemKey string) (t tenancy.Tenant, replayed bool, err error) {
	p, ok := authz.From(ctx)
	if !ok {
		return tenancy.Tenant{}, false, ErrUnauthenticated
	}
	if p.Person.IsZero() {
		return tenancy.Tenant{}, false, ErrPersonOnly
	}
	t, err = tenancy.NewTenant(tenancy.KindOrganization, slug, name)
	if err != nil {
		return tenancy.Tenant{}, false, err
	}
	if idemKey != "" {
		if err := checkIdempotencyKey(idemKey); err != nil {
			return tenancy.Tenant{}, false, err
		}
		t.ID = tenancy.ID(idempotentID("tenant.create", "", p.Actor, idemKey))
	}
	owner, err := s.Person(ctx, p.Person)
	if err != nil {
		return tenancy.Tenant{}, false, err
	}
	tctx := tenancy.ContextWithTenant(ctx, t.ID)
	err = s.tx.InTenant(tctx, func(ctx context.Context, st TenantStore) error {
		existing, err := st.CurrentTenant(ctx)
		if err == nil {
			// Only this person with this key derives this ID: it's a retry.
			if existing.Slug != t.Slug || existing.Name != t.Name {
				return ErrIdempotencyReuse
			}
			t, replayed = existing, true
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		m := domain.NewOwner(t.ID, owner.ID, owner.Email, s.now())
		if err := s.createTenantWithOwner(ctx, st, t, m); err != nil {
			return err
		}
		t, err = st.CurrentTenant(ctx)
		return err
	})
	if err != nil {
		return tenancy.Tenant{}, false, err
	}
	return t, replayed, nil
}

// GetTenant returns the context's tenant.
func (s *Service) GetTenant(ctx context.Context) (tenancy.Tenant, error) {
	if err := authz.Require(ctx, authz.TenantRead); err != nil {
		return tenancy.Tenant{}, err
	}
	var t tenancy.Tenant
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		var err error
		t, err = st.CurrentTenant(ctx)
		return err
	})
	return t, err
}
