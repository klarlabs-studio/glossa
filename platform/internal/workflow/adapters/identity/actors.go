package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
)

// Actors implements app.Actors over Identity: the actor whose event
// caused a transition is given exactly the grant Identity would give
// them on a request now (RFC 0006 §2.5) — a person's active
// membership's roles and locales, an API token's scopes.
//
// An actor who holds nothing in the tenant any more — a removed member,
// a revoked or expired token — gets no principal, and the transition's
// actions are refused. So does a token that is not an API token (a CI
// token or an in-context grant names its own grant only while it is
// presented, and is never re-derived afterwards): a workflow never acts
// with more than a request by that caller could do now.
type Actors struct {
	identity *identityapp.Service
	now      func() time.Time
}

// NewActors returns the adapter.
func NewActors(s *identityapp.Service) *Actors { return &Actors{identity: s, now: time.Now} }

var _ app.Actors = (*Actors)(nil)

// tokenReader reads a token's record to learn its scopes; it holds
// tokens.read and nothing else.
const tokenReader = "workflow.actors"

// Principal implements app.Actors.
func (a *Actors) Principal(ctx context.Context, actor outbox.Actor) (authz.Principal, bool, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return authz.Principal{}, false, errors.New("workflow: resolving an actor needs a tenant")
	}
	parsed, err := domain.ParseActor(actor.String())
	if err != nil {
		return authz.Principal{}, false, nil
	}
	switch parsed.Kind {
	case domain.ActorPerson:
		return a.person(ctx, tenant, domain.PersonID(parsed.ID))
	case domain.ActorToken:
		return a.token(ctx, tenant, domain.TokenID(parsed.ID))
	}
	return authz.Principal{}, false, nil
}

func (a *Actors) person(ctx context.Context, tenant tenancy.ID, person domain.PersonID) (authz.Principal, bool, error) {
	// Identity resolves memberships in its system scope, which refuses a
	// context that already names a tenant: the tenant is passed
	// explicitly instead, exactly as the HTTP edge does.
	noTenant := tenancy.ContextWithTenant(ctx, tenancy.ID{})
	p, err := a.identity.Authorize(noTenant, identityapp.Authn{Actor: domain.PersonActor(person), Person: person}, tenant)
	if errors.Is(err, identityapp.ErrForbidden) {
		return authz.Principal{}, false, nil
	}
	if err != nil {
		return authz.Principal{}, false, err
	}
	return p, true, nil
}

func (a *Actors) token(ctx context.Context, tenant tenancy.ID, id domain.TokenID) (authz.Principal, bool, error) {
	bg, err := authz.Background(ctx, tokenReader, authz.TokensRead)
	if err != nil {
		return authz.Principal{}, false, err
	}
	t, err := a.identity.GetToken(bg, id)
	if errors.Is(err, identityapp.ErrNotFound) {
		return authz.Principal{}, false, nil
	}
	if err != nil {
		return authz.Principal{}, false, fmt.Errorf("workflow: read token %s: %w", uuid.UUID(id), err)
	}
	if t.CheckUsable(a.now()) != nil {
		return authz.Principal{}, false, nil
	}
	return authz.Principal{
		Actor: domain.TokenActor(id), Tenant: tenant, TokenTenant: tenant, Grant: domain.GrantForScopes(t.Scopes),
	}, true, nil
}

// Held implements app.Actors.
func (a *Actors) Held(p authz.Principal, locale string) []string {
	var tag domain.Locale
	scoped := false
	if locale != "" {
		if l, err := domain.ParseLocale(locale); err == nil {
			tag, scoped = l, true
		}
	}
	var out []string
	for _, perm := range domain.AllPermissions() {
		if (scoped && p.Grant.AllowsFor(perm, tag)) || (!scoped && p.Grant.Allows(perm)) {
			out = append(out, string(perm))
		}
	}
	return out
}
