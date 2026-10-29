package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// `glossa assignments report` (RFC 0006 §3.4): the quality numbers of
// completed assignments, by assignee and locale, as the server computes
// them. The CLI aggregates nothing; --json is the API's document with
// the schema tag every `glossa assignments --json` carries.

type assignmentReportDoc struct {
	Schema      string                       `json:"schema"`
	Action      string                       `json:"action"`
	Project     string                       `json:"project,omitempty"`
	Vendor      string                       `json:"vendor,omitempty"`
	Since       *time.Time                   `json:"since,omitempty"`
	GeneratedAt time.Time                    `json:"generated_at"`
	Truncated   bool                         `json:"truncated"`
	Rows        []remote.AssignmentReportRow `json:"rows"`
}

// parseReportSince reads --since: an RFC 3339 time, or a duration back from
// now (720h).
func parseReportSince(v string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return time.Time{}, errors.New("not a time")
	}
	return now.Add(-d).UTC().Truncate(time.Second), nil
}

func (inv *invocation) assignmentsReport(ctx context.Context, p *project, a assignmentsArgs, since *time.Time) error {
	f := remote.AssignmentReportFilter{Vendor: a.vendor, Since: since}
	if a.project != "" {
		target, err := inv.workflowProject(ctx, p, a.project)
		if err != nil {
			return err
		}
		f.Project = target.Id
	}
	r, err := p.client.AssignmentReport(ctx, p.scope.Tenant, f)
	if err != nil {
		return inv.reportError(err)
	}
	sort.SliceStable(r.Rows, func(i, j int) bool {
		if r.Rows[i].Assignee != r.Rows[j].Assignee {
			return r.Rows[i].Assignee < r.Rows[j].Assignee
		}
		return r.Rows[i].Locale < r.Rows[j].Locale
	})
	out := assignmentReportDoc{Schema: assignmentsSchema, Action: "report", Project: f.Project, Vendor: a.vendor,
		Since: r.Since, GeneratedAt: r.GeneratedAt, Truncated: r.Truncated, Rows: r.Rows}
	if out.Rows == nil {
		out.Rows = []remote.AssignmentReportRow{}
	}
	return inv.emit(out, func(pr *printer) { printAssignmentReport(pr, out) })
}

func printAssignmentReport(pr *printer, d assignmentReportDoc) {
	if len(d.Rows) == 0 {
		pr.line("No completed assignments to report on.")
		return
	}
	rows := [][]string{{"ASSIGNEE", "LOCALE", "DONE", "ON TIME", "LATE", "UNITS", "WORDS", "TM WORDS", "APPROVED", "REJECTED", "EDIT", "FINDINGS/UNIT", "REWORK"}}
	for _, x := range d.Rows {
		rows = append(rows, []string{x.Assignee, x.Locale, fmt.Sprint(x.Assignments), fmt.Sprint(x.OnTime), fmt.Sprint(x.Late),
			fmt.Sprint(x.Units), fmt.Sprint(x.SourceWords), fmt.Sprint(x.TmWords["exact"]), fmt.Sprint(x.Approved), fmt.Sprint(x.Rejected),
			fmt.Sprintf("%.2f", x.MeanEditRatio), fmt.Sprintf("%.2f", x.FindingsPerUnit), fmt.Sprintf("%.0f%%", x.ReworkRate*100)})
	}
	pr.table(rows)
	pr.line("%s", pr.dim("EDIT is the reviewers' mean edit ratio on the vendor's text; computed on read, nothing is stored"))
	if d.Truncated {
		pr.line("%s the report hit a bound and covers only part of the work; narrow it with --vendor, --project or --since", pr.caution())
	}
}

// reportError explains a refused report: the numbers are about vendors,
// so the permission is assignments.read and an assigned member has none.
func (inv *invocation) reportError(err error) error {
	var ae *remote.APIError
	if errors.As(err, &ae) && ae.Status == 403 {
		e := asError(inv.apiError(err, "can't read the report"))
		e.Why = "the credential may not read assignments in this scope (" + ae.Code + detail(ae) + ")"
		e.Fix = "the report takes assignments.read; a vendor's member (visibility `assigned`) is refused, and so is a token without it"
		return e
	}
	return inv.assignmentsError(err, "can't read the report", "")
}
