package sources

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	release "go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Releases adapts Release's application service to tools.Releases: the
// three operations of RFC 0005 §7.3, behind the `publish` scope.
//
// Every one of them is the same method the REST endpoint calls, with
// the same arguments, so the environment's policy, the publish gate
// that answers `policy_not_met`, the promotability rules, the
// deployment history a rollback walks and the events the context emits
// are inherited whole. This file translates ids and shapes and does
// nothing else.
type Releases struct{ release *releaseapp.Service }

// NewReleases returns the adapter.
func NewReleases(s *releaseapp.Service) *Releases { return &Releases{release: s} }

var _ tools.Releases = (*Releases)(nil)

// Publish implements tools.Releases.
//
// It passes no force flag because the port has none: a publish the
// environment's policy refuses comes back as the context's own error,
// and overriding it takes a person and a recorded reason.
func (a *Releases) Publish(
	ctx context.Context, project uuid.UUID, in tools.PublishRequest,
) (tools.Published, error) {
	rel, replayed, err := a.release.Publish(ctx, project,
		releaseapp.PublishInput{Environment: in.Environment, Note: in.Note}, in.IdempotencyKey)
	if req, ok := held(err); ok {
		// Recorded, not deployed (RFC 0006 §5.1): an agent told
		// "published" would believe production changed.
		return tools.Published{Release: releaseOf(rel), Replayed: replayed, Held: heldOf(req)}, nil
	}
	if err != nil {
		return tools.Published{}, notFound(err, releaseNotFound...)
	}
	return tools.Published{Release: releaseOf(rel), Replayed: replayed}, nil
}

// Promote implements tools.Releases.
func (a *Releases) Promote(
	ctx context.Context, project uuid.UUID, environment string, id uuid.UUID,
) (tools.Deployed, error) {
	before := a.serving(ctx, project, environment)
	// The zero PromoteInput is "not forced". An MCP tool never offers to
	// force the destination's check policy — no `force` argument exists
	// in the schema or in the tools.Releases port — so a promotion an
	// agent asks for is refused exactly as the policy says, and someone
	// who means to override it does so where the reason can be attached
	// to a person.
	env, err := a.release.Promote(ctx, project, environment, id, releaseapp.PromoteInput{})
	if req, ok := held(err); ok {
		rel, err := a.release.GetRelease(ctx, project, req.ReleaseID)
		if err != nil {
			return tools.Deployed{}, notFound(err, releaseNotFound...)
		}
		return tools.Deployed{Environment: req.Environment, Release: releaseOf(rel), Held: heldOf(req)}, nil
	}
	if err != nil {
		return tools.Deployed{}, notFound(err, releaseNotFound...)
	}
	return a.deployed(ctx, project, env, before)
}

// held reports whether err is a publish or promote that became a
// release request, and returns the request.
func held(err error) (release.ReleaseRequest, bool) {
	var h *release.HeldError
	if errors.As(err, &h) {
		return h.Request, true
	}
	return release.ReleaseRequest{}, false
}

func heldOf(r release.ReleaseRequest) *tools.Held {
	return &tools.Held{RequestID: r.ID.String(), Environment: r.Environment, Approvals: r.Approval.N}
}

// Rollback implements tools.Releases. uuid.Nil is "the release before
// the one served now", which is what the context's own nil pointer
// means.
func (a *Releases) Rollback(
	ctx context.Context, project uuid.UUID, environment string, id uuid.UUID,
) (tools.Deployed, error) {
	var want *uuid.UUID
	if id != uuid.Nil {
		want = &id
	}
	before := a.serving(ctx, project, environment)
	env, err := a.release.Rollback(ctx, project, environment, want)
	if err != nil {
		return tools.Deployed{}, notFound(err, releaseNotFound...)
	}
	return a.deployed(ctx, project, env, before)
}

// serving is the release the environment served before the call, so the
// answer can say whether the pointer actually moved. An environment
// that cannot be read yields uuid.Nil and the move is reported as one:
// the pointer operation itself will fail or succeed on its own terms,
// and this read must never be the reason a publish-scoped call is
// refused.
func (a *Releases) serving(ctx context.Context, project uuid.UUID, environment string) uuid.UUID {
	env, err := a.release.GetEnvironment(ctx, project, environment)
	if err != nil {
		return uuid.Nil
	}
	return env.Current
}

