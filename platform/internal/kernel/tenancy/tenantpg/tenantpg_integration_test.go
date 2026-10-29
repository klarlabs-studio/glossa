//go:build integration

package tenantpg_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy/tenantpg"
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

func TestInsertAndCurrent(t *testing.T) {
	uow := db.NewUnitOfWork(env.App)
	tn, err := tenancy.NewTenant(tenancy.KindIndividual, "ada", "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.ContextWithTenant(context.Background(), tn.ID)

	err = uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		stored, err := tenantpg.Insert(ctx, tx, tn)
		if err == nil && stored.CreatedAt.IsZero() {
			t.Error("CreatedAt not set")
		}
		return err
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	err = uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		got, err := tenantpg.Current(ctx, tx)
		if err == nil && (got.ID != tn.ID || got.Slug != "ada" || got.Kind != tenancy.KindIndividual) {
			t.Errorf("Current = %+v", got)
		}
		return err
	})
	if err != nil {
		t.Fatalf("current: %v", err)
	}
}

func TestInsertRefusesAnotherTenant(t *testing.T) {
	uow := db.NewUnitOfWork(env.App)
	other, _ := tenancy.NewTenant(tenancy.KindOrganization, "other", "Other")
	ctx := tenancy.ContextWithTenant(context.Background(), tenancy.NewID())

	err := uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tenantpg.Insert(ctx, tx, other)
		return err
	})
	if !errors.Is(err, tenantpg.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

func TestCurrentWithoutRow(t *testing.T) {
	uow := db.NewUnitOfWork(env.App)
	ctx := tenancy.ContextWithTenant(context.Background(), tenancy.NewID())
	err := uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tenantpg.Current(ctx, tx)
		return err
	})
	if !errors.Is(err, tenantpg.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
