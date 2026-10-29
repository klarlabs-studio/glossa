package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// Fill limits.
const (
	MaxFillLocales = 20
	MaxFillKeys    = 500
	// MaxFillJobs bounds one fill; a larger catalog is filled in several
	// requests (filters) or through auto-translate.
	MaxFillJobs = 20_000
	// MaxReportedJobIDs bounds the ids a fill hands back. A caller with
	// more jobs than this is paging anyway and follows them with
	// ?fill=<id>; a caller that listed a few keys — the in-product
	// editor asks about one — gets exactly the ids it needs to poll.
	MaxReportedJobIDs = 200
	fillPage          = 100
)

// SkipNotSelected counts listed keys whose translation's state the
// fill's select leaves out.
const SkipNotSelected = "not_selected"

// Warnings a fill answers with when jobs will fail or do little.
const (
	WarnProviderConsentOff = "provider_consent_off"
	WarnNoBudget           = "no_budget"
	WarnNoProvider         = "no_provider"
)

// FillRequest asks to translate a project's messages into locales.
type FillRequest struct {
	Locales []string
	Filter  FillFilter
}

// FillResult is a created (or replayed) fill with its job counts and
// warnings.
type FillResult struct {
	Fill     Fill
	Counts   map[domain.JobState]int
	Warnings []string
}

// RequestFill queues jobs for the project's messages missing, outdated
// or either in each locale (the filter's select) — Studio's "Fill with
// AI" and the CLI's translate. Sensitive namespaces are skipped. Jobs that
// exist for the same message, locale, source revision and knowledge are
// reused (failed, dead and cancelled ones are queued again). Needs
// intelligence.translate for every locale.
func (s *Service) RequestFill(ctx context.Context, project uuid.UUID, req FillRequest, idemKey string) (FillResult, bool, error) {
	if err := authz.InProject(ctx, project); err != nil {
		return FillResult{}, false, err
	}
	locales, by, err := s.checkFill(ctx, &req)
	if err != nil {
		return FillResult{}, false, err
	}
	id, err := idempotentID(ctx, "intelligence.fill", by, idemKey)
	if err != nil {
		return FillResult{}, false, err
	}
	if prior, found, err := s.existingFill(ctx, id); err != nil || found {
		if err == nil && (prior.ProjectID != project || !slices.Equal(prior.Locales, locales)) {
			err = ErrIdempotencyReuse
		}
		res, werr := s.fillResult(ctx, prior)
		return res, found, errors.Join(err, werr)
	}
	if err := s.checkFillTarget(ctx, project, locales); err != nil {
		return FillResult{}, false, err
	}
	f := Fill{ID: id, ProjectID: project, Trigger: domain.TriggerFill, Locales: locales, Filter: req.Filter, RequestedBy: by, CreatedAt: s.Now()}
	if err := s.fill(ctx, &f, true); err != nil {
		return FillResult{}, false, err
	}
	res, err := s.fillResult(ctx, f)
	return res, false, err
}

func (s *Service) existingFill(ctx context.Context, id uuid.UUID) (Fill, bool, error) {
	var f Fill
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		f, err = st.Fill(ctx, id)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return Fill{}, false, nil
	}
	return f, err == nil, err
}

// fillResult adds the job counts and warnings.
func (s *Service) fillResult(ctx context.Context, f Fill) (FillResult, error) {
	res := FillResult{Fill: f}
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if f.ID != uuid.Nil {
			if res.Counts, err = st.FillJobCounts(ctx, f.ID); err != nil {
				return err
			}
		}
		settings, err := s.settings(ctx, st, false)
		if err != nil {
			return err
		}
		providers, err := st.AllProviders(ctx)
		if err != nil {
			return err
		}
		res.Warnings = fillWarnings(settings, providers)
		return nil
	})
	return res, err
}

