// Package identity adapts Identity's permission list to Workflow's
// Permissions port: a definition's actor_has_permission guard may only
// name a permission Identity has.
package identity

import (
	"slices"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
)

// Permissions implements app.Permissions over domain.AllPermissions.
type Permissions struct{}

var _ app.Permissions = Permissions{}

// Known reports whether Identity has the permission.
func (Permissions) Known(p string) bool {
	return slices.Contains(domain.AllPermissions(), domain.Permission(p))
}
