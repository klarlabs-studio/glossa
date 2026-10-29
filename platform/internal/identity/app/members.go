package app

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// ListMembers lists the tenant's members and open invitations.
func (s *Service) ListMembers(ctx context.Context, page pagination.Page) ([]MemberView, *string, error) {
	if err := authz.Require(ctx, authz.MembersRead); err != nil {
		return nil, nil, err
	}
	after, err := parseAfter(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []MemberView
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		rows, err = st.Members(ctx, domain.MemberID(after), page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(m MemberView) string { return m.ID.String() })
	return items, next, nil
}

// parseAfter reads a page cursor holding an ID. The kernel decoded the
// token; a cursor that isn't an ID was forged or belongs to another list.
func parseAfter(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this list issued")
	}
	return id, nil
}

// AddMember invites email into the tenant with roles and locales.
func (s *Service) AddMember(ctx context.Context, email string, roles, locales []string, idemKey string) (m MemberView, replayed bool, err error) {
	return s.InviteMember(ctx, Invitation{Email: email, Roles: roles, Locales: locales}, idemKey)
}

// Invitation is a member to invite. Projects, Vendor and Visibility are
// the member's restriction (RFC 0006 §3.3, §4.1); left empty, the
// member is unrestricted. An inviter who is project-scoped themselves
// can only invite within their own scope.
type Invitation struct {
	Email      string
	Roles      []string
	Locales    []string
	Projects   []string
	Vendor     string
	Visibility string
}

// InviteMember invites a member, possibly restricted. Naming a vendor
// also needs vendors.manage, and the vendor must exist in the tenant.
func (s *Service) InviteMember(ctx context.Context, inv Invitation, idemKey string) (m MemberView, replayed bool, err error) {
	if err := authz.Require(ctx, authz.MembersManage); err != nil {
		return MemberView{}, false, err
	}
	p, _ := authz.From(ctx)
	e, err := parseEmail(inv.Email)
	if err != nil {
		return MemberView{}, false, err
	}
	rs, err := domain.ParseRoles(inv.Roles)
	if err != nil {
		return MemberView{}, false, err
	}
	ls, err := domain.ParseLocaleScope(inv.Locales)
	if err != nil {
		return MemberView{}, false, err
	}
	r, err := parseRestriction(ctx, inv.Projects, inv.Vendor, inv.Visibility)
	if err != nil {
		return MemberView{}, false, err
	}
	if r.Projects, err = withinActor(p, r.Projects); err != nil {
		return MemberView{}, false, err
	}
	if idemKey != "" {
		if err := checkIdempotencyKey(idemKey); err != nil {
			return MemberView{}, false, err
		}
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		t, err := st.CurrentTenant(ctx)
		if err != nil {
			return err
		}
		if err := vendorExists(ctx, st, r.Vendor); err != nil {
			return err
		}
		invite, err := domain.InviteWith(t.ID, t.Kind, e, rs, ls, r, p.Grant, s.now())
		if err != nil {
			return err
		}
		if idemKey != "" {
			invite.ID = domain.MemberID(idempotentID("member.add", t.ID.String(), p.Actor, idemKey))
		}
		inserted, err := st.InsertMember(ctx, invite, p.Actor)
		if err != nil {
			return err
		}
		if !inserted {
			if m, err = st.Member(ctx, invite.ID); err != nil {
				return err
			}
			if m.Email != invite.Email {
				return ErrIdempotencyReuse
			}
			replayed = true
			return nil
		}
		m = MemberView{Member: invite}
		return st.Publish(ctx, memberAdded(invite, p.Actor))
	})
	return m, replayed, err
}

// parseRestriction reads a restriction's parts. Naming a vendor takes
// vendors.manage on top of members.manage.
func parseRestriction(ctx context.Context, projects []string, vendor, visibility string) (domain.Restriction, error) {
	ps, err := domain.ParseProjectScope(projects)
	if err != nil {
		return domain.Restriction{}, err
	}
	v, err := domain.ParseVisibility(visibility)
	if err != nil {
		return domain.Restriction{}, err
	}
	r := domain.Restriction{Projects: ps, Visibility: v}
	if vendor != "" {
		if err := authz.Require(ctx, authz.VendorsManage); err != nil {
			return domain.Restriction{}, err
		}
		if r.Vendor, err = domain.ParseVendorID(vendor); err != nil {
			return domain.Restriction{}, err
		}
	}
	return r, nil
}