// fillWarnings say when a fill's jobs will do little: consent off, no
// budget, no provider.
func fillWarnings(settings domain.TenantSettings, providers []StoredProvider) []string {
	var out []string
	if !settings.ProviderConsent {
		out = append(out, WarnProviderConsentOff)
	}
	if settings.MonthlyBudget == 0 {
		out = append(out, WarnNoBudget)
	}
	if !slices.ContainsFunc(providers, func(p StoredProvider) bool { return p.Enabled }) {
		out = append(out, WarnNoProvider)
	}
	return out
}

// GetFill returns a fill with its jobs' states. Needs intelligence.read.
func (s *Service) GetFill(ctx context.Context, id uuid.UUID) (FillResult, error) {
	var f Fill
	if err := authz.RequireRow(ctx, authz.IntelligenceRead, func() (uuid.UUID, error) {
		var found bool
		var err error
		if f, found, err = s.existingFill(ctx, id); err != nil || !found {
			return uuid.Nil, errors.Join(err, notFoundUnless(found))
		}
		return f.ProjectID, nil
	}); err != nil {
		return FillResult{}, err
	}
	return s.fillResult(ctx, f)
}

// CancelFill cancels a fill's queued jobs; running ones finish. Needs
// intelligence.translate for the fill's locales.
func (s *Service) CancelFill(ctx context.Context, id uuid.UUID) (FillResult, error) {
	if err := authz.Require(ctx, authz.IntelligenceRead); err != nil {
		return FillResult{}, err
	}
	f, found, err := s.existingFill(ctx, id)
	if err != nil || !found {
		return FillResult{}, errors.Join(err, notFoundUnless(found))
	}
	if err := authz.InProject(ctx, f.ProjectID); err != nil {
		return FillResult{}, err
	}
	var by string
	for _, l := range f.Locales {
		if by, err = actorFor(ctx, authz.IntelligenceTranslate, l); err != nil {
			return FillResult{}, err
		}
	}
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		_, err := st.CancelFillJobs(ctx, id, by, s.Now())
		return err
	})
	if err != nil {
		return FillResult{}, err
	}
	return s.fillResult(ctx, f)
}

func notFoundUnless(found bool) error {
	if found {
		return nil
	}
	return ErrNotFound
}

