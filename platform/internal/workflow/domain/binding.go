package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// ErrInvalidBinding is a binding whose selector cannot be stored.
var ErrInvalidBinding = errors.New("workflow: invalid binding")

// Binding selects which definition runs for a subject (RFC 0006 §2.3):
// a project, optionally narrowed to some locales and to one namespace.
// A project with no binding has no workflow, which is M4's behaviour.
type Binding struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// Subject is the bound definition's subject; a translation's
	// binding never selects a release request's definition.
	Subject SubjectKind
	// Locales, when set, narrow the binding to these canonical tags,
	// sorted.
	Locales []string
	// Namespace, when set, narrows the binding to one namespace.
	Namespace    string
	DefinitionID uuid.UUID
	// Position is the binding's place in creation order. It is what
	// "later" means when two bindings tie on specificity.
	Position  int64
	CreatedBy string
	CreatedAt time.Time
}

// NewBinding validates a selector and returns the binding it makes,
// locales canonical and sorted. Position is the store's to assign.
func NewBinding(project uuid.UUID, subject SubjectKind, locales []string, namespace string, definition uuid.UUID) (Binding, error) {
	if project == uuid.Nil {
		return Binding{}, fmt.Errorf("%w: a binding names a project", ErrInvalidBinding)
	}
	if definition == uuid.Nil {
		return Binding{}, fmt.Errorf("%w: a binding names a definition", ErrInvalidBinding)
	}
	if !subject.Valid() {
		return Binding{}, fmt.Errorf("%w: subject %q", ErrInvalidBinding, subject)
	}
	ls, err := CanonicalLocales(locales)
	if err != nil {
		return Binding{}, fmt.Errorf("%w: %w", ErrInvalidBinding, err)
	}
	if namespace != strings.TrimSpace(namespace) || len(namespace) > 256 {
		return Binding{}, fmt.Errorf("%w: namespace %q", ErrInvalidBinding, namespace)
	}
	return Binding{ProjectID: project, Subject: subject, Locales: ls, Namespace: namespace, DefinitionID: definition}, nil
}

// Target is the subject a binding is resolved for.
type Target struct {
	ProjectID uuid.UUID
	Subject   SubjectKind
	Locale    string
	Namespace string
}

// Specificity is how many fields the binding names: the project always,
// plus its locales and its namespace when it has them. It is the first
// half of precedence, exactly as for a check policy's rules.
func (b Binding) Specificity() int {
	n := 1
	if len(b.Locales) > 0 {
		n++
	}
	if b.Namespace != "" {
		n++
	}
	return n
}

// Matches reports whether b selects t. Every field the binding names
// must match; a field it leaves out matches anything. A target without a
// locale (a release request) never matches a binding that names locales.
func (b Binding) Matches(t Target) bool {
	if b.ProjectID != t.ProjectID || b.Subject != t.Subject {
		return false
	}
	if b.Namespace != "" && b.Namespace != t.Namespace {
		return false
	}
	if len(b.Locales) > 0 {
		tag, err := bcp47.Parse(t.Locale)
		if err != nil || !slices.Contains(b.Locales, tag.String()) {
			return false
		}
	}
	return true
}

// Resolve picks the binding that applies to t, with the check policy's
// precedence rule (RFC 0005 §4.1) — checkpolicy.MostSpecific itself, not
// a copy: the binding matching on more fields wins, and a tie goes to
// the later one. Bindings may come in any order; Resolve orders them by
// Position.
func Resolve(bindings []Binding, t Target) (Binding, bool) {
	ordered := slices.SortedFunc(slices.Values(bindings), func(a, b Binding) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
		}
		return 0
	})
	i := checkpolicy.MostSpecific(ordered, func(b Binding) bool { return b.Matches(t) }, Binding.Specificity)
	if i < 0 {
		return Binding{}, false
	}
	return ordered[i], true
}
