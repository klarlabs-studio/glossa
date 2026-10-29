package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// Assignments (RFC 0006 §3.1): a batch of translation units given to a
// member, a role, a group or a vendor. Completing one is a claim, not a
// decision — the project's workflow decides what follows — so this
// group never changes a review state.

// assignmentsSchema tags every `glossa assignments --json` document;
// `action` says which subcommand wrote it.
const assignmentsSchema = "glossa.cli.assignments/v1"

// ── output shapes ───────────────────────────────────────────────────

type assignmentUnitJSON struct {
	MessageID string `json:"message_id"`
	// Message is the message's key, when it could be read (show and
	// create name it; a list leaves it out).
	Message string `json:"message,omitempty"`
	Locale  string `json:"locale"`
}

type assigneeJSON struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Role string `json:"role,omitempty"`
}

// assignmentJSON is the API's Assignment.
type assignmentJSON struct {
	ID         string               `json:"id"`
	ProjectID  string               `json:"project_id"`
	InstanceID string               `json:"instance_id,omitempty"`
	Units      []assignmentUnitJSON `json:"units"`
	Assignee   assigneeJSON         `json:"assignee"`
	Permission string               `json:"permission"`
	State      string               `json:"state"`
	DueAt      *time.Time           `json:"due_at,omitempty"`
	CreatedBy  string               `json:"created_by"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at"`
	ClosedBy   string               `json:"closed_by,omitempty"`
	ClosedAt   *time.Time           `json:"closed_at,omitempty"`
	Reason     string               `json:"reason,omitempty"`
}

type assignmentsListDoc struct {
	Schema string `json:"schema"`
	Action string `json:"action"`
	// Mine is false only with --all.
	Mine        bool             `json:"mine"`
	Assignments []assignmentJSON `json:"assignments"`
}

type assignmentDoc struct {
	Schema     string         `json:"schema"`
	Action     string         `json:"action"`
	Replayed   bool           `json:"replayed,omitempty"`
	Assignment assignmentJSON `json:"assignment"`
}

// ── arguments ───────────────────────────────────────────────────────

const assignmentsUsage = `assignments [<action>] [flags]

Actions:
  list (the default)  my work: assignments given to me, to a role I hold, to my groups or
                      my vendor. --all: everyone's (needs assignments.manage).
                      Filters: --project P, --state open|accepted|done|declined|expired,
                      --locale L, --message key
  show <id>           one assignment, its units by key
  accept <id>         take an open assignment on
  complete <id>       claim it done; the project's workflow decides what follows
  decline <id> [--reason R]
                      hand it back (a manager may take it back)
  create --to <party> --units key@locale[,…] [--project P] [--due T] [--permission P]
                      give a project's translation units to someone. <party> is
                      member:<id|email>, role:<role>, group:<name|id> or vendor:<name|id>;
                      --due is RFC 3339 or a duration from now (72h)

  report [--vendor ID] [--project P] [--since T]
                      the quality numbers of completed assignments by assignee and locale
                      (on time, words from TM, review outcomes, edit, findings, rework);
                      needs assignments.read and a member who is not limited to their own
                      work. --vendor is a vendor's ID; --since is RFC 3339 or a duration
                      back from now (720h)

Assignments are a person's work: accepting, completing and declining take the assignee
signed in, and creating one takes assignments.manage, which no API token scope grants.`

type assignmentsArgs struct {
	action, id               string
	all                      bool
	project, state, locale   string
	message, reason, to, due string
	permission               string
	vendor, since            string
	units                    listFlag
}

var assignmentStates = []string{"open", "accepted", "done", "declined", "expired"}

