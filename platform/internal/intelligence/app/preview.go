package app

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/prompts"
)

// Reasons a preview says messages would not reach a provider. Sensitive
// messages are never queued; the others would be queued and fail.
const (
	RefusedSensitive       = domain.FailureSensitive
	RefusedProviderConsent = domain.FailureProviderConsent
	RefusedNoRoute         = domain.FailureNoRoute
	RefusedBudgetExceeded  = domain.FailureBudgetExceeded
)

// SkipLimit counts messages past MaxFillJobs.
const SkipLimit = "limit"

// FillPreview is what a fill would do, without doing any of it: the
// messages it would queue per locale, how many existing jobs and exact
// translation-memory hits cover (no provider call), how many would be
// refused and why, and what the rest would cost.
type FillPreview struct {
	ProjectID uuid.UUID
	// Select is the effective selection.
	Select   FillSelect
	Locales  []LocalePreview
	Warnings []string
	// Cost sums the locales' costs.
	Cost CostEstimate
}

// LocalePreview is one locale of a preview.
type LocalePreview struct {
	Locale string
	// Keys are the messages a fill would queue a job for, in key order.
	Keys []string
	// Existing counts jobs that exist (queued, running or finished) and
	// would be reused, not run again.
	Existing int
	// TMExact counts messages an exact translation-memory match covers:
	// reused without a provider call when it validates, as the job
	// checks.
	TMExact int
	// Provider counts messages that would call a provider.
	Provider int
	// Refused counts messages that would not reach a provider, by
	// reason: sensitive (never queued), provider_consent, no_route,
	// budget_exceeded (queued, then failing).
	Refused map[string]int
	// Skipped counts messages left out: up_to_date and not_selected
	// (listed keys), limit (past MaxFillJobs).
	Skipped map[string]int
	Cost    CostEstimate
}

// CostEstimate prices the provider calls with the price table in effect.
type CostEstimate struct {
	// Estimated is the expected cost: a draft and a self-assessment per
	// message, prompts at about three characters a token, a draft about
	// twice as long as its source.
	Estimated domain.MicroUSD
	// Max is the upper bound the budget guard reserves: every repair
	// taken and each call's whole max_tokens at the output price.
	Max domain.MicroUSD
	// Unpriced reports a route whose model has no price (counted as 0).
	Unpriced bool
}

func (c *CostEstimate) add(o CostEstimate) {
	c.Estimated += o.Estimated
	c.Max += o.Max
	c.Unpriced = c.Unpriced || o.Unpriced
}

// PreviewFill answers what RequestFill would do with req: the same
// checks, selection and limits, then per message whether a job exists,
// an exact TM hit covers it, or it would be refused — consent, route
// and budget decided the way a job's run decides them. It writes
// nothing: no fill, no job, no TM hit count, no spend. Needs
// intelligence.translate for every locale, like the fill.
func (s *Service) PreviewFill(ctx context.Context, project uuid.UUID, req FillRequest) (FillPreview, error) {
	if err := authz.InProject(ctx, project); err != nil {
		return FillPreview{}, err
	}
	locales, _, err := s.checkFill(ctx, &req)
	if err != nil {
		return FillPreview{}, err
	}
	if err := s.checkFillTarget(ctx, project, locales); err != nil {
		return FillPreview{}, err
	}
	env, err := s.previewEnv(ctx, project)
	if err != nil {
		return FillPreview{}, err
	}
	out := FillPreview{ProjectID: project, Select: req.Filter.Select, Warnings: env.warnings}
	q := newQueuer(s, project)
	total := 0
	for _, locale := range locales {
		lp := LocalePreview{Locale: locale, Keys: []string{}, Refused: map[string]int{}, Skipped: map[string]int{}}
		f := Fill{ProjectID: project, Locales: locales, Filter: req.Filter, Skipped: lp.Skipped}
		err := s.eachFillMessage(ctx, f, locale, func(msgs []SourceMessage) error {
			var page []previewItem
			for _, m := range msgs {
				switch {
				case slices.Contains(env.project.NamespaceTags.Of(m.Namespace), domain.TagSensitive):
					lp.Refused[RefusedSensitive]++
					continue
				case total >= MaxFillJobs:
					lp.Skipped[SkipLimit]++
					continue
				}
				total++
				j, err := q.job(ctx, m, locale, domain.TriggerFill, nil, f.Filter.Force, "")
				if err != nil {
					return err
				}
				page = append(page, previewItem{job: j, msg: m})
				lp.Keys = append(lp.Keys, m.Key)
			}
			return s.previewPage(ctx, env, q.source, page, &lp)
		})
		if err != nil {
			return FillPreview{}, err
		}
		slices.Sort(lp.Keys)
		out.Cost.add(lp.Cost)
		out.Locales = append(out.Locales, lp)
	}
	return out, nil
}

