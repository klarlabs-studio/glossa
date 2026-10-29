package tools

import (
	"context"
	"encoding/json"
	"fmt"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// The release tools of RFC 0005 §7.3, all three behind the `publish`
// scope and the publish toolset.
//
// They are the thinnest tools in this package on purpose. A release is
// the one thing in Glossa that reaches production, and every rule about
// it — which review states an environment's policy admits, whether a
// branch release may be promoted, the completeness gate that answers
// `policy_not_met`, the immutability of a published release, the
// deployment history a rollback walks — belongs to the Release context.
// None of it is restated here, and a tool that policed a release itself
// would be the second implementation RFC 0005 §7.1 forbids.
//
// What is *not* here is as deliberate: no environment creation, no
// policy edit, no delivery keys, no release deletion, and no `force`.
// Forcing past the publish gate takes an audited reason and is a
// person's decision, for the same reason a translation an agent writes
// always enters review.
const (
	// ReleasePublishName is the publish tool's wire name.
	ReleasePublishName = "release_publish"
	// ReleasePromoteName is the promote tool's wire name.
	ReleasePromoteName = "release_promote"
	// ReleaseRollbackName is the rollback tool's wire name.
	ReleaseRollbackName = "release_rollback"
)

// MaxNoteRunes bounds a release note. It is the Release context's own
// limit (release/domain.MaxNoteLen); the tool states it in its schema
// so a client is told before the call rather than after.
const MaxNoteRunes = 1000

// Publish returns the release tools of RFC 0005 §7.3, in the order a
// client sees them. Every one of them declares domain.ToolsetPublish
// and identity.PermReleasesPublish — the two locks, one each — and
// enforces neither itself: the toolset gate is app.Service's and the
// permission is checked by the same authz.Require the REST endpoint
// runs, and again by the Release context underneath.
//
// A tool whose port is nil is left out, like everywhere else here.
func Publish(s Sources) []app.Tool {
	if s.Releases == nil {
		return nil
	}
	return []app.Tool{releasePublish(s.Releases), releasePromote(s.Releases), releaseRollback(s.Releases)}
}

// ── publish ─────────────────────────────────────────────────────────

type releasePublishArgs struct {
	Project        string `json:"project"`
	Environment    string `json:"environment"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

// releasePublish builds a release and points the environment at it.
func releasePublish(r Releases) app.Tool {
	return app.Tool{
		Name:  ReleasePublishName,
		Title: "Publish a release",
		Description: "Build an immutable release of the project's catalog under an environment's " +
			"policy and point that environment at it. The environment decides which review states " +
			"ship and whether the catalog is complete enough to publish; a project that does not " +
			"meet that requirement is refused with policy_not_met, and overriding it takes a " +
			"person and a recorded reason — there is no way to force it from here. Run check_run " +
			"first to see what would fail. In an environment that requires release approval the " +
			"release is recorded but not deployed: the answer carries `held` with the release " +
			"request, and only people can approve it. Pass idempotency_key and repeat it if the call times " +
			"out, so a retry returns the release the first attempt made instead of publishing a " +
			"second one. Needs a publish session on a token carrying the publish scope.",
		Toolset:     domain.ToolsetPublish,
		Permission:  identity.PermReleasesPublish,
		Selectors:   selectors,
		InputSchema: json.RawMessage(releasePublishSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a releasePublishArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("environment", a.Environment); err != nil {
				return app.Result{}, err
			}
			if err := boundedRunes("note", a.Note, MaxNoteRunes); err != nil {
				return app.Result{}, err
			}
			out, err := r.Publish(ctx, project, PublishRequest{
				Environment: a.Environment, Note: a.Note, IdempotencyKey: a.IdempotencyKey,
			})
			if err != nil {
				return app.Result{}, err
			}
			if out.Held != nil {
				return app.Result{Explanation: heldExplanation(out.Held, out.Release, "published"), Data: out,
					Affected: []string{out.Release.ID, out.Held.RequestID}}, nil
			}
			what := "was published to"
			if out.Replayed {
				what = "had already been published to this key and is served by"
			}
			return app.Result{
				Explanation: fmt.Sprintf("Release %d (%s) %s %s, shipping %s in %s.",
					out.Release.Version, shortDigest(out.Release.Digest), what, a.Environment,
					plural(out.Release.Messages, "message", "messages"),
					plural(len(out.Release.Locales), "locale", "locales")),
				Data:     out,
				Affected: []string{out.Release.ID},
			}, nil
		},
	}
}

const releasePublishSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "environment": {"type": "string", "maxLength": 63, "description": "The environment to publish to, e.g. \"staging\"."},
    "note": {"type": "string", "maxLength": 1000, "description": "Why this release was published, kept with it forever."},
    "idempotency_key": {"type": "string", "maxLength": 200, "description": "Repeat this on a retry to get the first attempt's release back instead of a second one."}
  },
  "required": ["project", "environment"],
  "additionalProperties": false
}`

// ── promote ─────────────────────────────────────────────────────────

type releasePromoteArgs struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Release     string `json:"release"`
}

// releasePromote points an environment at an existing release.
func releasePromote(r Releases) app.Tool {
	return app.Tool{
		Name:  ReleasePromoteName,
		Title: "Promote a release",
		Description: "Point an environment at a release that already exists — staging's release " +
			"into production. Nothing is rebuilt and no text changes: only the environment's " +
			"manifest is written, so what production serves is exactly what was tested. The " +
			"target environment's policy must cover the one the release was built under, and a " +
			"branch release is never promotable. Promoting the release an environment already " +
			"serves changes nothing and is not an error. In an environment that requires release " +
			"approval nothing moves: the answer carries `held` with the release request, and only " +
			"people can approve it. Read explain_delivery first to see what " +
			"the environment serves now. Needs a publish session on a token carrying the publish " +
			"scope.",
		Toolset:     domain.ToolsetPublish,
		Permission:  identity.PermReleasesPublish,
		Selectors:   selectors,
		InputSchema: json.RawMessage(releasePromoteSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a releasePromoteArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("environment", a.Environment); err != nil {
				return app.Result{}, err
			}
			if err := required("release", a.Release); err != nil {
				return app.Result{}, err
			}
			id, err := optionalID("release", a.Release)
			if err != nil {
				return app.Result{}, err
			}
			out, err := r.Promote(ctx, project, a.Environment, id)
			if err != nil {
				return app.Result{}, err
			}
			if out.Held != nil {
				return app.Result{Explanation: heldExplanation(out.Held, out.Release, "promoted"), Data: out,
					Affected: []string{out.Release.ID, out.Held.RequestID}}, nil
			}
			return app.Result{Explanation: moved(out, "promoted to"), Data: out, Affected: []string{out.Release.ID}}, nil
		},
	}
}

const releasePromoteSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "environment": {"type": "string", "maxLength": 63, "description": "The environment to point at the release, e.g. \"production\"."},
    "release": {"type": "string", "format": "uuid", "description": "The release to serve; it must already exist in this project."}
  },
  "required": ["project", "environment", "release"],
  "additionalProperties": false
}`

// ── rollback ────────────────────────────────────────────────────────

type releaseRollbackArgs struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Release     string `json:"release"`
}

// releaseRollback points an environment back at a release it served
// before. Nothing is deleted: the release it was serving stays exactly
// where it is, and rolling forward again is another promote.
func releaseRollback(r Releases) app.Tool {
	return app.Tool{
		Name:  ReleaseRollbackName,
		Title: "Roll a release back",
		Description: "Point an environment back at a release it served before: the one named, or " +
			"by default the newest release older than the one it serves now, so rolling back twice " +
			"walks two steps back. Only the pointer moves — no release is changed or removed, and " +
			"rolling forward again is a promote. An environment that never served the release " +
			"named, or that has nothing earlier, is refused. Needs a publish session on a token " +
			"carrying the publish scope.",
		Toolset:     domain.ToolsetPublish,
		Permission:  identity.PermReleasesPublish,
		Selectors:   selectors,
		InputSchema: json.RawMessage(releaseRollbackSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a releaseRollbackArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("environment", a.Environment); err != nil {
				return app.Result{}, err
			}
			id, err := optionalID("release", a.Release)
			if err != nil {
				return app.Result{}, err
			}
			out, err := r.Rollback(ctx, project, a.Environment, id)
			if err != nil {
				return app.Result{}, err
			}
			return app.Result{Explanation: moved(out, "rolled back to"), Data: out, Affected: []string{out.Release.ID}}, nil
		},
	}
}

const releaseRollbackSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "environment": {"type": "string", "maxLength": 63, "description": "The environment to roll back, e.g. \"production\"."},
    "release": {"type": "string", "format": "uuid", "description": "Roll back to this release, which the environment must have served before; the previous one when absent."}
  },
  "required": ["project", "environment"],
  "additionalProperties": false
}`

