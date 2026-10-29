package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// ProviderInput is a provider as written. APIKey is write-only.
type ProviderInput struct {
	Name    string
	Kind    string
	BaseURL string
	Models  []string
	Enabled *bool
	APIKey  string
}

// ProviderPatch changes some of a provider's fields; nil keeps one.
type ProviderPatch struct {
	Name    *string
	BaseURL *string
	Models  *[]string
	Enabled *bool
	// APIKey replaces the key; ClearAPIKey removes it.
	APIKey      *string
	ClearAPIKey bool
}

// sealKey seals a provider key for its tenant and row.
func (s *Service) sealKey(tenant, provider uuid.UUID, key string) ([]byte, error) {
	if key == "" {
		return nil, nil
	}
	if len(key) > domain.MaxAPIKeyLen || strings.TrimSpace(key) != key {
		return nil, fmt.Errorf("%w: the API key must be at most %d characters without surrounding spaces", domain.ErrInvalidProvider, domain.MaxAPIKeyLen)
	}
	return s.Sealer.Seal([]byte(key), "intelligence.provider", tenant.String(), provider.String())
}

// openKey opens a stored provider key.
func (s *Service) openKey(tenant uuid.UUID, p StoredProvider) (string, error) {
	if len(p.SealedKey) == 0 {
		return "", nil
	}
	raw, err := s.Sealer.Open(p.SealedKey, "intelligence.provider", tenant.String(), p.ID.String())
	if err != nil {
		return "", fmt.Errorf("provider %s: the stored API key can't be opened (was GLOSSA_AUTH_SECRET rotated? enter the key again): %w", p.Name, err)
	}
	return string(raw), nil
}

// CreateProvider configures a provider. Needs intelligence.manage.
func (s *Service) CreateProvider(ctx context.Context, in ProviderInput, idemKey string) (domain.ProviderConfig, bool, error) {
	by, err := tenantActor(ctx, authz.IntelligenceManage)
	if err != nil {
		return domain.ProviderConfig{}, false, err
	}
	id, err := idempotentID(ctx, "intelligence.create_provider", by, idemKey)
	if err != nil {
		return domain.ProviderConfig{}, false, err
	}
	kind, err := domain.ParseProviderKind(in.Kind)
	if err != nil {
		return domain.ProviderConfig{}, false, err
	}
	now := s.Now()
	p := domain.ProviderConfig{
		ID: id, Name: in.Name, Kind: kind, BaseURL: in.BaseURL, Models: dedupe(in.Models), Enabled: in.Enabled == nil || *in.Enabled,
		HasKey: in.APIKey != "", Version: 1, CreatedBy: by, CreatedAt: now, UpdatedBy: by, UpdatedAt: now,
	}
	if err := p.Validate(s.AllowPrivateEndpoints); err != nil {
		return domain.ProviderConfig{}, false, err
	}
	sealed, err := s.sealKey(tenantOf(ctx), id, in.APIKey)
	if err != nil {
		return domain.ProviderConfig{}, false, err
	}
	var (
		out      domain.ProviderConfig
		replayed bool
	)
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		inserted, err := st.InsertProvider(ctx, p, sealed)
		if err != nil {
			return err
		}
		if inserted {
			out = p
			return nil
		}
		prior, err := st.Provider(ctx, id)
		if err != nil {
			return err
		}
		if prior.Name != p.Name || prior.Kind != p.Kind || prior.BaseURL != p.BaseURL || !slices.Equal(prior.Models, p.Models) {
			return ErrIdempotencyReuse
		}
		out, replayed = prior, true
		return nil
	})
	return out, replayed, err
}

// GetProvider returns a provider (never its key). Needs
// intelligence.read.
func (s *Service) GetProvider(ctx context.Context, id uuid.UUID) (domain.ProviderConfig, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return domain.ProviderConfig{}, err
	}
	var p domain.ProviderConfig
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		p, err = st.Provider(ctx, id)
		return err
	})
	return p, err
}

// ListProviders lists providers by name. Needs intelligence.read.
func (s *Service) ListProviders(ctx context.Context, page pagination.Page) ([]domain.ProviderConfig, *string, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return nil, nil, err
	}
	var rows []domain.ProviderConfig
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.Providers(ctx, page.After, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(p domain.ProviderConfig) string { return p.Name })
	return items, next, nil
}

