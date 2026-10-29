package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Linguistic-QA jobs (RFC 0005 §3.8, §13 wave 6).
//
// One layer needs a model, and that makes it a job rather than a check:
// `glossa check` never calls an AI provider (§14 decision 2), so the
// check stays free, offline and deterministic. A check *reports* the
// findings a job stored; it never computes one.
//
// The split across contexts is the one RFC 0002 §4 asks for. **Quality
// stores the findings** — they are tenant data in Quality's tables,
// under forced RLS, and they never go to the edge (§10). **Intelligence
// owns the model call**, reached here through the Linguist port, and
// with it everything M2 already decided and this slice inherits rather
// than restates:
//
//   - the provider port and the routing policy (RFC 0003 §3.1),
//   - the per-tenant **sending consent**, off by default (RFC 0003 §7),
//   - the **`sensitive` namespace rule**: a namespace tagged
//     `sensitive` is never sent to a provider (RFC 0003 §7), and this
//     job drops those namespaces from the request *before* it is handed
//     over, so the reviewer is never given the chance,
//   - the existing **per-tenant AI budget** (RFC 0005 §10: "the
//     linguistic layer is bounded by the existing per-tenant AI
//     budget"), asked for here as remaining spend and enforced again
//     inside Intelligence on every call.
//
// None of those four is re-implemented on this side. Preflight asks
// Intelligence what it already knows and the job records the answer;
// Intelligence refuses again on its own path, because a check that only
// happens at the door is a check that the second door does not have.

// LinguisticSpend is a budget figure in micro-USD, as Intelligence
// counts one. Quality never prices anything; it only asks whether
// anything is left.
type LinguisticSpend = int64

// LinguisticPreflight is what Intelligence answers before a review is
// handed over: may this tenant send text to a provider at all, has it
// any budget left this month, and which of the project's namespaces
// must never be sent.
type LinguisticPreflight struct {
	// ProviderConsent is the tenant's explicit permission to send text
	// to AI providers (RFC 0003 §7). It is off by default.
	ProviderConsent bool
	// BudgetRemaining is this month's unspent budget. A tenant that has
	// set no budget has none remaining, which is exactly how
	// Intelligence's own guard reads a zero cap: every call would
	// exceed it.
	BudgetRemaining LinguisticSpend
	// SensitiveNamespaces are the project's namespaces tagged
	// `sensitive`. They are excluded from the review, and the
	// translations under them are counted as skipped.
	SensitiveNamespaces []string
}

// LinguisticRequest is one review to run, as Intelligence receives it.
type LinguisticRequest struct {
	Project uuid.UUID
	// Job is the Quality job the request belongs to, so the two sides
	// can be joined in a log or a trace.
	Job uuid.UUID
	// Ref is the branch or environment reviewed.
	Ref   string
	Scope domain.LinguisticScope
	// ExcludeNamespaces are the namespaces the reviewer must not touch:
	// the project's `sensitive` ones. They are named here and not looked
	// up there, so that the rule is applied by the context that refuses
	// to store what it did not ask for.
	ExcludeNamespaces []string
}

// LinguisticProgress is where a handed-over review stands.
type LinguisticProgress struct {
	// Done is false while the review is still running.
	Done bool
	// Cancelled says the review was stopped rather than finished.
	Cancelled bool
	// Reviewed counts the translations the model saw, and
	// SkippedSensitive those left out under a `sensitive` namespace.
	Reviewed         int
	SkippedSensitive int
	// Findings are what the model suspects, once Done. They carry no
	// severity: see domain.LinguisticFinding.
	Findings []domain.LinguisticFinding
}

