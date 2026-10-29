package cli

import (
	"context"
	"fmt"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/release"
)

// Release requests (RFC 0006 §5.1): a publish or promote into an
// environment that requires approvals, held until enough distinct
// people other than the requester grant it. `glossa release requests`
// reads and withdraws them; `glossa approve` and `glossa deny` decide.

const releaseRequestsSchema = "glossa.cli.release.requests/v1"

// requestJSON is a release request with the release it would deploy.
type requestJSON struct {
	release.Request
	Release *releaseRef `json:"release"`
}

type releaseRequestsListDoc struct {
	Schema          string        `json:"schema"`
	Action          string        `json:"action"`
	Environment     string        `json:"environment,omitempty"`
	State           string        `json:"state,omitempty"`
	ReleaseRequests []requestJSON `json:"release_requests"`
}

type releaseRequestDoc struct {
	Schema         string      `json:"schema"`
	Action         string      `json:"action"`
	ReleaseRequest requestJSON `json:"release_request"`
	// Approval is the request's current approval with its decisions
	// (null: the workflow has not asked yet).
	Approval *approvalJSON `json:"approval"`
}

const releaseRequestsUsage = `release requests [<action>] [flags]

Actions:
  list (the default)  [--environment NAME] [--state S]   the project's release requests, newest first;
                      --state is pending (the default), deployed, denied, withdrawn, refused or all
  show <id>           one request: what it would deploy, where, who asked, the requirement, the
                      completeness verdict, whether it was forced and why, and every decision so far
  withdraw <id> [--reason R]
                      take a pending request back; nothing moves

People decide with ` + "`glossa approve <id>` and `glossa deny <id> --reason R`" + `.`

var releaseRequestStates = []string{"pending", "deployed", "denied", "withdrawn", "refused"}

type releaseRequestsArgs struct {
	action, id, environment, state, reason string
}

func parseReleaseRequestsArgs(inv *invocation, args []string) (releaseRequestsArgs, error) {
	fs := inv.flags(releaseRequestsUsage)
	var a releaseRequestsArgs
	fs.StringVar(&a.environment, "environment", "", "list: only requests into this environment")
	fs.StringVar(&a.state, "state", "pending", "list: pending, deployed, denied, withdrawn, refused or all")
	fs.StringVar(&a.reason, "reason", "", "withdraw: why")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	a.action = "list"
	if len(pos) > 0 {
		a.action, pos = pos[0], pos[1:]
	}
	switch a.action {
	case "list":
		if a.state != "all" && !contains(releaseRequestStates, a.state) {
			return a, usageError(inv.name, "--state is one of %s or all, not %q", strings.Join(releaseRequestStates, ", "), a.state)
		}
		if a.state == "all" {
			a.state = ""
		}
		return a, noMore(inv, pos)
	case "show", "withdraw":
		if len(pos) == 0 {
			return a, usageError(inv.name, "%s takes a release request ID (`glossa release requests` lists them)", a.action)
		}
		a.id = pos[0]
		return a, noMore(inv, pos[1:])
	}
	return a, usageError(inv.name, "unknown action %q (list, show, withdraw)", a.action)
}

func runReleaseRequests(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseReleaseRequestsArgs(inv, args)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	rc := inv.releaseClient(p)
	switch a.action {
	case "show":
		return inv.releaseRequestShow(ctx, p, rc, a.id)
	case "withdraw":
		return inv.releaseRequestWithdraw(ctx, rc, a)
	}
	return inv.releaseRequestsList(ctx, rc, a)
}

func (rc *releaseClient) requestJSON(ctx context.Context, inv *invocation, q release.Request) (requestJSON, error) {
	ref, err := rc.ref(ctx, inv, q.ReleaseID)
	return requestJSON{Request: q, Release: ref}, err
}

func (inv *invocation) releaseRequestsList(ctx context.Context, rc *releaseClient, a releaseRequestsArgs) error {
	items, err := rc.svc.ReleaseRequests(ctx, rc.scope, release.RequestFilter{Environment: a.environment, State: a.state})
	if err != nil {
		return inv.approvalError(err, "can't list release requests", "")
	}
	out := releaseRequestsListDoc{Schema: releaseRequestsSchema, Action: "list", Environment: a.environment, State: a.state,
		ReleaseRequests: []requestJSON{}}
	for _, q := range items {
		x, err := rc.requestJSON(ctx, inv, q)
		if err != nil {
			return err
		}
		out.ReleaseRequests = append(out.ReleaseRequests, x)
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.ReleaseRequests) == 0 {
			what := "release requests"
			if a.state != "" {
				what = a.state + " " + what
			}
			if a.environment != "" {
				what += " into " + a.environment
			}
			pr.line("No %s.", what)
			return
		}
		printRequestTable(pr, out.ReleaseRequests)
	})
}