// UpdateProvider changes a provider at version ifMatch. Renaming a
// provider a stored routing policy names is refused. Needs
// intelligence.manage.
func (s *Service) UpdateProvider(ctx context.Context, id uuid.UUID, ifMatch *int, patch ProviderPatch) (domain.ProviderConfig, error) {
	by, err := tenantActor(ctx, authz.IntelligenceManage)
	if err != nil {
		return domain.ProviderConfig{}, err
	}
	var out domain.ProviderConfig
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, err := st.LockProvider(ctx, id)
		if err != nil {
			return err
		}
		if err := checkIfMatch(cur.Version, ifMatch); err != nil {
			return err
		}
		p, sealed := cur.ProviderConfig, cur.SealedKey
		if patch.Name != nil && *patch.Name != p.Name {
			if err := s.refuseInUse(ctx, st, p.Name); err != nil {
				return err
			}
			p.Name = *patch.Name
		}
		if patch.BaseURL != nil {
			p.BaseURL = *patch.BaseURL
		}
		if patch.Models != nil {
			p.Models = dedupe(*patch.Models)
		}
		if patch.Enabled != nil {
			p.Enabled = *patch.Enabled
		}
		switch {
		case patch.ClearAPIKey:
			sealed, p.HasKey = nil, false
		case patch.APIKey != nil:
			if sealed, err = s.sealKey(tenantOf(ctx), id, *patch.APIKey); err != nil {
				return err
			}
			p.HasKey = len(sealed) > 0
		}
		if err := p.Validate(s.AllowPrivateEndpoints); err != nil {
			return err
		}
		p.UpdatedBy, p.UpdatedAt = by, s.Now()
		if err := st.UpdateProvider(ctx, p, sealed, cur.Version); err != nil {
			return err
		}
		p.Version = cur.Version + 1
		out = p
		return nil
	})
	return out, err
}

// DeleteProvider removes a provider no stored routing policy names.
// Needs intelligence.manage.
func (s *Service) DeleteProvider(ctx context.Context, id uuid.UUID) error {
	if _, err := tenantActor(ctx, authz.IntelligenceManage); err != nil {
		return err
	}
	return s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		p, err := st.LockProvider(ctx, id)
		if err != nil {
			return err
		}
		if err := s.refuseInUse(ctx, st, p.Name); err != nil {
			return err
		}
		return st.DeleteProvider(ctx, id)
	})
}

// refuseInUse refuses to rename or remove a provider a stored routing
// policy (the tenant's or a project's) routes to.
func (s *Service) refuseInUse(ctx context.Context, st Store, name string) error {
	policies, err := st.RoutingPolicies(ctx)
	if err != nil {
		return err
	}
	for _, r := range policies {
		for _, rule := range r.Policy.Rules {
			for _, rt := range rule.Routes {
				if rt.Provider == name {
					scope := "the tenant's routing policy"
					if r.ProjectID != nil {
						scope = "project " + r.ProjectID.String() + "'s routing policy"
					}
					return fmt.Errorf("%w: %s routes %s to %s", ErrProviderInUse, scope, rule.Task, name)
				}
			}
		}
	}
	return nil
}

// Settings are the tenant's AI settings with their version.
func (s *Service) settings(ctx context.Context, st Store, lock bool) (domain.TenantSettings, error) {
	cur, found, err := st.Settings(ctx, lock)
	if err != nil || !found {
		return domain.DefaultTenantSettings(), err
	}
	return cur, nil
}

// GetSettings returns the tenant's AI settings (defaults when none were
// saved: consent off, no budget). Needs intelligence.read.
func (s *Service) GetSettings(ctx context.Context) (domain.TenantSettings, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return domain.TenantSettings{}, err
	}
	var out domain.TenantSettings
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		out, err = s.settings(ctx, st, false)
		return err
	})
	return out, err
}

// SettingsInput changes the tenant's settings; nil keeps a field.
type SettingsInput struct {
	ProviderConsent   *bool
	MaxConcurrentJobs *int
	MonthlyBudget     *domain.MicroUSD
	Prices            *domain.PriceTable
}

