package app

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// Vendors and groups (RFC 0006 §3.3, §4.3). Both are about who people
// are, not what they may do: reading them takes members.read; a group
// is changed with members.manage, a vendor with vendors.manage. Their
// HTTP surface is RFC 0006 wave 3.

// VendorInput is a vendor's details.
type VendorInput struct {
	Name    string
	Contact string
	Locales []string
}

// CreateVendor adds a vendor to the organization.
func (s *Service) CreateVendor(ctx context.Context, in VendorInput, idemKey string) (v domain.Vendor, replayed bool, err error) {
	if err := authz.Require(ctx, authz.VendorsManage); err != nil {
		return domain.Vendor{}, false, err
	}
	p, _ := authz.From(ctx)
	if idemKey != "" {
		if err := checkIdempotencyKey(idemKey); err != nil {
			return domain.Vendor{}, false, err
		}
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		t, err := st.CurrentTenant(ctx)
		if err != nil {
			return err
		}
		if v, err = domain.NewVendor(t.ID, t.Kind, in.Name, in.Contact, in.Locales, s.now()); err != nil {
			return err
		}
		if idemKey != "" {
			v.ID = domain.VendorID(idempotentID("vendor.create", t.ID.String(), p.Actor, idemKey))
		}
		inserted, err := st.InsertVendor(ctx, v, p.Actor)
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := st.Vendor(ctx, v.ID)
			if err != nil {
				return err
			}
			if existing.Name != v.Name {
				return ErrIdempotencyReuse
			}
			v, replayed = existing, true
			return nil
		}
		return st.Publish(ctx, vendorEvent(domain.EventVendorCreated, v.ID, p.Actor, domain.VendorCreated{
			VendorID: v.ID.String(), Name: v.Name, Locales: v.LocaleStrings(), CreatedBy: p.Actor.String(),
		}))
	})
	return v, replayed, err
}

// GetVendor returns one vendor.
func (s *Service) GetVendor(ctx context.Context, id domain.VendorID) (domain.Vendor, error) {
	if err := authz.Require(ctx, authz.MembersRead); err != nil {
		return domain.Vendor{}, err
	}
	var v domain.Vendor
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		var err error
		v, err = st.Vendor(ctx, id)
		return err
	})
	return v, err
}

// ListVendors lists the organization's vendors.
func (s *Service) ListVendors(ctx context.Context, page pagination.Page) ([]domain.Vendor, *string, error) {
	if err := authz.Require(ctx, authz.MembersRead); err != nil {
		return nil, nil, err
	}
	after, err := parseAfter(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Vendor
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		rows, err = st.Vendors(ctx, domain.VendorID(after), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(v domain.Vendor) string { return v.ID.String() })
	return items, next, nil
}

// UpdateVendor replaces a vendor's details if it is still at version
// ifMatch.
func (s *Service) UpdateVendor(ctx context.Context, id domain.VendorID, ifMatch int, in VendorInput) (domain.Vendor, error) {
	if err := authz.Require(ctx, authz.VendorsManage); err != nil {
		return domain.Vendor{}, err
	}
	p, _ := authz.From(ctx)
	var v domain.Vendor
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		var err error
		if v, err = st.LockVendor(ctx, id); err != nil {
			return err
		}
		if v.Version != ifMatch {
			return ErrPreconditionFailed
		}
		if err := v.Change(in.Name, in.Contact, in.Locales, s.now()); err != nil {
			return err
		}
		if err := st.UpdateVendor(ctx, v); err != nil {
			return err
		}
		return st.Publish(ctx, vendorEvent(domain.EventVendorChanged, v.ID, p.Actor, domain.VendorChanged{
			VendorID: v.ID.String(), Name: v.Name, Locales: v.LocaleStrings(), ChangedBy: p.Actor.String(),
		}))
	})
	return v, err
}

// DeleteVendor deletes a vendor nobody works for any more. A vendor
// with members is refused (domain.ErrVendorHasMembers): taking people
// off it is a decision about each of them.
func (s *Service) DeleteVendor(ctx context.Context, id domain.VendorID) error {
	if err := authz.Require(ctx, authz.VendorsManage); err != nil {
		return err
	}
	p, _ := authz.From(ctx)
	return s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		if _, err := st.LockVendor(ctx, id); err != nil {
			return err
		}
		if n, err := st.CountVendorMembers(ctx, id); err != nil {
			return err
		} else if n > 0 {
			return domain.ErrVendorHasMembers
		}
		if err := st.DeleteVendor(ctx, id); err != nil {
			return err
		}
		return st.Publish(ctx, vendorEvent(domain.EventVendorDeleted, id, p.Actor, domain.VendorDeleted{
			VendorID: id.String(), DeletedBy: p.Actor.String(),
		}))
	})
}

// CreateGroup adds an empty group to the organization.
func (s *Service) CreateGroup(ctx context.Context, name, idemKey string) (g domain.Group, replayed bool, err error) {
	if err := authz.Require(ctx, authz.MembersManage); err != nil {
		return domain.Group{}, false, err
	}
	p, _ := authz.From(ctx)
	if idemKey != "" {
		if err := checkIdempotencyKey(idemKey); err != nil {
			return domain.Group{}, false, err
		}
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		t, err := st.CurrentTenant(ctx)
		if err != nil {
			return err
		}
		if g, err = domain.NewGroup(t.ID, t.Kind, name, s.now()); err != nil {
			return err
		}
		if idemKey != "" {
			g.ID = domain.GroupID(idempotentID("group.create", t.ID.String(), p.Actor, idemKey))
		}
		inserted, err := st.InsertGroup(ctx, g, p.Actor)
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := st.Group(ctx, g.ID)
			if err != nil {
				return err
			}
			if existing.Name != g.Name {
				return ErrIdempotencyReuse
			}
			g, replayed = existing, true
			return nil
		}
		return st.Publish(ctx, groupEvent(domain.EventGroupCreated, g, domain.MemberID{}, p.Actor))
	})
	return g, replayed, err
}