// Linguist is Intelligence's application port, as Quality uses it.
//
// Everything about the model — which provider, which prompt version,
// how the answer is constrained and parsed, the cassettes and the
// golden set — is on the far side of it (RFC 0003 §3.1, §3.2, §4). This
// side knows only that a review can be started, followed and stopped,
// and that a finding comes back without a severity.
type Linguist interface {
	// Preflight answers the three questions a review must pass before
	// any text is sent.
	Preflight(ctx context.Context, project uuid.UUID) (LinguisticPreflight, error)
	// Start hands one review over and returns Intelligence's handle for
	// it.
	Start(ctx context.Context, req LinguisticRequest) (batch string, err error)
	// Poll reports where a handed-over review stands.
	Poll(ctx context.Context, project uuid.UUID, batch string) (LinguisticProgress, error)
	// Cancel stops a review that has not finished. Cancelling one that
	// already finished is not an error.
	Cancel(ctx context.Context, project uuid.UUID, batch string) error
}

// SetLinguist wires Intelligence's reviewer. A deployment that does not
// wire one answers `linguistic_unavailable` rather than pretending a
// project has no linguistic findings, which would read as a clean bill
// of health nobody issued.
func (s *Service) SetLinguist(l Linguist) { s.linguist = l }

// Linguistic-QA errors. Each is the refusal seen from Quality's side of
// the port; the Intelligence adapter maps its own errors onto them, so
// the vocabulary a caller learns is the one M2 already uses.
var (
	// ErrLinguistUnavailable is a deployment with no reviewer wired.
	ErrLinguistUnavailable = errors.New("quality: linguistic QA is not available in this deployment")
	// ErrLinguisticLayerOff is a project whose policy switched the
	// linguistic layer off: it does not compute the layer and does not
	// pay for it (RFC 0005 §4.1).
	ErrLinguisticLayerOff = errors.New("quality: the project's check policy switches the linguistic layer off")
	// ErrLinguisticJobNotFound is a job that isn't this project's.
	ErrLinguisticJobNotFound = errors.New("quality: no such linguistic-QA job in the project")
	// ErrProviderConsent is a tenant that has not enabled sending text
	// to AI providers (RFC 0003 §7).
	ErrProviderConsent = errors.New("quality: sending text to AI providers is not enabled for this tenant")
	// ErrSensitive is a review with nothing left to send: every
	// namespace it selected is tagged `sensitive`.
	ErrSensitive = errors.New("quality: the selected namespaces are tagged sensitive and are never sent to a provider")
	// ErrBudgetExceeded is a tenant whose monthly AI budget is spent.
	ErrBudgetExceeded = errors.New("quality: the tenant's monthly AI budget is spent")
	// ErrNoRoute is a tenant with no provider configured for the task.
	ErrNoRoute = errors.New("quality: no AI provider is configured for linguistic review")
	// ErrInvalidLinguisticOutput is a model answer that did not parse.
	ErrInvalidLinguisticOutput = errors.New("quality: the model's answer was not a well-formed linguistic review")
	// ErrProviderError is a provider that refused or failed.
	ErrProviderError = errors.New("quality: the AI provider failed")
	// ErrIdempotencyReuse is an Idempotency-Key that already names a job
	// asking for something else. A review costs money, so a retry that
	// means a different thing is refused rather than merged.
	ErrIdempotencyReuse = errors.New("quality: Idempotency-Key reused for a different request")
)

// CreateLinguisticJob asks for one review.
type CreateLinguisticJob struct {
	// Ref is the branch or environment reviewed; the findings are
	// recorded in a check run of it.
	Ref   string
	Scope domain.LinguisticScope
	// IdempotencyKey makes a retried create find the job it already
	// made rather than starting a second review. Empty is a new job.
	IdempotencyKey string
}

