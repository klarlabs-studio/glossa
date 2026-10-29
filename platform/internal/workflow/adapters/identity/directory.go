package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	workflow "go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// TenantReader runs a read on Identity's tenant store, joining the
// tenant transaction ctx is already in. Identity's postgres Transactor
// implements it (InCurrentTenant).
type TenantReader interface {
	InCurrentTenant(ctx context.Context, fn func(context.Context, identityapp.TenantStore) error) error
}

// Directory implements app.Directory over Identity's tenant store: who
// a member is (roles, groups, vendor) and which member, group or vendor
// a definition's party names. It reads Identity's store, not its
// application service, because the questions are asked on behalf of
// whoever an assignment concerns — a vendor's translator holds no
// members.read — and answer nothing beyond what the caller already
// acts on.
type Directory struct{ tx TenantReader }

var _ app.Directory = Directory{}

// NewDirectory returns a Directory on Identity's store.
func NewDirectory(tx TenantReader) Directory { return Directory{tx: tx} }

// pageSize is how many groups or vendors one store read returns.
const pageSize = 100

// Affiliation implements app.Directory.
func (d Directory) Affiliation(ctx context.Context, member uuid.UUID) (app.Affiliation, error) {
	var out app.Affiliation
	err := d.tx.InCurrentTenant(ctx, func(ctx context.Context, st identityapp.TenantStore) error {
		m, err := st.Member(ctx, domain.MemberID(member))
		if err != nil {
			return mapErr(err)
		}
		out = app.Affiliation{Member: member, Vendor: m.Restriction.Vendor.UUID()}
		for _, r := range m.Roles {
			out.Roles = append(out.Roles, string(r))
		}
		return eachGroup(ctx, st, func(g domain.Group) bool {
			if g.Has(m.ID) {
				out.Groups = append(out.Groups, g.ID.UUID())
			}
			return true
		})
	})
	return out, err
}

// Resolve implements app.Directory: a member by id; a group or a vendor
// by id or by name, ignoring case.
func (d Directory) Resolve(ctx context.Context, kind workflow.AssigneeKind, ref string) (uuid.UUID, error) {
	id, parseErr := uuid.Parse(ref)
	isID := parseErr == nil
	var out uuid.UUID
	err := d.tx.InCurrentTenant(ctx, func(ctx context.Context, st identityapp.TenantStore) error {
		switch kind {
		case workflow.AssigneeMember:
			if !isID {
				return app.ErrUnknownParty
			}
			m, err := st.Member(ctx, domain.MemberID(id))
			if err != nil {
				return unknown(err)
			}
			out = m.ID.UUID()
		case workflow.AssigneeGroup:
			if isID {
				g, err := st.Group(ctx, domain.GroupID(id))
				if err != nil {
					return unknown(err)
				}
				out = g.ID.UUID()
				return nil
			}
			if err := eachGroup(ctx, st, func(g domain.Group) bool {
				if strings.EqualFold(g.Name, ref) {
					out = g.ID.UUID()
					return false
				}
				return true
			}); err != nil {
				return err
			}
		case workflow.AssigneeVendor:
			if isID {
				v, err := st.Vendor(ctx, domain.VendorID(id))
				if err != nil {
					return unknown(err)
				}
				out = v.ID.UUID()
				return nil
			}
			if err := eachVendor(ctx, st, func(v domain.Vendor) bool {
				if strings.EqualFold(v.Name, ref) {
					out = v.ID.UUID()
					return false
				}
				return true
			}); err != nil {
				return err
			}
		default:
			return app.ErrUnknownParty
		}
		if out == uuid.Nil {
			return app.ErrUnknownParty
		}
		return nil
	})
	return out, err
}

// eachGroup calls fn for every group until it returns false.
func eachGroup(ctx context.Context, st identityapp.TenantStore, fn func(domain.Group) bool) error {
	var after domain.GroupID
	for {
		page, err := st.Groups(ctx, after, pageSize)
		if err != nil {
			return err
		}
		for _, g := range page {
			if !fn(g) {
				return nil
			}
		}
		if len(page) < pageSize {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

// eachVendor calls fn for every vendor until it returns false.
func eachVendor(ctx context.Context, st identityapp.TenantStore, fn func(domain.Vendor) bool) error {
	var after domain.VendorID
	for {
		page, err := st.Vendors(ctx, after, pageSize)
		if err != nil {
			return err
		}
		for _, v := range page {
			if !fn(v) {
				return nil
			}
		}
		if len(page) < pageSize {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

func mapErr(err error) error {
	if errors.Is(err, identityapp.ErrNotFound) {
		return app.ErrNotFound
	}
	return err
}

func unknown(err error) error {
	if errors.Is(err, identityapp.ErrNotFound) {
		return app.ErrUnknownParty
	}
	return err
}