// PutSettings changes the tenant's AI settings (consent, concurrency,
// budget, prices). A consent change records who and when. Needs
// intelligence.manage.
func (s *Service) PutSettings(ctx context.Context, in SettingsInput, ifMatch *int) (domain.TenantSettings, error) {
	by, err := tenantActor(ctx, authz.IntelligenceManage)
	if err != nil {
		return domain.TenantSettings{}, err
	}
	var out domain.TenantSettings
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, err := s.settings(ctx, st, true)
		if err != nil {
			return err
		}
		if err := checkOptionalIfMatch(cur.Version, ifMatch); err != nil {
			return err
		}
		next, now := cur, s.Now()
		if in.ProviderConsent != nil && *in.ProviderConsent != cur.ProviderConsent {
			next.ProviderConsent, next.ConsentChangedBy, next.ConsentChangedAt = *in.ProviderConsent, by, &now
		}
		if in.MaxConcurrentJobs != nil {
			next.MaxConcurrentJobs = *in.MaxConcurrentJobs
		}
		if in.MonthlyBudget != nil {
			next.MonthlyBudget = *in.MonthlyBudget
		}
		if in.Prices != nil {
			next.Prices = *in.Prices
		}
		if err := next.Validate(); err != nil {
			return err
		}
		next.UpdatedBy, next.UpdatedAt = by, now
		if err := st.SaveSettings(ctx, next, cur.Version); err != nil {
			return preconditionOf(err, ifMatch)
		}
		next.Version = cur.Version + 1
		out = next
		return nil
	})
	return out, err
}

// Budget is the tenant's monthly budget and this month's spend.
type Budget struct {
	Settings   domain.TenantSettings
	MonthStart time.Time
	Spent      domain.MicroUSD
	Calls      int
	ByProvider []ProviderSpend
}

// Remaining is what the month still allows (never negative).
func (b Budget) Remaining() domain.MicroUSD { return max(b.Settings.MonthlyBudget-b.Spent, 0) }

// GetBudget returns the budget with this month's spend per provider.
// Needs intelligence.read.
func (s *Service) GetBudget(ctx context.Context) (Budget, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return Budget{}, err
	}
	b := Budget{MonthStart: monthStart(s.Now())}
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if b.Settings, err = s.settings(ctx, st, false); err != nil {
			return err
		}
		if b.Spent, b.Calls, err = st.SpendSince(ctx, b.MonthStart); err != nil {
			return err
		}
		b.ByProvider, err = st.SpendByProvider(ctx, b.MonthStart)
		return err
	})
	return b, err
}

// CanRead checks intelligence.read (reads that touch no store, like the
// eval baseline).
func (s *Service) CanRead(ctx context.Context) error {
	return authz.Require(ctx, authz.IntelligenceRead)
}

// ListSpend lists the ledger from since on (this month when zero),
// newest first. Needs intelligence.read.
func (s *Service) ListSpend(ctx context.Context, since time.Time, page pagination.Page) ([]SpendEntry, *string, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return nil, nil, err
	}
	before, err := parseCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	if since.IsZero() {
		since = monthStart(s.Now())
	}
	var rows []SpendEntry
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.SpendEntries(ctx, since, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(e SpendEntry) string { return Cursor{At: e.OccurredAt, ID: e.ID}.String() })
	return items, next, nil
}

// EffectivePrices are the deployment's defaults with the tenant's
// overrides. Configured providers of kind anthropic named other than
// "anthropic" get the Anthropic defaults under their name.
func (s *Service) effectivePrices(settings domain.TenantSettings, providers []StoredProvider) domain.PriceTable {
	base := domain.PriceTable{}
	for k, v := range s.Prices {
		base[k] = v
	}
	for _, p := range providers {
		if p.Kind != domain.KindAnthropic || p.Name == "anthropic" {
			continue
		}
		for k, v := range s.Prices {
			if provider, model, ok := strings.Cut(k, "/"); ok && provider == "anthropic" {
				base[domain.PriceKey(p.Name, model)] = v
			}
		}
	}
	return base.Merged(settings.Prices)
}

// Prices are the price table in effect.
type Prices struct {
	Defaults, Overrides, Effective domain.PriceTable
	Version                        int
}

// GetPrices returns the default, overridden and effective prices. Needs
// intelligence.read.
func (s *Service) GetPrices(ctx context.Context) (Prices, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return Prices{}, err
	}
	var out Prices
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		settings, err := s.settings(ctx, st, false)
		if err != nil {
			return err
		}
		providers, err := st.AllProviders(ctx)
		if err != nil {
			return err
		}
		out = Prices{Defaults: s.Prices, Overrides: settings.Prices, Effective: s.effectivePrices(settings, providers), Version: settings.Version}
		return nil
	})
	return out, err
}

