package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The Glossa PR check's worker (RFC 0004 §6.4).
//
// Its whole shape follows from two things learned the hard way:
//
//   - GitHub **appends** a check run's annotations, so an annotation
//     must be sent exactly once per run. The run is the unit — a new
//     commit or a `check_run.rerequested` makes a new one — and each
//     run carries a ledger of what it has been sent.
//   - The sticky comment must have **one writer per pull request**. The
//     queue row *is* the pull request, so claiming it is the lock: two
//     jobs for one pull request cannot exist, and a retry updates the
//     stored comment rather than writing a second.
//
// Everything else is the usual queue worker: claimed with a lease,
// at-least-once, idempotent, with a leased sweep on the scheduler for
// the thirty-minute wait.

// principalCheck is the background principal the check worker acts as.
const principalCheck = "integration.github_check"

// CheckConfig tunes the check worker.
type CheckConfig struct {
	// Workers is how many checks this process renders at once.
	Workers int
	// PollInterval is how long an idle worker waits before looking again.
	PollInterval time.Duration
	// Timeout bounds one attempt; Lease (longer) is how long a claimed
	// check is reserved against the other replicas.
	Timeout time.Duration
	Lease   time.Duration
	// DepthInterval is how often the queue-depth metric is sampled.
	DepthInterval time.Duration
}

func (c *CheckConfig) defaults() {
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = time.Minute
	}
	if c.Lease <= c.Timeout {
		c.Lease = c.Timeout + time.Minute
	}
	if c.DepthInterval <= 0 {
		c.DepthInterval = 30 * time.Second
	}
}

// CheckWorker renders the Glossa check and writes it to GitHub.
type CheckWorker struct {
	s   *GitHubService
	cfg CheckConfig
}

// NewCheckWorker returns a worker pool for s. It is only useful where
// the service has a check queue and its sources; without them Run does
// nothing, because a deployment with no GitHub App has no checks.
func NewCheckWorker(s *GitHubService, cfg CheckConfig) *CheckWorker {
	cfg.defaults()
	return &CheckWorker{s: s, cfg: cfg}
}

// Run works until ctx is cancelled.
func (w *CheckWorker) Run(ctx context.Context) error {
	if !w.s.checksEnabled() {
		<-ctx.Done()
		return nil
	}
	var wg sync.WaitGroup
	for range w.cfg.Workers {
		wg.Go(func() { w.loop(ctx) })
	}
	wg.Go(func() { w.depth(ctx) })
	wg.Wait()
	return nil
}

