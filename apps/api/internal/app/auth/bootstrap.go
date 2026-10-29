package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/domain/tenant"
	"go.klarlabs.de/glossa/apps/api/internal/domain/user"
)

// Bootstrap idempotently seeds a tenant + admin user from env-
// supplied config so a fresh deploy can reach first login without
// psql gymnastics. Re-running is a no-op once an admin exists for
// the target tenant; the function never mutates an existing admin.
//
// Inputs are all required when running — caller decides whether
// to call this at all (cmd/api skips it when the env vars are
// empty).
type BootstrapInput struct {
	TenantSlug    string
	TenantName    string
	AdminEmail    string
	AdminPassword string
}

// Bootstrap creates the tenant + admin if and only if no admin
// currently exists for that tenant. Returns nil on either "did
// the work" or "nothing to do" — the audit trail is the log line
// the caller emits with the returned action.
type BootstrapAction string

const (
	BootstrapNoop   BootstrapAction = "noop"
	BootstrapSeeded BootstrapAction = "seeded"
)

// Bootstrap creates/looks-up the tenant and ensures it has at
// least one admin user. Returns BootstrapSeeded when it actually
// wrote rows, BootstrapNoop when an admin already existed.
func Bootstrap(
	ctx context.Context,
	tenants tenant.Repository,
	users user.Repository,
	in BootstrapInput,
) (BootstrapAction, error) {
	slug, err := tenant.NewSlug(in.TenantSlug)
	if err != nil {
		return BootstrapNoop, fmt.Errorf("bootstrap: %w", err)
	}
	name, err := tenant.NewName(in.TenantName)
	if err != nil {
		return BootstrapNoop, fmt.Errorf("bootstrap: %w", err)
	}
	email, err := user.NormalizeEmail(in.AdminEmail)
	if err != nil {
		return BootstrapNoop, fmt.Errorf("bootstrap: %w", err)
	}

	// Tenant: lookup-or-create. Tenant table's RLS policy keys off
	// id == app.current_tenant, so we can't read it without first
	// claiming an identity — caller wires this path against a
	// BYPASSRLS-capable connection (the bootstrap user IS the
	// owner here; the migration role runs once at startup).
	t, err := tenants.FindBySlug(ctx, slug)
	if err != nil {
		if !isNotFound(err) {
			return BootstrapNoop, fmt.Errorf("bootstrap find tenant: %w", err)
		}
		// Need to create. tenants.Save now writes the caller-supplied
		// ID through (matched the CreateTenant query to take id
		// explicitly), so t.ID stays correct for the user insert below.
		t = tenant.Tenant{ID: uuid.New(), Slug: slug, Name: name}
		if err := tenants.Save(ctx, t); err != nil {
			return BootstrapNoop, fmt.Errorf("bootstrap save tenant: %w", err)
		}
	}

	count, err := users.CountAdmins(ctx, t.ID)
	if err != nil {
		return BootstrapNoop, fmt.Errorf("bootstrap count admins: %w", err)
	}
	if count > 0 {
		return BootstrapNoop, nil
	}

	hash, err := HashPassword(in.AdminPassword)
	if err != nil {
		return BootstrapNoop, fmt.Errorf("bootstrap hash: %w", err)
	}
	_, err = users.Save(ctx, user.User{
		TenantID:     t.ID,
		Email:        email,
		PasswordHash: hash,
		Role:         user.RoleAdmin,
		// users.locales is NOT NULL DEFAULT '{}'; pgx encodes a
		// nil Go slice as NULL, which violates the constraint.
		// Empty []string round-trips as the empty array literal.
		Locales: []string{},
	})
	if err != nil {
		return BootstrapNoop, fmt.Errorf("bootstrap save admin: %w", err)
	}
	return BootstrapSeeded, nil
}

// isNotFound matches against the sqlcadapter not-found sentinel
// without importing it (would be a layering violation — auth is
// app-layer, sqlcadapter is infra). Repos return their own
// not-found sentinel; we compare by error chain.
func isNotFound(err error) bool {
	type notFounder interface{ Error() string }
	var nf notFounder
	if errors.As(err, &nf) && nf.Error() != "" {
		// All our infra errors stringify with "not found" — cheap
		// substring match keeps the check decoupled from the
		// concrete error type.
		return contains(nf.Error(), "not found")
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
