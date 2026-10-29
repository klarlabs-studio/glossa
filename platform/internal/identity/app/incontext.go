package app

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// The in-product editor's credential (RFC 0004 §5.2).
//
// The overlay runs on the product's own preview deployment, so it can
// never use Studio's session cookie: the cookie is `SameSite=Lax` on
// another site, and third-party cookies are going away. Instead a popup
// on Studio — which does have the session — asks for an in-context
// grant for one origin, and posts the secret back to that origin alone.
//
// The grant is deliberately small: the person's own permissions cut down
// to what an editor needs, for fifteen minutes, on one project, from one
// origin. It cannot be widened, it cannot be refreshed (the overlay
// re-opens the popup), and it dies with the clock.

// ListPreviewOrigins lists a project's registered preview origins.
// Reading them needs only catalog.read: the authorize popup has to tell
// people where the editor may run, and everyone who can see the project
// can see that.
func (s *Service) ListPreviewOrigins(ctx context.Context, project domain.ProjectRef) ([]domain.PreviewOrigin, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project.UUID()); err != nil {
		return nil, err
	}
	var out []domain.PreviewOrigin
	err := s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		var err error
		out, err = st.PreviewOrigins(ctx, project)
		return err
	})
	if err != nil {
		return nil, err
	}
	domain.SortOrigins(out)
	return out, nil
}

// RegisterPreviewOrigin adds an origin the in-product editor may run on.
//
// It needs tokens.manage, not catalog.write: registering an origin is
// the decision that a page served from there may borrow a person's
// permissions, which is credential surface, not content. Developers,
// admins and owners hold it; translators and reviewers do not.
func (s *Service) RegisterPreviewOrigin(
	ctx context.Context, project domain.ProjectRef, origin, label, idemKey string,
) (domain.PreviewOrigin, bool, error) {
	if err := authz.RequireIn(ctx, authz.TokensManage, project.UUID()); err != nil {
		return domain.PreviewOrigin{}, false, err
	}
	p, _ := authz.From(ctx)
	o, err := domain.ParseOrigin(origin)
	if err != nil {
		return domain.PreviewOrigin{}, false, err
	}
	row, err := domain.NewPreviewOrigin(p.Tenant, project, o, label, p.Actor, s.now())
	if err != nil {
		return domain.PreviewOrigin{}, false, err
	}
	if idemKey != "" {
		if err := checkIdempotencyKey(idemKey); err != nil {
			return domain.PreviewOrigin{}, false, err
		}
		row.ID = domain.PreviewOriginID(idempotentID("preview_origin.create", project.String(), p.Actor, idemKey))
	}
	var replayed bool
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		have, err := st.PreviewOrigins(ctx, project)
		if err != nil {
			return err
		}
		if len(have) >= domain.MaxPreviewOrigins {
			return domain.ErrTooManyPreviewOrigins
		}
		inserted, err := st.InsertPreviewOrigin(ctx, row)
		if err != nil {
			return err
		}
		if inserted {
			return nil
		}
		// Either the same origin is registered already, or this is a
		// retry of the same idempotent request.
		existing, err := st.PreviewOriginFor(ctx, project, o)
		if err != nil {
			return err
		}
		if existing.ID != row.ID {
			return domain.ErrOriginRegistered
		}
		row, replayed = existing, true
		return nil
	})
	if err != nil {
		return domain.PreviewOrigin{}, false, err
	}
	return row, replayed, nil
}

// UnregisterPreviewOrigin removes a registration and, with it, every
// in-context grant minted for that origin: taking an origin away ends
// the editor sessions on it now, not in fifteen minutes.
func (s *Service) UnregisterPreviewOrigin(ctx context.Context, project domain.ProjectRef, id domain.PreviewOriginID) error {
	if err := authz.RequireIn(ctx, authz.TokensManage, project.UUID()); err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		o, err := st.PreviewOrigin(ctx, id)
		if err != nil {
			return err
		}
		if o.ProjectID != project {
			return ErrNotFound
		}
		return st.DeletePreviewOrigin(ctx, o)
	})
}

// MintedGrant is a fresh in-context grant and its one-time secret.
type MintedGrant struct {
	Grant  domain.InContextGrant
	Secret domain.TokenSecret
}