func (w *CheckWorker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			w.s.logger.ErrorContext(ctx, "integration: GitHub check worker", slog.Any("error", err))
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

func (w *CheckWorker) depth(ctx context.Context) {
	t := time.NewTicker(w.cfg.DepthInterval)
	defer t.Stop()
	for {
		if n, err := w.s.checks.Depth(ctx); err == nil {
			w.s.checkMetrics.QueueDepth(n)
		} else if ctx.Err() == nil {
			w.s.logger.WarnContext(ctx, "integration: sampling the GitHub check queue", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce claims one due check and reports it; worked is false when
// none was due. Tests drive the queue with it.
func (w *CheckWorker) RunOnce(ctx context.Context) (worked bool, err error) {
	if !w.s.checksEnabled() {
		return false, nil
	}
	c, ok, err := w.s.checks.Claim(ctx, w.s.now(), w.cfg.Lease)
	if err != nil || !ok {
		return false, err
	}
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.Timeout)
	defer cancel()
	return true, w.s.handleCheck(hctx, c)
}

// Sweep is the thirty-minute wait (RFC 0004 §6.4). It makes every check
// whose head SHA never saw a Glossa CI run due again; the worker then
// completes it `neutral`. It runs on the scheduler, leased once per
// deployment, as the inbox's own sweep does — the decision stays in the
// worker, so there is one place that completes a check.
func (w *CheckWorker) Sweep(ctx context.Context) error {
	if !w.s.checksEnabled() {
		return nil
	}
	now := w.s.now()
	n, err := w.s.checks.Expire(ctx, now.Add(-domain.CheckWait), now)
	if n > 0 && err == nil {
		w.s.logger.InfoContext(ctx, "integration: checks past their wait for CI", slog.Int("checks", n))
	}
	return err
}

// checksEnabled reports whether this deployment wired the check queue
// and its sources.
func (s *GitHubService) checksEnabled() bool { return s.checks != nil && s.sources != nil }

// handleCheck renders one claimed check and settles it. It never fails
// the caller for a reason the next attempt could fix: a GitHub outage
// or a slow read is a retry, and everything else is recorded on the row.
func (s *GitHubService) handleCheck(ctx context.Context, c domain.Check) error {
	start := s.now()
	ctx, end := s.checkSpan(ctx, c)
	outcome, err := s.reportCheck(ctx, &c)
	end(&err)
	s.checkMetrics.CheckHandled(outcome, s.now().Sub(start))
	if err != nil {
		if c.Attempts >= domain.MaxCheckAttempts {
			s.logger.ErrorContext(ctx, "integration: giving up on a Glossa check until the next event",
				slog.Int64("repository_id", c.RepositoryID), slog.Int("pull_request", c.PullRequest),
				slog.Int("attempts", c.Attempts), slog.Any("error", err))
			return s.checks.Save(ctx, c, c.Deadline())
		}
		return s.checks.Retry(ctx, c, s.now(), domain.RetryDelay(c.Attempts), shortFailure(err.Error()))
	}
	c.Failure = ""
	// A check still waiting looks again at its deadline, so a commit
	// whose CI never runs completes by itself even where the sweep is
	// not running.
	return s.checks.Save(ctx, c, c.Deadline())
}

// checkSpan starts the one trace per check job (RFC 0004 §11).
func (s *GitHubService) checkSpan(ctx context.Context, c domain.Check) (context.Context, func(*error)) {
	ctx, sp := s.tracer.Start(ctx, "github check", trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.Int64("github.repository_id", c.RepositoryID),
			attribute.Int("github.pull_request", c.PullRequest),
			attribute.String("github.head_sha", c.HeadSHA),
		))
	return ctx, func(err *error) {
		if err != nil && *err != nil {
			sp.RecordError(*err)
			sp.SetStatus(codes.Error, (*err).Error())
		}
		sp.End()
	}
}

// reportCheck does the work: it resolves the repository's connections,
// renders one report per connection, writes the check runs, and updates
// the one sticky comment.
func (s *GitHubService) reportCheck(ctx context.Context, c *domain.Check) (outcome string, err error) {
	tctx := tenancy.ContextWithTenant(ctx, tenancy.ID(c.TenantID))
	bg, err := authz.Background(tctx, principalCheck,
		authz.IntegrationRead, authz.CatalogRead, authz.TranslationsRead, authz.KnowledgeRead, authz.ReleasesRead)
	if err != nil {
		return "failed", err
	}
	var conns []domain.GitConnection
	if err := s.tx.InGitHub(bg, func(ctx context.Context, st GitHubStore) error {
		var err error
		conns, err = st.ConnectionsForRepository(ctx, c.RepositoryID)
		return err
	}); err != nil {
		return "retry", err
	}
	if len(conns) == 0 {
		// The repository feeds nothing any more. Say nothing on GitHub —
		// a check nobody asked for is noise — and drop the row.
		if _, err := s.checks.DropRepository(withoutTenant(ctx), c.RepositoryID); err != nil {
			return "retry", err
		}
		c.State = domain.CheckCompleted
		return "ignored", nil
	}
	target := GitHubTarget{TenantID: c.TenantID, InstallationID: c.InstallationID, RepositoryID: c.RepositoryID}
	var (
		sections []string
		verdict  string
		waiting  bool
	)
	for _, conn := range conns {
		section, concluded, err := s.reportConnection(bg, c, target, conn)
		if err != nil {
			return "retry", err
		}
		sections = append(sections, section)
		if concluded == "" {
			waiting = true
			continue
		}
		verdict = domain.WorseConclusion(verdict, concluded)
	}
	if err := s.upsertStickyComment(bg, c, target, sections); err != nil {
		return "retry", err
	}
	if waiting {
		c.State = domain.CheckQueued
		return "waiting", nil
	}
	s.completeCheck(c, verdict)
	return "done", nil
}

// completeCheck records the row's verdict and the §11 latency, from the
// pull-request event to the completed check. A verdict that has not
// changed is not counted twice: a job woken by a translation that
// changed nothing is not a new check.
func (s *GitHubService) completeCheck(c *domain.Check, verdict string) {
	now := s.now()
	changed := c.State != domain.CheckCompleted || c.Conclusion != verdict
	c.State, c.Conclusion = domain.CheckCompleted, verdict
	c.CompletedAt = &now
	if changed {
		s.checkMetrics.CheckCompleted(verdict, now.Sub(c.RequestedAt))
		s.logger.InfoContext(context.Background(), "integration: a Glossa check completed",
			slog.Int64("repository_id", c.RepositoryID), slog.Int("pull_request", c.PullRequest),
			slog.String("head_sha", c.HeadSHA), slog.String("conclusion", verdict))
	}
}

// reportConnection writes one Git connection's check run and returns
// its section of the sticky comment. concluded is "" while the check is
// still waiting for this commit's CI.
func (s *GitHubService) reportConnection(ctx context.Context, c *domain.Check, target GitHubTarget,
	conn domain.GitConnection,
) (section, concluded string, err error) {
	t := c.Target(conn.ID)
	// The check run exists from the pull-request event onwards, queued,
	// so the pull request shows Glossa is looking at the commit. It is
	// idempotent by (repository, head SHA, name), and the stored id is
	// tried first, so a retry never makes a second.
	run, err := s.gh.EnsureCheckRun(ctx, target,
		CheckRunKey{HeadSHA: c.HeadSHA, Name: domain.CheckRunName(conn.Path)}, c.ID.String(), t.CheckRunID)
	if err != nil {
		return "", "", err
	}
	if t.CheckRunID != run.ID {
		// A new run: its annotation ledger starts empty.
		t.CheckRunID, t.Annotations = run.ID, nil
	}
	if c.FromFork {
		// RFC 0004 §6.3. The head is in another repository, so this
		// commit's CI has no Glossa token and cannot upload: waiting for
		// it would only delay the same answer by thirty minutes. The
		// sources are not read either — Catalog never tracked the branch.
		rep := ForkCheckReport()
		if err := s.writeCheckRun(ctx, target, &t, rep); err != nil {
			return "", "", err
		}
		t.Conclusion = rep.Conclusion
		c.SetTarget(conn.ID, t)
		return ForkComment(conn.RepositoryName+pathSuffix(conn.Path), rep), rep.Conclusion, nil
	}
	in, ready, err := s.checkInput(ctx, c, conn)
	if err != nil {
		return "", "", err
	}
	t.Pushed, t.Usages, t.Recorded = ready.pushed, ready.usages, ready.recorded
	switch {
	case ready.complete():
		rep := BuildCheckReport(in)
		if err := s.writeCheckRun(ctx, target, &t, rep); err != nil {
			return "", "", err
		}
		t.Conclusion = rep.Conclusion
		c.SetTarget(conn.ID, t)
		return StickyComment(conn.RepositoryName+pathSuffix(conn.Path), s.links(ctx, c, conn, in), in, rep), rep.Conclusion, nil
	case c.Expired(s.now()) && ready.uploaded():
		// CI is here — the catalogs and the usages arrived — but nothing
		// recorded a `glossa check` run for this commit within the wait.
		// The report is Integration's own reduced view, and it says so
		// rather than passing itself off as the check CI ran
		// (RFC 0005 §12.3). in.Recorded is nil, which is what makes it
		// the reduced one.
		rep := BuildCheckReport(in)
		if err := s.writeCheckRun(ctx, target, &t, rep); err != nil {
			return "", "", err
		}
		t.Conclusion = rep.Conclusion
		c.SetTarget(conn.ID, t)
		return StickyComment(conn.RepositoryName+pathSuffix(conn.Path), s.links(ctx, c, conn, in), in, rep), rep.Conclusion, nil
	case c.Expired(s.now()):
		rep := CheckReport{
			Conclusion: ConclusionNeutral,
			Title:      "No Glossa CI run for this commit",
			Summary: "Glossa waited 30 minutes for this commit's `glossa push` and usages upload and neither " +
				"arrived, so there is nothing to check.\n\nIf the branch has no Glossa CI job yet, see the " +
				"composite action in `.github/actions/glossa`.\n",
		}
		if err := s.writeCheckRun(ctx, target, &t, rep); err != nil {
			return "", "", err
		}
		t.Conclusion = rep.Conclusion
		c.SetTarget(conn.ID, t)
		return StickyComment(conn.RepositoryName+pathSuffix(conn.Path), s.links(ctx, c, conn, in), in, rep), rep.Conclusion, nil
	}
	c.SetTarget(conn.ID, t)
	return StickyComment(conn.RepositoryName+pathSuffix(conn.Path), s.links(ctx, c, conn, in),
		in, CheckReport{Conclusion: ConclusionNeutral}), "", nil
}

func pathSuffix(path string) string {
	if path == "" {
		return ""
	}
	return " (" + path + ")"
}

// writeCheckRun patches the run with the report, sending only the
// annotations this run has not been sent.
func (s *GitHubService) writeCheckRun(ctx context.Context, target GitHubTarget, t *domain.CheckTarget, rep CheckReport) error {
	send, fingerprints := UnsentAnnotations(*t, rep.Annotations)
	err := s.gh.UpdateCheckRun(ctx, target, t.CheckRunID, CheckRunUpdate{
		Status: CheckCompleted, Conclusion: rep.Conclusion,
		Title: rep.Title, Summary: rep.Summary, Annotations: send,
	})
	if errors.Is(err, ErrGitHubNotFound) {
		// Somebody deleted the run. Forget it; the next attempt makes a
		// new one, with an empty ledger.
		t.CheckRunID, t.Annotations = 0, nil
		return err
	}
	if err != nil {
		return err
	}
	t.Record(fingerprints)
	return nil
}

// upsertStickyComment writes the one comment the pull request gets. The
// claim on the row is what makes this safe: one job per pull request,
// so the stored comment id can never be raced.
func (s *GitHubService) upsertStickyComment(ctx context.Context, c *domain.Check, target GitHubTarget, sections []string) error {
	body := strings.Join(sections, "\n---\n\n")
	id, err := s.gh.UpsertStickyComment(ctx, target, c.PullRequest, c.CommentID, body)
	if err != nil {
		s.checkMetrics.CommentUpserted("failed")
		return err
	}
	s.checkMetrics.CommentUpserted("ok")
	c.CommentID = id
	return nil
}

// checkReadiness is what this commit's CI has done: pushed the
// catalogs, uploaded the build's usages, and recorded a `glossa check`
// run.
//
// The third is new with RFC 0005 §12.3, and it is waited for only where
// a run is coming. The pull request renders the run CI recorded, so for
// a project whose CI runs `glossa check`, a commit whose run has not
// landed is a commit whose verdict is on its way: the check waits the
// same CheckWait for it, and when the wait runs out with a push and
// usages but no run it falls back to Integration's reduced view and
// says so.
//
// A project that has never recorded a run is not waited for
// (expectsRun is false). Its CI does not run `glossa check`, so a run
// is not coming, and waiting thirty minutes for one would leave every
// such pull request silent for half an hour — which is what this
// condition did to every such project when it was first added, and what
// M3's exit test caught. It
// reports the labelled reduced view as soon as the push and the usages
// are in, as the check did before it rendered recorded runs.
//
// "Has ever recorded a run" is learned from the record rather than
// configured, and it has one honest edge: a repository's first pull
// request after adding `glossa check` to its CI is still a project with
// no run on the record. It is not waited for, so it reports the
// labelled reduced view first; when its run lands,
// `quality.check_run.recorded` wakes the check and it is re-rendered
// from the run — which can move the verdict. From its next pull request
// on the project records runs, and the check waits. The reverse can
// happen through Quality's 90-day retention: once every reported run a
// project made has been swept (the newest run of each ref is kept), it
// is no longer waited for.
type checkReadiness struct{ pushed, usages, recorded, expectsRun bool }

func (r checkReadiness) complete() bool {
	return r.uploaded() && (r.recorded || !r.expectsRun)
}

// uploaded reports whether this commit's CI uploaded anything at all.
// A commit with a push and usages but no recorded run has CI; it just
// does not run `glossa check` yet, and there is a report to give it.
func (r checkReadiness) uploaded() bool { return r.pushed && r.usages }

// checkInput reads the report's sources and works out whether this
// commit's CI has run.
//
// Readiness is derived, never remembered from an event: the branch's
// head commit says the push landed, and a current build on this commit
// says the usages did. An event that arrives twice, out of order or not
// at all therefore changes nothing — it only wakes the job.
func (s *GitHubService) checkInput(ctx context.Context, c *domain.Check, conn domain.GitConnection) (
	in CheckInput, ready checkReadiness, err error,
) {
	if in.Policy, err = s.sources.Policy(ctx, conn.ProjectID); err != nil {
		return in, ready, err
	}
	// The policy's grace is measured from the moment it was saved, so
	// the report needs both times and reads neither itself: when this
	// pull request was opened (GitHub's own `created_at`, kept on the
	// row by migration 0033) and when the check runs. A pull request
	// older than a stricter policy keeps grading against the version it
	// was opened under until the grace ends, and the summary says which
	// version it used and when that ends (RFC 0005 §4.3).
	//
	// A row written before 0033 carries the zero time, and so does a
	// payload that named no `created_at`. That is "nobody recorded
	// when", and the project's current version grades it — the behaviour
	// every check had before this column existed.
	in.OpenedAt, in.Now = c.OpenedAt, s.now()
	// The run CI recorded for *this commit*, not for the branch. A
	// branch moves under a pull request with every push; the run that
	// graded a commit belongs to that commit and always will, which is
	// what makes "the same commit, two surfaces, one verdict" a
	// statement anyone can check (RFC 0005 §12.3).
	if run, ok, err := s.sources.RecordedRun(ctx, conn.ProjectID, c.HeadSHA); err != nil {
		return in, ready, err
	} else if ok {
		in.Recorded, ready.recorded = &run, true
	}
	if in.Status, err = s.sources.BranchStatus(ctx, conn.ProjectID, c.Branch); err != nil {
		return in, ready, err
	}
	keys := append(append([]string{}, in.Status.NewKeys...), in.Status.SourceProposals...)
	if in.Quality, err = s.sources.BranchQuality(ctx, conn.ProjectID, c.Branch, keys); err != nil {
		return in, ready, err
	}
	if in.Usages, err = s.sources.BranchUsages(ctx, conn.ProjectID, c.Branch); err != nil {
		return in, ready, err
	}
	ready.pushed = sameCommit(in.Status.HeadCommit, c.HeadSHA)
	for _, commit := range in.Usages.Commits {
		if sameCommit(commit, c.HeadSHA) {
			ready.usages = true
			break
		}
	}
	// Whether to wait for a run is only a question once there is
	// something to report and no run to render, so the project's history
	// is read then and never otherwise: a check waiting on its push, or
	// one whose run is already in, costs nothing more.
	if ready.uploaded() && !ready.recorded {
		if ready.expectsRun, err = s.sources.RecordsRuns(ctx, conn.ProjectID); err != nil {
			return in, ready, err
		}
	}
	return in, ready, nil
}

// sameCommit compares a stored commit with a head SHA. A push may name
// an abbreviated SHA, so a prefix of at least seven hex digits counts —
// the same rule Git itself uses.
func sameCommit(stored, head string) bool {
	if len(stored) < 7 || head == "" {
		return false
	}
	return strings.HasPrefix(head, stored) || strings.HasPrefix(stored, head)
}

// links are the places the sticky comment points at for one connection:
// the branch in Studio, the product's own preview, and the branch
// environment's manifest at the edge.
//
// A manifest nobody has published is a missing link, never a failed
// check, so the lookup's error is logged and dropped.
func (s *GitHubService) links(ctx context.Context, c *domain.Check, conn domain.GitConnection, in CheckInput) CommentLinks {
	l := CommentLinks{
		Studio:  StudioBranchURL(s.studioURL, c.TenantID, conn.ProjectID, c.Branch),
		Preview: in.Status.PreviewURL,
	}
	manifest, err := s.sources.ManifestURL(ctx, conn.ProjectID, c.Branch, c.PullRequest)
	if err != nil {
		s.logger.DebugContext(ctx, "integration: no branch manifest for the pull request comment",
			slog.String("branch", c.Branch), slog.Any("error", err))
		return l
	}
	l.Manifest = manifest
	return l
}
