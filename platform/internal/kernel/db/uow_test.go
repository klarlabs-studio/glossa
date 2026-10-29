package db_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// These guards reject misuse before a connection is touched, so a nil
// pool is enough: reaching the pool would panic and fail the test.

func TestInTenantTxRequiresTenant(t *testing.T) {
	uow := db.NewUnitOfWork(nil)
	err := uow.InTenantTx(context.Background(), func(context.Context, *db.TenantTx) error {
		t.Fatal("fn ran without a tenant")
		return nil
	})
	if !errors.Is(err, db.ErrNoTenant) {
		t.Fatalf("err = %v, want ErrNoTenant", err)
	}
}

func TestInSystemTxRejectsTenantContext(t *testing.T) {
	uow := db.NewUnitOfWork(nil)
	ctx := tenancy.ContextWithTenant(context.Background(), tenancy.NewID())
	err := uow.InSystemTx(ctx, db.NewSystemScope("outbox.relay"), func(context.Context, *db.SystemTx) error {
		t.Fatal("fn ran in system scope from a tenant request")
		return nil
	})
	if !errors.Is(err, db.ErrTenantInSystemScope) {
		t.Fatalf("err = %v, want ErrTenantInSystemScope", err)
	}
}

func TestInSystemTxRejectsInvalidScope(t *testing.T) {
	uow := db.NewUnitOfWork(nil)
	for _, scope := range []db.SystemScope{{}, db.NewSystemScope("Has Spaces"), db.NewSystemScope("")} {
		err := uow.InSystemTx(context.Background(), scope, func(context.Context, *db.SystemTx) error {
			t.Fatal("fn ran with an invalid scope")
			return nil
		})
		if !errors.Is(err, db.ErrInvalidScope) {
			t.Errorf("scope %q: err = %v, want ErrInvalidScope", scope, err)
		}
	}
}

func TestSystemScopeName(t *testing.T) {
	if got := db.NewSystemScope("outbox.relay").String(); got != "outbox.relay" {
		t.Errorf("String() = %q", got)
	}
}
