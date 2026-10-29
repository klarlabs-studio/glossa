package cli

import (
	"context"
	"fmt"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/release"
)

// A publish or promote into an environment that requires approvals is
// held (RFC 0006 §5.1): the server answers 202 with a release request
// and moves no pointer. The CLI says so, names the next step, and exits
// ExitHeld, so a CI step never reads "held" as "deployed".

func (inv *invocation) releaseHeldPublish(ctx context.Context, rc *releaseClient, key string, pub release.Published) error {
	h := pub.Held
	rel, err := rc.resolve(ctx, inv, h.ReleaseID)
	if err != nil {
		return err
	}
	serving, err := inv.servingRef(ctx, rc, h.Request.Environment)
	if err != nil {
		return err
	}
	req := h.Request
	out := releasePublishJSON{Schema: "glossa.cli.release.publish/v1", Replayed: pub.Replayed, IdempotencyKey: key,
		Release: rel, Held: true, ReleaseRequest: &req}
	if err := inv.emit(out, func(pr *printer) {
		if pub.Replayed {
			pr.line("%s v%d was already requested for %s with this idempotency key (request %s); nothing new was published.",
				pr.caution(), rel.Version, req.Environment, h.RequestID)
		} else {
			pr.line("%s Held for approval: v%d was recorded but not deployed to %s %s",
				pr.caution(), rel.Version, req.Environment, pr.dim("("+rel.ID+")"))
		}
		printHeld(pr, req, serving)
	}); err != nil {
		return err
	}
	return silentExit(ExitHeld, "release_held")
}

func (inv *invocation) releaseHeldPromote(ctx context.Context, rc *releaseClient, before release.Environment, h *release.Held) error {
	env, err := rc.environmentJSON(ctx, inv, before)
	if err != nil {
		return err
	}
	target, err := rc.ref(ctx, inv, h.ReleaseID)
	if err != nil {
		return err
	}
	req := h.Request
	out := releaseMoveJSON{Schema: "glossa.cli.release.promote/v1", Environment: env, Previous: env.Release,
		Held: true, Release: target, ReleaseRequest: &req}
	if err := inv.emit(out, func(pr *printer) {
		pr.line("%s Held for approval: %s does not serve v%d yet %s", pr.caution(), env.Name, target.Version, pr.dim("("+target.ID+")"))
		printHeld(pr, req, env.Release)
	}); err != nil {
		return err
	}
	return silentExit(ExitHeld, "release_held")
}

// servingRef is what an environment serves now (nil: nothing).
func (inv *invocation) servingRef(ctx context.Context, rc *releaseClient, environment string) (*releaseRef, error) {
	e, err := rc.svc.Environment(ctx, rc.scope, environment)
	if err != nil {
		return nil, inv.releaseError(err, "can't read environment "+environment)
	}
	return rc.ref(ctx, inv, e.CurrentReleaseID)
}

// printHeld says what a held request waits for and what to do next.
func printHeld(pr *printer, req release.Request, serving *releaseRef) {
	keeps := "nothing yet"
	if serving != nil {
		keeps = fmt.Sprintf("v%d", serving.Version)
	}
	pr.line("  release request %s: %s keeps serving %s until %s",
		pr.bold(req.ID), req.Environment, keeps, requirementText(req.Approval))
	if req.Forced {
		pr.line("  forced past the completeness requirement (%s); the approvers see that and why", req.ForceReason)
	} else if !req.Gate.Met && len(req.Gate.Unmet) > 0 {
		pr.line("  the completeness requirement is not met: %s", strings.Join(req.Gate.Unmet, "; "))
	}
	pr.line("  next: someone else runs `glossa approve %s`, signed in as a person (`glossa login --device`), or approves it in Studio", req.ID)
	pr.line("  %s", pr.dim("nothing was deployed; `glossa release requests show "+req.ID+"` follows it"))
}

// requirementText reads an approval requirement as a sentence's end:
// "2 people of role reviewer, none of them the requester, approve".
func requirementText(a release.Requirement) string {
	who := "person"
	if a.N != 1 {
		who = "people"
	}
	return fmt.Sprintf("%d %s of %s, none of them the requester, approve", a.N, who, partyText(a.From))
}

func partyText(p release.Party) string {
	switch {
	case p.Role != "":
		return "role " + p.Role
	case p.Group != "":
		return "group " + p.Group
	case p.Member != "":
		return "member " + p.Member
	}
	return "the environment's approvers"
}