func parseAssignmentsArgs(inv *invocation, args []string) (assignmentsArgs, error) {
	fs := inv.flags(assignmentsUsage)
	var a assignmentsArgs
	fs.BoolVar(&a.all, "all", false, "list: everyone's assignments, not only mine (needs assignments.manage)")
	fs.StringVar(&a.project, "project", "", "list, report: only this project's; create: the project (default glossa.yaml's)")
	fs.StringVar(&a.vendor, "vendor", "", "report: only this vendor's (its ID)")
	fs.StringVar(&a.since, "since", "", "report: only assignments completed since, RFC 3339 or a duration back (720h)")
	fs.StringVar(&a.state, "state", "", "list: only assignments in this state")
	fs.StringVar(&a.locale, "locale", "", "list: only assignments with a unit in this locale")
	fs.StringVar(&a.message, "message", "", "list: only assignments with a unit of this message (by key)")
	fs.StringVar(&a.reason, "reason", "", "decline: why")
	fs.StringVar(&a.to, "to", "", "create: member:<id|email>, role:<role>, group:<name|id> or vendor:<name|id>")
	fs.Var(&a.units, "units", "create: key@locale (repeatable or comma-separated)")
	fs.StringVar(&a.due, "due", "", "create: when it is due, RFC 3339 or a duration from now (72h)")
	fs.StringVar(&a.permission, "permission", "", "create: what doing it takes (default translations.write)")
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
		if a.state != "" && !contains(assignmentStates, a.state) {
			return a, usageError(inv.name, "--state is one of %s, not %q", strings.Join(assignmentStates, ", "), a.state)
		}
		if a.locale != "" {
			l, err := normalizeLocale(inv, "--locale", a.locale)
			if err != nil {
				return a, err
			}
			a.locale = l
		}
		return a, noMore(inv, pos)
	case "report":
		return a, noMore(inv, pos)
	case "show", "accept", "complete", "decline":
		if len(pos) == 0 {
			return a, usageError(inv.name, "%s takes an assignment ID (`glossa assignments` lists yours)", a.action)
		}
		a.id = pos[0]
		return a, noMore(inv, pos[1:])
	case "create":
		if a.to == "" {
			return a, usageError(inv.name, "create needs --to: member:<id|email>, role:<role>, group:<name> or vendor:<name>")
		}
		if len(splitList(a.units)) == 0 {
			return a, usageError(inv.name, "create needs --units key@locale[,key@locale…]: the translation units to give")
		}
		return a, noMore(inv, pos)
	}
	return a, usageError(inv.name, "unknown action %q (list, show, accept, complete, decline, create, report)", a.action)
}

func runAssignments(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseAssignmentsArgs(inv, args)
	if err != nil {
		return err
	}
	var in remote.NewAssignment
	if a.action == "create" {
		if in, err = parseNewAssignment(inv, a, time.Now()); err != nil {
			return err
		}
	}
	var since *time.Time
	if a.action == "report" && a.since != "" {
		t, err := parseReportSince(a.since, time.Now())
		if err != nil {
			return usageError(inv.name, "--since %q is neither RFC 3339 (2026-09-01T00:00:00Z) nor a duration back from now (720h)", a.since)
		}
		since = &t
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	switch a.action {
	case "report":
		return inv.assignmentsReport(ctx, p, a, since)
	case "list":
		return inv.assignmentsList(ctx, p, a)
	case "show":
		return inv.assignmentShow(ctx, p, a.id)
	case "create":
		return inv.assignmentCreate(ctx, p, a, in)
	}
	return inv.assignmentChange(ctx, p, a)
}

// parseNewAssignment reads create's flags; the project and a member
// named by email are resolved against the server afterwards.
func parseNewAssignment(inv *invocation, a assignmentsArgs, now time.Time) (remote.NewAssignment, error) {
	var in remote.NewAssignment
	kind, ref, ok := strings.Cut(a.to, ":")
	ref = strings.TrimSpace(ref)
	if !ok || ref == "" {
		return in, usageError(inv.name, "--to %q is not a party: write member:<id|email>, role:<role>, group:<name> or vendor:<name>", a.to)
	}
	switch kind {
	case "member":
		in.To.Member = &ref
	case "role":
		r := remote.Role(ref)
		in.To.Role = &r
	case "group":
		in.To.Group = &ref
	case "vendor":
		in.To.Vendor = &ref
	default:
		return in, usageError(inv.name, "--to %q: the kind is member, role, group or vendor, not %q", a.to, kind)
	}
	for _, u := range splitList(a.units) {
		i := strings.LastIndex(u, "@")
		if i <= 0 || i == len(u)-1 {
			return in, usageError(inv.name, "--units %q is not key@locale (e.g. checkout.pay@de)", u)
		}
		l, err := normalizeLocale(inv, "--units", u[i+1:])
		if err != nil {
			return in, err
		}
		in.Units = append(in.Units, remote.AssignmentUnitRef{Message: u[:i], Locale: l})
	}
	if a.due != "" {
		due, err := parseDue(a.due, now)
		if err != nil {
			return in, usageError(inv.name, "--due %q is neither RFC 3339 (2026-10-09T17:00:00Z) nor a duration (72h)", a.due)
		}
		in.DueAt = &due
	}
	in.Permission = a.permission
	return in, nil
}

func parseDue(v string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return time.Time{}, errors.New("not a time")
	}
	return now.Add(d).UTC().Truncate(time.Second), nil
}

