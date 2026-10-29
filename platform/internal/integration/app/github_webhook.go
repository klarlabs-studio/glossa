package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// ReceiveWebhook verifies a delivery and stores it (RFC 0004 §6.2).
//
// The order is the point: the signature is checked with HMAC-SHA256
// over the raw body, in constant time, before anything parses it, and
// the body is capped at 5 MB while it is read. Only then are the
// headers read and the row written. The endpoint does no work beyond
// that, so it answers well inside GitHub's ten seconds; a worker
// processes the inbox.
//
// accepted is false for a delivery ID we have seen inside the replay
// window: a duplicate is a no-op, and the caller still answers 202.
func (s *GitHubService) ReceiveWebhook(ctx context.Context, h http.Header, body io.Reader) (accepted bool, err error) {
	d, err := s.verifier.Verify(h, body)
	if err != nil {
		s.metrics.DeliveryReceived(eventLabel(h.Get("X-GitHub-Event")), verifyOutcome(err))
		return false, err
	}
	// Parsing happens after verification, and only to learn the action
	// and the installation: an unhandled event is stored as ignored
	// rather than dropped, so the log shows what GitHub sent.
	ev, perr := s.events.Parse(d)
	row := domain.Delivery{
		ID: d.ID, Event: d.Event, Action: ev.Action, GitHubInstallationID: ev.InstallationID,
		Payload: d.Body, ReceivedAt: s.now(),
	}
	if perr != nil && !errors.Is(perr, ErrWebhookIgnored) && !errors.Is(perr, ErrWebhookPayload) {
		return false, perr
	}
	stored, err := s.inbox.Store(ctx, row)
	if err != nil {
		return false, err
	}
	outcome := DeliveryAccepted
	if !stored {
		outcome = DeliveryDuplicate
	}
	s.metrics.DeliveryReceived(d.Event, outcome)
	s.logger.InfoContext(ctx, "integration: a GitHub webhook delivery arrived",
		slog.String("delivery_id", d.ID), slog.String("event", d.Event), slog.String("action", ev.Action),
		slog.Int64("installation_id", ev.InstallationID), slog.String("outcome", outcome))
	return stored, nil
}

// eventLabel keeps an unverified header out of a metric's label set.
func eventLabel(event string) string {
	for _, r := range event {
		if (r < 'a' || r > 'z') && r != '_' {
			return "unknown"
		}
	}
	if event == "" || len(event) > 64 {
		return "unknown"
	}
	return event
}

func verifyOutcome(err error) string {
	if errors.Is(err, ErrWebhookTooLarge) {
		return DeliveryOversized
	}
	return DeliveryRejected
}

// ── the inbox worker ─────────────────────────────────────────────────

// InboxConfig tunes the worker that drains the webhook inbox.
type InboxConfig struct {
	// Workers is the number of deliveries this process handles at once.
	Workers int
	// PollInterval is how long an idle worker waits before looking again.
	PollInterval time.Duration
	// Timeout bounds one attempt; Lease (longer) is how long a claimed
	// delivery is reserved before another worker takes it over.
	Timeout time.Duration
	Lease   time.Duration
	// DepthInterval is how often the inbox-depth metric is sampled.
	DepthInterval time.Duration
}

func (c *InboxConfig) defaults() {
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.Lease <= c.Timeout {
		c.Lease = c.Timeout + time.Minute
	}
	if c.DepthInterval <= 0 {
		c.DepthInterval = 30 * time.Second
	}
}

// InboxWorker drains the webhook inbox the way the outbox relay drains
// events: claimed with FOR UPDATE SKIP LOCKED, at least once, with
// idempotent handlers (RFC 0004 §6.2).
//
// It is a queue worker rather than a leased periodic job because a
// delivery must be handled within seconds of arriving and every replica
// should share the work; the lease lives on the row, not on the job.
// The seven-day sweep of delivery IDs is the periodic half, and runs on
// the scheduler.
type InboxWorker struct {
	s   *GitHubService
	cfg InboxConfig
}

// NewInboxWorker returns a worker pool for s.
func NewInboxWorker(s *GitHubService, cfg InboxConfig) *InboxWorker {
	cfg.defaults()
	return &InboxWorker{s: s, cfg: cfg}
}

// Run works until ctx is cancelled; a delivery in progress finishes its
// current attempt first (bounded by Timeout).
func (w *InboxWorker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for range w.cfg.Workers {
		wg.Go(func() { w.loop(ctx) })
	}
	wg.Go(func() { w.depth(ctx) })
	wg.Wait()
	return nil
}

