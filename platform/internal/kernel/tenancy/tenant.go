// Package tenancy is the kernel's view of a tenant: the identifier that
// scopes every row of tenant-owned data, the two tenant kinds of the
// Klarlabs product standard (§2), and the plumbing that carries the
// current tenant through a request.
//
// The Identity context owns tenant lifecycle (registration, membership,
// roles). This package only defines what every other context needs to
// agree on, so it stays small on purpose.
package tenancy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidID is returned for a malformed or nil tenant ID.
	ErrInvalidID = errors.New("tenancy: invalid tenant id")
	// ErrInvalidKind is returned for a kind other than individual/organization.
	ErrInvalidKind = errors.New("tenancy: invalid tenant kind")
	// ErrInvalidSlug is returned for a slug that isn't a DNS-style label.
	ErrInvalidSlug = errors.New("tenancy: invalid tenant slug")
	// ErrInvalidName is returned for an empty or overlong display name.
	ErrInvalidName = errors.New("tenancy: invalid tenant name")
)

// ID identifies a tenant. The zero value means "no tenant".
type ID uuid.UUID

// NewID returns a fresh, time-ordered (UUIDv7) tenant ID.
func NewID() ID { return ID(uuid.Must(uuid.NewV7())) }

// ParseID parses the canonical string form of a tenant ID.
func ParseID(s string) (ID, error) {
	u, err := uuid.Parse(s)
	if err != nil || u == uuid.Nil {
		return ID{}, fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return ID(u), nil
}

// String returns the canonical UUID form.
func (id ID) String() string { return uuid.UUID(id).String() }

// UUID returns the underlying UUID, for adapters.
func (id ID) UUID() uuid.UUID { return uuid.UUID(id) }

// IsZero reports whether id is the zero ("no tenant") value.
func (id ID) IsZero() bool { return uuid.UUID(id) == uuid.Nil }

// Kind distinguishes a single person's tenant from a team's.
type Kind string

const (
	// KindIndividual is created automatically at registration.
	KindIndividual Kind = "individual"
	// KindOrganization is a multi-member tenant with roles.
	KindOrganization Kind = "organization"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindIndividual || k == KindOrganization }

// Slug is a tenant's URL-safe handle: a lowercase DNS label.
type Slug string

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ParseSlug validates s as a slug.
func ParseSlug(s string) (Slug, error) {
	if !slugPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidSlug, s)
	}
	return Slug(s), nil
}

const maxNameLen = 200

// Tenant is the kernel's tenant record.
type Tenant struct {
	ID        ID
	Kind      Kind
	Slug      Slug
	Name      string
	CreatedAt time.Time
}

// NewTenant validates the inputs and returns a tenant with a new ID.
func NewTenant(kind Kind, slug, name string) (Tenant, error) {
	if !kind.Valid() {
		return Tenant{}, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
	}
	s, err := ParseSlug(slug)
	if err != nil {
		return Tenant{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxNameLen {
		return Tenant{}, fmt.Errorf("%w: must be 1–%d characters", ErrInvalidName, maxNameLen)
	}
	return Tenant{ID: NewID(), Kind: kind, Slug: s, Name: name}, nil
}
