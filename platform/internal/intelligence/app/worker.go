package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// WorkerConfig tunes the job workers that run inside glossa-server.
type WorkerConfig struct {
	// Workers is the number of jobs this process runs at once.
	Workers int
	// PollInterval is how long an idle worker waits before looking again.
	PollInterval time.Duration
	// Lease is how long a claimed job is reserved; a job whose worker
	// died is claimed again after it. It must exceed JobTimeout.
	Lease time.Duration
	// JobTimeout bounds one job, model calls and retries included.
	JobTimeout time.Duration
}

func (c *WorkerConfig) defaults() {
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 10 * time.Minute
	}
	if c.Lease <= c.JobTimeout {
		c.Lease = c.JobTimeout + 5*time.Minute
	}
}

// Worker claims jobs across tenants with FOR UPDATE SKIP LOCKED (each
// tenant under its concurrency cap) and runs them in the tenant's scope
// as the background principal intelligence.worker.
type Worker struct {
	s       *Service
	claimer Claimer
	cfg     WorkerConfig
}

// NewWorker returns a worker pool.
func NewWorker(s *Service, claimer Claimer, cfg WorkerConfig) *Worker {
	cfg.defaults()
	return &Worker{s: s, claimer: claimer, cfg: cfg}
}

// Run works until ctx is cancelled; a job in progress finishes first
// (bounded by JobTimeout).
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for range w.cfg.Workers {
		wg.Go(func() { w.loop(ctx) })
	}
	wg.Go(func() { w.gauge(ctx) })
	wg.Wait()
	return nil
}

func (w *Worker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.RunOnce(ctx)
		if err != nil {
			w.s.Logger.ErrorContext(ctx, "intelligence: job worker", slog.Any("error", err))
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.cfg.PollInterval):
		}
	}
}

func (w *Worker) gauge(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		if depth, err := w.claimer.QueueDepth(ctx); err == nil {
			w.s.Metrics.QueueDepth(depth)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce claims one due job and runs it; worked is false when none was
// due. Tests drive the queue with it.
func (w *Worker) RunOnce(ctx context.Context) (worked bool, err error) {
	c, ok, err := w.claimer.Claim(ctx, w.cfg.Lease)
	if err != nil || !ok {
		return false, err
	}
	// The job finishes even when shutdown starts; the timeout bounds it.
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.JobTimeout)
	defer cancel()
	jctx = tenancy.ContextWithTenant(jctx, tenancy.ID(c.TenantID))
	bg, err := authz.Background(jctx, principalWorker,
		authz.CatalogRead, authz.TranslationsRead, authz.KnowledgeRead, authz.ReleasesRead,
		authz.TranslationsWrite, authz.TranslationsReview)
	if err != nil {
		return true, err
	}
	return true, w.s.runJob(bg, c)
}

// outcome is how a job ended before it is settled.
type outcome struct {
	state      domain.JobState
	code       string
	message    string
	retryable  bool
	suggestion *domain.SuggestionRecord
	result     Result
}

// runJob executes a claimed job and settles it under its claim token.
func (s *Service) runJob(ctx context.Context, c Claim) error {
	started := s.Now()
	var view JobView
	if err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		view, err = st.Job(ctx, c.JobID)
		return err
	}); err != nil {
		return err
	}
	j := view.Job
	var o outcome
	if c.Attempts > c.MaxAttempts {
		o = outcome{state: domain.JobDead, code: domain.FailureRetriesExceeded,
			message: fmt.Sprintf("the job was claimed %d times; its workers kept stopping before it finished", c.Attempts)}
	} else {
		o = s.execute(ctx, j)
	}
	if err := s.recordDisclosures(ctx, j, o.result.Disclosures); err != nil {
		return err
	}
	settled, err := s.settle(ctx, c, j, &o)
	if err != nil || !settled {
		return err
	}
	s.Metrics.JobFinished(j.Trigger, o.state, o.code, s.Now().Sub(started))
	if r := o.suggestion; r != nil {
		s.Metrics.SuggestionCreated(r.Locale, r.Provenance.Origin, r.Action, r.Confidence.Score)
		if r.Action == domain.ActionAutoApprove {
			s.autoApply(ctx, *r)
		}
	}
	return nil
}

