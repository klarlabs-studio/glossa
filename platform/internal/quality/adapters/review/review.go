// Package review is the seam between a linguistic-QA job and the
// linguistic layer (RFC 0005 §3.8, §13 wave 6).
//
// The two wave-6 slices meet here and nowhere else. The **job** thinks
// in scopes and batches: it asks for "de and fr under `checkout`, on
// `main`", hands the request over and follows a handle. The **layer**
// thinks in one translation: it is given a message, a locale, the two
// texts and the knowledge that bears on them, and answers with what a
// model suspects. Neither is wrong; one is asynchronous and plural and
// the other is synchronous and singular, and something has to expand
// the first into the second.
//
// That is this package's whole job:
//
//   - **Expansion.** The scope becomes a list of (message, locale)
//     pairs, read through ports from the contexts that own the data —
//     Catalog for the messages, their namespaces and their descriptions;
//     Localization for the translations and the revisions a finding is
//     pinned to; Knowledge for the recognized terms and the effective
//     style guide. Quality reads no other context's tables (RFC 0002 §4).
//   - **The `sensitive` rule, absolutely.** A pair under a namespace the
//     job excluded is never enqueued, so no provider is ever offered it,
//     and it is counted into SkippedSensitive rather than quietly
//     dropped: a reviewer has to know the layer did not look there.
//   - **The message ID.** The layer refuses a request without one and is
//     right to: the fingerprint is hashed over it, and an identity
//     minted over a key is not the one every other surface computes
//     (RFC 0005 §2.1). Expansion carries the real catalog ID or refuses.
//   - **Bounds.** A scope expands to at most MaxPairs pairs, and at most
//     Concurrency reviews are in flight at once. A large project cannot
//     turn one job into an unbounded fan-out of model calls.
//   - **Stopping.** A cancel stops the batch and Poll says so. A refusal
//     that ends the tenant's ability to make the *next* call — the
//     budget above all — stops the batch cleanly and reports what it
//     managed, because evidence already paid for is not thrown away to
//     report a code.
//
// Nothing here knows what a prompt is, and nothing here calls a
// provider: the model call is the layer's, which is Intelligence's,
// behind the Layer port. Every test in this package fakes that port, so
// a test that somehow reached a network would have to have been handed
// a real router — and the composition root is the only place that
// happens.
package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/linguistic"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Bounds on one batch (RFC 0005 §10).
const (
	// MaxPairs is the most (message, locale) pairs one job reviews. A
	// scope that selects more is refused with the sentence that says how
	// to narrow it, rather than silently reviewing an arbitrary prefix
	// of somebody's catalog.
	MaxPairs = 5000
	// DefaultConcurrency is how many reviews run at once. Each is a
	// model call, so this is the fan-out one job may cause; it is small
	// on purpose, and the tenant's budget bounds the total either way.
	DefaultConcurrency = 4
	// Retention is how long a finished batch is remembered. A job reads
	// its result once — recording is idempotent through `check_run_id` —
	// so this only has to outlive the read that follows the review.
	Retention = time.Hour
)

// ErrScopeTooLarge is a scope that selects more than MaxPairs
// translations. It is not a refusal of the layer but of the request:
// the caller narrows the namespace, the key prefix or the locales.
var ErrScopeTooLarge = errors.New("quality: the linguistic-QA scope selects too many translations")

// ── the ports ───────────────────────────────────────────────────────

// Message is one source message as the expansion needs it: its catalog
// identity, where it lives, and the text and description a reviewer is
// shown.
type Message struct {
	// ID is the catalog message's ID, and it is required. See the
	// package comment: a fingerprint over a key is not the one every
	// other surface computes.
	ID        string
	Key       string
	Namespace string
	// Description is what the message is for, where somebody wrote it
	// down. It is evidence, not a rule.
	Description string
	// Revision is the message's current source revision.
	Revision int
	// Source is the source text in canonical MF2 syntax.
	Source string
}

// Catalog is Catalog's port: the project's active source messages, with
// the namespaces and descriptions a review needs.
type Catalog interface {
	Messages(ctx context.Context, project uuid.UUID) ([]Message, error)
}

// Translation is one stored translation as the expansion needs it.
type Translation struct {
	// Text is the translation in canonical MF2 syntax.
	Text string
	// Revision is the translation revision under review — the locus a
	// finding carries — and SourceRevision the source revision it was
	// made against, which is what a waiver dies with.
	Revision       string
	SourceRevision int
}