// ── shared ──────────────────────────────────────────────────────────

// moved is the one-line explanation of a pointer move, and it says
// plainly when nothing happened: an agent told "promoted" about a
// no-op would believe it had changed something.
func moved(d Deployed, what string) string {
	if !d.Moved {
		return fmt.Sprintf("%s already served release %d (%s); nothing moved.",
			d.Environment, d.Release.Version, shortDigest(d.Release.Digest))
	}
	return fmt.Sprintf("%s was %s release %d (%s), built for %s.",
		d.Environment, what, d.Release.Version, shortDigest(d.Release.Digest), d.Release.Environment)
}

// heldExplanation says plainly that nothing was deployed: the move waits
// for people to approve it (RFC 0006 §5.1), and no tool can approve.
func heldExplanation(h *Held, r Release, what string) string {
	return fmt.Sprintf("Release %d (%s) was NOT deployed: %s requires release approval, so it was %s as release "+
		"request %s and %s still serves what it served before. It deploys once %s other than the requester "+
		"approve it; approving is a person's decision and no tool can make it.",
		r.Version, shortDigest(r.Digest), h.Environment, what, h.RequestID, h.Environment,
		plural(h.Approvals, "person", "people"))
}

// shortDigest renders a manifest digest for a sentence. The full digest
// is in the payload; a sentence wants something a person can compare at
// a glance.
func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "no digest"
	}
	return d
}

// boundedRunes rejects an optional text argument longer than max runes.
// A note is optional, so an empty one is fine; what is not fine is
// discovering the limit from the context after a build ran.
func boundedRunes(name, s string, maximum int) error {
	if len([]rune(s)) > maximum {
		return invalid(name, fmt.Sprintf("must be at most %d characters", maximum))
	}
	return nil
}