func (w *InboxWorker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			w.s.logger.ErrorContext(ctx, "integration: GitHub inbox worker", slog.Any("error", err))
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

// depth samples the inbox-depth metric (RFC 0004 §11).
func (w *InboxWorker) depth(ctx context.Context) {
	t := time.NewTicker(w.cfg.DepthInterval)
	defer t.Stop()
	for {
		if d, err := w.s.inbox.Depth(ctx); err == nil {
			w.s.metrics.InboxDepth(d)
		} else if ctx.Err() == nil {
			w.s.logger.WarnContext(ctx, "integration: sampling the GitHub inbox depth", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce claims one due delivery and handles it; worked is false when
// none was due. Tests drive the inbox with it.
func (w *InboxWorker) RunOnce(ctx context.Context) (worked bool, err error) {
	c, ok, err := w.s.inbox.Claim(ctx, w.cfg.Lease)
	if err != nil || !ok {
		return false, err
	}
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.Timeout)
	defer cancel()
	return true, w.s.handleDelivery(hctx, c)
}

// Sweep removes delivery IDs and install intents older than the replay
// window. It is the scheduler's half of the inbox: one run per
// deployment, on the periodic job "integration.github.sweep".
func (w *InboxWorker) Sweep(ctx context.Context) error {
	before := w.s.now().Add(-domain.DeliveryReplayWindow)
	deliveries, intents, err := w.s.inbox.Sweep(ctx, before)
	if (deliveries > 0 || intents > 0) && err == nil {
		w.s.logger.InfoContext(ctx, "integration: swept the GitHub inbox",
			slog.Int("deliveries", deliveries), slog.Int("install_intents", intents))
	}
	return err
}

// handleDelivery resolves the delivery's tenant, runs its handler in
// that tenant's scope and settles the row. The payload is dropped when
// it settles, whatever the outcome.
func (s *GitHubService) handleDelivery(ctx context.Context, c ClaimedDelivery) error {
	start := s.now()
	state, tenant, failure, retry := s.processDelivery(ctx, c)
	s.metrics.DeliveryHandled(c.Event, handledOutcome(state, retry), s.now().Sub(start))
	if retry {
		if domain.GaveUp(c.Attempts) {
			s.logger.ErrorContext(ctx, "integration: giving up on a GitHub delivery",
				slog.String("delivery_id", c.ID), slog.String("event", c.Event),
				slog.Int("attempts", c.Attempts), slog.String("failure", failure))
			return s.inbox.Settle(ctx, c.ID, c.Token, domain.DeliveryFailed, tenant, failure, s.now())
		}
		return s.inbox.Retry(ctx, c.ID, c.Token, domain.RetryDelay(c.Attempts), failure)
	}
	return s.inbox.Settle(ctx, c.ID, c.Token, state, tenant, failure, s.now())
}

func handledOutcome(state domain.DeliveryState, retry bool) string {
	if retry {
		return "retry"
	}
	return string(state)
}

// processDelivery decides what became of a delivery. It never returns
// an error: a failure is a state plus a reason, so the caller always
// settles or retries.
func (s *GitHubService) processDelivery(ctx context.Context, c ClaimedDelivery) (
	state domain.DeliveryState, tenant *uuid.UUID, failure string, retry bool,
) {
	ev, err := s.events.Parse(WebhookDelivery{ID: c.ID, Event: c.Event, Body: c.Payload})
	switch {
	case errors.Is(err, ErrWebhookIgnored):
		return domain.DeliveryIgnored, nil, "not an event Glossa acts on", false
	case err != nil:
		// A verified payload we cannot read will not read better later.
		return domain.DeliveryIgnored, nil, "the payload could not be read", false
	}
	if ev.InstallationID <= 0 {
		return domain.DeliveryIgnored, nil, "the payload names no installation", false
	}
	owner, ok, err := s.inbox.Owner(ctx, ev.InstallationID)
	if err != nil {
		return domain.DeliveryPending, nil, shortFailure(err.Error()), true
	}
	if !ok {
		// Nobody has claimed this installation: an install that was
		// never finished in Studio, or one already forgotten. Nothing to
		// do, and nothing to retry.
		return domain.DeliveryIgnored, nil, "no workspace has claimed this installation", false
	}
	id := owner.Tenant.UUID()
	tctx := tenancy.ContextWithTenant(ctx, owner.Tenant)
	bg, err := authz.Background(tctx, principalGitHub,
		authz.IntegrationRead, authz.IntegrationManage, authz.CatalogRead, authz.CatalogWrite)
	if err != nil {
		return domain.DeliveryPending, &id, shortFailure(err.Error()), true
	}
	if err := s.applyEvent(bg, ev); err != nil {
		return domain.DeliveryPending, &id, shortFailure(err.Error()), true
	}
	return domain.DeliveryDone, &id, "", false
}

// applyEvent is the idempotent handler for one event. Every branch can
// run twice without a different result, because delivery is at least
// once.
func (s *GitHubService) applyEvent(ctx context.Context, ev WebhookEvent) error {
	switch ev.Event {
	case "installation":
		return s.applyInstallation(ctx, ev)
	case "installation_repositories":
		return s.applyInstallationRepositories(ctx, ev)
	case "pull_request":
		return s.applyPullRequest(ctx, ev)
	case "check_run":
		return s.applyCheckRun(ctx, ev)
	}
	return nil
}

// applyCheckRun is `check_run.rerequested`: somebody pressed Re-run on
// the Glossa check. GitHub makes a new check run for it, so the stored
// runs and the annotations they were sent are forgotten — a new run's
// ledger starts empty — and the check is enqueued again.
func (s *GitHubService) applyCheckRun(ctx context.Context, ev WebhookEvent) error {
	if ev.Action != "rerequested" || !s.checksEnabled() {
		return nil
	}
	n, err := s.checks.Rerun(withoutTenant(ctx), ev.RepositoryID, ev.HeadSHA, s.now())
	if err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "integration: a Glossa check was rerequested",
		slog.Int64("installation_id", ev.InstallationID), slog.Int64("repository_id", ev.RepositoryID),
		slog.String("head_sha", ev.HeadSHA), slog.Int64("check_run_id", ev.CheckRunID),
		slog.Int("checks", n))
	return nil
}

// applyInstallation records what GitHub says about the installation
// itself: suspended, unsuspended, or uninstalled.
func (s *GitHubService) applyInstallation(ctx context.Context, ev WebhookEvent) error {
	state := domain.InstallationActive
	switch ev.Action {
	case "suspend":
		state = domain.InstallationSuspended
	case "deleted":
		state = domain.InstallationRevoked
	case "created", "unsuspend", "new_permissions_accepted":
		state = domain.InstallationActive
	default:
		return nil
	}
	err := s.tx.InGitHub(ctx, func(ctx context.Context, st GitHubStore) error {
		return st.SetInstallationState(ctx, ev.InstallationID, state, ev.AccountLogin, s.now())
	})
	if errors.Is(err, ErrNotFound) {
		// `installation.created` arrives before the callback claims it;
		// the callback is what maps it to a tenant.
		return nil
	}
	if err == nil {
		s.logger.InfoContext(ctx, "integration: a GitHub installation changed state",
			slog.Int64("installation_id", ev.InstallationID), slog.String("state", string(state)))
	}
	return err
}

// applyInstallationRepositories drops the connections of repositories
// the installation no longer covers: a connection to a repository the
// App cannot see would only fail later, and silently.
func (s *GitHubService) applyInstallationRepositories(ctx context.Context, ev WebhookEvent) error {
	if len(ev.RepositoriesRemoved) == 0 {
		return nil
	}
	var gone []int64
	err := s.tx.InGitHub(ctx, func(ctx context.Context, st GitHubStore) error {
		for _, repo := range ev.RepositoriesRemoved {
			conns, err := st.ConnectionsForRepository(ctx, repo)
			if err != nil {
				return err
			}
			for _, c := range conns {
				if err := st.DeleteGitConnection(ctx, c.ID); err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
				s.logger.InfoContext(ctx, "integration: a repository left the installation, so its connection is gone",
					slog.Int64("repository_id", repo), slog.String("connection_id", c.ID.String()))
			}
			gone = append(gone, repo)
		}
		return nil
	})
	if err != nil || !s.checksEnabled() {
		return err
	}
	// Its pull requests' checks go with the connections: there is
	// nothing left to report on, and the rows would only be claimed and
	// dropped one at a time.
	for _, repo := range gone {
		if _, err := s.checks.DropRepository(withoutTenant(ctx), repo); err != nil {
			return err
		}
	}
	return nil
}

// applyPullRequest keeps the Catalog branch of every project the
// repository feeds in step with the pull request, and opens its check.
//
// It calls Catalog's branch service, which owns the branch lifecycle
// (RFC 0004 §4.1) — this never touches Catalog's tables. And nothing
// here is load-bearing: a merge activates messages through the default
// branch's push, not through this event, so a webhook that never
// arrives costs a stale branch view and nothing more.
//
// A pull request from a fork is the one split case. Its head lives in
// another repository, so its branch is not ours to track and Catalog
// never hears of it — but it still gets a check, which says plainly
// that a fork gets no Glossa CI token (RFC 0004 §6.3). Saying nothing
// at all would leave the pull request with a Glossa check missing
// rather than answered.
func (s *GitHubService) applyPullRequest(ctx context.Context, ev WebhookEvent) error {
	var conns []domain.GitConnection
	if err := s.tx.InGitHub(ctx, func(ctx context.Context, st GitHubStore) error {
		var err error
		conns, err = st.ConnectionsForRepository(ctx, ev.RepositoryID)
		return err
	}); err != nil {
		return err
	}
	if err := s.movePullRequestBranch(ctx, ev, conns); err != nil {
		return err
	}
	return s.openCheck(ctx, ev, len(conns))
}

// movePullRequestBranch tells Catalog where the pull request's branch
// now is. A fork's head is in another repository, so there is no branch
// of ours to move and this does nothing.
func (s *GitHubService) movePullRequestBranch(ctx context.Context, ev WebhookEvent,
	conns []domain.GitConnection,
) error {
	if ev.FromFork {
		return nil
	}
	pr := ev.PullRequest
	for _, c := range conns {
		var err error
		switch ev.Action {
		case "opened", "reopened", "synchronize":
			err = s.branches.UpsertBranch(ctx, c.ProjectID, ev.HeadRef, ev.HeadSHA, &pr)
		case "closed":
			if ev.Merged {
				err = s.branches.MergeBranch(ctx, c.ProjectID, ev.HeadRef)
			} else {
				err = s.branches.CloseBranch(ctx, c.ProjectID, ev.HeadRef)
			}
		default:
			continue
		}
		if err != nil {
			return err
		}
		s.logger.InfoContext(ctx, "integration: a pull request moved a branch",
			slog.String("project_id", c.ProjectID.String()), slog.String("branch", ev.HeadRef),
			slog.Int("pull_request", pr), slog.String("action", ev.Action))
	}
	return nil
}

// openCheck is the check's trigger (RFC 0004 §6.4): a pull request that
// opened, was pushed to or reopened gets a Glossa check run, queued for
// its head SHA, and thirty minutes for its CI to upload.
//
// A fork's pull request gets one too, marked on the row: it has no
// Glossa CI token, so the worker concludes it `neutral` at once rather
// than waiting out a deadline nothing can meet (RFC 0004 §6.3).
//
// The row is written here and the check run is created by the worker,
// so the webhook stays a database write: GitHub's ten seconds are not
// the place for a call back to GitHub.
func (s *GitHubService) openCheck(ctx context.Context, ev WebhookEvent, connections int) error {
	switch {
	case !s.checksEnabled(), connections == 0, ev.HeadSHA == "", ev.HeadRef == "":
		return nil
	}
	switch ev.Action {
	case "opened", "reopened", "synchronize":
	default:
		return nil
	}
	now := s.now()
	// The check queue is the system scope's, because the worker claims
	// across tenants (migration 0021); the tenant travels on the row.
	c, err := s.checks.Open(withoutTenant(ctx), domain.Check{
		ID: uuid.Must(uuid.NewV7()), TenantID: tenantOf(ctx), InstallationID: ev.InstallationID,
		RepositoryID: ev.RepositoryID, PullRequest: ev.PullRequest, Branch: ev.HeadRef, HeadSHA: ev.HeadSHA,
		FromFork: ev.FromFork,
		// GitHub's own `created_at`, never s.now(): the grace exists for
		// a pull request older than the policy, and a clock of ours
		// would call every one of them new (RFC 0005 §4.3). The queue
		// keeps the first value it is given, so a `synchronize` does not
		// make the pull request younger.
		OpenedAt: ev.PullRequestOpenedAt,
		State:    domain.CheckQueued, RequestedAt: now, AvailableAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "integration: a Glossa check was requested",
		slog.Int64("repository_id", c.RepositoryID), slog.Int("pull_request", c.PullRequest),
		slog.String("head_sha", c.HeadSHA), slog.String("branch", c.Branch))
	return nil
}

// shortFailure keeps a failure reason inside the column.
func shortFailure(s string) string {
	if len(s) > 500 {
		return s[:497] + "..."
	}
	return s
}