// RequestLinguisticReview creates a linguistic-QA job and hands it to
// Intelligence.
//
// It answers a job whatever happened, including when the review was
// refused: a refused job is `failed` with the code that says what to
// change — `provider_consent`, `budget_exceeded`, `sensitive`,
// `no_route` — which is how Intelligence's own jobs report the same
// four refusals (RFC 0003 §3.4). A caller reads `state` and
// `failure_code`, not an exception, and the refusal is on the record
// instead of being a 4xx nobody kept.
//
// Two things are refused before a row exists, because neither is about
// this job: a project whose policy switched the layer off, and a
// deployment with no reviewer at all.
//
// Needs `catalog.write`: the job records a check run and its findings,
// and it spends the tenant's AI budget.
func (s *Service) RequestLinguisticReview(
	ctx context.Context, project uuid.UUID, in CreateLinguisticJob,
) (job domain.LinguisticJob, created bool, err error) {
	actor, err := s.write(ctx, project)
	if err != nil {
		return domain.LinguisticJob{}, false, err
	}
	if s.linguist == nil {
		return domain.LinguisticJob{}, false, ErrLinguistUnavailable
	}
	scope, err := normalizeScope(in.Scope)
	if err != nil {
		return domain.LinguisticJob{}, false, err
	}
	stored, err := s.storedPolicy(ctx, project)
	if err != nil {
		return domain.LinguisticJob{}, false, err
	}
	// `off` means the project does not compute the layer, and a job that
	// cannot store a finding should not cost a model call.
	if !domain.Computes(stored.Policy, "", domain.LayerLinguistic) {
		return domain.LinguisticJob{}, false, ErrLinguisticLayerOff
	}
	id, err := linguisticID(ctx, actor, in.IdempotencyKey)
	if err != nil {
		return domain.LinguisticJob{}, false, err
	}
	// A retry finds the job the first request made — a review costs
	// money, and starting a second one because a client resent a request
	// is the failure the Idempotency-Key convention exists to prevent.
	if prior, found, err := s.existingLinguisticJob(ctx, project, id); err != nil || found {
		if err == nil && (prior.Ref != in.Ref || !slices.Equal(prior.Scope.Locales, scope.Locales)) {
			err = ErrIdempotencyReuse
		}
		return prior, false, err
	}
	now := s.now()
	job = domain.LinguisticJob{
		ID: id, Project: project, Ref: in.Ref, Scope: scope,
		State: domain.LinguisticQueued, CreatedBy: actor, CreatedAt: now, UpdatedAt: now,
	}
	if err := job.Validate(); err != nil {
		return domain.LinguisticJob{}, false, err
	}
	ctx, end := s.span(ctx, "quality.request_linguistic_review",
		attribute.String("glossa.project_id", project.String()),
		attribute.String("glossa.quality.linguistic_job_id", job.ID.String()),
		attribute.Int("glossa.quality.locales", len(scope.Locales)))
	defer end(&err)

	if err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return st.InsertLinguisticJob(ctx, job)
	}); err != nil {
		return domain.LinguisticJob{}, false, err
	}
	job, err = s.startLinguistic(ctx, job)
	return job, err == nil, err
}

// startLinguistic runs the preflight and hands the review over, or
// settles the job with the refusal.
func (s *Service) startLinguistic(ctx context.Context, job domain.LinguisticJob) (domain.LinguisticJob, error) {
	pre, err := s.linguist.Preflight(ctx, job.Project)
	if err != nil {
		return s.settleLinguistic(ctx, failLinguistic(job, err, s.now()))
	}
	// The three inherited gates, in the order that costs least. Consent
	// is a setting, the budget is a number, and the sensitive rule is
	// about what may be sent at all.
	switch {
	case !pre.ProviderConsent:
		return s.settleLinguistic(ctx, job.Fail(domain.LinguisticFailureProviderConsent,
			"sending text to AI providers is not enabled for this tenant (ai-settings provider_consent);"+
				" the linguistic layer is the one layer that needs a model", s.now()))
	case pre.BudgetRemaining <= 0:
		return s.settleLinguistic(ctx, job.Fail(domain.LinguisticFailureBudgetExceeded,
			"this month's AI budget is spent; the linguistic layer is bounded by the existing per-tenant budget",
			s.now()))
	}
	excluded := slices.Clone(pre.SensitiveNamespaces)
	slices.Sort(excluded)
	excluded = slices.Compact(excluded)
	// A job that asked for exactly one namespace, and that namespace is
	// sensitive, has nothing it is allowed to send. Saying so is better
	// than starting a review that can only come back empty and reading
	// as "nothing wrong here".
	if job.Scope.Namespace != "" && slices.Contains(excluded, job.Scope.Namespace) {
		return s.settleLinguistic(ctx, job.Fail(domain.LinguisticFailureSensitive,
			fmt.Sprintf("namespace %q is tagged sensitive: it is never sent to a provider and needs a human reviewer",
				job.Scope.Namespace), s.now()))
	}
	batch, err := s.linguist.Start(ctx, LinguisticRequest{
		Project: job.Project, Job: job.ID, Ref: job.Ref, Scope: job.Scope, ExcludeNamespaces: excluded,
	})
	if err != nil {
		return s.settleLinguistic(ctx, failLinguistic(job, err, s.now()))
	}
	now := s.now()
	job.Batch, job.State, job.UpdatedAt, job.StartedAt = batch, domain.LinguisticRunning, now, &now
	return s.settleLinguistic(ctx, job)
}

