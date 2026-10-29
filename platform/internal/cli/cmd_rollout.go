package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/release"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// glossa release rollout (RFC 0006 §5.2): serve a candidate release to
// a share of installations, chosen by the runtimes from the manifest
// (runtimes/SPEC.md §1.4), then advance, complete or abort it.
//
// A change is conditional on what was read: advance and complete send
// the rollout's ETag as If-Match — the one --if-match names, else the
// one read just before — so two people changing one rollout never
// overwrite each other (412). Abort sends one only with --if-match: it
// must never be slowed by a stale tag.

const rolloutSchema = "glossa.cli.release.rollout/v1"

// rolloutJSON is a rollout with its candidate and stable releases named.
type rolloutJSON struct {
	release.Rollout
	Release       *releaseRef `json:"release"`
	StableRelease *releaseRef `json:"stable_release"`
}

type rolloutDoc struct {
	Schema string `json:"schema"`
	// Action is start, status, advance, complete or abort.
	Action         string `json:"action"`
	Environment    string `json:"environment"`
	Replayed       bool   `json:"replayed,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// ETag is the rollout's version, for --if-match on the next change.
	ETag string `json:"etag,omitempty"`
	// PreviousPercent is what advance changed.
	PreviousPercent *int `json:"previous_percent,omitempty"`
	// Rollout is null for status when the environment has none active.
	Rollout *rolloutJSON `json:"rollout"`
}

type rolloutListDoc struct {
	Schema      string        `json:"schema"`
	Action      string        `json:"action"`
	Environment string        `json:"environment"`
	Rollouts    []rolloutJSON `json:"rollouts"`
}

const rolloutUsage = `release rollout <action> --environment NAME [flags]

Actions:
  start --release R --percent N [--max-duration D] [--force --force-reason TEXT] [--idempotency-key K]
                     serve release R (ID or v<N>) to N % of installations; the rest stay on what
                     the environment serves. --max-duration (1h to 90d, e.g. 72h or 14d; default
                     14d) is when it is aborted unless someone ended it first
  status [<id>]      the active rollout (or one by ID) and its ETag; alias: show
  list [--limit N]   the environment's rollouts, newest first
  advance [<id>] --percent N [--if-match ETAG]
                     change the share, down as well as up
  complete [<id>] [--if-match ETAG]
                     point the environment at the candidate: every installation gets it
  abort [<id>] [--if-match ETAG]
                     end it; every installation returns to the stable release on its next refresh

Without <id>, advance, complete and abort act on the environment's active rollout. advance
and complete are conditional on the ETag read just before (or --if-match): a rollout someone
else changed meanwhile is refused (precondition_failed) rather than overwritten. An
environment that requires approvals refuses rollouts (rollout_needs_approval): publish or
promote there, and approve the request.`

type rolloutArgs struct {
	action, id, environment, releaseRef string
	percent                             int
	percentSet                          bool
	maxDuration                         time.Duration
	force                               bool
	forceReason, idempotencyKey         string
	ifMatch                             string
	limit                               int
}

func parseRolloutArgs(inv *invocation, args []string) (rolloutArgs, error) {
	fs := inv.flags(rolloutUsage)
	var a rolloutArgs
	fs.StringVar(&a.environment, "environment", "", "the environment (required)")
	fs.StringVar(&a.releaseRef, "release", "", "start: the candidate release, ID or v<N>")
	percent := fs.String("percent", "", "start, advance: the share of installations in the candidate, 0–100")
	maxDuration := fs.String("max-duration", "", "start: when it is aborted unless ended first (1h–90d, e.g. 72h, 14d; default 14d)")
	fs.BoolVar(&a.force, "force", false, "start: start although the completeness requirement refuses the candidate (needs --force-reason)")
	fs.StringVar(&a.forceReason, "force-reason", "", "start: why --force; the completing deployment records it")
	fs.StringVar(&a.idempotencyKey, "idempotency-key", "", "start: the Idempotency-Key (default: a new one per invocation)")
	fs.StringVar(&a.ifMatch, "if-match", "", "advance, complete, abort: the ETag the change is based on (default for advance and complete: the one read just before)")
	fs.IntVar(&a.limit, "limit", 20, "list: at most this many rollouts (0: all)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: start, status, list, advance, complete or abort")
	}
	a.action, pos = pos[0], pos[1:]
	if a.action == "show" {
		a.action = "status"
	}
	if a.environment == "" {
		return a, usageError(inv.name, "%s needs --environment <name>", a.action)
	}
	if *percent != "" {
		n, err := strconv.Atoi(*percent)
		if err != nil {
			return a, usageError(inv.name, "--percent is a whole number from 0 to 100, not %q", *percent)
		}
		a.percent, a.percentSet = n, true
	}
	if *maxDuration != "" {
		if a.maxDuration, err = parseMaxDuration(*maxDuration); err != nil {
			return a, usageError(inv.name, "--max-duration %q is not a duration (72h, 14d)", *maxDuration)
		}
	}
	if a.idempotencyKey != "" && !idempotencyKeyPattern.MatchString(a.idempotencyKey) {
		return a, usageError(inv.name, "--idempotency-key must be 1-255 printable ASCII characters without spaces")
	}
	switch a.action {
	case "start":
		if a.releaseRef == "" || !a.percentSet {
			return a, usageError(inv.name, "start needs --release <release> and --percent <0-100>")
		}
		return a, noMore(inv, pos)
	case "list":
		if a.limit < 0 {
			return a, usageError(inv.name, "--limit must be 0 (all) or more")
		}
		return a, noMore(inv, pos)
	case "status", "advance", "complete", "abort":
		if a.action == "advance" && !a.percentSet {
			return a, usageError(inv.name, "advance needs --percent <0-100>")
		}
		if len(pos) > 0 {
			a.id, pos = pos[0], pos[1:]
		}
		return a, noMore(inv, pos)
	}
	return a, usageError(inv.name, "unknown action %q (start, status, list, advance, complete, abort)", a.action)
}

// parseMaxDuration reads a Go duration, or whole days as "14d".
func parseMaxDuration(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, errors.New("not a number of days")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, errors.New("not a duration")
	}
	return d, nil
}

func runReleaseRollout(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseRolloutArgs(inv, args)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	rc := inv.releaseClient(p)
	switch a.action {
	case "start":
		return inv.rolloutStart(ctx, rc, a)
	case "list":
		return inv.rolloutList(ctx, rc, a)
	case "status":
		return inv.rolloutStatus(ctx, rc, a)
	}
	return inv.rolloutChange(ctx, rc, a)
}

func (rc *releaseClient) rolloutJSON(ctx context.Context, inv *invocation, r release.Rollout) (rolloutJSON, error) {
	cand, err := rc.ref(ctx, inv, r.ReleaseID)
	if err != nil {
		return rolloutJSON{}, err
	}
	stable, err := rc.ref(ctx, inv, r.StableReleaseID)
	return rolloutJSON{Rollout: r, Release: cand, StableRelease: stable}, err
}

func (inv *invocation) rolloutStart(ctx context.Context, rc *releaseClient, a rolloutArgs) error {
	cand, err := rc.resolve(ctx, inv, a.releaseRef)
	if err != nil {
		return err
	}
	key := orDefault(a.idempotencyKey, newIdempotencyKey())
	v, err := rc.svc.StartRollout(ctx, rc.scope, release.StartRollout{Environment: a.environment, ReleaseID: cand.ID,
		Percent: a.percent, MaxDurationSeconds: int(a.maxDuration / time.Second), Force: a.force, ForceReason: a.forceReason,
		IdempotencyKey: key})
	if err != nil {
		return inv.rolloutError(err, "can't start a rollout in "+a.environment, a.environment)
	}
	x, err := rc.rolloutJSON(ctx, inv, v.Rollout)
	if err != nil {
		return err
	}
	out := rolloutDoc{Schema: rolloutSchema, Action: "start", Environment: a.environment, Replayed: v.Replayed,
		IdempotencyKey: key, ETag: v.ETag, Rollout: &x}
	return inv.emit(out, func(pr *printer) {
		if v.Replayed {
			pr.line("%s Rollout %s was already started with this idempotency key; nothing new was started.", pr.pass(), x.ID)
		} else {
			pr.line("%s Started rollout %s: %s serves %s to %d %% of installations; the rest stay on %s",
				pr.pass(), x.ID, x.Environment, refText(x.Release), x.Percent, refText(x.StableRelease))
		}
		printRolloutTail(pr, x)
	})
}

func printRolloutTail(pr *printer, x rolloutJSON) {
	if x.Forced {
		pr.line("  forced past the completeness requirement: %s", x.ForceReason)
	}
	pr.line("  %s", pr.dim(fmt.Sprintf("the pointer doesn't move until it completes; it is aborted by itself at %s unless someone ends it first",
		when(x.ExpiresAt))))
	pr.line("  %s", pr.dim(fmt.Sprintf("next: `glossa release rollout advance --environment %s --percent 50`, `complete` or `abort`", x.Environment)))
}

func (inv *invocation) rolloutList(ctx context.Context, rc *releaseClient, a rolloutArgs) error {
	items, err := rc.svc.Rollouts(ctx, rc.scope, a.environment, a.limit)
	if err != nil {
		return inv.rolloutError(err, "can't list the rollouts of "+a.environment, a.environment)
	}
	out := rolloutListDoc{Schema: rolloutSchema, Action: "list", Environment: a.environment, Rollouts: []rolloutJSON{}}
	for _, r := range items {
		x, err := rc.rolloutJSON(ctx, inv, r)
		if err != nil {
			return err
		}
		out.Rollouts = append(out.Rollouts, x)
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Rollouts) == 0 {
			pr.line("No rollouts in %s yet: `glossa release rollout start --environment %s --release <release> --percent 10` starts one.",
				a.environment, a.environment)
			return
		}
		rows := [][]string{{"ID", "STATUS", "PERCENT", "CANDIDATE", "STABLE", "STARTED", "ENDED"}}
		for _, x := range out.Rollouts {
			status := x.Status
			if x.End != "" && x.End != x.Status {
				status += " (" + x.End + ")"
			}
			ended := "—"
			if x.EndedAt != nil {
				ended = when(*x.EndedAt)
			}
			rows = append(rows, []string{x.ID, status, fmt.Sprintf("%d %%", x.Percent), refText(x.Release), refText(x.StableRelease),
				when(x.StartedAt), ended})
		}
		pr.table(rows)
	})
}

// activeRollout finds the environment's active rollout, or the one id
// names, with its ETag. found is false when there is no active one.
func (inv *invocation) activeRollout(ctx context.Context, rc *releaseClient, env, id string) (release.RolloutVersion, bool, error) {
	if id == "" {
		items, err := rc.svc.Rollouts(ctx, rc.scope, env, 0)
		if err != nil {
			return release.RolloutVersion{}, false, inv.rolloutError(err, "can't list the rollouts of "+env, env)
		}
		for _, r := range items {
			if r.Status == "active" {
				id = r.ID
				break
			}
		}
		if id == "" {
			return release.RolloutVersion{}, false, nil
		}
	}
	v, err := rc.svc.Rollout(ctx, rc.scope, env, id)
	if err != nil {
		return release.RolloutVersion{}, false, inv.rolloutError(err, "can't read rollout "+id, env)
	}
	return v, true, nil
}

func (inv *invocation) rolloutStatus(ctx context.Context, rc *releaseClient, a rolloutArgs) error {
	v, found, err := inv.activeRollout(ctx, rc, a.environment, a.id)
	if err != nil {
		return err
	}
	out := rolloutDoc{Schema: rolloutSchema, Action: "status", Environment: a.environment}
	if found {
		x, err := rc.rolloutJSON(ctx, inv, v.Rollout)
		if err != nil {
			return err
		}
		out.Rollout, out.ETag = &x, v.ETag
	}
	return inv.emit(out, func(pr *printer) {
		if out.Rollout == nil {
			pr.line("No active rollout in %s.", a.environment)
			return
		}
		printRollout(pr, *out.Rollout, out.ETag)
	})
}

func printRollout(pr *printer, x rolloutJSON, etag string) {
	state := x.Status
	if x.End != "" && x.End != x.Status {
		state += " (" + x.End + ")"
	}
	pr.line("%s in %s: %s at %d %%", pr.bold("Rollout "+x.ID), x.Environment, state, x.Percent)
	field := func(label, value string) { pr.line("  %-10s %s", label, value) }
	field("candidate", fmt.Sprintf("%s %s", refText(x.Release), pr.dim(x.ReleaseID)))
	field("stable", fmt.Sprintf("%s %s", refText(x.StableRelease), pr.dim(x.StableReleaseID)))
	field("started", fmt.Sprintf("%s by %s", when(x.StartedAt), x.StartedBy))
	if x.EndedAt != nil {
		field("ended", fmt.Sprintf("%s by %s", when(*x.EndedAt), dash(x.EndedBy)))
	} else {
		field("expires", fmt.Sprintf("%s (max %s)", when(x.ExpiresAt), maxDurationText(x.MaxDurationSeconds)))
	}
	if x.Forced {
		field("forced", x.ForceReason)
	}
	if etag != "" {
		field("etag", etag)
	}
}

func maxDurationText(seconds int) string {
	d := time.Duration(seconds) * time.Second
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// rolloutChange advances, completes or aborts.
func (inv *invocation) rolloutChange(ctx context.Context, rc *releaseClient, a rolloutArgs) error {
	before, found, err := inv.activeRollout(ctx, rc, a.environment, a.id)
	if err != nil {
		return err
	}
	if !found {
		return &Error{Exit: ExitNetwork, Code: "no_active_rollout", What: fmt.Sprintf("can't %s: no active rollout in %s", a.action, a.environment),
			Fix: fmt.Sprintf("`glossa release rollout list --environment %s` shows the ended ones; `start` begins a new one", a.environment)}
	}
	ifMatch := a.ifMatch
	if ifMatch == "" && a.action != "abort" {
		ifMatch = before.ETag
	}
	id := before.Rollout.ID
	var v release.RolloutVersion
	switch a.action {
	case "advance":
		v, err = rc.svc.AdvanceRollout(ctx, rc.scope, a.environment, id, ifMatch, a.percent)
	case "complete":
		v, err = rc.svc.CompleteRollout(ctx, rc.scope, a.environment, id, ifMatch)
	default:
		v, err = rc.svc.AbortRollout(ctx, rc.scope, a.environment, id, ifMatch)
	}
	if err != nil {
		e := inv.rolloutError(err, fmt.Sprintf("can't %s rollout %s", a.action, id), a.environment)
		var ce *Error
		if errors.As(e, &ce) && ce.Code == "precondition_failed" && a.ifMatch != "" {
			ce.Why = fmt.Sprintf("--if-match %s is not the rollout's current version, which is %s", a.ifMatch, before.ETag)
		}
		return e
	}
	x, err := rc.rolloutJSON(ctx, inv, v.Rollout)
	if err != nil {
		return err
	}
	out := rolloutDoc{Schema: rolloutSchema, Action: a.action, Environment: a.environment, ETag: v.ETag, Rollout: &x}
	if a.action == "advance" {
		prev := before.Rollout.Percent
		out.PreviousPercent = &prev
	}
	return inv.emit(out, func(pr *printer) {
		switch a.action {
		case "advance":
			pr.line("%s Rollout %s in %s: %d %% → %d %% of installations on %s", pr.pass(), x.ID, x.Environment,
				before.Rollout.Percent, x.Percent, refText(x.Release))
			pr.line("  %s", pr.dim("the salt stays: installations in the candidate at the lower share stay in it at a higher one"))
		case "complete":
			pr.line("%s %s now serves %s %s; was %s — rollout %s completed", pr.pass(), x.Environment, refText(x.Release),
				pr.dim("("+x.ReleaseID+")"), refText(x.StableRelease), x.ID)
			pr.line("  %s", pr.dim("the pointer moved; glossa-edge serves it within seconds"))
		default:
			pr.line("%s Aborted rollout %s: every installation returns to %s on its next refresh", pr.pass(), x.ID, refText(x.StableRelease))
		}
	})
}

// ── errors ──────────────────────────────────────────────────────────

// rolloutFixes explains the rollout operations' problem codes (RFC 0006
// §5.2; platform/api/openapi.yaml, tag rollouts). {env} is the
// environment.
var rolloutFixes = map[string]struct {
	exit ExitCode
	fix  string
}{
	"invalid_percent":            {ExitUsage, "--percent is a whole number from 0 to 100"},
	"invalid_max_duration":       {ExitUsage, "--max-duration is one hour to 90 days (e.g. 72h, 14d); leave it out for 14 days"},
	"force_reason_required":      {ExitUsage, "--force needs --force-reason: the completing deployment records why"},
	"invalid_force_reason":       {ExitUsage, "--force-reason goes with --force, and is at most 1,000 characters"},
	"invalid_idempotency_key":    {ExitUsage, "--idempotency-key must be 1-255 printable ASCII characters"},
	"idempotency_key_reused":     {ExitUsage, "use a new --idempotency-key, or leave it out"},
	"release_not_found":          {ExitNetwork, "run `glossa release list` for the project's releases"},
	"rollout_active":             {ExitNetwork, "{env} has one rollout at a time: `glossa release rollout status --environment {env}`, then advance, complete or abort it"},
	"rollout_no_stable":          {ExitNetwork, "{env} serves no release yet, and a rollout compares a candidate with it: publish or promote one there first"},
	"rollout_candidate_served":   {ExitNetwork, "{env} already serves that release: there is nothing to roll out"},
	"rollout_branch_environment": {ExitNetwork, "a branch environment has no rollouts (RFC 0006 §5.3): it publishes itself when its branch changes"},
	"rollout_source_locale":      {ExitNetwork, "the candidate's source locale differs from what {env} serves, and the two share one manifest: promote it instead"},
	"rollout_needs_approval": {ExitNetwork, "{env} requires approvals, and a release request can't carry a rollout yet: " +
		"`glossa release promote <release> --to {env}` makes a request, which people approve with `glossa approve <request>`"},
	"release_ineligible":            {ExitNetwork, "{env}'s policy must cover the policy the candidate was built under: publish it to an environment {env}'s policy covers (staging for production)"},
	"branch_release_not_promotable": {ExitNetwork, "a branch release holds text that exists only on its branch: merge the branch, then publish from the main catalog"},
	"policy_not_met": {ExitNetwork, "the candidate doesn't meet {env}'s completeness requirement: translate and approve what is short (`glossa status`), " +
		"or start anyway with --force --force-reason \"why\", which the completing deployment records"},
	"rollout_ended": {ExitNetwork, "it already ended (completed, aborted, expired or rolled back): `glossa release rollout list --environment {env}` says how; `start` begins a new one"},
	"precondition_failed": {ExitNetwork, "someone advanced, completed or aborted the rollout since it was read: " +
		"`glossa release rollout status --environment {env}` shows it now; decide again from there"},
	"precondition_required": {ExitNetwork, "the server wants If-Match: run `glossa release rollout status --environment {env}` and pass its etag as --if-match"},
	"storage_unavailable":   {ExitNetwork, "retry; the server can't reach artifact storage right now"},
}

// rolloutError explains a failed rollout request.
func (inv *invocation) rolloutError(err error, what, env string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if f, ok := rolloutFixes[ae.Code]; ok {
		e.Exit, e.Fix = f.exit, strings.ReplaceAll(f.fix, "{env}", env)
		e.Why = orDefault(ae.Detail, e.Why)
		if ae.Code == "precondition_failed" {
			e.What = what + ": the rollout changed since it was read"
		}
		return e
	}
	switch {
	case ae.Status == 412:
		e.Code = "precondition_failed"
		e.What = what + ": the rollout changed since it was read"
		e.Fix = strings.ReplaceAll(rolloutFixes["precondition_failed"].fix, "{env}", env)
	case ae.Status == 403:
		e.Fix = "starting, advancing, completing and aborting need releases.publish (a token with the publish scope); reading needs releases.read"
	case ae.Status == 404:
		e.Why = "not found (" + ae.Code + detail(ae) + ")"
		e.Fix = fmt.Sprintf("check --environment; `glossa release rollout list --environment %s` lists its rollouts", env)
	case ae.Status == 400 || ae.Status == 422:
		e.Exit = ExitUsage
	}
	return e
}