// checkFill validates a fill request, checks the caller's permission
// for every locale and sets the effective select.
func (s *Service) checkFill(ctx context.Context, req *FillRequest) (locales []string, by string, err error) {
	if len(req.Locales) == 0 || len(req.Locales) > MaxFillLocales {
		return nil, "", ErrTooManyLocales
	}
	if len(req.Filter.Keys) > MaxFillKeys {
		return nil, "", ErrTooManyKeys
	}
	if err := req.Filter.validate(); err != nil {
		return nil, "", err
	}
	req.Filter.Select = req.Filter.Selection()
	if locales, err = canonicalLocales(req.Locales); err != nil {
		return nil, "", err
	}
	for _, l := range locales {
		if by, err = actorFor(ctx, authz.IntelligenceTranslate, l); err != nil {
			return nil, "", err
		}
	}
	return locales, by, nil
}

// checkFillTarget checks that the project exists and has the locales.
func (s *Service) checkFillTarget(ctx context.Context, project uuid.UUID, locales []string) error {
	if _, err := s.Catalog.Project(ctx, project); err != nil {
		return err
	}
	have, err := s.Localization.Locales(ctx, project)
	if err != nil {
		return err
	}
	for _, l := range locales {
		if !slices.Contains(have, l) {
			return fmt.Errorf("%w: %s", ErrLocaleNotFound, l)
		}
	}
	return nil
}

// previewEnv is what a preview decides with: the settings, routing,
// providers, prices and remaining budget a job would run with, read
// once.
type previewEnv struct {
	settings  domain.TenantSettings
	project   domain.ProjectSettings
	policy    domain.RoutingPolicy
	enabled   map[string]domain.ProviderConfig
	prices    domain.PriceTable
	remaining domain.MicroUSD
	warnings  []string
	// spend is what the previewed provider calls would spend so far.
	spend domain.MicroUSD
}

func (s *Service) previewEnv(ctx context.Context, project uuid.UUID) (*previewEnv, error) {
	env := &previewEnv{enabled: map[string]domain.ProviderConfig{}}
	var providers []StoredProvider
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if env.settings, err = s.settings(ctx, st, false); err != nil {
			return err
		}
		ps, found, err := st.ProjectSettings(ctx, project)
		if err != nil {
			return err
		}
		env.project = domain.DefaultProjectSettings(project)
		if found {
			env.project = ps
		}
		if providers, err = st.AllProviders(ctx); err != nil {
			return err
		}
		view, err := routing(ctx, st, &project)
		if err != nil {
			return err
		}
		env.policy = view.Record.Policy
		spent, _, err := st.SpendSince(ctx, monthStart(s.Now()))
		env.remaining = env.settings.MonthlyBudget - spent
		return err
	})
	if err != nil {
		return nil, err
	}
	env.warnings = fillWarnings(env.settings, providers)
	for _, p := range providers {
		if p.Enabled {
			env.enabled[p.Name] = p.ProviderConfig
		}
	}
	env.prices = s.effectivePrices(env.settings, providers)
	return env, nil
}

type previewItem struct {
	job domain.Job
	msg SourceMessage
}