// execute runs the agent for a job and classifies how it ended.
func (s *Service) execute(ctx context.Context, j domain.Job) outcome {
	msg, err := s.Catalog.MessageByID(ctx, j.ProjectID, j.MessageID)
	switch {
	case errors.Is(err, ErrNotFound) || errors.Is(err, ErrProjectNotFound):
		return skipped(domain.SkipMessage, "the message or its project no longer exists")
	case err != nil:
		return transient(err)
	case !msg.Active:
		return skipped(domain.SkipMessage, "the message is obsolete")
	case msg.Revision != j.SourceRevision:
		return skipped(domain.SkipSuperseded, fmt.Sprintf("the source moved on to revision %d", msg.Revision))
	}
	tr, err := s.Localization.Translation(ctx, j.ProjectID, j.MessageKey, j.Locale)
	switch {
	case errors.Is(err, ErrLocaleNotFound):
		return skipped(domain.SkipLocale, "the project no longer has the locale")
	case err != nil && !errors.Is(err, ErrNotFound):
		return transient(err)
	// A forced job was asked for precisely because the translation is
	// current: someone is reading it and wants a second opinion.
	case tr.upToDate(msg.Revision) && !j.Forced:
		return skipped(domain.SkipUpToDate, "the message was translated meanwhile")
	}
	rt, err := s.runtimeFor(ctx, j)
	if err != nil {
		return transient(err)
	}
	tags := rt.project.NamespaceTags.Of(msg.Namespace)
	project, err := s.Catalog.Project(ctx, j.ProjectID)
	if err != nil {
		return transient(err)
	}
	source, err := sourceText(msg.Source)
	if err != nil {
		return failed(domain.FailureInvalidSource, err.Error())
	}
	tr2, err := NewTranslator(Config{
		Router: rt.router, Knowledge: &jobKnowledge{Knowledge: s.Knowledge, s: s, msg: msg},
		Review: rt.project.Review.ReviewPolicy, Prompts: s.prompts,
	})
	if err != nil {
		return transient(err)
	}
	req := domain.TranslationRequest{
		Scope:     domain.Scope{TenantID: tenantOf(ctx).String(), ProjectID: j.ProjectID.String()},
		MessageID: j.MessageID.String(), Key: j.MessageKey, Namespace: msg.Namespace,
		SourceRevision: strconv.Itoa(j.SourceRevision), SourceLocale: project.SourceLocale, TargetLocale: j.Locale,
		Source: source, Tags: tags, ProviderConsent: rt.settings.ProviderConsent,
	}
	res, err := tr2.Translate(ctx, req)
	o := classify(err)
	o.result = res
	if err != nil {
		return o
	}
	rec := domain.SuggestionRecord{
		ID: uuid.Must(uuid.NewV7()), JobID: j.ID, ProjectID: j.ProjectID, MessageID: j.MessageID,
		MessageKey: j.MessageKey, Namespace: msg.Namespace, Locale: j.Locale, SourceRevision: j.SourceRevision,
		Suggestion: res.Suggestion, RiskTags: domain.RiskTagsOf(tags, res.Suggestion), Status: domain.StatusPending,
		Version: 1, CreatedAt: s.Now(),
	}
	if rec.Action == domain.ActionAutoApprove {
		if note := s.autoApproveBlocked(ctx, j.ProjectID, rt.project.Review.AutoApproveEnvironments); note != "" {
			rec.Action, rec.ActionNote = domain.ActionApproveRecommended, "auto_approve not honoured: "+note
		}
	}
	o.state, o.suggestion = domain.JobSucceeded, &rec
	return o
}

func skipped(reason, message string) outcome {
	return outcome{state: domain.JobSkipped, code: reason, message: message}
}

func failed(code, message string) outcome {
	return outcome{state: domain.JobFailed, code: code, message: message}
}

func transient(err error) outcome {
	return outcome{state: domain.JobFailed, code: domain.FailureInternal, message: err.Error(), retryable: true}
}

// classify maps the agent's error to a job outcome (RFC 0003 §3.2,
// §7). Transient provider outages are retried; everything the job
// can't fix by waiting fails for good, with a reason that says what to
// change.
func classify(err error) outcome {
	switch {
	case err == nil:
		return outcome{state: domain.JobSucceeded}
	case errors.Is(err, domain.ErrSensitive):
		return failed(domain.FailureSensitive, "the message's namespace is tagged sensitive: it is never sent to a provider and needs a human translator")
	case errors.Is(err, domain.ErrProviderConsent):
		return failed(domain.FailureProviderConsent, "sending text to AI providers is not enabled for this tenant (ai-settings provider_consent) and no exact translation-memory match could be reused")
	case errors.Is(err, domain.ErrInvalidOutput):
		return failed(domain.FailureInvalidOutput, err.Error())
	case errors.Is(err, domain.ErrBudgetExceeded):
		return failed(domain.FailureBudgetExceeded, err.Error())
	case errors.Is(err, domain.ErrNoRoute):
		return failed(domain.FailureNoRoute, err.Error())
	case errors.Is(err, domain.ErrUnparsable):
		return failed(domain.FailureInvalidSource, err.Error())
	case Transient(err):
		return outcome{state: domain.JobFailed, code: domain.FailureProviderError, message: err.Error(), retryable: true}
	case errors.Is(err, ErrAllRoutesFailed):
		if isUnconfigured(err) {
			return failed(domain.FailureNoRoute, err.Error())
		}
		return failed(domain.FailureProviderError, err.Error())
	}
	var pe *domain.ProviderError
	if errors.As(err, &pe) {
		return failed(domain.FailureProviderError, err.Error())
	}
	return transient(err)
}