// ── commands ────────────────────────────────────────────────────────

func (inv *invocation) assignmentsList(ctx context.Context, p *project, a assignmentsArgs) error {
	f := remote.AssignmentFilter{State: a.state, Locale: a.locale, Message: a.message, Mine: !a.all}
	if a.project != "" || a.message != "" {
		// A message key means something only in a project.
		target, err := inv.workflowProject(ctx, p, a.project)
		if err != nil {
			return err
		}
		f.Project = target.Id
	}
	items, err := p.client.Assignments(ctx, p.scope.Tenant, f)
	if err != nil {
		return inv.assignmentsError(err, "can't list assignments", "")
	}
	out := assignmentsListDoc{Schema: assignmentsSchema, Action: "list", Mine: !a.all, Assignments: []assignmentJSON{}}
	for _, x := range items {
		out.Assignments = append(out.Assignments, toAssignmentJSON(x, nil))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Assignments) == 0 {
			if out.Mine {
				pr.line("Nothing is assigned to you%s.", filterSuffix(a))
				pr.line("  %s", pr.dim("work is given to a member, a role, a group or a vendor; an API token is none of them, so a token's list is always empty"))
			} else {
				pr.line("No assignments%s.", filterSuffix(a))
			}
			return
		}
		rows := [][]string{{"ID", "STATE", "UNITS", "ASSIGNEE", "PERMISSION", "DUE"}}
		for _, x := range out.Assignments {
			due := "-"
			if x.DueAt != nil {
				due = x.DueAt.Format(time.RFC3339)
			}
			rows = append(rows, []string{x.ID, x.State, strconv.Itoa(len(x.Units)), assigneeLabel(x.Assignee), x.Permission, due})
		}
		pr.table(rows)
	})
}

func filterSuffix(a assignmentsArgs) string {
	var parts []string
	if a.state != "" {
		parts = append(parts, "in state "+a.state)
	}
	if a.project != "" {
		parts = append(parts, "in project "+a.project)
	}
	if a.locale != "" {
		parts = append(parts, "for "+a.locale)
	}
	if a.message != "" {
		parts = append(parts, "of "+a.message)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func (inv *invocation) assignmentShow(ctx context.Context, p *project, id string) error {
	x, err := p.client.Assignment(ctx, p.scope.Tenant, id)
	if err != nil {
		return inv.assignmentsError(err, "can't read assignment "+id, id)
	}
	out := assignmentDoc{Schema: assignmentsSchema, Action: "show",
		Assignment: toAssignmentJSON(x, inv.messageKeys(ctx, p, x.ProjectId))}
	return inv.emit(out, func(pr *printer) { printAssignment(pr, out.Assignment) })
}

func (inv *invocation) assignmentCreate(ctx context.Context, p *project, a assignmentsArgs, in remote.NewAssignment) error {
	target, err := inv.workflowProject(ctx, p, a.project)
	if err != nil {
		return err
	}
	in.Project = target.Id
	if in.To.Member != nil && strings.Contains(*in.To.Member, "@") {
		id, err := inv.memberByEmail(ctx, p, *in.To.Member)
		if err != nil {
			return err
		}
		in.To.Member = &id
	}
	x, replayed, err := p.client.CreateAssignment(ctx, p.scope.Tenant, in, newIdempotencyKey())
	if err != nil {
		return inv.assignmentsError(err, "can't create the assignment", "")
	}
	out := assignmentDoc{Schema: assignmentsSchema, Action: "create", Replayed: replayed,
		Assignment: toAssignmentJSON(x, inv.messageKeys(ctx, p, x.ProjectId))}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Assigned %s to %s (%s)", pr.pass(), plural(len(x.Units), "unit", "units"), assigneeLabel(out.Assignment.Assignee), x.Id)
		pr.line("  %s", pr.dim("the assignee accepts and completes it; completing raises assignment.completed and the project's workflow decides what follows"))
	})
}