// Translated is one project's translations: the source locale the
// review compares against, and the translations by locale and then by
// catalog message ID.
type Translated struct {
	SourceLocale string
	ByLocale     map[string]map[string]Translation
}

// Localization is Localization's port: what has actually been
// translated, keyed the way Catalog's messages are.
type Localization interface {
	Translations(ctx context.Context, project uuid.UUID, locales []string) (Translated, error)
}

// Knowledge is the Knowledge context's port, in Intelligence's shape
// because Intelligence's adapter onto Knowledge already has it: the
// termbase concepts recognized in a source text, and the effective
// style guide for a locale and namespace. The linguistic layer takes
// both as evidence; the terminology and style layers are what grade
// them (RFC 0003 §2.2, §3.2).
type Knowledge interface {
	RecognizeTerms(
		ctx context.Context, scope inteldomain.Scope, pair inteldomain.LocalePair, sourceText string,
	) ([]inteldomain.TermHit, error)
	EffectiveStyle(
		ctx context.Context, scope inteldomain.Scope, locale, namespace string,
	) (inteldomain.StyleGuide, error)
}

// Layer is the linguistic layer — the other wave-6 slice — as this
// package uses it: one translation reviewed at a time, synchronously.
// *linguistic.Reviewer satisfies it.
type Layer interface {
	Review(ctx context.Context, req linguistic.Request) (linguistic.Result, error)
}

// Intelligence is the Intelligence context's port: the layer for one
// project, and the tenant's standing permission to send text to a
// provider at all.
//
// The layer is asked for per call and not held at startup, because it
// runs on the tenant's router — its providers, its routing policy, its
// prices and its budget guard — and a router can only be built from a
// request's own context. A composition root has no tenant.
type Intelligence interface {
	// ProviderConsent is the tenant's explicit permission to send text
	// to AI providers (RFC 0003 §7). Off by default. The job's preflight
	// already refused without it; the reviewer is told again, because
	// Intelligence refuses a request that does not say so and a gate
	// only at the door is a gate the second door does not have.
	ProviderConsent(ctx context.Context) (bool, error)
	// Layer builds the linguistic layer for one project.
	Layer(ctx context.Context, project uuid.UUID) (Layer, error)
}

// Deps are the four contexts a review reads through.
type Deps struct {
	Catalog      Catalog
	Localization Localization
	Knowledge    Knowledge
	Intelligence Intelligence
}

// ── the batcher ─────────────────────────────────────────────────────

// Option configures a Batcher.
type Option func(*Batcher)

// WithConcurrency caps the reviews in flight at once (default
// DefaultConcurrency). A value below one is ignored.
func WithConcurrency(n int) Option {
	return func(b *Batcher) {
		if n > 0 {
			b.concurrency = n
		}
	}
}

// WithMaxPairs caps one scope's expansion (default MaxPairs).
func WithMaxPairs(n int) Option {
	return func(b *Batcher) {
		if n > 0 {
			b.maxPairs = n
		}
	}
}

// WithLogger sets the logger a stopped batch reports itself through.
func WithLogger(l *slog.Logger) Option {
	return func(b *Batcher) {
		if l != nil {
			b.log = l
		}
	}
}

// WithClock sets the clock a batch's retention is measured on.
func WithClock(now func() time.Time) Option {
	return func(b *Batcher) {
		if now != nil {
			b.now = now
		}
	}
}

// Batcher implements the Reviewer the linguistic-QA job hands its work
// to (quality/adapters/intelligence.Reviewer).
//
// Batches live in memory, which is the honest shape for what they are:
// a handle on work in flight in this process. What matters survives —
// the job row is Quality's, under forced RLS, and the findings are
// stored in a check run the moment the review finishes. A batch that is
// lost with the process is a job that never reaches `succeeded`, which
// a caller sees and can ask for again; it is not a finding that
// disappeared.
type Batcher struct {
	deps        Deps
	concurrency int
	maxPairs    int
	log         *slog.Logger
	now         func() time.Time

	mu      sync.Mutex
	batches map[string]*batch
}

