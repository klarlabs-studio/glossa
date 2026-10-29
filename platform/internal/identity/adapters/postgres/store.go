// Package postgres implements Identity's persistence ports on the
// kernel's unit of work: the application's Transactor (system and tenant
// stores over sqlc) and auth-go's repository ports (sessions, links,
// TOTP, passkeys, lockout), each running in system scope.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// systemScope names Identity's tenantless transactions.
var systemScope = db.NewSystemScope("identity.auth")

// Transactor implements app.Transactor.
type Transactor struct {
	uow    *db.UnitOfWork
	cipher authgo.SecretCipher
}

// NewTransactor returns a Transactor on uow. cipher seals TOTP secrets
// at rest (auth-go aesgcm).
func NewTransactor(uow *db.UnitOfWork, cipher authgo.SecretCipher) *Transactor {
	return &Transactor{uow: uow, cipher: cipher}
}

// InSystem implements app.Transactor.
func (t *Transactor) InSystem(ctx context.Context, fn func(context.Context, app.SystemStore) error) error {
	return t.uow.InSystemTx(ctx, systemScope, func(ctx context.Context, tx *db.SystemTx) error {
		return fn(ctx, &systemStore{q: identitysql.New(tx), cipher: t.cipher})
	})
}

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.TenantStore) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &tenantStore{tx: tx, q: identitysql.New(tx)})
	})
}

// Constraint names from migrations 0002 and 0041 and the kernel's 0001.
var uniqueErrors = map[string]error{
	"identity_people_email_key":                app.ErrEmailTaken,
	"identity_members_tenant_id_email_key":     app.ErrDuplicate,
	"identity_members_tenant_id_person_id_key": app.ErrDuplicate,
	"tenants_slug_key":                         app.ErrSlugTaken,
	// 0041: vendor and group names are unique per tenant, ignoring case.
	"identity_vendors_name":       app.ErrNameTaken,
	"identity_groups_name":        app.ErrNameTaken,
	"identity_group_members_pkey": domain.ErrAlreadyInGroup,
	// Two requests with one idempotency key race for the same ID.
	"tenants_pkey": app.ErrIdempotencyBusy,
	// 0054: a fresh user code that a pending authorization already
	// holds; the start draws another.
	"identity_device_authorizations_pending_user_code": app.ErrUserCodeTaken,
}

// storeError maps storage errors to the application's.
func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if mapped, ok := uniqueErrors[pgErr.ConstraintName]; ok {
			return mapped
		}
	}
	return err
}

func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func timestamptzNow() pgtype.Timestamptz {
	now := time.Now().UTC()
	return timestamptz(&now)
}

func pgInt8(v int64) pgtype.Int8 { return pgtype.Int8{Int64: v, Valid: true} }

func nullUUID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

func publish(ctx context.Context, tx *db.TenantTx, e outbox.Event) error {
	_, err := outbox.Publish(ctx, tx, e)
	return err
}
