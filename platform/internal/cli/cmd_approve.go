package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/release"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// glossa approve and glossa deny (RFC 0006 §8): a person's decision on
// a release request (§5.1) or a translation unit's approval (§3.2).
// Deciding is human-only. The CLI sends whatever credential it holds;
// the server refuses an API token (`person_required`), and the CLI
// says so plainly rather than as "forbidden": approvals need a person,
// signed in with `glossa login --device`.

const approveSchema = "glossa.cli.approve/v1"

// ── output shapes ───────────────────────────────────────────────────

type approvalDecisionJSON struct {
	Principal string    `json:"principal"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason,omitempty"`
	At        time.Time `json:"at"`
}

// approvalJSON is the API's Approval, with the message's key when it
// could be read and the count of grants so far.
type approvalJSON struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	InstanceID string `json:"instance_id,omitempty"`
	// Subject is translation or release_request; SubjectID the message
	// or the release request.
	Subject   string `json:"subject"`
	SubjectID string `json:"subject_id"`
	Message   string `json:"message,omitempty"`
	Locale    string `json:"locale,omitempty"`
	Required  int    `json:"required"`
	// Granted counts the distinct grants recorded so far.
	Granted            int                    `json:"granted"`
	Eligible           assigneeJSON           `json:"eligible"`
	DistinctFromAuthor bool                   `json:"distinct_from_author"`
	DueAt              *time.Time             `json:"due_at,omitempty"`
	State              string                 `json:"state"`
	Decisions          []approvalDecisionJSON `json:"decisions"`
	CreatedBy          string                 `json:"created_by"`
	CreatedAt          time.Time              `json:"created_at"`
	ClosedAt           *time.Time             `json:"closed_at,omitempty"`
}

type approveListDoc struct {
	Schema          string         `json:"schema"`
	Action          string         `json:"action"`
	ProjectID       string         `json:"project_id"`
	ReleaseRequests []requestJSON  `json:"release_requests"`
	Approvals       []approvalJSON `json:"approvals"`
}

type approveDoc struct {
	Schema string `json:"schema"`
	// Action is approve or deny.
	Action string `json:"action"`
	// Kind is release_request or translation.
	Kind           string       `json:"kind"`
	ReleaseRequest *requestJSON `json:"release_request,omitempty"`
	Approval       approvalJSON `json:"approval"`
}

func toApprovalJSON(a remote.Approval, key string) approvalJSON {
	out := approvalJSON{ID: a.Id, ProjectID: a.ProjectId, InstanceID: derefStr(a.InstanceId), Subject: string(a.Subject),
		SubjectID: a.SubjectId, Message: key, Locale: derefStr(a.Locale), Required: a.Required,
		Eligible: assigneeJSON{Kind: string(a.Eligible.Kind), ID: derefStr(a.Eligible.Id)}, DistinctFromAuthor: a.DistinctFromAuthor,
		DueAt: a.DueAt, State: string(a.State), Decisions: []approvalDecisionJSON{}, CreatedBy: a.CreatedBy,
		CreatedAt: a.CreatedAt, ClosedAt: a.ClosedAt}
	if a.Eligible.Role != nil {
		out.Eligible.Role = string(*a.Eligible.Role)
	}
	granted := map[string]bool{}
	for _, d := range a.Decisions {
		out.Decisions = append(out.Decisions, approvalDecisionJSON{Principal: d.Principal, Decision: string(d.Decision),
			Reason: derefStr(d.Reason), At: d.At})
		if d.Decision == "granted" {
			granted[d.Principal] = true
		}
	}
	out.Granted = len(granted)
	return out
}

// ── arguments ───────────────────────────────────────────────────────

const approveUsage = `approve [<request|translation>] [flags]

  glossa approve                     what waits for approval in this project: pending release
                                     requests and translation approvals (--environment E,
                                     --locale L, --message key narrow it)
  glossa approve <ref> [--reason R]  grant it
  glossa deny <ref> --reason R       deny it: a release request closes and nothing moves

<ref> is a release request's ID, an approval's ID, or key@locale for the pending approval of
a translation unit (checkout.pay@de).

Approvals need a person. An API token is refused (person_required): sign in with
` + "`glossa login --device`" + ` and decide as yourself, or decide in Studio's approvals inbox.
The requester (or the text's author) never counts: four-eyes.`

type approveArgs struct {
	ref, reason, environment, locale, message string
	granted                                   bool
}