// New returns a Batcher over the four ports.
func New(d Deps, opts ...Option) (*Batcher, error) {
	switch {
	case d.Catalog == nil:
		return nil, errors.New("review: the Catalog port is required")
	case d.Localization == nil:
		return nil, errors.New("review: the Localization port is required")
	case d.Knowledge == nil:
		return nil, errors.New("review: the Knowledge port is required")
	case d.Intelligence == nil:
		return nil, errors.New("review: the Intelligence port is required")
	}
	b := &Batcher{
		deps: d, concurrency: DefaultConcurrency, maxPairs: MaxPairs,
		log: slog.Default(), now: time.Now, batches: map[string]*batch{},
	}
	for _, o := range opts {
		o(b)
	}
	return b, nil
}

// pair is one translation to review.
type pair struct {
	msg    Message
	locale string
	tr     Translation
}

// batch is one handed-over review's state.
type batch struct {
	project uuid.UUID
	cancel  context.CancelFunc

	mu        sync.Mutex
	done      bool
	cancelled bool
	// stop says the batch is winding down: the feeder enqueues nothing
	// more and the workers drain.
	stop     bool
	reviewed int
	skipped  int
	findings []domain.LinguisticFinding
	// stopped is the refusal that ended the batch early, or the first
	// one a single review answered. It is reported only when the batch
	// produced nothing at all: a review that ran out of budget halfway
	// has still found what it found, and throwing that away to report a
	// code would lose work the tenant has already paid for.
	stopped  error
	finished time.Time
}

// Start implements the job's Reviewer: it expands the scope and runs
// the reviews.
//
// Expansion happens here, on the caller's context and with the caller's
// permissions, because it is a read the caller could have made through
// the API and because a caller that gives up should not have paid for a
// scan. The reviews that follow run on a context detached from the
// request — the principal, the tenant and the trace travel, the
// deadline does not — because the job outlives the POST that asked for
// it, and Cancel is what stops it.
func (b *Batcher) Start(ctx context.Context, req app.LinguisticRequest) (string, error) {
	consent, err := b.deps.Intelligence.ProviderConsent(ctx)
	if err != nil {
		return "", translate(err)
	}
	// The job refused this already. Refusing it again is the second
	// door: nothing below here builds a request that is allowed to say
	// `provider_consent: true` on a tenant that never said it.
	if !consent {
		return "", app.ErrProviderConsent
	}
	tenant, err := tenantOf(ctx)
	if err != nil {
		return "", err
	}
	pairs, sourceLocale, skipped, err := b.expand(ctx, req)
	if err != nil {
		return "", err
	}
	layer, err := b.deps.Intelligence.Layer(ctx, req.Project)
	if err != nil {
		return "", translate(err)
	}
	id := "lq_" + uuid.Must(uuid.NewV7()).String()
	st := &batch{project: req.Project, skipped: skipped}
	// Nothing to send is a finished review with nothing found, not a
	// review still running. The job records the skipped count either
	// way, which is how "the layer did not look there" reaches a person.
	if len(pairs) == 0 {
		st.done, st.finished = true, b.now()
		b.remember(id, st)
		return id, nil
	}
	run, cancel := context.WithCancel(context.WithoutCancel(ctx))
	st.cancel = cancel
	b.remember(id, st)
	go b.run(run, st, layer, reviewing{tenant: tenant, source: sourceLocale, req: req}, pairs)
	return id, nil
}

// reviewing is what every pair of one batch shares: who is asking,
// what the translations are compared against, and which job they
// belong to.
type reviewing struct {
	tenant string
	source string
	req    app.LinguisticRequest
}