// isUnconfigured reports that no route reached a provider at all: every
// route names a provider that isn't configured (or enabled).
func isUnconfigured(err error) bool {
	var pe *domain.ProviderError
	return !errors.As(err, &pe)
}

// settle records the outcome under the claim token: the suggestion and
// the job's end, a retry with backoff, or the dead letter. settled is
// false when another worker holds the job now.
func (s *Service) settle(ctx context.Context, c Claim, j domain.Job, o *outcome) (bool, error) {
	audit, err := json.Marshal(o.result.Audit)
	if err != nil {
		return false, err
	}
	if len(o.result.Audit) == 0 {
		audit = nil
	}
	now := s.Now()
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if _, err := st.LockClaimedJob(ctx, c.JobID, c.Token); err != nil {
			return err
		}
		out := JobOutcome{JobID: j.ID, State: o.state, FailureCode: o.code, LastError: truncate(o.message, 4000), Audit: audit, At: now}
		if r := o.suggestion; r != nil {
			if err := st.InsertSuggestion(ctx, *r); err != nil {
				return err
			}
			if err := st.SupersedePending(ctx, r.MessageID, r.Locale, r.ID); err != nil {
				return err
			}
			out.SuggestionID = &r.ID
			return st.FinishJob(ctx, out)
		}
		if o.retryable && c.Attempts < c.MaxAttempts {
			return st.RetryJob(ctx, j.ID, now.Add(domain.Backoff(c.Attempts)), out)
		}
		if o.retryable {
			o.state, out.State = domain.JobDead, domain.JobDead
			out.FailureCode = domain.FailureRetriesExceeded
			out.LastError = truncate(fmt.Sprintf("gave up after %d attempts: %s", c.Attempts, o.message), 4000)
		}
		return st.FinishJob(ctx, out)
	})
	if errors.Is(err, domain.ErrLeaseLost) {
		s.Logger.WarnContext(ctx, "intelligence: job lease lost; another worker settles it", slog.String("job_id", j.ID.String()))
		return false, nil
	}
	if err == nil && o.retryable && o.state != domain.JobDead {
		o.state = domain.JobQueued
	}
	return err == nil, err
}

// recordDisclosures stores which provider saw which message, even when
// the job failed afterwards (RFC 0003 §7).
func (s *Service) recordDisclosures(ctx context.Context, j domain.Job, ds []Disclosure) error {
	if len(ds) == 0 {
		return nil
	}
	now := s.Now()
	recs := make([]DisclosureRecord, len(ds))
	for i, d := range ds {
		recs[i] = DisclosureRecord{
			ID: uuid.Must(uuid.NewV7()), JobID: j.ID, ProjectID: j.ProjectID, MessageID: j.MessageID,
			Locale: j.Locale, Disclosure: d, OccurredAt: now,
		}
	}
	return s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error { return st.InsertDisclosures(ctx, recs) })
}

// autoApply writes an auto-approved suggestion as an approved revision.
// If Localization refuses it, the suggestion stays pending for a person
// with the reason noted.
func (s *Service) autoApply(ctx context.Context, r domain.SuggestionRecord) {
	approved := "approved"
	// Nobody was looking at a screen: auto-apply has no in-context.
	rev, err := s.write(ctx, r, r.Message, &approved, false, nil)
	next := r
	if err != nil {
		next.ActionNote = "auto-apply failed; review it: " + err.Error()
		s.Logger.WarnContext(ctx, "intelligence: auto-apply failed", slog.String("suggestion_id", r.ID.String()), slog.Any("error", err))
	} else {
		now := s.Now()
		next.Status, next.TranslationRevision, next.DecidedBy, next.DecidedAt = domain.StatusAutoApplied, &rev, "system:"+principalWorker, &now
	}
	if err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error { return st.DecideSuggestion(ctx, next, r.Version) }); err != nil {
		s.Logger.ErrorContext(ctx, "intelligence: record auto-apply", slog.String("suggestion_id", r.ID.String()), slog.Any("error", err))
		return
	}
	if err == nil {
		s.Metrics.SuggestionDecided(r.Locale, domain.StatusAutoApplied, 0)
	}
}

// write makes a suggestion (or its edit) a translation revision with
// provenance ai or translation_memory and its origin_detail. ctx's
// principal must be allowed to write (and, for approved, review) the
// locale.
func (s *Service) write(
	ctx context.Context, r domain.SuggestionRecord, text string, state *string, edited bool, in *domain.InContext,
) (int, error) {
	cur, err := s.Localization.Translation(ctx, r.ProjectID, r.MessageKey, r.Locale)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	w := TranslationWrite{
		Text: text, Origin: r.Provenance.Origin, OriginDetail: r.OriginDetail(edited, in),
		SourceRevision: r.SourceRevision, State: state,
	}
	return s.Localization.Write(ctx, r.ProjectID, r.MessageKey, r.Locale, w, cur.Revision)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