func parseApproveArgs(inv *invocation, args []string, granted bool) (approveArgs, error) {
	fs := inv.flags(approveUsage)
	a := approveArgs{granted: granted}
	fs.StringVar(&a.reason, "reason", "", "why (deny: required; the requester sees it)")
	fs.StringVar(&a.environment, "environment", "", "list: only release requests into this environment")
	fs.StringVar(&a.locale, "locale", "", "list: only translation approvals in this locale")
	fs.StringVar(&a.message, "message", "", "list: only translation approvals of this message (by key)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) > 0 && pos[0] != "list" {
		a.ref = pos[0]
		pos = pos[1:]
	} else if len(pos) > 0 {
		pos = pos[1:]
	}
	if err := noMore(inv, pos); err != nil {
		return a, err
	}
	if a.locale != "" {
		if a.locale, err = normalizeLocale(inv, "--locale", a.locale); err != nil {
			return a, err
		}
	}
	if !granted {
		if a.ref == "" {
			return a, usageError(inv.name, "deny takes a release request, an approval or key@locale (`glossa approve` lists them)")
		}
		if strings.TrimSpace(a.reason) == "" {
			return a, &Error{Exit: ExitUsage, Code: "reason_required", What: "deny needs --reason",
				Why: "a denial closes the request; the requester and the trail see why",
				Fix: fmt.Sprintf("glossa deny %s --reason \"what has to change\"", a.ref)}
		}
	}
	return a, nil
}

func runApprove(ctx context.Context, inv *invocation, args []string) error {
	return runDecide(ctx, inv, args, true)
}

func runDeny(ctx context.Context, inv *invocation, args []string) error {
	return runDecide(ctx, inv, args, false)
}

func runDecide(ctx context.Context, inv *invocation, args []string, granted bool) error {
	a, err := parseApproveArgs(inv, args, granted)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	rc := inv.releaseClient(p)
	if a.ref == "" {
		return inv.approveList(ctx, p, rc, a)
	}
	return inv.decide(ctx, p, rc, a)
}

// ── the inbox ───────────────────────────────────────────────────────

func approvalFilterForRequests(p *project) remote.ApprovalFilter {
	return remote.ApprovalFilter{Project: p.scope.Project, Subject: "release_request"}
}

func (inv *invocation) approveList(ctx context.Context, p *project, rc *releaseClient, a approveArgs) error {
	out := approveListDoc{Schema: approveSchema, Action: "list", ProjectID: p.scope.Project,
		ReleaseRequests: []requestJSON{}, Approvals: []approvalJSON{}}
	if a.locale == "" && a.message == "" {
		reqs, err := rc.svc.ReleaseRequests(ctx, rc.scope, release.RequestFilter{Environment: a.environment, State: "pending"})
		if err != nil {
			return inv.approvalError(err, "can't list release requests", "")
		}
		for _, q := range reqs {
			x, err := rc.requestJSON(ctx, inv, q)
			if err != nil {
				return err
			}
			out.ReleaseRequests = append(out.ReleaseRequests, x)
		}
	}
	if a.environment == "" {
		list, err := p.client.Approvals(ctx, p.scope.Tenant, remote.ApprovalFilter{Project: p.scope.Project, Subject: "translation",
			State: "pending", Locale: a.locale, Message: a.message})
		if err != nil {
			return inv.approvalError(err, "can't list translation approvals", "")
		}
		keys := map[string]string{}
		if len(list) > 0 {
			keys = inv.messageKeys(ctx, p, p.scope.Project)
		}
		for _, x := range list {
			out.Approvals = append(out.Approvals, toApprovalJSON(x, keys[x.SubjectId]))
		}
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.ReleaseRequests)+len(out.Approvals) == 0 {
			pr.line("Nothing waits for approval in this project.")
			return
		}
		if len(out.ReleaseRequests) > 0 {
			pr.line("%s", pr.bold("Release requests"))
			printRequestTable(pr, out.ReleaseRequests)
		}
		if len(out.Approvals) > 0 {
			if len(out.ReleaseRequests) > 0 {
				pr.line("")
			}
			pr.line("%s", pr.bold("Translations"))
			rows := [][]string{{"UNIT", "GRANTED", "ASKS", "APPROVAL", "SINCE"}}
			for _, x := range out.Approvals {
				rows = append(rows, []string{orDefault(x.Message, x.SubjectID) + "@" + x.Locale,
					fmt.Sprintf("%d of %d", x.Granted, x.Required), assigneeLabel(x.Eligible), x.ID, when(x.CreatedAt)})
			}
			pr.table(rows)
		}
		pr.line("")
		pr.line("%s", pr.dim("`glossa approve <ID or key@locale>` grants one, `glossa deny <…> --reason R` denies it — as a person (`glossa login --device`)"))
	})
}

// ── deciding ────────────────────────────────────────────────────────

