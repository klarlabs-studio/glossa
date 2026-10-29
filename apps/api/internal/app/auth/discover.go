package auth

import (
	"context"

	"go.klarlabs.de/glossa/apps/api/internal/domain/user"
)

// DiscoverTenants resolves an email to the tenants that user
// belongs to. Empty results never reveal whether the email exists
// at all — caller must rate-limit the endpoint identically to
// /auth/login so the discovery surface isn't a free
// user-enumeration probe.
type DiscoverTenants struct {
	users user.Repository
}

func NewDiscoverTenants(users user.Repository) *DiscoverTenants {
	return &DiscoverTenants{users: users}
}

// TenantOption is the wire-friendly projection of a tenant
// membership. Mirrors user.TenantMembership without the UUID so
// the SPA only deals in the public slug + display name.
type TenantOption struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func (d *DiscoverTenants) Execute(ctx context.Context, rawEmail string) ([]TenantOption, error) {
	email, err := user.NormalizeEmail(rawEmail)
	if err != nil {
		// Garbage emails return an empty list, NOT 422 — same
		// shape as "no such user" so a probing caller can't
		// distinguish malformed from non-existent.
		return []TenantOption{}, nil
	}
	rows, err := d.users.ListTenantsForEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	out := make([]TenantOption, 0, len(rows))
	for _, r := range rows {
		out = append(out, TenantOption{Slug: r.TenantSlug, Name: r.TenantName})
	}
	return out, nil
}
