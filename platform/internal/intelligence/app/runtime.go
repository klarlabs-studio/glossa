package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// jobRuntime is what one job runs with: the tenant's settings, the
// project's, and a router over the tenant's enabled providers.
type jobRuntime struct {
	settings domain.TenantSettings
	project  domain.ProjectSettings
	router   *Router
}

// runtimeFor loads a job's configuration and builds its router: the
// routing policy in effect (project → tenant → default), the effective
// prices and the tenant's budget, over the enabled providers (each
// limited to its model allow-list).
func (s *Service) runtimeFor(ctx context.Context, j domain.Job) (jobRuntime, error) {
	return s.runtimeOf(ctx, j.ProjectID, &j.ID)
}

// LinguisticRouter is the router one project's linguistic review runs
// on (RFC 0005 §3.8): the tenant's enabled providers, the routing
// policy in effect, the effective prices and — the part that matters —
// the same monthly budget guard a translation job spends through.
//
// It exists because the linguistic layer is Quality's job and
// Intelligence's model call, and the two meet at a router that can only
// be built per tenant, from the caller's own context. A composition
// root has no tenant, so it cannot hold one; it holds this accessor
// instead. RFC 0005 §10 bounds the layer by the *existing* per-tenant
// AI budget, and handing out the job runtime rather than a second one
// is how that stays true.
//
// There is no Intelligence job behind the call — the job is Quality's —
// so the spend is booked against the project with no job ID.
func (s *Service) LinguisticRouter(ctx context.Context, project uuid.UUID) (*Router, error) {
	if err := authz.RequireIn(ctx, authz.IntelligenceRead, project); err != nil {
		return nil, err
	}
	rt, err := s.runtimeOf(ctx, project, nil)
	if err != nil {
		return nil, err
	}
	return rt.router, nil
}

// runtimeOf builds the runtime for one project. job is the Intelligence
// job the spend is booked against, or nil where the work belongs to
// another context's job.
func (s *Service) runtimeOf(ctx context.Context, project uuid.UUID, job *uuid.UUID) (jobRuntime, error) {
	var (
		rt        jobRuntime
		providers []StoredProvider
		policy    RoutingView
	)
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if rt.settings, err = s.settings(ctx, st, false); err != nil {
			return err
		}
		ps, found, err := st.ProjectSettings(ctx, project)
		if err != nil {
			return err
		}
		rt.project = domain.DefaultProjectSettings(project)
		if found {
			rt.project = ps
		}
		if providers, err = st.AllProviders(ctx); err != nil {
			return err
		}
		policy, err = routing(ctx, st, &project)
		return err
	})
	if err != nil {
		return jobRuntime{}, err
	}
	tenant := tenantOf(ctx)
	byName := map[string]domain.Provider{}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		// A provider that can't be built (its key doesn't open, or is
		// missing) is left out: routes to it fail as unconfigured, and the
		// job says so, instead of retrying what waiting can't fix.
		key, err := s.openKey(tenant, p)
		if err != nil {
			s.Logger.WarnContext(ctx, "intelligence: provider unusable", slog.String("provider", p.Name), slog.Any("error", err))
			continue
		}
		prov, err := s.Providers.Provider(p.ProviderConfig, key)
		if err != nil {
			s.Logger.WarnContext(ctx, "intelligence: provider unusable", slog.String("provider", p.Name), slog.Any("error", err))
			continue
		}
		byName[p.Name] = allowListed{next: prov, cfg: p.ProviderConfig}
	}
	prices := s.effectivePrices(rt.settings, providers)
	budget := &budgetGuard{s: s, tenant: tenant, job: job, project: project, prices: prices}
	rt.router = NewRouter(byName, policy.Record.Policy, prices, budget)
	return rt, nil
}

// allowListed refuses models outside a provider's allow-list, as a
// rejected request: the router falls back to the next route.
type allowListed struct {
	next domain.Provider
	cfg  domain.ProviderConfig
}

func (a allowListed) Name() string { return a.cfg.Name }

func (a allowListed) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	if !a.cfg.Allows(req.Model) {
		return domain.Completion{}, &domain.ProviderError{
			Provider: a.cfg.Name, Kind: domain.KindInvalidRequest,
			Err: fmt.Errorf("model %q is not in the provider's allow-list", req.Model),
		}
	}
	return a.next.Complete(ctx, req)
}

// budgetGuard is the tenant's monthly budget (RFC 0003 §3.1): a call is
// refused when this month's spend plus its upper-bound estimate would
// pass the cap, and every priced call is booked in the ledger. A tenant
// without a budget (0) can't spend at all: the hard stop. Calls in
// flight when the cap is reached finish, so a month can end at most
// their cost over it.
type budgetGuard struct {
	s      *Service
	tenant uuid.UUID
	// job is the Intelligence job the spend belongs to, or nil where the
	// call was made for another context's job (the linguistic layer's,
	// RFC 0005 §3.8): the money is the tenant's either way, and the
	// ledger row names the project rather than a job that does not
	// exist here.
	job     *uuid.UUID
	project uuid.UUID
	prices  domain.PriceTable
}

var _ domain.BudgetGuard = (*budgetGuard)(nil)

// Check implements domain.BudgetGuard.
func (b *budgetGuard) Check(ctx context.Context, _ domain.Scope, estimate domain.MicroUSD) error {
	return b.s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		settings, err := b.s.settings(ctx, st, false)
		if err != nil {
			return err
		}
		spent, _, err := st.SpendSince(ctx, monthStart(b.s.Now()))
		if err != nil {
			return err
		}
		if spent+estimate > settings.MonthlyBudget {
			return fmt.Errorf("%w: this month's spend is %v of the %v budget and the call may cost up to %v",
				domain.ErrBudgetExceeded, spent, settings.MonthlyBudget, estimate)
		}
		return nil
	})
}

