package app

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// The events that wake the Glossa PR check (RFC 0004 §6.4). Their
// contracts are Integration's own payload types, never the publishing
// context's Go types, and the subscriber name is stored with each
// event: never rename it.
const (
	catalogBranchPushed  = "catalog.branch.pushed"
	contextBuildIngested = "context.build.ingested"
	localizationRevised  = "localization.translation.revised"
	localizationReviewed = "localization.translation.reviewed"
	// qualityCheckRunRecorded is a `glossa check` run landing on the
	// record. It is the event the pull request is actually waiting for
	// now: the check renders the run CI recorded (RFC 0005 §12.3), and
	// no branch push, usages upload or translation announces one.
	qualityCheckRunRecorded = "quality.check_run.recorded"
	// subscriberRequestCheck doubles as the worker's background
	// principal, as Integration's other subscriber does.
	subscriberRequestCheck = principalCheck
)

// branchEvent is the part of catalog.branch.pushed the check needs.
type branchEvent struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
}

// buildEvent is the part of context.build.ingested the check needs.
type buildEvent struct {
	ProjectID string `json:"project_id"`
	Branch    string `json:"branch"`
}

// translationEvent is the part of localization.translation.revised the
// check needs. It names no branch, so every open check of the project
// is woken: a translation that landed on a branch's proposed message
// changes that branch's report, and one that landed on the default
// branch changes nothing the next render will not notice.
type translationEvent struct {
	ProjectID string `json:"project_id"`
}

// checkRunEvent is the part of quality.check_run.recorded the check
// needs: whose project, and which ref was graded. The findings are not
// on it and must not be — the check reads the run when it renders, so a
// duplicated or reordered delivery costs a render and never a wrong
// verdict.
type checkRunEvent struct {
	ProjectID string `json:"project_id"`
	Ref       string `json:"ref"`
}

// SubscribeChecks registers the events that make a pull request's check
// due again. Waking is all they do: what the check reports is read from
// the catalog when it runs, never carried on an event, so a duplicate,
// a reordering or a lost delivery costs nothing but a render.
func (s *GitHubService) SubscribeChecks(r *outbox.Registry) error {
	if !s.checksEnabled() {
		// No check queue: nothing would act on these events, and a
		// subscriber that does nothing would still mark them delivered.
		return nil
	}
	for typ, h := range map[string]outbox.HandlerFunc{
		catalogBranchPushed:     s.handleBranchPushed,
		contextBuildIngested:    s.handleBuildIngested,
		localizationRevised:     s.handleTranslationChanged,
		localizationReviewed:    s.handleTranslationChanged,
		qualityCheckRunRecorded: s.handleCheckRunRecorded,
	} {
		if err := r.Subscribe(typ, subscriberRequestCheck, h); err != nil {
			return err
		}
	}
	return nil
}

func (s *GitHubService) handleBranchPushed(ctx context.Context, d outbox.Delivery) error {
	var e branchEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	return s.wakeChecks(ctx, e.ProjectID, e.Name)
}

func (s *GitHubService) handleBuildIngested(ctx context.Context, d outbox.Delivery) error {
	var e buildEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	if e.Branch == "" {
		// A default-branch build: no pull request is waiting on it.
		return nil
	}
	return s.wakeChecks(ctx, e.ProjectID, e.Branch)
}

// handleCheckRunRecorded wakes the checks of the ref a run graded. A
// run of an environment names a ref no branch has, and wakes nothing —
// which is the right answer, not a special case.
func (s *GitHubService) handleCheckRunRecorded(ctx context.Context, d outbox.Delivery) error {
	var e checkRunEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	return s.wakeChecks(ctx, e.ProjectID, e.Ref)
}

func (s *GitHubService) handleTranslationChanged(ctx context.Context, d outbox.Delivery) error {
	var e translationEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	return s.wakeChecks(ctx, e.ProjectID, "")
}

// wakeChecks makes the project's checks due again. The repositories are
// resolved in the tenant's own scope, from its Git connections, and the
// queue is written in the system scope: no statement joins across the
// boundary.
func (s *GitHubService) wakeChecks(ctx context.Context, projectID, branch string) error {
	project, err := uuid.Parse(projectID)
	if err != nil {
		return outbox.Permanent(err)
	}
	var repositories []int64
	if err := s.tx.InGitHub(ctx, func(ctx context.Context, st GitHubStore) error {
		conns, err := st.GitConnections(ctx, ConnectionFilter{Project: &project})
		for _, c := range conns {
			if !containsInt64(repositories, c.RepositoryID) {
				repositories = append(repositories, c.RepositoryID)
			}
		}
		return err
	}); err != nil {
		return err
	}
	if len(repositories) == 0 {
		return nil
	}
	n, err := s.checks.Wake(withoutTenant(ctx), repositories, branch, s.now())
	if err != nil || n == 0 {
		return err
	}
	s.logger.InfoContext(ctx, "integration: a change woke a pull request's Glossa check",
		slog.String("project_id", project.String()), slog.String("branch", branch), slog.Int("checks", n))
	return nil
}

func containsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
