package postgres

import (
	"context"
	"errors"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

// InCurrentTenant runs fn on Identity's tenant store inside the tenant
// transaction ctx is already in, or in a new one when it is in none.
//
// It is for another context's in-process read of who people are —
// Workflow resolving a group, a vendor or a member's affiliations while
// it steps an instance or filters a read path — which may run inside
// that context's own transaction, where a second unit of work would be
// refused as nested (db.ErrNestedTx). Joining reads in the caller's
// snapshot; fn must only read.
func (t *Transactor) InCurrentTenant(ctx context.Context, fn func(context.Context, app.TenantStore) error) error {
	run := func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &tenantStore{tx: tx, q: identitysql.New(tx)})
	}
	err := t.uow.InCurrentTenantTx(ctx, run)
	if errors.Is(err, db.ErrNoTx) {
		return t.uow.InTenantTx(ctx, run)
	}
	return err
}