// memberByEmail finds a member's ID by their address.
func (inv *invocation) memberByEmail(ctx context.Context, p *project, email string) (string, error) {
	members, err := p.client.Members(ctx, p.scope.Tenant)
	if err != nil {
		return "", inv.assignmentsError(err, "can't read the members to find "+email, "")
	}
	for _, m := range members {
		if strings.EqualFold(string(m.Email), email) {
			return m.Id, nil
		}
	}
	return "", &Error{Exit: ExitUsage, Code: "unknown_party", What: fmt.Sprintf("no member %s in this workspace", email),
		Where: "--to", Fix: "invite them in Studio first, or pass member:<id>"}
}

func (inv *invocation) assignmentChange(ctx context.Context, p *project, a assignmentsArgs) error {
	var (
		x   remote.Assignment
		err error
	)
	switch a.action {
	case "accept":
		x, err = p.client.AcceptAssignment(ctx, p.scope.Tenant, a.id)
	case "complete":
		x, err = p.client.CompleteAssignment(ctx, p.scope.Tenant, a.id)
	default:
		x, err = p.client.DeclineAssignment(ctx, p.scope.Tenant, a.id, a.reason)
	}
	if err != nil {
		return inv.assignmentsError(err, fmt.Sprintf("can't %s assignment %s", a.action, a.id), a.id)
	}
	out := assignmentDoc{Schema: assignmentsSchema, Action: a.action, Assignment: toAssignmentJSON(x, nil)}
	return inv.emit(out, func(pr *printer) {
		switch a.action {
		case "accept":
			pr.line("%s Accepted %s: %s are yours to translate", pr.pass(), x.Id, plural(len(x.Units), "unit", "units"))
		case "complete":
			pr.line("%s Completed %s", pr.pass(), x.Id)
			pr.line("  %s", pr.dim("a claim, not a decision: no review state changed; the project's workflow decides what follows"))
		default:
			pr.line("%s Declined %s", pr.pass(), x.Id)
		}
	})
}

// messageKeys names a project's messages by ID, for showing units by
// key. Best effort: a label, never a reason to fail.
func (inv *invocation) messageKeys(ctx context.Context, p *project, projectID string) map[string]string {
	keys := map[string]string{}
	msgs, err := p.client.Messages(ctx, remote.Scope{Tenant: p.scope.Tenant, Project: projectID}, remote.MessageFilter{})
	if err != nil {
		return keys
	}
	for _, m := range msgs {
		keys[m.Id] = string(m.Key)
	}
	return keys
}

