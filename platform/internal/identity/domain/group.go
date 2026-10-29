package domain

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

const (
	maxGroupName     = 100
	maxVendorName    = 100
	maxVendorContact = 200
	// MaxGroupMembers bounds one group.
	MaxGroupMembers = 1000
)

// Group is a named set of members in an organization — "de reviewers",
// "legal" (RFC 0006 §4.3). Groups carry no permissions: roles do. A
// group exists so that assignments and approvals can name people
// without naming persons, and its name is data the organization
// chooses, which keeps "legal" out of the code.
type Group struct {
	ID       GroupID
	TenantID tenancy.ID
	Name     string
	// Members is sorted and duplicate-free.
	Members []MemberID
	// Version increments with every change; it is the group's ETag.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewGroup creates an empty group in an organization.
func NewGroup(tenant tenancy.ID, kind tenancy.Kind, name string, now time.Time) (Group, error) {
	if kind == tenancy.KindIndividual {
		return Group{}, ErrIndividualTenant
	}
	name, err := groupName(name)
	if err != nil {
		return Group{}, err
	}
	return Group{ID: NewGroupID(), TenantID: tenant, Name: name, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func groupName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > maxGroupName {
		return "", ErrInvalidGroupName
	}
	return s, nil
}

// Rename changes the group's name.
func (g *Group) Rename(name string, now time.Time) error {
	name, err := groupName(name)
	if err != nil {
		return err
	}
	g.Name = name
	g.touch(now)
	return nil
}

// Has reports whether the member is in the group.
func (g Group) Has(id MemberID) bool { return slices.Contains(g.Members, id) }

// AddMember puts m in the group. Invitations may join: a group names
// people, and the person behind an invitation is still a person.
func (g *Group) AddMember(m Member, now time.Time) error {
	if m.TenantID != g.TenantID {
		return ErrMemberOfAnotherTenant
	}
	if g.Has(m.ID) {
		return ErrAlreadyInGroup
	}
	if len(g.Members) >= MaxGroupMembers {
		return ErrGroupFull
	}
	g.Members = append(g.Members, m.ID)
	slices.SortFunc(g.Members, func(a, b MemberID) int { return strings.Compare(a.String(), b.String()) })
	g.touch(now)
	return nil
}

// RemoveMember takes the member out of the group.
func (g *Group) RemoveMember(id MemberID, now time.Time) error {
	i := slices.Index(g.Members, id)
	if i < 0 {
		return ErrNotInGroup
	}
	g.Members = slices.Delete(g.Members, i, i+1)
	g.touch(now)
	return nil
}

func (g *Group) touch(now time.Time) {
	g.Version++
	g.UpdatedAt = now
}

// Vendor is an agency or freelancer working for the organization
// (RFC 0006 §3.3). A vendor is a named group of members inside the
// customer's tenant, not a tenant of its own (§14 decision 6): its
// people are ordinary Members whose Restriction names the vendor and
// has VisibilityAssigned. Vendor membership is held on the Member, so
// a vendor has no member list of its own to drift from it.
type Vendor struct {
	ID       VendorID
	TenantID tenancy.ID
	Name     string
	// Contact is free text — a name, an address — at most 200
	// characters; it is shown, never mailed to.
	Contact string
	// Locales are the locales the vendor offers, sorted; empty means
	// none stated. They describe the vendor and grant nothing.
	Locales   []Locale
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewVendor creates a vendor in an organization.
func NewVendor(tenant tenancy.ID, kind tenancy.Kind, name, contact string, locales []string, now time.Time) (Vendor, error) {
	if kind == tenancy.KindIndividual {
		return Vendor{}, ErrIndividualTenant
	}
	v := Vendor{ID: NewVendorID(), TenantID: tenant, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := v.set(name, contact, locales); err != nil {
		return Vendor{}, err
	}
	return v, nil
}

// Change replaces the vendor's name, contact and offered locales.
func (v *Vendor) Change(name, contact string, locales []string, now time.Time) error {
	if err := v.set(name, contact, locales); err != nil {
		return err
	}
	v.Version++
	v.UpdatedAt = now
	return nil
}

func (v *Vendor) set(name, contact string, locales []string) error {
	name, contact = strings.TrimSpace(name), strings.TrimSpace(contact)
	if name == "" || utf8.RuneCountInString(name) > maxVendorName {
		return ErrInvalidVendorName
	}
	if utf8.RuneCountInString(contact) > maxVendorContact {
		return ErrInvalidVendorContact
	}
	offered, err := ParseLocaleList(locales)
	if err != nil {
		return err
	}
	v.Name, v.Contact, v.Locales = name, contact, offered
	return nil
}

// ParseLocaleList canonicalizes, sorts and de-duplicates tags into a
// plain list of locales, where empty means none (unlike a LocaleScope,
// where empty means every locale).
func ParseLocaleList(tags []string) ([]Locale, error) {
	s, err := ParseLocaleScope(tags)
	return s.locales, err
}

// LocaleStrings returns the offered locales' tags, sorted.
func (v Vendor) LocaleStrings() []string { return LocaleScope{locales: v.Locales}.Strings() }