// Poll implements the job's Reviewer.
func (b *Batcher) Poll(_ context.Context, project uuid.UUID, id string) (app.LinguisticProgress, error) {
	st, err := b.lookup(project, id)
	if err != nil {
		return app.LinguisticProgress{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	out := app.LinguisticProgress{
		Cancelled: st.cancelled, Done: st.done && !st.cancelled,
		Reviewed: st.reviewed, SkippedSensitive: st.skipped,
	}
	switch {
	case st.cancelled:
		return out, nil
	case !st.done:
		return out, nil
	// A batch that produced nothing and was stopped by a refusal has
	// nothing to report but the refusal, so it reports that and the job
	// fails with the code that says what to change. One that produced
	// something reports it: partial evidence is evidence.
	case st.reviewed == 0 && len(st.findings) == 0 && st.stopped != nil:
		return app.LinguisticProgress{}, translate(st.stopped)
	}
	out.Findings = slices.Clone(st.findings)
	return out, nil
}

// Cancel implements the job's Reviewer. Cancelling a batch that has
// already finished changes nothing and is not an error, which is what
// the job's port promises.
func (b *Batcher) Cancel(_ context.Context, project uuid.UUID, id string) error {
	st, err := b.lookup(project, id)
	if err != nil {
		return err
	}
	st.mu.Lock()
	running := !st.done
	if running {
		st.cancelled = true
	}
	cancel := st.cancel
	st.mu.Unlock()
	if running && cancel != nil {
		cancel()
	}
	return nil
}

func (b *Batcher) remember(id string, st *batch) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Finished batches are swept here rather than by a goroutine of
	// their own: a Start is the only moment the map can grow, so it is
	// the only moment it has to be trimmed.
	cutoff := b.now().Add(-Retention)
	for k, old := range b.batches {
		old.mu.Lock()
		expired := old.done && !old.finished.IsZero() && old.finished.Before(cutoff)
		old.mu.Unlock()
		if expired {
			delete(b.batches, k)
		}
	}
	b.batches[id] = st
}

func (b *Batcher) lookup(project uuid.UUID, id string) (*batch, error) {
	b.mu.Lock()
	st, ok := b.batches[id]
	b.mu.Unlock()
	// A batch this process never held, or one another project's job
	// named, is simply not there. The job treats ErrNotFound on a cancel
	// as nothing to stop.
	if !ok || st.project != project {
		return nil, app.ErrNotFound
	}
	return st, nil
}

// ── expansion ───────────────────────────────────────────────────────

// expand turns the scope into the pairs to review, and counts what the
// `sensitive` rule kept out.
//
// A pair exists where a selected message has a translation in a
// selected locale. A message with no translation in a locale is not
// something a linguistic review has an opinion about — the
// completeness layer is what reports its absence — so it is neither
// reviewed nor counted as skipped.
func (b *Batcher) expand(
	ctx context.Context, req app.LinguisticRequest,
) (pairs []pair, sourceLocale string, skipped int, err error) {
	msgs, err := b.deps.Catalog.Messages(ctx, req.Project)
	if err != nil {
		return nil, "", 0, err
	}
	tr, err := b.deps.Localization.Translations(ctx, req.Project, req.Scope.Locales)
	if err != nil {
		return nil, "", 0, err
	}
	exclude := make(map[string]bool, len(req.ExcludeNamespaces))
	for _, ns := range req.ExcludeNamespaces {
		exclude[ns] = true
	}
	keys := make(map[string]bool, len(req.Scope.Keys))
	for _, k := range req.Scope.Keys {
		keys[k] = true
	}
	var out []pair
	for _, m := range msgs {
		if !selects(req.Scope, keys, m) {
			continue
		}
		// The layer refuses a request without an ID, deliberately, and
		// the refusal is checked here so it is about the catalog rather
		// than about one translation. A source that cannot name a
		// message is a broken source, not a finding to mint under an
		// identity no other surface would compute (RFC 0005 §2.1).
		if m.ID == "" {
			return nil, "", 0, fmt.Errorf(
				"quality: the catalog gave message %q no ID, and a linguistic finding's fingerprint is hashed over it",
				m.Key)
		}
		for _, locale := range req.Scope.Locales {
			t, ok := tr.ByLocale[locale][m.ID]
			if !ok || strings.TrimSpace(t.Text) == "" || strings.TrimSpace(m.Source) == "" {
				continue
			}
			// The `sensitive` rule, and the only place it is applied:
			// what is not enqueued here is never offered to a provider.
			// It is counted, because a namespace the layer did not look
			// at is not a namespace with nothing wrong in it.
			if exclude[m.Namespace] {
				skipped++
				continue
			}
			if len(out) == b.maxPairs {
				return nil, "", 0, fmt.Errorf(
					"%w: more than %d; narrow the namespace, the key prefix or the locales",
					ErrScopeTooLarge, b.maxPairs)
			}
			out = append(out, pair{msg: m, locale: locale, tr: t})
		}
	}
	if tr.SourceLocale == "" && len(out) > 0 {
		return nil, "", 0, errors.New(
			"quality: the project has no source locale, and a review compares a translation against one")
	}
	return out, tr.SourceLocale, skipped, nil
}

// selects reports whether the scope names the message. An empty member
// does not narrow, which is the scope's documented meaning.
func selects(scope domain.LinguisticScope, keys map[string]bool, m Message) bool {
	switch {
	case scope.Namespace != "" && m.Namespace != scope.Namespace:
		return false
	case scope.KeyPrefix != "" && !strings.HasPrefix(m.Key, scope.KeyPrefix):
		return false
	case len(keys) > 0 && !keys[m.Key]:
		return false
	}
	return true
}

// ── running ─────────────────────────────────────────────────────────

// run reviews the pairs with at most concurrency in flight. The feeder
// stops the moment the batch is halted or cancelled, so a refusal that
// will refuse every remaining call costs at most the calls already in
// flight.
func (b *Batcher) run(ctx context.Context, st *batch, layer Layer, of reviewing, pairs []pair) {
	defer st.finish(b.now())
	work := make(chan pair)
	var wg sync.WaitGroup
	for range min(b.concurrency, len(pairs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				if ctx.Err() != nil {
					return
				}
				b.reviewOne(ctx, st, layer, of, p)
			}
		}()
	}
	for _, p := range pairs {
		if st.halted() {
			break
		}
		select {
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return
		case work <- p:
		}
	}
	close(work)
	wg.Wait()
}