// GetLinguisticJob reads a job and brings it up to date.
//
// A job that has been handed over is reconciled on read: Intelligence
// is asked where the review stands, and a review that has finished has
// its findings recorded here, once. Recording is idempotent through the
// job's `check_run_id` — a job that already names a run never records a
// second — and the guarded UPDATE settles a job exactly once even when
// two readers reconcile it at the same moment.
//
// Needs `catalog.read` to look, and the reconciliation that follows
// writes findings as the job's own creator, not as the reader: it is
// the job's work being finished, not the reader's.
func (s *Service) GetLinguisticJob(ctx context.Context, project, id uuid.UUID) (domain.LinguisticJob, error) {
	if err := s.read(ctx, project); err != nil {
		return domain.LinguisticJob{}, err
	}
	job, err := s.linguisticJob(ctx, project, id)
	if err != nil {
		return domain.LinguisticJob{}, err
	}
	if job.State.Final() || job.Batch == "" || s.linguist == nil {
		return job, nil
	}
	return s.reconcileLinguistic(ctx, job)
}

// ListLinguisticJobs pages a project's jobs, newest first. It does not
// reconcile: a list is a list, and one page should not fan out into a
// poll per row. A job is brought up to date when it is read.
func (s *Service) ListLinguisticJobs(
	ctx context.Context, project uuid.UUID, state string, page pagination.Page,
) ([]domain.LinguisticJob, *string, error) {
	if err := s.read(ctx, project); err != nil {
		return nil, nil, err
	}
	if state != "" {
		if _, err := domain.ParseLinguisticJobState(state); err != nil {
			return nil, nil, fmt.Errorf("%w: state %q", ErrInvalidQuery, state)
		}
	}
	after, err := parseLinguisticCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.LinguisticJob
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.ListLinguisticJobs(ctx, project, state, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, linguisticCursor)
	return items, next, nil
}

// CancelLinguisticJob stops a review that has not finished. A job that
// is already over is not cancellable: cancelling is not a way to undo
// findings, which are waived and never deleted (RFC 0005 §14 decision
// 5). Needs `catalog.write`.
func (s *Service) CancelLinguisticJob(ctx context.Context, project, id uuid.UUID) (domain.LinguisticJob, error) {
	if _, err := s.write(ctx, project); err != nil {
		return domain.LinguisticJob{}, err
	}
	job, err := s.linguisticJob(ctx, project, id)
	if err != nil {
		return domain.LinguisticJob{}, err
	}
	if !job.Cancellable() {
		return domain.LinguisticJob{}, fmt.Errorf("%w: it is %s", domain.ErrLinguisticJobNotCancellable, job.State)
	}
	if s.linguist != nil && job.Batch != "" {
		if err := s.linguist.Cancel(ctx, project, job.Batch); err != nil && !errors.Is(err, ErrNotFound) {
			return domain.LinguisticJob{}, err
		}
	}
	now := s.now()
	job.State, job.UpdatedAt, job.FinishedAt = domain.LinguisticCancelled, now, &now
	return s.settleLinguistic(ctx, job)
}

// ── reconciliation ──────────────────────────────────────────────────

