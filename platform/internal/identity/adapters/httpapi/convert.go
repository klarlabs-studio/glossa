package httpapi

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func toPerson(p app.PersonRecord) apiv1.Person {
	out := apiv1.Person{
		Id:                 p.ID.String(),
		Email:              openapi_types.Email(p.Email.String()),
		EmailVerified:      p.EmailVerified(),
		TotpEnabled:        p.TOTPEnabled,
		IndividualTenantId: p.IndividualTenantID.String(),
		CreatedAt:          p.CreatedAt.UTC(),
	}
	if p.DisplayName != "" {
		out.DisplayName = &p.DisplayName
	}
	return out
}

func toTenant(t tenancy.Tenant) apiv1.Tenant {
	return apiv1.Tenant{
		Id: t.ID.String(), Kind: apiv1.TenantKind(t.Kind), Slug: string(t.Slug), Name: t.Name,
		CreatedAt: t.CreatedAt.UTC(),
	}
}

func toRoles(rs domain.Roles) []apiv1.Role {
	out := make([]apiv1.Role, len(rs))
	for i, r := range rs {
		out[i] = apiv1.Role(r)
	}
	return out
}

func fromRoles(rs []apiv1.Role) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}

func toMember(m app.MemberView) apiv1.Member {
	out := apiv1.Member{
		Id: m.ID.String(), Email: openapi_types.Email(m.Email.String()), Status: apiv1.MemberStatus(m.Status),
		Roles: toRoles(m.Roles), Locales: m.Locales.Strings(), Projects: m.Restriction.Projects.Strings(),
		Visibility: apiv1.VisibilityAll, CreatedAt: m.CreatedAt.UTC(), UpdatedAt: m.UpdatedAt.UTC(),
	}
	if m.Restriction.Visibility == domain.VisibilityAssigned {
		out.Visibility = apiv1.VisibilityAssigned
	}
	if !m.Restriction.Vendor.IsZero() {
		out.VendorId = ptr(m.Restriction.Vendor.String())
	}
	if !m.PersonID.IsZero() {
		id := m.PersonID.String()
		out.PersonId = &id
	}
	if m.DisplayName != "" {
		out.DisplayName = &m.DisplayName
	}
	return out
}

func toToken(t domain.APIToken) apiv1.Token {
	scopes := make([]apiv1.Scope, len(t.Scopes))
	for i, s := range t.Scopes {
		scopes[i] = apiv1.Scope(s)
	}
	return apiv1.Token{
		Id: t.ID.String(), Name: t.Name, Hint: t.Hint, Scopes: scopes, Projects: t.Projects.Strings(), CreatedBy: t.CreatedBy.String(),
		CreatedAt: t.CreatedAt.UTC(), ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
}

func fromScopes(ss []apiv1.Scope) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}
	return out
}

func base64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// etag renders a member version as a strong entity tag.
func etag(version int) string { return `"` + strconv.Itoa(version) + `"` }

// parseETag reads an If-Match value this API issued.
func parseETag(s string) (int, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "W/")
	if len(s) < 3 || s[0] != '"' || s[len(s)-1] != '"' {
		return 0, fmt.Errorf("%w: If-Match must be an ETag this API issued", app.ErrPreconditionFailed)
	}
	v, err := strconv.Atoi(s[1 : len(s)-1])
	if err != nil || v < 1 {
		return 0, fmt.Errorf("%w: If-Match must be an ETag this API issued", app.ErrPreconditionFailed)
	}
	return v, nil
}

func ptr[T any](v T) *T { return &v }