// fill enumerates the messages to translate per locale and queues a job
// for each. ctx carries a principal that may read the catalog and
// translations (the caller, or a background principal).
func (s *Service) fill(ctx context.Context, f *Fill, requeue bool) error {
	f.Skipped = map[string]int{}
	if err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error { return st.InsertFill(ctx, *f) }); err != nil {
		return err
	}
	ps, err := s.projectSettings(ctx, f.ProjectID)
	if err != nil {
		return err
	}
	q := newQueuer(s, f.ProjectID)
	total := 0
	for _, locale := range f.Locales {
		err := s.eachFillMessage(ctx, *f, locale, func(msgs []SourceMessage) error {
			var jobs []domain.Job
			for _, m := range msgs {
				if slices.Contains(ps.NamespaceTags.Of(m.Namespace), domain.TagSensitive) {
					f.Skipped[domain.FailureSensitive]++
					continue
				}
				if total >= MaxFillJobs {
					f.Skipped[SkipLimit]++
					continue
				}
				total++
				j, err := q.job(ctx, m, locale, domain.TriggerFill, &f.ID, f.Filter.Force, f.RequestedBy)
				if err != nil {
					return err
				}
				jobs = append(jobs, j)
			}
			ids, created, existing, err := s.enqueue(ctx, jobs, requeue)
			f.JobsCreated += created
			f.JobsExisting += existing
			// A caller that listed keys gets the ids back so it can poll
			// those jobs instead of watching the whole list; a fill big
			// enough to page is followed with ?fill=<id> instead.
			if len(f.JobIDs) < MaxReportedJobIDs {
				f.JobIDs = append(f.JobIDs, ids...)
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	return s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error { return st.FinishFill(ctx, *f) })
}

// eachFillMessage calls fn with pages of the messages a fill covers in
// locale: those whose translation there is in the state the fill
// selects (missing, outdated or either) — among the listed keys, or
// every active message, filtered.
func (s *Service) eachFillMessage(ctx context.Context, f Fill, locale string, fn func([]SourceMessage) error) error {
	sel := f.Filter.Selection()
	if len(f.Filter.Keys) > 0 {
		msgs, err := s.Catalog.MessagesByKeys(ctx, f.ProjectID, f.Filter.Keys)
		if err != nil {
			return err
		}
		var todo []SourceMessage
		for _, m := range msgs {
			if !m.Active {
				continue
			}
			tr, err := s.Localization.Translation(ctx, f.ProjectID, m.Key, locale)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			missing := !tr.usable()
			switch {
			// force is the only way past "nothing to translate": the
			// editor asks about text someone is reading, which is
			// current by definition.
			case f.Filter.Force:
				todo = append(todo, m)
			case tr.upToDate(m.Revision):
				f.Skipped[domain.SkipUpToDate]++
			case missing && sel.missing(), !missing && sel.outdated():
				todo = append(todo, m)
			default:
				f.Skipped[SkipNotSelected]++
			}
		}
		return fn(todo)
	}
	var queries []MessageQuery
	if sel.missing() {
		queries = append(queries, MessageQuery{Namespace: f.Filter.Namespace, KeyPrefix: f.Filter.KeyPrefix, MissingIn: locale})
	}
	if sel.outdated() {
		queries = append(queries, MessageQuery{Namespace: f.Filter.Namespace, KeyPrefix: f.Filter.KeyPrefix, OutdatedIn: locale})
	}
	for _, q := range queries {
		after := ""
		for {
			msgs, err := s.Catalog.Messages(ctx, f.ProjectID, q, after, fillPage)
			if err != nil {
				return err
			}
			if len(msgs) == 0 {
				break
			}
			if err := fn(msgs); err != nil {
				return err
			}
			if len(msgs) < fillPage {
				break
			}
			after = msgs[len(msgs)-1].Key
		}
	}
	return nil
}

// usable reports a translation that exists and wasn't rejected: one
// that isn't missing.
func (t TranslationState) usable() bool { return t.Exists && t.State != "rejected" }

// upToDate reports a usable translation made against the current
// source revision: nothing to translate.
func (t TranslationState) upToDate(revision int) bool {
	return t.usable() && t.SourceRevision >= revision
}

// enqueue stores jobs in one transaction and counts new and existing.
func (s *Service) enqueue(ctx context.Context, jobs []domain.Job, requeue bool) (ids []uuid.UUID, created, existing int, err error) {
	if len(jobs) == 0 {
		return nil, 0, 0, nil
	}
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		ids, created, existing = ids[:0], 0, 0
		for _, j := range jobs {
			stored, isNew, err := st.EnqueueJob(ctx, j, requeue)
			if err != nil {
				return err
			}
			// The stored row's id, not the one just built: reusing an
			// existing job keeps that job's id, and a caller polling
			// needs the id that exists.
			ids = append(ids, stored.ID)
			if isNew {
				created++
			} else {
				existing++
			}
		}
		return nil
	})
	return ids, created, existing, err
}

// queuer builds jobs with their knowledge fingerprints, caching the
// style sources and termbase versions per locale and namespace.
type queuer struct {
	s        *Service
	project  uuid.UUID
	source   string
	styles   map[string][]string
	termbase map[string]string
}

func newQueuer(s *Service, project uuid.UUID) *queuer {
	return &queuer{s: s, project: project, styles: map[string][]string{}, termbase: map[string]string{}}
}

