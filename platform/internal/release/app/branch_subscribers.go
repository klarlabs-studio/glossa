package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// The branch environment lifecycle over the outbox (RFC 0004 §4.2).
// Every handler is idempotent: opening an environment twice returns the
// first one, a burst of publish requests is one debounced publish, and
// destroying a branch without an environment does nothing.

// branchEventPayload is Catalog's branch event, as Release reads it.
type branchEventPayload struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	PR        *int   `json:"pr_number"`
}

// translationRevisedPayload is Localization's, likewise.
type translationRevisedPayload struct {
	ProjectID string `json:"project_id"`
	MessageID string `json:"message_id"`
}

// branchBackground acts as the branch-environment principal: it reads
// the catalog and translations the way a publish does, and publishes
// releases, nothing else.
func branchBackground(ctx context.Context) (context.Context, error) {
	bg, err := authz.Background(ctx, subscriberBranchEnvironments,
		authz.ReleasesRead, authz.ReleasesPublish, authz.CatalogRead, authz.TranslationsRead)
	if err != nil {
		return nil, outbox.Permanent(err)
	}
	return bg, nil
}

// handleBranchLive opens the branch's environment (idempotent) and asks
// for it to be published, debounced. A project deleted since, or a
// catalog the build can't release, is not an error to retry: the
// branch's next push asks again.
func (s *Service) handleBranchLive(ctx context.Context, d outbox.Delivery) error {
	project, branch, pr, err := branchOf(d)
	if err != nil {
		return err
	}
	bg, err := branchBackground(ctx)
	if err != nil {
		return err
	}
	if _, _, err := s.OpenBranchEnvironment(bg, project, branch, pr); err != nil {
		return branchLifecycleError(err)
	}
	_, err = s.RequestBranchPublish(bg, project, branch)
	return branchLifecycleError(err)
}

// handleBranchGone destroys the environment of a closed or merged
// branch, so the edge answers 404 for it. Its releases stay as history.
func (s *Service) handleBranchGone(ctx context.Context, d outbox.Delivery) error {
	project, branch, _, err := branchOf(d)
	if err != nil {
		return err
	}
	bg, err := branchBackground(ctx)
	if err != nil {
		return err
	}
	return branchLifecycleError(s.DestroyBranchEnvironment(bg, project, branch))
}

// handleTranslationRevised publishes the branch previews that show the
// revised message again: the open branches proposing it. A translation
// of a message no branch proposes changes no preview.
func (s *Service) handleTranslationRevised(ctx context.Context, d outbox.Delivery) error {
	var e translationRevisedPayload
	if err := d.Decode(&e); err != nil {
		return err
	}
	project, err := parseID("project_id", e.ProjectID)
	if err != nil {
		return err
	}
	message, err := parseID("message_id", e.MessageID)
	if err != nil {
		return err
	}
	bg, err := branchBackground(ctx)
	if err != nil {
		return err
	}
	branches, err := s.source.BranchesProposing(bg, project, message)
	if err != nil {
		return branchLifecycleError(err)
	}
	for _, branch := range branches {
		if _, err := s.RequestBranchPublish(bg, project, branch); err != nil {
			return branchLifecycleError(err)
		}
	}
	return nil
}

func branchOf(d outbox.Delivery) (project uuid.UUID, branch string, pr int, err error) {
	var e branchEventPayload
	if err := d.Decode(&e); err != nil {
		return uuid.Nil, "", 0, err
	}
	project, err = parseID("project_id", e.ProjectID)
	if err != nil {
		return uuid.Nil, "", 0, err
	}
	if e.Name == "" {
		return uuid.Nil, "", 0, outbox.Permanent(fmt.Errorf("release: event %s has no branch name", d.Type))
	}
	if e.PR != nil {
		pr = *e.PR
	}
	return project, e.Name, pr, nil
}

// branchLifecycleError drops what retrying can't fix: a project or
// environment gone since the event, and a project that has more branch
// environments than it may (the branch keeps its proposals; its
// environment opens once another closes).
func branchLifecycleError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, domain.ErrTooManyBranches), errors.Is(err, ErrEnvironmentExists):
		return nil
	}
	return err
}