// reviewOne runs one translation past the layer and folds the answer
// into the batch.
func (b *Batcher) reviewOne(ctx context.Context, st *batch, layer Layer, of reviewing, p pair) {
	res, err := layer.Review(ctx, b.request(ctx, of, p))
	if err != nil {
		b.fold(ctx, st, err, p)
		return
	}
	st.record(found(res))
}

// request builds the layer's request: the texts, the revisions and the
// knowledge that bears on them.
//
// The terms and the style guide are evidence the reviewer is shown, not
// rules it grades — the terminology and style layers do that — so a
// knowledge read that fails does not fail the review. It costs the
// reviewer some context and says so in the log, which is better than
// refusing to look at a translation because a style guide could not be
// merged.
func (b *Batcher) request(ctx context.Context, of reviewing, p pair) linguistic.Request {
	scope := inteldomain.Scope{TenantID: of.tenant, ProjectID: of.req.Project.String()}
	rev := p.tr.SourceRevision
	out := linguistic.Request{
		Tenant: of.tenant, Project: of.req.Project.String(),
		Message: p.msg.ID, Key: p.msg.Key, Namespace: p.msg.Namespace,
		Revision: p.tr.Revision, SourceRevision: &rev,
		SourceLocale: of.source, TargetLocale: p.locale,
		Source: p.msg.Source, Translation: p.tr.Text,
		Description: p.msg.Description, ProviderConsent: true,
	}
	locales := inteldomain.LocalePair{Source: of.source, Target: p.locale}
	if hits, err := b.deps.Knowledge.RecognizeTerms(ctx, scope, locales, p.msg.Source); err != nil {
		b.log.WarnContext(ctx, "quality: linguistic review without termbase evidence",
			slog.String("key", p.msg.Key), slog.String("locale", p.locale), slog.Any("error", err))
	} else {
		out.Terms = hits
	}
	if g, err := b.deps.Knowledge.EffectiveStyle(ctx, scope, p.locale, p.msg.Namespace); err != nil {
		b.log.WarnContext(ctx, "quality: linguistic review without a style guide",
			slog.String("key", p.msg.Key), slog.String("locale", p.locale), slog.Any("error", err))
	} else {
		out.Style = &g
	}
	return out
}

// fold classifies one review's failure. A refusal that ends the
// tenant's ability to make the next call stops the batch; anything
// about this one translation — a malformed answer, a provider that
// failed — costs that translation and no other.
func (b *Batcher) fold(ctx context.Context, st *batch, err error, p pair) {
	if terminal(err) {
		b.log.WarnContext(ctx, "quality: linguistic review stopped",
			slog.String("key", p.msg.Key), slog.String("locale", p.locale), slog.Any("error", err))
		st.halt(err)
		return
	}
	b.log.InfoContext(ctx, "quality: one translation was not reviewed",
		slog.String("key", p.msg.Key), slog.String("locale", p.locale), slog.Any("error", err))
	st.note(err)
}