// ProjectSettingsInput changes a project's settings; nil keeps a field.
type ProjectSettingsInput struct {
	NamespaceTags        *domain.NamespaceTags
	AutoTranslateLocales *[]string
	Review               *domain.ReviewSettings
}

// GetProjectSettings returns a project's AI settings (defaults when
// none were saved). Needs intelligence.read.
func (s *Service) GetProjectSettings(ctx context.Context, project uuid.UUID) (domain.ProjectSettings, error) {
	if err := authz.RequireIn(ctx, authz.IntelligenceRead, project); err != nil {
		return domain.ProjectSettings{}, err
	}
	if _, err := s.Catalog.Project(ctx, project); err != nil {
		return domain.ProjectSettings{}, err
	}
	return s.projectSettings(ctx, project)
}

func (s *Service) projectSettings(ctx context.Context, project uuid.UUID) (domain.ProjectSettings, error) {
	out := domain.DefaultProjectSettings(project)
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, found, err := st.ProjectSettings(ctx, project)
		if found {
			out = cur
		}
		return err
	})
	return out, err
}

// PutProjectSettings changes a project's namespace tags, auto-translate
// locales and review routing. Turning auto_approve on is refused unless
// every listed environment ships approved text (Release's eligibility
// policies). Unsaved defaults have version 0. Needs intelligence.manage.
func (s *Service) PutProjectSettings(ctx context.Context, project uuid.UUID, in ProjectSettingsInput, ifMatch *int) (domain.ProjectSettings, error) {
	by, err := actorIn(ctx, authz.IntelligenceManage, project)
	if err != nil {
		return domain.ProjectSettings{}, err
	}
	if _, err := s.Catalog.Project(ctx, project); err != nil {
		return domain.ProjectSettings{}, err
	}
	var tags domain.NamespaceTags
	if in.NamespaceTags != nil {
		if err := in.NamespaceTags.Validate(); err != nil {
			return domain.ProjectSettings{}, err
		}
		tags = in.NamespaceTags.Normalized()
	}
	var locales []string
	if in.AutoTranslateLocales != nil {
		if locales, err = canonicalLocales(*in.AutoTranslateLocales); err != nil {
			return domain.ProjectSettings{}, err
		}
	}
	if in.Review != nil {
		if err := in.Review.Validate(); err != nil {
			return domain.ProjectSettings{}, err
		}
		if in.Review.AutoApprove {
			if note := s.autoApproveBlocked(ctx, project, in.Review.AutoApproveEnvironments); note != "" {
				return domain.ProjectSettings{}, fmt.Errorf("%w: %s", ErrAutoApproveIneligible, note)
			}
		}
	}
	var out domain.ProjectSettings
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, found, err := st.ProjectSettings(ctx, project)
		if err != nil {
			return err
		}
		if !found {
			cur = domain.DefaultProjectSettings(project)
		}
		if err := checkOptionalIfMatch(cur.Version, ifMatch); err != nil {
			return err
		}
		next := cur
		if in.NamespaceTags != nil {
			next.NamespaceTags = tags
		}
		if in.AutoTranslateLocales != nil {
			next.AutoTranslateLocales = locales
		}
		if in.Review != nil {
			next.Review = *in.Review
		}
		next.UpdatedBy, next.UpdatedAt = by, s.Now()
		if err := st.SaveProjectSettings(ctx, next, cur.Version); err != nil {
			return preconditionOf(err, ifMatch)
		}
		next.Version = cur.Version + 1
		out = next
		return nil
	})
	return out, err
}

// autoApproveBlocked explains why auto_approve can't be honoured for the
// environments ("" when it can): each must exist and ship approved.
func (s *Service) autoApproveBlocked(ctx context.Context, project uuid.UUID, environments []string) string {
	if len(environments) == 0 {
		return "no auto_approve_environments are set"
	}
	for _, env := range environments {
		ok, err := s.Environments.ShipsApproved(ctx, project, env)
		switch {
		case err != nil:
			return fmt.Sprintf("environment %s can't be read: %v", env, err)
		case !ok:
			return fmt.Sprintf("environment %s does not exist or its policy doesn't ship approved translations", env)
		}
	}
	return ""
}

// RoutingView is the routing policy in effect for a scope and where it
// comes from: "project", "tenant" or "default" (DefaultRouting).
type RoutingView struct {
	Record domain.RoutingRecord
	Source string
}