// Record implements domain.BudgetGuard.
func (b *budgetGuard) Record(ctx context.Context, _ domain.Scope, sp domain.Spend) error {
	project := b.project
	_, priced := b.prices[domain.PriceKey(sp.Provider, sp.Model)]
	e := SpendEntry{ID: uuid.Must(uuid.NewV7()), JobID: b.job, ProjectID: &project, Spend: sp, Priced: priced, OccurredAt: b.s.Now()}
	if err := b.s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error { return st.InsertSpend(ctx, e) }); err != nil {
		return err
	}
	b.s.Metrics.Spent(b.tenant, sp.Provider, sp.Model, sp.Cost)
	return nil
}

// jobKnowledge is the agent's Knowledge for one job: translation memory,
// termbase and style from the Knowledge context, and the message's
// context from Catalog and Localization.
type jobKnowledge struct {
	Knowledge
	s   *Service
	msg SourceMessage
}

var _ domain.Knowledge = (*jobKnowledge)(nil)

// What the context shows: at most usageLimit usages and neighbourLimit
// neighbouring messages (RFC 0004 §2.2).
const (
	usageLimit     = 10
	neighbourLimit = 5
)

// MessageContext implements domain.MessageContexts.
func (k *jobKnowledge) MessageContext(ctx context.Context, _ domain.Scope, _ string, targetLocale string) (domain.MessageContext, error) {
	return k.s.messageContext(ctx, k.msg, targetLocale)
}

// messageContext is a message's context for the agent: description, max
// length, where it is used (`file:line (component, route)`, the default
// branch first) and its neighbours with their translations — the
// messages shown together with it (sharing a route or a capture, RFC
// 0004 §8) first, then those sharing its key prefix. The namespace's
// policy tags travel on the request (not here), so they are counted
// once.
func (s *Service) messageContext(ctx context.Context, msg SourceMessage, locale string) (domain.MessageContext, error) {
	c := domain.MessageContext{
		MessageID: msg.ID.String(), Key: msg.Key, Namespace: msg.Namespace,
		Description: msg.Description, MaxLength: msg.MaxLength,
	}
	var err error
	if c.Usages, err = s.usagesOf(ctx, msg); err != nil {
		return domain.MessageContext{}, err
	}
	if c.Neighbours, err = s.coLocated(ctx, msg, locale); err != nil {
		return domain.MessageContext{}, err
	}
	prefix, _, ok := cutLast(msg.Key, ".")
	if !ok || len(c.Neighbours) == neighbourLimit {
		return c, nil
	}
	ns, err := s.Localization.Neighbours(ctx, msg.ProjectID, prefix+".", msg.Key, locale, neighbourLimit+len(c.Neighbours))
	if err != nil {
		return domain.MessageContext{}, err
	}
	for _, n := range ns {
		if len(c.Neighbours) == neighbourLimit {
			break
		}
		if !slices.ContainsFunc(c.Neighbours, func(have domain.Neighbour) bool { return have.Key == n.Key }) {
			c.Neighbours = append(c.Neighbours, domain.Neighbour{Key: n.Key, Source: n.SourceMF2, Translation: n.Translation})
		}
	}
	return c, nil
}

// usagesOf formats the message's current usages, without repeats (two
// collectors can report the same place).
func (s *Service) usagesOf(ctx context.Context, msg SourceMessage) ([]string, error) {
	if s.Usages == nil {
		return nil, nil
	}
	us, err := s.Usages.Usages(ctx, msg.ProjectID, msg.ID, usageLimit)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range us {
		if line := u.String(); !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out, nil
}

// coLocated returns the active messages shown together with msg, in
// the port's order, with their translations in locale (a rejected one
// is left out, as for key-prefix neighbours).
func (s *Service) coLocated(ctx context.Context, msg SourceMessage, locale string) ([]domain.Neighbour, error) {
	if s.Usages == nil {
		return nil, nil
	}
	ids, err := s.Usages.CoLocated(ctx, msg.ProjectID, msg.ID, neighbourLimit)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	found, err := s.Catalog.MessagesByIDs(ctx, msg.ProjectID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]SourceMessage, len(found))
	for _, m := range found {
		byID[m.ID] = m
	}
	var out []domain.Neighbour
	for _, id := range ids {
		m, ok := byID[id]
		if !ok || !m.Active {
			continue
		}
		text, err := sourceText(m.Source)
		if err != nil {
			continue
		}
		n := domain.Neighbour{Key: m.Key, Source: text}
		tr, err := s.Localization.Translation(ctx, msg.ProjectID, m.Key, locale)
		switch {
		case err == nil && tr.Exists && tr.State != "rejected":
			n.Translation = tr.Text
		case err != nil && !errors.Is(err, ErrNotFound):
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// String is the usage as message_context shows it:
// `file:line (component, route)`, leaving out what is unknown.
func (u Usage) String() string {
	var where []string
	for _, s := range []string{u.Component, u.Route} {
		if s != "" {
			where = append(where, s)
		}
	}
	out := u.File + ":" + strconv.Itoa(u.Line)
	if len(where) > 0 {
		out += " (" + strings.Join(where, ", ") + ")"
	}
	return out
}

func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// sourceText is a message's canonical MF2 syntax.
func sourceText(m mf.Message) (string, error) {
	text, err := mf.Stringify(m)
	if err != nil {
		return "", fmt.Errorf("%w: %w", domain.ErrUnparsable, err)
	}
	return text, nil
}