// deployed reads back the release the environment now serves. The
// pointer methods answer with the environment, and what an agent needs
// to know is which release that is — including for a rollback, where
// the context chose the release and the caller did not.
//
// Moved compares the pointer with what it was: promoting the release an
// environment already serves changes nothing and is not an error, and
// an agent told "promoted" about a no-op would believe it had changed
// production.
func (a *Releases) deployed(
	ctx context.Context, project uuid.UUID, env release.Environment, before uuid.UUID,
) (tools.Deployed, error) {
	if env.Current == uuid.Nil {
		return tools.Deployed{}, tools.ErrNotFound
	}
	rel, err := a.release.GetRelease(ctx, project, env.Current)
	if err != nil {
		return tools.Deployed{}, notFound(err, releaseNotFound...)
	}
	return tools.Deployed{Environment: env.Name, Release: releaseOf(rel), Moved: env.Current != before}, nil
}

// releaseOf renders a release for a tool's payload.
func releaseOf(rel release.Release) tools.Release {
	out := tools.Release{
		ID: rel.ID.String(), Version: rel.Version, Environment: rel.Environment,
		Digest: rel.Digest, Policy: rel.Policy.States, Branch: rel.Branch,
		Messages: rel.Stats.Messages, Note: rel.Note, Author: rel.Author,
		CreatedAt: rel.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, l := range rel.Content.Locales {
		out.Locales = append(out.Locales, l.Code)
	}
	return out
}

// ReleaseReads adapts Release's request and rollout lists to
// tools.ReleaseReads (RFC 0006 §8): the services the API lists with,
// so releases.read, project scope and the environment's existence are
// the context's.
type ReleaseReads struct{ release *releaseapp.Service }

// NewReleaseReads returns the adapter.
func NewReleaseReads(s *releaseapp.Service) *ReleaseReads { return &ReleaseReads{release: s} }

var _ tools.ReleaseReads = (*ReleaseReads)(nil)

// ReleaseRequests implements tools.ReleaseReads.
func (a *ReleaseReads) ReleaseRequests(
	ctx context.Context, project uuid.UUID, environment, state, cursor string, limit int,
) ([]tools.ReleaseRequest, string, error) {
	pg, err := page(cursor, limit)
	if err != nil {
		return nil, "", err
	}
	rows, nextToken, err := a.release.ListReleaseRequests(ctx, project, environment, release.RequestState(state), pg)
	if err != nil {
		return nil, "", notFound(err, releaseNotFound...)
	}
	out := make([]tools.ReleaseRequest, len(rows))
	for i, r := range rows {
		out[i] = tools.ReleaseRequest{
			ID: r.ID.String(), Environment: r.Environment, ReleaseID: r.ReleaseID.String(), Action: string(r.Action),
			Requester: r.Requester, ApprovalsRequired: r.Approval.N, State: string(r.State), Forced: r.Override.Forced,
			GateMet: r.Verdict.Met, GateUnmet: r.Verdict.Unmet, CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
			DecidedBy: r.DecidedBy,
		}
		if r.Override.Forced {
			out[i].ForceReason = r.Override.Reason
		}
		if r.DecidedAt != nil {
			out[i].DecidedAt = r.DecidedAt.UTC().Format(time.RFC3339)
		}
	}
	return out, next(nextToken), nil
}

// Rollouts implements tools.ReleaseReads.
func (a *ReleaseReads) Rollouts(
	ctx context.Context, project uuid.UUID, environment, cursor string, limit int,
) ([]tools.Rollout, string, error) {
	pg, err := page(cursor, limit)
	if err != nil {
		return nil, "", err
	}
	rows, nextToken, err := a.release.ListRollouts(ctx, project, environment, pg)
	if err != nil {
		return nil, "", notFound(err, releaseNotFound...)
	}
	out := make([]tools.Rollout, len(rows))
	for i, r := range rows {
		out[i] = tools.Rollout{
			ID: r.ID.String(), Environment: r.Environment, Candidate: r.Candidate.String(), Stable: r.Stable.String(),
			Percent: r.Percent, Status: string(r.Status), StartedBy: r.StartedBy,
			StartedAt: r.StartedAt.UTC().Format(time.RFC3339), ExpiresAt: r.ExpiresAt.UTC().Format(time.RFC3339),
		}
		if !r.Active() {
			out[i].End, out[i].EndedAt = string(r.End), r.EndedAt.UTC().Format(time.RFC3339)
		}
	}
	return out, next(nextToken), nil
}