// previewPage decides a page of would-be jobs of one locale: existing
// ones (one query), exact TM hits (one exact-only lookup each, not
// counted as hits), then consent, route and budget for the rest.
func (s *Service) previewPage(ctx context.Context, env *previewEnv, sourceLocale string, page []previewItem, lp *LocalePreview) error {
	if len(page) == 0 {
		return nil
	}
	jobs := make([]domain.Job, len(page))
	for i, it := range page {
		jobs[i] = it.job
	}
	var existing map[uuid.UUID]domain.JobState
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		existing, err = st.JobStates(ctx, jobs)
		return err
	})
	if err != nil {
		return err
	}
	scope := domain.Scope{TenantID: tenantOf(ctx).String(), ProjectID: env.project.ProjectID.String()}
	for _, it := range page {
		if st, ok := existing[it.job.MessageID]; ok && !requeued(st) {
			lp.Existing++
			continue
		}
		source, err := sourceText(it.msg.Source)
		if err != nil {
			lp.Provider++ // the job fails invalid_source; nothing is spent
			continue
		}
		hits, err := s.Knowledge.LookupTM(ctx, scope, domain.TMQuery{
			Pair: domain.LocalePair{Source: sourceLocale, Target: lp.Locale}, Source: source,
			Key: it.msg.Key, Namespace: it.msg.Namespace, Limit: 1, ExactOnly: true, Uncounted: true,
		})
		if err != nil {
			return err
		}
		if slices.ContainsFunc(hits, domain.TMMatch.IsExact) {
			lp.TMExact++
			continue
		}
		if reason, cost := env.providerCall(lp.Locale, source); reason != "" {
			lp.Refused[reason]++
		} else {
			lp.Provider++
			lp.Cost.add(cost)
		}
	}
	return nil
}

// requeued reports a job state a fill queues again.
func requeued(st domain.JobState) bool {
	return st == domain.JobFailed || st == domain.JobDead || st == domain.JobCancelled
}

// providerCall decides a message that needs a provider as its job would:
// consent, then a route to an enabled provider that allows the model,
// then the budget (this month's spend, plus the previewed calls, plus
// this call's upper bound). It returns the refusal, or the call's cost.
func (e *previewEnv) providerCall(locale, source string) (string, CostEstimate) {
	if !e.settings.ProviderConsent {
		return RefusedProviderConsent, CostEstimate{}
	}
	draft, ok := e.route(domain.TaskTranslate, locale)
	if !ok {
		return RefusedNoRoute, CostEstimate{}
	}
	srcTokens := utf8.RuneCountInString(source)/charsPerToken + 1
	c := e.call(draft, prompts.Translate, srcTokens, 2*srcTokens+draftOverheadTokens, MaxRepairs+1)
	if assess, ok := e.route(domain.TaskAssess, locale); ok {
		c.add(e.call(assess, prompts.Assess, 2*srcTokens+draftOverheadTokens, assessOutputTokens, 1))
	}
	upfront := e.prices.Estimate(draft.Provider, draft.Model, promptTokens(prompts.Translate)+srcTokens, draft.MaxTokens)
	if e.spend+upfront > e.remaining {
		return RefusedBudgetExceeded, CostEstimate{}
	}
	e.spend += c.Estimated
	return "", c
}

// Estimation constants: the router's own rule of about three characters
// per token, a draft's JSON wrapper, and the self-assessment's answer.
const (
	charsPerToken       = 3
	draftOverheadTokens = 32
	assessOutputTokens  = 64
)

// call prices one task: the expected call once, the upper bound times
// calls (a draft may be repaired).
func (e *previewEnv) call(r domain.Route, task string, inputTokens, outputTokens, calls int) CostEstimate {
	in := promptTokens(task) + inputTokens
	c, priced := e.prices.Cost(r.Provider, r.Model, domain.Usage{InputTokens: int64(in), OutputTokens: int64(outputTokens)})
	upper := e.prices.Estimate(r.Provider, r.Model, in, r.MaxTokens)
	return CostEstimate{Estimated: c, Max: upper * domain.MicroUSD(calls), Unpriced: !priced}
}

// route is the first route for task and locale whose provider is
// enabled and allows the model — the one a job would try first.
func (e *previewEnv) route(task domain.Task, locale string) (domain.Route, bool) {
	routes, err := e.policy.Resolve(task, locale)
	if err != nil {
		return domain.Route{}, false
	}
	for _, r := range routes {
		if p, ok := e.enabled[r.Provider]; ok && p.Allows(r.Model) {
			return r, true
		}
	}
	return domain.Route{}, false
}

// promptTokens estimates the fixed part of a task's prompt from its
// current template.
func promptTokens(task string) int {
	v := DefaultPromptVersions()
	version := map[string]string{prompts.Translate: v.Translate, prompts.Assess: v.Assess, prompts.Repair: v.Repair}[task]
	b, err := fs.ReadFile(prompts.Files(), task+"/"+version+".tmpl")
	if err != nil {
		return 0
	}
	return utf8.RuneCount(b) / charsPerToken
}
