package coverage

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	locapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

// PolicyLocales implements app.ProjectLocales for exactly one caller:
// Catalog validating the locales a check policy's require_complete
// names, inside the project write that saves it (RFC 0005 §4.1).
//
// It is a type of its own, separate from Port, because it is the one
// path into Localization that does not ask for translations.read. The
// caller has already proved catalog.write on the project; that a locale
// it names exists is an internal invariant, not a user-facing read, and
// refusing it for want of translations.read would stop a CI token
// (catalog.read and catalog.write, the whole CI ceiling) from writing a
// policy that names locales at all.
//
// What it may not become: a general read of Localization. It carries
// locale codes and nothing else, it is wired only where Catalog asks
// "does this project have this locale?", and anything a caller reads
// goes through Port and the ordinary authorized use cases, which keep
// checking translations.read.
type PolicyLocales struct{ svc *locapp.Service }

// NewPolicyLocales returns the port. The composition root wires it into
// Catalog with SetLocales and nowhere else.
func NewPolicyLocales(svc *locapp.Service) *PolicyLocales { return &PolicyLocales{svc: svc} }

var _ app.ProjectLocales = (*PolicyLocales)(nil)

// LocaleCodes implements app.ProjectLocales.
func (p *PolicyLocales) LocaleCodes(ctx context.Context, project domain.ProjectID) ([]string, error) {
	return p.svc.CheckPolicyLocaleCodes(ctx, project.UUID())
}