func (q *queuer) job(
	ctx context.Context, m SourceMessage, locale string, trigger domain.Trigger, fill *uuid.UUID, forced bool, by string,
) (domain.Job, error) {
	scope := domain.Scope{TenantID: tenantOf(ctx).String(), ProjectID: q.project.String()}
	if q.source == "" {
		p, err := q.s.Catalog.Project(ctx, q.project)
		if err != nil {
			return domain.Job{}, err
		}
		q.source = p.SourceLocale
	}
	styleKey := locale + "\n" + m.Namespace
	styles, ok := q.styles[styleKey]
	if !ok {
		var err error
		if styles, err = q.s.Knowledge.StyleSources(ctx, scope, locale, m.Namespace); err != nil {
			return domain.Job{}, err
		}
		q.styles[styleKey] = styles
	}
	tb, ok := q.termbase[locale]
	if !ok {
		var err error
		if tb, err = q.s.Knowledge.TermbaseVersion(ctx, scope, domain.LocalePair{Source: q.source, Target: locale}); err != nil {
			return domain.Job{}, err
		}
		q.termbase[locale] = tb
	}
	p := q.s.prompts
	fp := domain.Fingerprint{Prompts: strings.Join([]string{p.Translate, p.Repair, p.Assess}, ","), Styles: styles, Concepts: []string{tb}}
	now := q.s.Now()
	return domain.Job{
		ID: uuid.Must(uuid.NewV7()), ProjectID: q.project, MessageID: m.ID, MessageKey: m.Key, Namespace: m.Namespace,
		Locale: locale, SourceRevision: m.Revision, Fingerprint: fp.Sum(), Trigger: trigger, FillID: fill,
		Forced: forced,
		State:  domain.JobQueued, MaxAttempts: domain.DefaultMaxAttempts, AvailableAt: now, CreatedBy: by,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ListJobs lists jobs, newest first, of the projects the caller may
// see. Needs intelligence.read.
func (s *Service) ListJobs(ctx context.Context, f JobFilter, page pagination.Page) ([]domain.Job, *string, error) {
	scope, err := authz.Projects(ctx, authz.IntelligenceRead)
	if err != nil {
		return nil, nil, err
	}
	f.Projects = scope.IDs()
	if f.Locale != "" {
		t, err := bcp47.Parse(f.Locale)
		if err != nil {
			return nil, nil, err
		}
		f.Locale = t.String()
	}
	before, err := parseCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Job
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.Jobs(ctx, f, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(j domain.Job) string { return Cursor{At: j.CreatedAt, ID: j.ID}.String() })
	return items, next, nil
}

// GetJob returns a job with its audit ledger. Needs intelligence.read.
func (s *Service) GetJob(ctx context.Context, id uuid.UUID) (JobView, error) {
	var v JobView
	if err := authz.RequireRow(ctx, authz.IntelligenceRead, func() (uuid.UUID, error) {
		err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
			var err error
			v, err = st.Job(ctx, id)
			return err
		})
		return v.ProjectID, err
	}); err != nil {
		return JobView{}, err
	}
	return v, nil
}

// CancelJob cancels a queued job. Needs intelligence.translate for its
// locale.
func (s *Service) CancelJob(ctx context.Context, id uuid.UUID) (JobView, error) {
	v, err := s.GetJob(ctx, id)
	if err != nil {
		return JobView{}, err
	}
	by, err := actorFor(ctx, authz.IntelligenceTranslate, v.Locale)
	if err != nil {
		return JobView{}, err
	}
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		ok, err := st.CancelJob(ctx, id, by, s.Now())
		if err != nil {
			return err
		}
		if !ok {
			cur, err := st.Job(ctx, id)
			if err == nil && cur.State != domain.JobCancelled {
				return domain.ErrJobNotCancellable
			}
			return err
		}
		v, err = st.Job(ctx, id)
		return err
	})
	return v, err
}

// sinceDefault is the metrics window when none is given.
func sinceDefault(now time.Time) time.Time { return now.AddDate(0, 0, -30) }
