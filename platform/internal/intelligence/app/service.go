package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// Deps are the Service's ports.
type Deps struct {
	Tx           Transactor
	Catalog      Catalog
	Localization Localization
	Environments Environments
	Knowledge    Knowledge
	// Usages is the Context context, for message_context's usages and
	// co-located neighbours; nil leaves usages empty and neighbours to
	// the key prefix.
	Usages    UsageContext
	Providers ProviderFactory
	Sealer    Sealer
	Metrics   Metrics
	Logger    *slog.Logger
	// Prices are the deployment's default prices (DefaultPrices when nil);
	// tenants override them.
	Prices domain.PriceTable
	// AllowPrivateEndpoints lets tenants point providers at loopback or
	// private addresses (self-hosted models in the cluster).
	AllowPrivateEndpoints bool
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// Service implements the Intelligence wiring's use cases (RFC 0003
// §3.1, §3.3, §3.4, §7): provider configuration, routing, prices,
// budgets, privacy settings, jobs and their triggers, suggestions and
// the review queue.
type Service struct {
	Deps
	prompts PromptVersions
}

// NewService returns the service.
func NewService(d Deps) (*Service, error) {
	if d.Tx == nil || d.Catalog == nil || d.Localization == nil || d.Environments == nil || d.Knowledge == nil ||
		d.Providers == nil || d.Sealer == nil {
		return nil, errors.New("intelligence: every port is required")
	}
	if d.Metrics == nil {
		d.Metrics = NoMetrics{}
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Prices == nil {
		d.Prices = DefaultPrices()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{Deps: d, prompts: DefaultPromptVersions()}, nil
}

// actor returns the acting principal after checking perm.
func actor(ctx context.Context, perm authz.Permission) (string, error) {
	if err := authz.Require(ctx, perm); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// actorIn returns the acting principal after checking perm in project:
// a project outside the caller's scope is authz.ErrNotVisible, the
// answer for one that does not exist (RFC 0006 §4.1).
func actorIn(ctx context.Context, perm authz.Permission, project uuid.UUID) (string, error) {
	if err := authz.RequireIn(ctx, perm, project); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// tenantActor returns the acting principal after checking perm for a
// change to the tenant's own AI configuration — providers, consent,
// budget, prices, the tenant's routing — which applies to every project
// and so is refused to a principal limited to some (RFC 0006 §4.1).
func tenantActor(ctx context.Context, perm authz.Permission) (string, error) {
	if err := authz.RequireUnscoped(ctx, perm); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// actorFor checks a locale-scoped permission.
func actorFor(ctx context.Context, perm authz.Permission, locale string) (string, error) {
	l, err := authz.ParseLocale(locale)
	if err != nil {
		return "", invalid("invalid_locale", err.Error())
	}
	if err := authz.RequireFor(ctx, perm, l); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// allowedFor reports whether the caller holds perm for locale.
func allowedFor(ctx context.Context, perm authz.Permission, locale string) bool {
	l, err := authz.ParseLocale(locale)
	return err == nil && authz.RequireFor(ctx, perm, l) == nil
}

// tenantOf returns the tenant of the principal on ctx.
func tenantOf(ctx context.Context) uuid.UUID {
	p, _ := authz.From(ctx)
	return p.Tenant.UUID()
}

// idempotentID derives a create's ID from its Idempotency-Key, or a new
// time-ordered ID when there is none.
func idempotentID(ctx context.Context, operation, by, key string) (uuid.UUID, error) {
	if key == "" {
		return uuid.Must(uuid.NewV7()), nil
	}
	if err := idempotency.CheckKey(key); err != nil {
		return uuid.Nil, err
	}
	p, _ := authz.From(ctx)
	return idempotency.ID(operation, p.Tenant.String(), by, key), nil
}

// checkIfMatch applies optimistic concurrency to an existing resource.
func checkIfMatch(version int, ifMatch *int) error {
	switch {
	case ifMatch == nil:
		return ErrPreconditionRequired
	case *ifMatch != version:
		return ErrPreconditionFailed
	}
	return nil
}

// checkOptionalIfMatch applies If-Match when it is sent (settings
// documents that always exist, with defaults). Unsaved defaults are at
// version 0, so If-Match 0 means "only while nobody saved them".
func checkOptionalIfMatch(version int, ifMatch *int) error {
	if ifMatch != nil && *ifMatch != version {
		return ErrPreconditionFailed
	}
	return nil
}

// preconditionOf turns a lost race of a conditional write — another
// writer saved the document (or created it, at version 0) since it was
// read — into the failed precondition it is.
func preconditionOf(err error, ifMatch *int) error {
	if ifMatch != nil && errors.Is(err, ErrStaleVersion) {
		return ErrPreconditionFailed
	}
	return err
}

func invalid(code problem.Code, detail string) error {
	return problem.New(http.StatusBadRequest, code, detail)
}

// monthStart is the first instant of t's calendar month (UTC): budgets
// are monthly.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