func (inv *invocation) decide(ctx context.Context, p *project, rc *releaseClient, a approveArgs) error {
	if key, locale, ok := strings.Cut(a.ref, "@"); ok && key != "" {
		return inv.decideUnit(ctx, p, a, key, locale)
	}
	q, err := rc.svc.ReleaseRequest(ctx, rc.scope, a.ref)
	if err == nil {
		return inv.decideRequest(ctx, p, rc, a, q)
	}
	if !isNotFound(err) {
		return inv.approvalError(err, "can't read release request "+a.ref, a.ref)
	}
	ap, err := p.client.Approval(ctx, p.scope.Tenant, a.ref)
	if isNotFound(err) {
		return &Error{Exit: ExitNetwork, Code: "not_found", What: fmt.Sprintf("no release request or approval %s", a.ref),
			Why: "this project has no release request with that ID, and the workspace no approval",
			Fix: "`glossa approve` lists what waits for approval; for a translation, pass key@locale"}
	}
	if err != nil {
		return inv.approvalError(err, "can't read approval "+a.ref, a.ref)
	}
	if string(ap.Subject) == "release_request" {
		q, err := rc.svc.ReleaseRequest(ctx, rc.scope, ap.SubjectId)
		if err != nil {
			return inv.approvalError(err, "can't read release request "+ap.SubjectId, ap.SubjectId)
		}
		return inv.decideRequest(ctx, p, rc, a, q)
	}
	return inv.decideApproval(ctx, p, a, ap.Id)
}

func verb(granted bool) string {
	if granted {
		return "approve"
	}
	return "deny"
}

func (inv *invocation) decideRequest(ctx context.Context, p *project, rc *releaseClient, a approveArgs, q release.Request) error {
	ap, err := p.client.DecideReleaseRequest(ctx, p.scope, q.ID, a.granted, a.reason)
	if err != nil {
		return inv.approvalError(err, fmt.Sprintf("can't %s release request %s", verb(a.granted), q.ID), q.ID)
	}
	// The last grant deploys through the workflow: read where it stands.
	if after, err := rc.svc.ReleaseRequest(ctx, rc.scope, q.ID); err == nil {
		q = after
	}
	x, err := rc.requestJSON(ctx, inv, q)
	if err != nil {
		return err
	}
	serving, err := inv.servingRef(ctx, rc, q.Environment)
	if err != nil {
		return err
	}
	out := approveDoc{Schema: approveSchema, Action: verb(a.granted), Kind: "release_request", ReleaseRequest: &x,
		Approval: toApprovalJSON(ap, "")}
	return inv.emit(out, func(pr *printer) { printRequestDecision(pr, out, serving) })
}

func printRequestDecision(pr *printer, d approveDoc, serving *releaseRef) {
	q, ap := d.ReleaseRequest, d.Approval
	what := fmt.Sprintf("release request %s (%s to %s)", q.ID, refText(q.Release), q.Environment)
	if d.Action == "deny" {
		pr.line("%s Denied %s: nothing moves; %s keeps serving %s", pr.pass(), what, q.Environment, refText(serving))
		return
	}
	pr.line("%s Approved %s: %d of %d", pr.pass(), what, ap.Granted, ap.Required)
	switch {
	case q.State == "deployed":
		pr.line("  the requirement is met: %s now serves %s; glossa-edge delivers it within seconds", q.Environment, refText(q.Release))
	case q.State == "refused":
		pr.line("  approved, but the completeness requirement, run again at deploy time, refused it: %s", q.Reason)
	case ap.State == "granted":
		pr.line("  the requirement is met: the release-approval workflow deploys it now (`glossa release requests show %s` follows it)", q.ID)
	default:
		left, who, does := ap.Required-ap.Granted, "people", "approve"
		if left == 1 {
			who, does = "person", "approves"
		}
		pr.line("  %s keeps serving %s until %d more %s, other than the requester, %s", q.Environment, refText(serving), left, who, does)
	}
}

func (inv *invocation) decideUnit(ctx context.Context, p *project, a approveArgs, key, locale string) error {
	l, err := normalizeLocale(inv, "the unit's locale", locale)
	if err != nil {
		return err
	}
	list, err := p.client.Approvals(ctx, p.scope.Tenant, remote.ApprovalFilter{Project: p.scope.Project, Subject: "translation",
		State: "pending", Message: key, Locale: l})
	if err != nil {
		return inv.approvalError(err, "can't find the approval of "+key+"@"+l, "")
	}
	if len(list) == 0 {
		return &Error{Exit: ExitNetwork, Code: "approval_not_found", What: fmt.Sprintf("nothing waits for approval on %s@%s", key, l),
			Why: "the unit has no pending approval: its workflow has not asked for one, or it was already decided",
			Fix: "`glossa approve` lists what waits; `glossa workflow instances --message " + key + "` shows where its workflow stands; to review the translation yourself: `glossa translations review " + key + "@" + l + " --state approved`"}
	}
	// Only the newest approval of a unit takes decisions.
	return inv.decideApproval(ctx, p, a, list[len(list)-1].Id)
}