// found turns one review's accepted notes into the findings the port
// hands back. They carry no severity: see domain.LinguisticFinding.
func found(res linguistic.Result) []domain.LinguisticFinding {
	out := make([]domain.LinguisticFinding, 0, len(res.Reviewed))
	for _, r := range res.Reviewed {
		out = append(out, domain.LinguisticFinding{
			Code:      r.Code,
			Message:   r.Locus.Message,
			Key:       r.Locus.Key,
			Locale:    r.Locus.Locale,
			Namespace: r.Locus.Namespace,
			Revision:  r.Locus.Revision,
			// Explanation is the model's sentence; Subject the words it
			// pointed at, which is what makes two suspicions about one
			// message two findings.
			Explanation:    r.Explanation,
			Subject:        r.Quote,
			Span:           r.Locus.Span,
			Suggestion:     r.Suggestion,
			Evidence:       maps.Clone(r.Evidence),
			SourceRevision: r.SourceRevision,
		})
	}
	return out
}

// ── batch state ─────────────────────────────────────────────────────

func (s *batch) record(fs []domain.LinguisticFinding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewed++
	s.findings = append(s.findings, fs...)
	// One run may not carry more findings than the service will store,
	// so a batch that reaches the cap stops rather than producing a
	// result the job can only refuse.
	if len(s.findings) >= app.MaxRunFindings {
		s.findings = s.findings[:app.MaxRunFindings]
		s.stopped = app.ErrTooManyFindings
		s.cancelHalted()
	}
}

// halt stops the batch: the refusal is remembered and the workers are
// told to stop through the batch's own context.
func (s *batch) halt(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped == nil {
		s.stopped = err
	}
	s.cancelHalted()
}

// note remembers a refusal without stopping anything, so a batch that
// produced nothing at all can still say why.
func (s *batch) note(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped == nil {
		s.stopped = err
	}
}

// cancelHalted stops the workers. The caller holds the lock.
func (s *batch) cancelHalted() {
	s.stop = true
	if s.cancel != nil {
		s.cancel()
	}
}

// halted reports whether the feeder should stop enqueuing: the batch
// was stopped by a standing refusal, or cancelled by its job.
func (s *batch) halted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop || s.cancelled
}

func (s *batch) finish(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done, s.finished = true, at
	if s.cancel != nil {
		s.cancel()
	}
}

// tenantOf is the tenant every review is scoped, routed and billed to.
// It is the caller's own — the review is an authorized use case of the
// contexts it reads, not a privileged sweep — so it comes off the
// principal and nowhere else.
func tenantOf(ctx context.Context) (string, error) {
	p, ok := authz.From(ctx)
	if !ok || p.Tenant.IsZero() {
		return "", authz.ErrUnauthenticated
	}
	return p.Tenant.String(), nil
}

// ── error translation ───────────────────────────────────────────────

// terminal reports whether a refusal ends the batch rather than one
// review. Each is a standing condition: the next call would be refused
// for the same reason, so there is nothing to gain by making it.
func terminal(err error) bool {
	return errors.Is(err, inteldomain.ErrBudgetExceeded) ||
		errors.Is(err, inteldomain.ErrProviderConsent) ||
		errors.Is(err, inteldomain.ErrNoRoute) ||
		errors.Is(err, context.Canceled)
}

// translate maps Intelligence's refusals onto Quality's, which is the
// vocabulary the job's failure codes are spelled in. An error that is
// already Quality's passes through.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, inteldomain.ErrBudgetExceeded):
		return fmt.Errorf("%w: %w", app.ErrBudgetExceeded, err)
	case errors.Is(err, inteldomain.ErrProviderConsent):
		return fmt.Errorf("%w: %w", app.ErrProviderConsent, err)
	case errors.Is(err, inteldomain.ErrSensitive):
		return fmt.Errorf("%w: %w", app.ErrSensitive, err)
	case errors.Is(err, inteldomain.ErrNoRoute):
		return fmt.Errorf("%w: %w", app.ErrNoRoute, err)
	case errors.Is(err, inteldomain.ErrInvalidOutput):
		return fmt.Errorf("%w: %w", app.ErrInvalidLinguisticOutput, err)
	}
	var pe *inteldomain.ProviderError
	if errors.As(err, &pe) {
		return fmt.Errorf("%w: %w", app.ErrProviderError, err)
	}
	return err
}