// withinActor keeps a project scope the actor sets — on an invitation, a
// member or a token — inside the actor's own (RFC 0006 §4.1): an
// unscoped actor sets any scope, a scoped actor's "every project" means
// their projects, and naming a project outside them is refused. Without
// it a project-scoped admin could invite themselves a second, unscoped
// account.
func withinActor(actor authz.Principal, ps domain.ProjectScope) (domain.ProjectScope, error) {
	switch {
	case actor.Projects.All():
		return ps, nil
	case ps.All():
		return actor.Projects, nil
	case !ps.Within(actor.Projects):
		return domain.ProjectScope{}, domain.ErrScopeExceedsGrant
	}
	return ps, nil
}

// vendorExists checks a named vendor is the tenant's (RLS hides any
// other tenant's), so the foreign key never has to say it.
func vendorExists(ctx context.Context, st TenantStore, id domain.VendorID) error {
	if id.IsZero() {
		return nil
	}
	_, err := st.Vendor(ctx, id)
	return err
}

// RestrictionChange is a partial update of a member's restriction; nil
// fields keep their value, and an empty Vendor clears it.
type RestrictionChange struct {
	Projects   *[]string
	Vendor     *string
	Visibility *string
}

// RestrictMember changes a member's project scope, vendor and visibility
// if the member is still at version ifMatch. Adding, changing or
// removing a vendor also needs vendors.manage.
// It takes effect on the member's next request: their principal is
// built from the stored membership every time.
func (s *Service) RestrictMember(ctx context.Context, id domain.MemberID, ifMatch int, c RestrictionChange) (MemberView, error) {
	if err := authz.Require(ctx, authz.MembersManage); err != nil {
		return MemberView{}, err
	}
	p, _ := authz.From(ctx)
	var view MemberView
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		m, err := st.LockMember(ctx, id)
		if err != nil {
			return err
		}
		if m.Version != ifMatch {
			return ErrPreconditionFailed
		}
		r, err := applyRestriction(ctx, m.Restriction, c)
		if err != nil {
			return err
		}
		if r.Projects, err = withinActor(p, r.Projects); err != nil {
			return err
		}
		if err := vendorExists(ctx, st, r.Vendor); err != nil {
			return err
		}
		if err := m.Restrict(r, s.now()); err != nil {
			return err
		}
		if err := st.UpdateMemberRestriction(ctx, m); err != nil {
			return err
		}
		if err := st.Publish(ctx, memberRestrictionChanged(m, p.Actor)); err != nil {
			return err
		}
		view, err = st.Member(ctx, id)
		return err
	})
	return view, err
}

func applyRestriction(ctx context.Context, r domain.Restriction, c RestrictionChange) (domain.Restriction, error) {
	var err error
	if c.Projects != nil {
		if r.Projects, err = domain.ParseProjectScope(*c.Projects); err != nil {
			return r, err
		}
	}
	if c.Visibility != nil {
		if r.Visibility, err = domain.ParseVisibility(*c.Visibility); err != nil {
			return r, err
		}
	}
	if c.Vendor != nil {
		if err := authz.Require(ctx, authz.VendorsManage); err != nil {
			return r, err
		}
		r.Vendor = domain.VendorID{}
		if *c.Vendor != "" {
			if r.Vendor, err = domain.ParseVendorID(*c.Vendor); err != nil {
				return r, err
			}
		}
	}
	return r, nil
}

func memberRestrictionChanged(m domain.Member, by domain.Actor) outbox.Event {
	e := domain.MemberRestrictionChanged{
		MemberID: m.ID.String(), Projects: m.Restriction.Projects.Strings(),
		Visibility: string(m.Restriction.Visibility), ChangedBy: by.String(),
	}
	if m.IsVendorMember() {
		e.VendorID = m.Restriction.Vendor.String()
	}
	return outbox.Event{
		Type: domain.EventMemberRestrictionChanged, AggregateType: domain.AggregateMember,
		AggregateID: m.ID.String(), Actor: outbox.Actor(by.String()), Payload: e,
	}
}