func printAssignment(pr *printer, x assignmentJSON) {
	pr.line("%s · %s · %s", pr.bold(x.ID), x.State, assigneeLabel(x.Assignee))
	pr.line("  project %s · needs %s", x.ProjectID, x.Permission)
	if x.InstanceID != "" {
		pr.line("  made by workflow instance %s", x.InstanceID)
	}
	if x.DueAt != nil {
		pr.line("  due %s", x.DueAt.Format(time.RFC3339))
	}
	pr.line("  created by %s at %s", x.CreatedBy, x.CreatedAt.Format(time.RFC3339))
	if x.ClosedAt != nil {
		pr.line("  closed by %s at %s%s", x.ClosedBy, x.ClosedAt.Format(time.RFC3339), reasonSuffix(x.Reason))
	}
	rows := [][]string{{"MESSAGE", "LOCALE"}}
	for _, u := range x.Units {
		rows = append(rows, []string{orDefault(u.Message, u.MessageID), u.Locale})
	}
	pr.table(rows)
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return ": " + r
}

func assigneeLabel(a assigneeJSON) string {
	if a.Kind == "role" {
		return "role " + a.Role
	}
	return a.Kind + " " + a.ID
}

func toAssignmentJSON(x remote.Assignment, keys map[string]string) assignmentJSON {
	out := assignmentJSON{ID: x.Id, ProjectID: x.ProjectId, InstanceID: derefStr(x.InstanceId), Units: []assignmentUnitJSON{},
		Assignee: assigneeJSON{Kind: string(x.Assignee.Kind), ID: derefStr(x.Assignee.Id)}, Permission: x.Permission,
		State: string(x.State), DueAt: x.DueAt, CreatedBy: x.CreatedBy, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt,
		ClosedBy: derefStr(x.ClosedBy), ClosedAt: x.ClosedAt, Reason: derefStr(x.Reason)}
	if x.Assignee.Role != nil {
		out.Assignee.Role = string(*x.Assignee.Role)
	}
	for _, u := range x.Units {
		out.Units = append(out.Units, assignmentUnitJSON{MessageID: u.MessageId, Message: keys[u.MessageId], Locale: u.Locale})
	}
	return out
}

// ── errors ──────────────────────────────────────────────────────────

// assignmentsFixes explains the Assignments API's problem codes (RFC
// 0006 §3).
var assignmentsFixes = map[string]string{
	"assignment_state":       "`glossa assignments show <id>` shows its state: only an open assignment can be accepted, and only a live one (open or accepted) completed or declined",
	"invalid_assignment":     "check --units (keys the project has, at most 10,000) and --due (in the future)",
	"unknown_party":          "check --to: a member's ID or address, a role (translator, reviewer, …), or a group's or vendor's name or ID",
	"idempotency_key_reused": "run the command again: it sends a fresh key",
	"invalid_query":          "check --state, --locale and --message",
}

// assignmentsError explains a failed Assignments request. Assignment
// work belongs to people, and every CLI credential is a token, so the
// refusals that follow from that say so rather than "forbidden".
func (inv *invocation) assignmentsError(err error, what, id string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if fix, ok := assignmentsFixes[ae.Code]; ok {
		e.Fix = fix
	}
	switch {
	case ae.Status == 403 && strings.Contains(ae.Detail, "assignments.manage"):
		e.Code = "assignments_manage_required"
		e.Why = "creating assignments and listing others' work take assignments.manage, a person's permission " +
			"(owner or admin) that no API token scope grants"
		e.Fix = "create the assignment in Studio, or let a workflow's assign action make it; `glossa assignments` (my work) needs only assignments.read"
	case ae.Status == 403:
		e.Why = "the credential may not do this (" + ae.Code + detail(ae) + ")"
		e.Fix = "only the assignee, holding the assignment's permission for every unit's locale, accepts or completes it"
	case ae.Status == 404 && id != "":
		e.Why = orDefault(ae.Detail, "not found") + ": an assignment given to someone else does not exist to you"
		e.Fix = "`glossa assignments` lists yours. Work is given to people — a member, a role, a group, a vendor — " +
			"and an API token is none of them, so accepting and completing happen signed in as the assignee, in Studio's My work"
	case ae.Status == 400 || ae.Status == 422:
		e.Exit = ExitUsage
		e.Why = orDefault(ae.Detail, e.Why)
	case ae.Status == 409:
		e.Why = orDefault(ae.Detail, e.Why)
	}
	return e
}
