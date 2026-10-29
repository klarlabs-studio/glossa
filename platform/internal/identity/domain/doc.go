// Package domain is the Identity bounded context's model (RFC 0002 §4):
// people, the tenants they belong to, their memberships with roles and
// locale scopes, and the tenant-owned API tokens used by the CLI, CI and
// agents.
//
// Authentication primitives — email, sessions, magic links, passwords,
// TOTP, passkeys, lockout — are auth-go's (github.com/klarlabs-studio/
// auth-go); this package reuses its value objects (authgo.Email) and
// owns only what auth-go deliberately leaves to the product: who may do
// what, where.
//
// Two worlds meet here:
//
//   - Global, deployment-wide: a Person and their credentials. A person
//     exists before any tenant is known (a login) and belongs to many
//     tenants, so these rows carry no tenant_id.
//   - Tenant-owned: a Member (a person's place in one tenant) and an
//     APIToken. Row-level security isolates them per tenant.
//
// Authorization is the Grant: the permissions a principal holds in one
// tenant, derived from a member's roles and locale scope or from a
// token's scopes. Other contexts ask for it through package authz.
package domain