func (inv *invocation) decideApproval(ctx context.Context, p *project, a approveArgs, id string) error {
	ap, err := p.client.DecideApproval(ctx, p.scope.Tenant, id, a.granted, a.reason)
	if err != nil {
		return inv.approvalError(err, fmt.Sprintf("can't %s approval %s", verb(a.granted), id), id)
	}
	key := inv.messageKeys(ctx, p, ap.ProjectId)[ap.SubjectId]
	out := approveDoc{Schema: approveSchema, Action: verb(a.granted), Kind: "translation", Approval: toApprovalJSON(ap, key)}
	return inv.emit(out, func(pr *printer) {
		x := out.Approval
		unit := orDefault(x.Message, x.SubjectID) + "@" + x.Locale
		if !a.granted {
			pr.line("%s Denied %s %s: the project's workflow decides what follows", pr.pass(), unit, pr.dim("("+x.ID+")"))
			return
		}
		pr.line("%s Approved %s %s: %d of %d", pr.pass(), unit, pr.dim("("+x.ID+")"), x.Granted, x.Required)
		if x.State == "granted" {
			pr.line("  %s", pr.dim("the requirement is met; the project's workflow approves the translation"))
		}
	})
}

// ── errors ──────────────────────────────────────────────────────────

// approvalFixes explains the approval and release-request problem codes
// (RFC 0006 §3.2, §5.1).
var approvalFixes = map[string]struct {
	exit ExitCode
	fix  string
}{
	"approval_not_requested":  {ExitNetwork, "the release-approval workflow asks for the approval a moment after the request is made: retry in a few seconds"},
	"release_request_closed":  {ExitNetwork, "it was deployed, denied, withdrawn or refused: `glossa release requests show <id>` says which; a new publish or promote makes a new request"},
	"approval_closed":         {ExitNetwork, "it was already granted or denied; `glossa approve` lists what still waits"},
	"approval_superseded":     {ExitNetwork, "a newer request replaced it: `glossa approve` lists the current one"},
	"invalid_approval":        {ExitUsage, "--reason is at most 2,000 characters"},
	"invalid_withdraw_reason": {ExitUsage, "--reason is at most 1,000 characters"},
	"invalid_query":           {ExitUsage, "check --environment, --state, --locale and --message"},
}

// approvalError explains a failed approval or release-request request.
// The human-only refusals say what they mean: approvals need a person.
func (inv *invocation) approvalError(err error, what, id string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if f, ok := approvalFixes[ae.Code]; ok {
		e.Exit, e.Fix = f.exit, f.fix
		e.Why = orDefault(ae.Detail, e.Why)
		return e
	}
	switch {
	case ae.Status == 403 && ae.Code == "person_required":
		e.What = "approvals need a person: sign in with `glossa login --device`"
		e.Why = "the server refused this credential (person_required): an API token or an agent never decides an approval — no token scope grants approvals.decide (" + what + ")"
		e.Fix = "run `glossa login --device` and decide as yourself, or decide in Studio's approvals inbox"
	case ae.Status == 403 && ae.Code == "own_text":
		e.Why = "four-eyes: you made this request, or wrote the text under approval, so your decision never counts"
		e.Fix = "someone else of the party the approval asks decides it; `glossa approve` lists who is asked"
	case ae.Status == 403 && ae.Code == "not_eligible":
		e.Why = "you are not of the party the approval asks (" + orDefault(ae.Detail, "not eligible") + ")"
		e.Fix = "`glossa approve` lists who each approval asks; an owner can change the environment's approval or the workflow"
	case ae.Status == 403:
		e.Why = "the credential may not do this (" + ae.Code + detail(ae) + ")"
		e.Fix = "deciding needs approvals.decide in the environment (release requests) or the unit's locale (translations); " +
			"reading needs releases.read and workflows.read"
	case ae.Status == 404 && id != "":
		e.Why = orDefault(ae.Detail, "not found")
		e.Fix = "`glossa approve` lists what waits for approval; `glossa release requests --state all` lists every release request"
	case ae.Status == 400 || ae.Status == 422:
		e.Exit = ExitUsage
		e.Why = orDefault(ae.Detail, e.Why)
	case ae.Status == 409:
		e.Why = orDefault(ae.Detail, e.Why)
	}
	return e
}
