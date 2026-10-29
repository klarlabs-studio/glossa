// Package tenantpg persists kernel tenants in Postgres. Both functions
// take a tenant-scoped transaction, so row-level security — not the
// caller — decides which tenant row they touch.
//
// Creating a tenant (Identity's registration flow) scopes the
// transaction to the new tenant's own ID first:
//
//	t, _ := tenancy.NewTenant(tenancy.KindIndividual, slug, name)
//	ctx = tenancy.ContextWithTenant(ctx, t.ID)
//	err := uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
//		t, err = tenantpg.Insert(ctx, tx, t)
//		// … create the first member in the same transaction …
//		return err
//	})
package tenantpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy/tenancysql"
)

// ErrNotFound means the transaction's tenant has no row.
var ErrNotFound = errors.New("tenantpg: tenant not found")

// ErrScopeMismatch means the tenant being inserted is not the one the
// transaction is scoped to.
var ErrScopeMismatch = errors.New("tenantpg: transaction scoped to a different tenant")

// Insert stores t and returns it with CreatedAt set.
func Insert(ctx context.Context, tx *db.TenantTx, t tenancy.Tenant) (tenancy.Tenant, error) {
	if tx.Tenant() != t.ID {
		return tenancy.Tenant{}, ErrScopeMismatch
	}
	created, err := tenancysql.New(tx).InsertTenant(ctx, tenancysql.InsertTenantParams{
		ID:   t.ID.UUID(),
		Kind: string(t.Kind),
		Slug: string(t.Slug),
		Name: t.Name,
	})
	if err != nil {
		return tenancy.Tenant{}, fmt.Errorf("tenantpg: insert %s: %w", t.Slug, err)
	}
	t.CreatedAt = created
	return t, nil
}

// Current loads the tenant the transaction is scoped to.
func Current(ctx context.Context, tx *db.TenantTx) (tenancy.Tenant, error) {
	row, err := tenancysql.New(tx).GetCurrentTenant(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.Tenant{}, ErrNotFound
	}
	if err != nil {
		return tenancy.Tenant{}, fmt.Errorf("tenantpg: load current tenant: %w", err)
	}
	return tenancy.Tenant{
		ID:        tenancy.ID(row.ID),
		Kind:      tenancy.Kind(row.Kind),
		Slug:      tenancy.Slug(row.Slug),
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
	}, nil
}