// GetMember returns one member.
func (s *Service) GetMember(ctx context.Context, id domain.MemberID) (MemberView, error) {
	if err := authz.Require(ctx, authz.MembersRead); err != nil {
		return MemberView{}, err
	}
	var m MemberView
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		var err error
		m, err = st.Member(ctx, id)
		return err
	})
	return m, err
}

// MemberChange is a partial update; nil fields keep their value.
type MemberChange struct {
	Roles   *[]string
	Locales *[]string
}

// UpdateMember changes roles and locales if the member is still at
// version ifMatch.
func (s *Service) UpdateMember(ctx context.Context, id domain.MemberID, ifMatch int, c MemberChange) (MemberView, error) {
	if err := authz.Require(ctx, authz.MembersManage); err != nil {
		return MemberView{}, err
	}
	p, _ := authz.From(ctx)
	var view MemberView
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		// Owners first, then the member: one lock order for every writer.
		owners, err := st.LockActiveOwners(ctx)
		if err != nil {
			return err
		}
		m, err := st.LockMember(ctx, id)
		if err != nil {
			return err
		}
		if m.Version != ifMatch {
			return ErrPreconditionFailed
		}
		roles, locales, err := applyChange(m, c)
		if err != nil {
			return err
		}
		if err := m.ChangeAccess(roles, locales, p.Grant, owners, s.now()); err != nil {
			return err
		}
		if err := st.UpdateMemberAccess(ctx, m); err != nil {
			return err
		}
		if err := st.Publish(ctx, outbox.Event{
			Type: domain.EventMemberAccessChange, AggregateType: domain.AggregateMember, AggregateID: m.ID.String(),
			Actor: outbox.Actor(p.Actor.String()),
			Payload: domain.MemberAccessChanged{
				MemberID: m.ID.String(), Roles: m.Roles.Strings(), Locales: m.Locales.Strings(), ChangedBy: p.Actor.String(),
			},
		}); err != nil {
			return err
		}
		view, err = st.Member(ctx, id)
		return err
	})
	return view, err
}

func applyChange(m domain.Member, c MemberChange) (domain.Roles, domain.LocaleScope, error) {
	roles, locales := m.Roles, m.Locales
	var err error
	if c.Roles != nil {
		if roles, err = domain.ParseRoles(*c.Roles); err != nil {
			return nil, domain.LocaleScope{}, err
		}
	}
	if c.Locales != nil {
		if locales, err = domain.ParseLocaleScope(*c.Locales); err != nil {
			return nil, domain.LocaleScope{}, err
		}
	}
	return roles, locales, nil
}

// RemoveMember ends a membership or withdraws an invitation. ifMatch, if
// set, must equal the member's version.
func (s *Service) RemoveMember(ctx context.Context, id domain.MemberID, ifMatch *int) error {
	if err := authz.Require(ctx, authz.MembersManage); err != nil {
		return err
	}
	p, _ := authz.From(ctx)
	return s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		owners, err := st.LockActiveOwners(ctx)
		if err != nil {
			return err
		}
		m, err := st.LockMember(ctx, id)
		if err != nil {
			return err
		}
		if ifMatch != nil && m.Version != *ifMatch {
			return ErrPreconditionFailed
		}
		if err := m.CheckRemoval(p.Grant, owners); err != nil {
			return err
		}
		if err := st.DeleteMember(ctx, id); err != nil {
			return err
		}
		e := domain.MemberRemoved{MemberID: m.ID.String(), RemovedBy: p.Actor.String()}
		if !m.PersonID.IsZero() {
			e.PersonID = m.PersonID.String()
		}
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventMemberRemoved, AggregateType: domain.AggregateMember, AggregateID: m.ID.String(),
			Actor: outbox.Actor(p.Actor.String()), Payload: e,
		})
	})
}