func printRequestTable(pr *printer, items []requestJSON) {
	rows := [][]string{{"ID", "STATE", "ENVIRONMENT", "RELEASE", "ACTION", "NEEDS", "REQUESTER", "CREATED"}}
	for _, q := range items {
		rows = append(rows, []string{q.ID, q.State + forcedMark(q.Request), q.Environment, refText(q.Release), q.Action,
			fmt.Sprintf("%d of %s", q.Approval.N, partyText(q.Approval.From)), q.Requester, when(q.CreatedAt)})
	}
	pr.table(rows)
}

func forcedMark(q release.Request) string {
	if q.Forced {
		return " (forced)"
	}
	return ""
}

func refText(r *releaseRef) string {
	if r == nil {
		return "—"
	}
	return fmt.Sprintf("v%d", r.Version)
}

// currentApproval finds the newest approval Workflow holds for a
// release request (nil: none asked yet). Best effort for show: a
// credential that may read releases but not workflows sees the request
// without its decisions.
func (inv *invocation) currentApproval(ctx context.Context, p *project, requestID string) *approvalJSON {
	list, err := p.client.Approvals(ctx, p.scope.Tenant, approvalFilterForRequests(p))
	if err != nil {
		return nil
	}
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].SubjectId == requestID {
			a := toApprovalJSON(list[i], "")
			return &a
		}
	}
	return nil
}

func (inv *invocation) releaseRequestShow(ctx context.Context, p *project, rc *releaseClient, id string) error {
	q, err := rc.svc.ReleaseRequest(ctx, rc.scope, id)
	if err != nil {
		return inv.approvalError(err, "can't read release request "+id, id)
	}
	x, err := rc.requestJSON(ctx, inv, q)
	if err != nil {
		return err
	}
	out := releaseRequestDoc{Schema: releaseRequestsSchema, Action: "show", ReleaseRequest: x, Approval: inv.currentApproval(ctx, p, id)}
	return inv.emit(out, func(pr *printer) { printRequest(pr, out.ReleaseRequest, out.Approval) })
}

func printRequest(pr *printer, q requestJSON, a *approvalJSON) {
	pr.line("%s · %s · %s %s to %s", pr.bold(q.ID), q.State, q.Action, refText(q.Release), q.Environment)
	field := func(label, value string) { pr.line("  %-12s %s", label, value) }
	field("requested", fmt.Sprintf("%s by %s", when(q.CreatedAt), q.Requester))
	field("needs", fmt.Sprintf("%d of %s, none of them the requester", q.Approval.N, partyText(q.Approval.From)))
	gate := "met"
	if !q.Gate.Met {
		gate = "not met: " + strings.Join(q.Gate.Unmet, "; ")
	}
	field("completeness", gate)
	if q.Forced {
		field("forced", q.ForceReason)
	}
	if q.DecidedBy != "" {
		field("closed", fmt.Sprintf("by %s%s", q.DecidedBy, reasonSuffix(q.Reason)))
	}
	if a == nil {
		if q.State == "pending" {
			field("approvals", "not asked yet: the release-approval workflow asks within seconds")
		}
		return
	}
	field("approvals", fmt.Sprintf("%d of %d granted (%s)", a.Granted, a.Required, a.State))
	for _, d := range a.Decisions {
		pr.line("    %s %s at %s%s", d.Decision, d.Principal, when(d.At), reasonSuffix(d.Reason))
	}
}

func (inv *invocation) releaseRequestWithdraw(ctx context.Context, rc *releaseClient, a releaseRequestsArgs) error {
	q, err := rc.svc.WithdrawReleaseRequest(ctx, rc.scope, a.id, a.reason)
	if err != nil {
		return inv.approvalError(err, "can't withdraw release request "+a.id, a.id)
	}
	x, err := rc.requestJSON(ctx, inv, q)
	if err != nil {
		return err
	}
	out := releaseRequestDoc{Schema: releaseRequestsSchema, Action: "withdraw", ReleaseRequest: x}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Withdrew release request %s: %s was not deployed to %s, and nothing moved", pr.pass(), q.ID, refText(x.Release), q.Environment)
	})
}