// reconcileLinguistic asks Intelligence where a handed-over review
// stands and records what came back.
func (s *Service) reconcileLinguistic(ctx context.Context, job domain.LinguisticJob) (domain.LinguisticJob, error) {
	progress, err := s.linguist.Poll(ctx, job.Project, job.Batch)
	if err != nil {
		return s.settleLinguistic(ctx, failLinguistic(job, err, s.now()))
	}
	job.Reviewed, job.SkippedSensitive = progress.Reviewed, progress.SkippedSensitive
	job.UpdatedAt = s.now()
	switch {
	case progress.Cancelled:
		now := s.now()
		job.State, job.FinishedAt = domain.LinguisticCancelled, &now
		return s.settleLinguistic(ctx, job)
	case !progress.Done:
		job.State = domain.LinguisticRunning
		return s.settleLinguistic(ctx, job)
	}
	run, stored, err := s.recordLinguisticFindings(ctx, job, progress.Findings)
	if err != nil {
		return s.settleLinguistic(ctx, failLinguistic(job, err, s.now()))
	}
	now := s.now()
	job.State, job.Findings, job.FinishedAt = domain.LinguisticSucceeded, stored, &now
	if run != uuid.Nil {
		job.CheckRun = &run
	}
	return s.settleLinguistic(ctx, job)
}

// recordLinguisticFindings seals what the model suspects and stores it
// as one check run of the linguistic layer.
//
// The findings are sealed by domain.SealLinguistic — which is where
// `warning` is set, because the reviewer has no severity to offer — and
// then graded by domain.Evaluate, the same evaluator every other layer
// goes through. Evaluate asks checkpolicy.Policy.Decide, and Decide
// clamps an advisory layer's severity back to Warning whatever the
// document asks for (RFC 0005 §14 decision 10). That is the whole of
// "advisory severity enforced server-side": there is no path from a
// model's opinion to a failed build, because the only path from a model
// to a stored finding runs through both.
func (s *Service) recordLinguisticFindings(
	ctx context.Context, job domain.LinguisticJob, in []domain.LinguisticFinding,
) (uuid.UUID, int, error) {
	// A job that already recorded its run does not record a second one:
	// following a finished job twice cannot double a project's findings.
	if job.CheckRun != nil {
		return *job.CheckRun, job.Findings, nil
	}
	if len(in) > MaxRunFindings {
		return uuid.Nil, 0, fmt.Errorf("%w: %d findings, at most %d", ErrTooManyFindings, len(in), MaxRunFindings)
	}
	sealed := make([]domain.Finding, 0, len(in))
	for _, lf := range in {
		f, err := domain.SealLinguistic(lf)
		if err != nil {
			return uuid.Nil, 0, err
		}
		sealed = append(sealed, f)
	}
	if len(sealed) == 0 {
		return uuid.Nil, 0, nil
	}
	stored, err := s.storedPolicy(ctx, job.Project)
	if err != nil {
		return uuid.Nil, 0, err
	}
	ev := domain.Evaluate(stored.Policy, "", sealed)
	graded := ev.Findings()
	if len(graded) == 0 {
		return uuid.Nil, 0, nil
	}
	run, err := s.RecordCheckRun(ctx, RecordRun{
		Project: job.Project, Ref: job.Ref, Trigger: domain.TriggerAPI, PolicyVersion: ev.PolicyVersion,
		Layers: []domain.Layer{domain.LayerLinguistic}, Policy: stored.Policy, Findings: graded,
		StartedAt: linguisticStart(job),
	})
	if err != nil {
		return uuid.Nil, 0, err
	}
	return run.ID, len(graded), nil
}

func linguisticStart(job domain.LinguisticJob) time.Time {
	if job.StartedAt != nil {
		return *job.StartedAt
	}
	return job.CreatedAt
}

// settleLinguistic writes the job's progress. The UPDATE only moves a
// job that has not finished, so a job settled by another reader stays
// settled and this one reads back what actually stands.
func (s *Service) settleLinguistic(ctx context.Context, job domain.LinguisticJob) (domain.LinguisticJob, error) {
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		moved, err := st.UpdateLinguisticJob(ctx, job)
		if err != nil || moved {
			return err
		}
		job, err = st.LinguisticJob(ctx, job.Project, job.ID)
		return err
	})
	if err != nil {
		return domain.LinguisticJob{}, err
	}
	return job, nil
}