// GetRoutingPolicy returns the policy in effect for a project (nil: the
// tenant). Needs intelligence.read.
func (s *Service) GetRoutingPolicy(ctx context.Context, project *uuid.UUID) (RoutingView, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return RoutingView{}, err
	}
	if project != nil {
		if err := authz.RequireIn(ctx, authz.IntelligenceRead, *project); err != nil {
			return RoutingView{}, err
		}
	}
	if project != nil {
		if _, err := s.Catalog.Project(ctx, *project); err != nil {
			return RoutingView{}, err
		}
	}
	var out RoutingView
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		out, err = routing(ctx, st, project)
		return err
	})
	return out, err
}

// routing resolves project → tenant → default.
func routing(ctx context.Context, st Store, project *uuid.UUID) (RoutingView, error) {
	if project != nil {
		r, found, err := st.RoutingPolicy(ctx, project)
		if err != nil || found {
			return RoutingView{Record: r, Source: "project"}, err
		}
	}
	r, found, err := st.RoutingPolicy(ctx, nil)
	if err != nil || found {
		return RoutingView{Record: r, Source: "tenant"}, err
	}
	return RoutingView{Record: domain.RoutingRecord{Policy: DefaultRouting()}, Source: "default"}, nil
}

// PutRoutingPolicy stores the tenant's (project nil) or a project's
// policy. Every route must name a configured provider whose allow-list
// admits the model. Needs intelligence.manage.
func (s *Service) PutRoutingPolicy(ctx context.Context, project *uuid.UUID, policy domain.RoutingPolicy, ifMatch *int) (RoutingView, error) {
	var by string
	var err error
	if project == nil {
		by, err = tenantActor(ctx, authz.IntelligenceManage)
	} else {
		by, err = actorIn(ctx, authz.IntelligenceManage, *project)
	}
	if err != nil {
		return RoutingView{}, err
	}
	if project != nil {
		if _, err := s.Catalog.Project(ctx, *project); err != nil {
			return RoutingView{}, err
		}
	}
	var out RoutingView
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		providers, err := st.AllProviders(ctx)
		if err != nil {
			return err
		}
		configs := make([]domain.ProviderConfig, len(providers))
		for i, p := range providers {
			configs[i] = p.ProviderConfig
		}
		if err := domain.ValidateRouting(policy, configs); err != nil {
			return err
		}
		cur, found, err := st.RoutingPolicy(ctx, project)
		if err != nil {
			return err
		}
		version := 0
		if found {
			version = cur.Version
		}
		if err := checkOptionalIfMatch(version, ifMatch); err != nil {
			return err
		}
		rec := domain.RoutingRecord{ProjectID: project, Policy: policy, UpdatedBy: by, UpdatedAt: s.Now()}
		if err := st.SaveRoutingPolicy(ctx, rec, version); err != nil {
			return preconditionOf(err, ifMatch)
		}
		rec.Version = version + 1
		out = RoutingView{Record: rec, Source: "tenant"}
		if project != nil {
			out.Source = "project"
		}
		return nil
	})
	return out, err
}

// DeleteRoutingPolicy removes a project's policy: the tenant's (or the
// default) applies again. Needs intelligence.manage.
func (s *Service) DeleteRoutingPolicy(ctx context.Context, project uuid.UUID) error {
	if _, err := actorIn(ctx, authz.IntelligenceManage, project); err != nil {
		return err
	}
	return s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return st.DeleteRoutingPolicy(ctx, project)
	})
}

// canonicalLocales parses, canonicalizes and de-duplicates locales.
func canonicalLocales(in []string) ([]string, error) {
	out := []string{}
	for _, l := range in {
		t, err := bcp47.Parse(l)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, t.String()) {
			out = append(out, t.String())
		}
	}
	if len(out) > 200 {
		return nil, fmt.Errorf("%w: at most 200 auto-translate locales", domain.ErrInvalidSettings)
	}
	return out, nil
}

func dedupe(in []string) []string {
	out := []string{}
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// String encodes a cursor for a page token.
func (c Cursor) String() string { return c.At.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String() }

func parseCursor(s string) (*Cursor, error) {
	if s == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(s, "|")
	t, err1 := time.Parse(time.RFC3339Nano, at)
	u, err2 := uuid.Parse(id)
	if !ok || errors.Join(err1, err2) != nil {
		return nil, invalid("invalid_page_token", "page_token is not one this list issued")
	}
	return &Cursor{At: t, ID: u}, nil
}