// GetGroup returns one group with its members.
func (s *Service) GetGroup(ctx context.Context, id domain.GroupID) (domain.Group, error) {
	if err := authz.Require(ctx, authz.MembersRead); err != nil {
		return domain.Group{}, err
	}
	var g domain.Group
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		var err error
		g, err = st.Group(ctx, id)
		return err
	})
	return g, err
}

// ListGroups lists the organization's groups with their members.
func (s *Service) ListGroups(ctx context.Context, page pagination.Page) ([]domain.Group, *string, error) {
	if err := authz.Require(ctx, authz.MembersRead); err != nil {
		return nil, nil, err
	}
	after, err := parseAfter(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Group
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		rows, err = st.Groups(ctx, domain.GroupID(after), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(g domain.Group) string { return g.ID.String() })
	return items, next, nil
}

// RenameGroup renames a group if it is still at version ifMatch.
func (s *Service) RenameGroup(ctx context.Context, id domain.GroupID, ifMatch int, name string) (domain.Group, error) {
	return s.changeGroup(ctx, id, &ifMatch, func(ctx context.Context, st TenantStore, g *domain.Group, by domain.Actor) error {
		if err := g.Rename(name, s.now()); err != nil {
			return err
		}
		if err := st.UpdateGroup(ctx, *g); err != nil {
			return err
		}
		return st.Publish(ctx, groupEvent(domain.EventGroupRenamed, *g, domain.MemberID{}, by))
	})
}

// AddGroupMember puts a member of the tenant into a group.
func (s *Service) AddGroupMember(ctx context.Context, id domain.GroupID, member domain.MemberID) (domain.Group, error) {
	return s.changeGroup(ctx, id, nil, func(ctx context.Context, st TenantStore, g *domain.Group, by domain.Actor) error {
		m, err := st.Member(ctx, member)
		if err != nil {
			return err
		}
		if err := g.AddMember(m.Member, s.now()); err != nil {
			return err
		}
		if err := st.AddGroupMember(ctx, *g, member, by); err != nil {
			return err
		}
		return st.Publish(ctx, groupEvent(domain.EventGroupMemberAdded, *g, member, by))
	})
}

// RemoveGroupMember takes a member out of a group.
func (s *Service) RemoveGroupMember(ctx context.Context, id domain.GroupID, member domain.MemberID) (domain.Group, error) {
	return s.changeGroup(ctx, id, nil, func(ctx context.Context, st TenantStore, g *domain.Group, by domain.Actor) error {
		if err := g.RemoveMember(member, s.now()); err != nil {
			return err
		}
		if err := st.RemoveGroupMember(ctx, *g, member); err != nil {
			return err
		}
		return st.Publish(ctx, groupEvent(domain.EventGroupMemberRemoved, *g, member, by))
	})
}

// DeleteGroup deletes a group. Its members stay members.
func (s *Service) DeleteGroup(ctx context.Context, id domain.GroupID) error {
	_, err := s.changeGroup(ctx, id, nil, func(ctx context.Context, st TenantStore, g *domain.Group, by domain.Actor) error {
		if err := st.DeleteGroup(ctx, g.ID); err != nil {
			return err
		}
		return st.Publish(ctx, groupEvent(domain.EventGroupDeleted, *g, domain.MemberID{}, by))
	})
	return err
}

// changeGroup locks a group, checks ifMatch when given, and applies fn.
func (s *Service) changeGroup(
	ctx context.Context, id domain.GroupID, ifMatch *int,
	fn func(context.Context, TenantStore, *domain.Group, domain.Actor) error,
) (domain.Group, error) {
	if err := authz.Require(ctx, authz.MembersManage); err != nil {
		return domain.Group{}, err
	}
	p, _ := authz.From(ctx)
	var g domain.Group
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		var err error
		if g, err = st.LockGroup(ctx, id); err != nil {
			return err
		}
		if ifMatch != nil && g.Version != *ifMatch {
			return ErrPreconditionFailed
		}
		return fn(ctx, st, &g, p.Actor)
	})
	return g, err
}

// vendorEvent and groupEvent build every vendor and group event in one
// place each, so a change to the outbox envelope lands in two lines.
func vendorEvent(typ string, id domain.VendorID, by domain.Actor, payload any) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateVendor, AggregateID: id.String(),
		Actor: outbox.Actor(by.String()), Payload: payload,
	}
}

func groupEvent(typ string, g domain.Group, member domain.MemberID, by domain.Actor) outbox.Event {
	e := domain.GroupChanged{GroupID: g.ID.String(), Name: g.Name, By: by.String()}
	if !member.IsZero() {
		e.MemberID = member.String()
	}
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateGroup, AggregateID: g.ID.String(),
		Actor: outbox.Actor(by.String()), Payload: e,
	}
}
