package db

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

var (
	// ErrNoTenant means a tenant-scoped transaction was requested on a
	// context that carries no tenant (see tenancy.Middleware).
	ErrNoTenant = errors.New("db: no tenant in context")
	// ErrTenantInSystemScope means a tenant-scoped context (a request)
	// tried to open a system-scope transaction. System scope is for
	// tenantless background paths only.
	ErrTenantInSystemScope = errors.New("db: system scope requested from a tenant context")
	// ErrInvalidScope means the SystemScope was zero or badly named.
	ErrInvalidScope = errors.New("db: invalid system scope")
	// ErrNestedTx means a unit of work was started inside another. The
	// inner one would run on a second connection and commit on its own,
	// so it is refused; pass the outer transaction down instead.
	ErrNestedTx = errors.New("db: nested unit of work")
	// ErrNoTx means InCurrentTenantTx found no tenant transaction of the
	// context's tenant to join.
	ErrNoTx = errors.New("db: no tenant transaction to join")
)

// Role names provisioned by migration 0001.
const (
	systemRole = "glossa_system"
	tenantGUC  = "app.tenant_id"
)

// Querier is what sqlc-generated code needs (sqlc's DBTX for pgx/v5).
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error)
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// txQuerier exposes a transaction's statements but not Commit or
// Rollback: the unit of work owns the transaction's outcome.
type txQuerier struct{ tx pgx.Tx }

func (q txQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return q.tx.Exec(ctx, sql, args...)
}

func (q txQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.tx.Query(ctx, sql, args...)
}

func (q txQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.tx.QueryRow(ctx, sql, args...)
}

func (q txQuerier) CopyFrom(ctx context.Context, t pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error) {
	return q.tx.CopyFrom(ctx, t, cols, src)
}

func (q txQuerier) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return q.tx.SendBatch(ctx, b)
}

// TenantTx is a transaction scoped to one tenant: row-level security
// admits only that tenant's rows. Pass it to sqlc's New(…).
type TenantTx struct {
	txQuerier
	tenant tenancy.ID
}

// Tenant returns the tenant the transaction is scoped to.
func (t *TenantTx) Tenant() tenancy.ID { return t.tenant }

// SystemTx is a transaction running as glossa_system. It reaches only
// the tables a migration has explicitly opened to that role.
type SystemTx struct {
	txQuerier
	scope SystemScope
}

// Scope returns the declared purpose of the transaction.
func (t *SystemTx) Scope() SystemScope { return t.scope }

// SystemScope names a tenantless background path, such as the outbox
// relay. Declare one per path; the name appears in errors and traces.
type SystemScope struct{ name string }

var scopeName = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,62}$`)

// NewSystemScope declares a system scope. Names are lowercase
// dot-separated identifiers, e.g. "outbox.relay".
func NewSystemScope(name string) SystemScope { return SystemScope{name: name} }

// String returns the scope name.
func (s SystemScope) String() string { return s.name }

func (s SystemScope) valid() bool { return scopeName.MatchString(s.name) }

// UnitOfWork runs functions inside transactions with the tenancy
// context row-level security depends on.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork returns a unit of work on the application pool.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork { return &UnitOfWork{pool: pool} }

// InTenantTx runs fn in a transaction scoped to the context's tenant.
// It commits if fn returns nil and rolls back otherwise (and on panic).
// The tenant comes only from ctx; there is deliberately no parameter
// for it.
func (u *UnitOfWork) InTenantTx(ctx context.Context, fn func(context.Context, *TenantTx) error) error {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return ErrNoTenant
	}
	return u.run(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", tenantGUC, tenant.String()); err != nil {
			return fmt.Errorf("db: scope transaction to tenant: %w", err)
		}
		ttx := &TenantTx{txQuerier: txQuerier{tx}, tenant: tenant}
		return fn(context.WithValue(ctx, inTxKey{}, ttx), ttx)
	})
}

// InCurrentTenantTx runs fn in the tenant transaction ctx is already
// inside, so one context's in-process port can keep its own tables in
// step with its caller's write — in the same commit, rolled back with
// it. It is an explicit join, not a nested unit of work: fn's work
// commits only when the caller's transaction does, and a failure fails
// the caller. It refuses (ErrNoTx) a context outside a tenant
// transaction or whose tenant differs from the transaction's.
func (u *UnitOfWork) InCurrentTenantTx(ctx context.Context, fn func(context.Context, *TenantTx) error) error {
	ttx, ok := ctx.Value(inTxKey{}).(*TenantTx)
	tenant, hasTenant := tenancy.FromContext(ctx)
	if !ok || !hasTenant || ttx.tenant != tenant {
		return ErrNoTx
	}
	return fn(ctx, ttx)
}

// InSystemTx runs fn in a transaction as glossa_system, for tenantless
// background paths (the outbox relay, admin jobs). Misuse is contained
// three ways: it refuses a context that carries a tenant, it requires a
// declared scope, and the role it switches to sees only tables a
// migration explicitly grants it, with no tenant GUC set.
func (u *UnitOfWork) InSystemTx(ctx context.Context, scope SystemScope, fn func(context.Context, *SystemTx) error) error {
	if !scope.valid() {
		return fmt.Errorf("%w: %q", ErrInvalidScope, scope.name)
	}
	if _, ok := tenancy.FromContext(ctx); ok {
		return fmt.Errorf("%w (scope %s)", ErrTenantInSystemScope, scope.name)
	}
	return u.run(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+systemRole); err != nil {
			return fmt.Errorf("db: enter system scope %s: %w", scope.name, err)
		}
		return fn(ctx, &SystemTx{txQuerier: txQuerier{tx}, scope: scope})
	})
}

// inTxKey marks a context inside a unit of work: the *TenantTx of a
// tenant transaction (which InCurrentTenantTx joins), or inSystemTx.
type inTxKey struct{}

type inSystemTx struct{}

// run owns the transaction lifecycle shared by both scopes.
func (u *UnitOfWork) run(ctx context.Context, fn func(context.Context, pgx.Tx) error) (err error) {
	if ctx.Value(inTxKey{}) != nil {
		return ErrNestedTx
	}
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			rollback(ctx, tx)
		}
	}()

	if err := fn(context.WithValue(ctx, inTxKey{}, inSystemTx{}), tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	committed = true
	return nil
}

// rollback runs even when ctx is already cancelled, so the connection
// goes back to the pool clean instead of being discarded mid-transaction.
func rollback(ctx context.Context, tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