// MintInContextGrant issues the in-product editor's credential for one
// project and one origin.
//
// Only a signed-in person may ask: the grant's actor is the person, so
// the audit trail names them, and an API token minting one would launder
// a tenant credential into a person's. The origin must already be
// registered for the project. What the grant allows is the person's own
// grant intersected with domain.InContextPermissions — locale scopes
// intact, so a translator limited to de stays limited to de — and never
// more, whatever the person may otherwise do.
func (s *Service) MintInContextGrant(ctx context.Context, project domain.ProjectRef, origin string) (MintedGrant, error) {
	p, ok := authz.From(ctx)
	if !ok {
		return MintedGrant{}, ErrUnauthenticated
	}
	if p.Person.IsZero() {
		return MintedGrant{}, domain.ErrPersonGrantOnly
	}
	// The grant is cut from the person and never widens them (RFC 0006
	// §4.1, §3.3): a project outside their scope does not exist to
	// them, and a member who sees only their assignments gets no
	// in-context grant at all — the overlay shows a whole page of a
	// product's strings, which no assignment covers.
	if err := authz.InProject(ctx, project.UUID()); err != nil {
		return MintedGrant{}, err
	}
	if p.Assigned() {
		return MintedGrant{}, &authz.DeniedError{Permission: authz.TranslationsWrite, Assigned: true}
	}
	o, err := domain.ParseOrigin(origin)
	if err != nil {
		return MintedGrant{}, err
	}
	var out MintedGrant
	err = s.tx.InTenant(ctx, func(ctx context.Context, st TenantStore) error {
		if _, err := st.PreviewOriginFor(ctx, project, o); err != nil {
			if errors.Is(err, ErrNotFound) {
				return domain.ErrOriginNotRegistered
			}
			return err
		}
		g, secret, err := domain.NewInContextGrant(p.Tenant, project, p.Person, o, p.Grant, s.now())
		if err != nil {
			return err
		}
		if err := st.InsertGrant(ctx, g); err != nil {
			return err
		}
		out = MintedGrant{Grant: g, Secret: secret}
		return nil
	})
	if err != nil {
		return MintedGrant{}, err
	}
	return out, nil
}

// AuthenticateInContextGrant resolves an in-context grant to its record,
// refusing one that is malformed, unknown, expired — or presented from
// an origin other than the one it was minted for. That last check is
// what makes the credential worth carrying in a page's memory: copied
// anywhere else, it opens nothing.
func (s *Service) AuthenticateInContextGrant(ctx context.Context, bearer, origin string) (Authn, error) {
	secret, err := domain.ParseInContextSecret(bearer)
	if err != nil {
		return Authn{}, ErrUnauthenticated
	}
	now := s.now()
	var rec GrantRecord
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		if rec, err = st.GrantByHash(ctx, secret.Hash()); err != nil {
			return err
		}
		if !now.Before(rec.ExpiresAt) {
			return ErrUnauthenticated
		}
		return st.TouchGrant(ctx, rec.ID, now)
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnauthenticated) {
		return Authn{}, ErrUnauthenticated
	}
	if err != nil {
		return Authn{}, err
	}
	// The binding is checked after the lookup so a wrong origin and a
	// wrong secret take the same path, and only ever after the
	// credential itself proved good.
	from, err := domain.ParseOrigin(origin)
	if err != nil || from.String() != rec.Origin.String() {
		return Authn{}, domain.ErrOriginNotBound
	}
	return Authn{Actor: domain.PersonActor(rec.Person), Person: rec.Person, Grant: &rec}, nil
}

// PreviewOriginRegistered reports whether origin is a registered preview
// origin of any project, which is what a CORS preflight can know: it
// carries no credentials, so there is no tenant and no project yet. The
// grant's own binding does the per-project part later.
func (s *Service) PreviewOriginRegistered(ctx context.Context, origin string) (bool, error) {
	o, err := domain.ParseOrigin(origin)
	if err != nil {
		return false, nil
	}
	var ok bool
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		ok, err = st.OriginRegistered(ctx, o.String())
		return err
	})
	return ok, err
}

// PurgeExpiredGrants drops grants past their fifteen minutes. Nothing
// depends on it for safety — authentication checks expires_at on every
// request — it only keeps the table small.
func (s *Service) PurgeExpiredGrants(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		n, err = st.PurgeExpiredGrants(ctx, before)
		return err
	})
	return n, err
}