// linguisticID derives a create's job ID from its Idempotency-Key, or
// mints a new one. It is the kernel's convention, without a key store:
// the same (operation, tenant, caller, key) always names the same job,
// so a retry finds that job instead of starting a second review.
func linguisticID(ctx context.Context, actor, key string) (uuid.UUID, error) {
	if key == "" {
		return uuid.Must(uuid.NewV7()), nil
	}
	if err := idempotency.CheckKey(key); err != nil {
		return uuid.Nil, err
	}
	p, _ := authz.From(ctx)
	return idempotency.ID("quality.linguistic_review", p.Tenant.String(), actor, key), nil
}

func (s *Service) existingLinguisticJob(
	ctx context.Context, project, id uuid.UUID,
) (domain.LinguisticJob, bool, error) {
	job, err := s.linguisticJob(ctx, project, id)
	if errors.Is(err, ErrLinguisticJobNotFound) {
		return domain.LinguisticJob{}, false, nil
	}
	return job, err == nil, err
}

func (s *Service) linguisticJob(ctx context.Context, project, id uuid.UUID) (domain.LinguisticJob, error) {
	var job domain.LinguisticJob
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		job, err = st.LinguisticJob(ctx, project, id)
		return err
	})
	return job, err
}

// failLinguistic classifies a refusal from the port. Every code here is
// one Intelligence already reports for the same refusal on a
// translation job, seen from this side of the port.
func failLinguistic(job domain.LinguisticJob, err error, at time.Time) domain.LinguisticJob {
	code := domain.LinguisticFailureInternal
	switch {
	case errors.Is(err, ErrProviderConsent):
		code = domain.LinguisticFailureProviderConsent
	case errors.Is(err, ErrSensitive):
		code = domain.LinguisticFailureSensitive
	case errors.Is(err, ErrBudgetExceeded):
		code = domain.LinguisticFailureBudgetExceeded
	case errors.Is(err, ErrNoRoute), errors.Is(err, ErrLinguistUnavailable):
		code = domain.LinguisticFailureNoRoute
	case errors.Is(err, ErrInvalidLinguisticOutput),
		errors.Is(err, domain.ErrUnknownLinguisticCode),
		errors.Is(err, domain.ErrInvalidLinguisticFinding):
		code = domain.LinguisticFailureInvalidOutput
	case errors.Is(err, ErrProviderError):
		code = domain.LinguisticFailureProviderError
	}
	return job.Fail(code, err.Error(), at)
}

// normalizeScope canonicalizes the locales and trims the selection, so
// two requests that mean the same thing are the same job.
func normalizeScope(in domain.LinguisticScope) (domain.LinguisticScope, error) {
	if err := in.Validate(); err != nil {
		return domain.LinguisticScope{}, err
	}
	out := domain.LinguisticScope{
		Namespace: strings.TrimSpace(in.Namespace),
		KeyPrefix: strings.TrimSpace(in.KeyPrefix),
		Keys:      slices.Clone(in.Keys),
	}
	for _, l := range in.Locales {
		t, err := bcp47.Parse(strings.TrimSpace(l))
		if err != nil {
			return domain.LinguisticScope{}, fmt.Errorf("%w: %s", domain.ErrInvalidLinguisticScope, err)
		}
		out.Locales = append(out.Locales, t.String())
	}
	slices.Sort(out.Locales)
	out.Locales = slices.Compact(out.Locales)
	slices.Sort(out.Keys)
	out.Keys = slices.Compact(out.Keys)
	return out, out.Validate()
}

func linguisticCursor(j domain.LinguisticJob) string {
	return j.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + j.ID.String()
}

func parseLinguisticCursor(s string) (*LinguisticCursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // no cursor is the first page
	}
	at, id, ok := strings.Cut(s, "|")
	t, errTime := time.Parse(time.RFC3339Nano, at)
	u, errID := uuid.Parse(id)
	if !ok || errTime != nil || errID != nil {
		return nil, invalidPageToken()
	}
	return &LinguisticCursor{CreatedAt: t, ID: u}, nil
}
