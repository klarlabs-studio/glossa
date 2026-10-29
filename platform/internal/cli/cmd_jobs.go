package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// Import and export jobs (RFC 0003 §6): the shapes, polling and errors
// `import --format`, `export` and `jobs` share.

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

type jobFileJSON struct {
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
}

type importCountsJSON struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Conflict  int `json:"conflict"`
	Invalid   int `json:"invalid"`
}

type importSummaryJSON struct {
	importCountsJSON
	// ByKind has the same counts per kind of result: message,
	// translation, tm_unit or concept.
	ByKind map[string]importCountsJSON `json:"by_kind"`
}

// jobJSON is an import or export job.
type jobJSON struct {
	ID        string `json:"id"`
	Direction string `json:"direction"` // import or export
	Kind      string `json:"kind"`      // catalog, tm or termbase
	Format    string `json:"format"`
	// Mode (imports): dry_run, merge or overwrite.
	Mode  string `json:"mode,omitempty"`
	State string `json:"state"`
	// ProjectID is null for a tenant-wide TMX or TBX job.
	ProjectID *string      `json:"project_id"`
	FileName  string       `json:"file_name"`
	File      *jobFileJSON `json:"file,omitempty"`
	// Options are the API's ImportOptions or ExportOptions.
	Options any `json:"options"`
	// TotalItems and ProcessedItems (imports): entries, units or
	// concepts in the file and applied so far.
	TotalItems     *int               `json:"total_items,omitempty"`
	ProcessedItems *int               `json:"processed_items,omitempty"`
	Summary        *importSummaryJSON `json:"summary,omitempty"`
	// Written (exports): messages, units or concepts written.
	Written *int `json:"written,omitempty"`
	// ReusedJobID (imports): the earlier import of the same file and
	// options whose result this one reuses.
	ReusedJobID     string     `json:"reused_job_id,omitempty"`
	FailureCode     string     `json:"failure_code,omitempty"`
	FailureMessage  string     `json:"failure_message,omitempty"`
	CancelRequested bool       `json:"cancel_requested"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
	FilesDeletedAt  *time.Time `json:"files_deleted_at,omitempty"`

	uploadURL, downloadURL string
}

// importResultJSON is one item of an import's results.
type importResultJSON struct {
	Seq    int    `json:"seq"`
	Kind   string `json:"kind"` // message, translation, tm_unit or concept
	Key    string `json:"key"`
	Locale string `json:"locale,omitempty"`
	Status string `json:"status"` // created, updated, unchanged, conflict or invalid
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	// Ref names the item in the format's own terms: an XLIFF fragment
	// identifier, a JSON pointer, a PO msgctxt and msgid, tu[n].
	Ref string `json:"ref,omitempty"`
	// Location is file:line:column, for editors to jump to.
	Location string `json:"location,omitempty"`
}

func toFile(f *remote.IntegrationFile) *jobFileJSON {
	if f == nil {
		return nil
	}
	return &jobFileJSON{Size: f.Size, SHA256: f.Sha256, ContentType: f.ContentType}
}

func toCountsJSON(c remote.ImportCounts) importCountsJSON {
	return importCountsJSON{Created: c.Created, Updated: c.Updated, Unchanged: c.Unchanged, Conflict: c.Conflict, Invalid: c.Invalid}
}

func fromImportJob(j remote.ImportJob) jobJSON {
	s := &importSummaryJSON{
		importCountsJSON: importCountsJSON{Created: j.Summary.Created, Updated: j.Summary.Updated, Unchanged: j.Summary.Unchanged,
			Conflict: j.Summary.Conflict, Invalid: j.Summary.Invalid},
		ByKind: map[string]importCountsJSON{},
	}
	for k, c := range j.Summary.ByKind {
		s.ByKind[k] = toCountsJSON(c)
	}
	total, processed := j.TotalItems, j.ProcessedItems
	return jobJSON{ID: j.Id, Direction: "import", Kind: string(j.Kind), Format: string(j.Format), Mode: string(j.Mode),
		State: string(j.State), ProjectID: j.ProjectId, FileName: j.FileName, File: toFile(j.File), Options: j.Options,
		TotalItems: &total, ProcessedItems: &processed, Summary: s, ReusedJobID: derefStr(j.ReusedJobId),
		FailureCode: derefStr(j.FailureCode), FailureMessage: derefStr(j.FailureMessage), CancelRequested: j.CancelRequested,
		CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, ExpiresAt: j.ExpiresAt,
		FilesDeletedAt: j.FilesDeletedAt, uploadURL: derefStr(j.UploadUrl)}
}

func fromExportJob(j remote.ExportJob) jobJSON {
	written := j.Written
	return jobJSON{ID: j.Id, Direction: "export", Kind: string(j.Kind), Format: string(j.Format), State: string(j.State),
		ProjectID: j.ProjectId, FileName: j.FileName, File: toFile(j.File), Options: j.Options, Written: &written,
		FailureCode: derefStr(j.FailureCode), FailureMessage: derefStr(j.FailureMessage), CancelRequested: j.CancelRequested,
		CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, ExpiresAt: j.ExpiresAt,
		FilesDeletedAt: j.FilesDeletedAt, downloadURL: derefStr(j.DownloadUrl)}
}

func toResultJSON(r remote.ImportResult, file string) importResultJSON {
	out := importResultJSON{Seq: r.Seq, Kind: string(r.Kind), Key: r.Key, Locale: derefStr(r.Locale), Status: string(r.Status),
		Code: derefStr(r.Code), Detail: derefStr(r.Detail), Ref: derefStr(r.Ref)}
	if r.Line != nil {
		out.Line = *r.Line
	}
	if r.Column != nil {
		out.Column = *r.Column
	}
	out.Location = fileLocation(file, out.Line, out.Column)
	return out
}

// fileLocation is file:line:column (file:line without a column), the form
// editors and CI annotations jump to; empty without a line.
func fileLocation(file string, line, column int) string {
	if line <= 0 || file == "" {
		return ""
	}
	if column <= 0 {
		return fmt.Sprintf("%s:%d", file, line)
	}
	return fmt.Sprintf("%s:%d:%d", file, line, column)
}

// ── jobs and their results ──────────────────────────────────────────

// Job states.
const (
	jobSucceeded = "succeeded"
	jobFailed    = "failed"
	jobCancelled = "cancelled"
)

func finalIntegrationState(s string) bool {
	return s == jobSucceeded || s == jobFailed || s == jobCancelled
}

// problemStatuses are the results an import reports by default.
var problemStatuses = []string{"conflict", "invalid"}

// importResults reads a job's results: only conflicts and invalid items
// unless all. file names the file in their locations.
func importResults(ctx context.Context, p *project, id, file string, all bool) ([]importResultJSON, error) {
	statuses := problemStatuses
	if all {
		statuses = []string{""}
	}
	out := []importResultJSON{}
	for _, st := range statuses {
		rs, err := p.client.ImportResults(ctx, p.scope.Tenant, id, st)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, toResultJSON(r, file))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func resultsFilter(all bool) string {
	if all {
		return "all"
	}
	return "problems"
}

// waitArgs bound polling.
type waitArgs struct {
	timeout, interval time.Duration
}

// waitForJob polls a job until it is final or the timeout passes,
// printing its progress to stderr (not with --json or --quiet).
func (inv *invocation) waitForJob(ctx context.Context, job jobJSON, get func(context.Context) (jobJSON, error), w waitArgs) (jobJSON, error) {
	deadline := time.Now().Add(w.timeout)
	progress := !inv.json && !inv.quiet
	last := ""
	for {
		if progress {
			if line := "  " + jobProgress(job); line != last {
				if inv.env.ColorOutput {
					fmt.Fprintf(inv.env.Stderr, "\r\x1b[K%s", line)
				} else {
					fmt.Fprintln(inv.env.Stderr, line)
				}
				last = line
			}
		}
		if finalIntegrationState(job.State) {
			if progress && inv.env.ColorOutput {
				fmt.Fprintln(inv.env.Stderr)
			}
			return job, nil
		}
		if time.Now().After(deadline) {
			return job, &Error{Exit: ExitNetwork, Code: "wait_timeout",
				What: fmt.Sprintf("the %s job didn't finish within %s", job.Direction, w.timeout),
				Why:  fmt.Sprintf("job %s is %s", job.ID, jobProgress(job)),
				Fix:  fmt.Sprintf("wait longer (--timeout); it keeps running on the server: `glossa jobs show %s --wait`", job.ID)}
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-time.After(w.interval):
		}
		var err error
		if job, err = get(ctx); err != nil {
			return job, inv.integrationError(err, "can't read the job's progress")
		}
	}
}

// jobProgress is "running, 500/1200 items".
func jobProgress(j jobJSON) string {
	s := strings.ReplaceAll(j.State, "_", " ")
	if j.TotalItems != nil && *j.TotalItems > 0 && !finalIntegrationState(j.State) {
		s += fmt.Sprintf(", %d/%d items", *j.ProcessedItems, *j.TotalItems)
	}
	if j.CancelRequested && !finalIntegrationState(j.State) {
		s += ", cancelling"
	}
	return s
}

// getJob reads an import job, else the export job with that ID.
func getJob(ctx context.Context, p *project, id string) (jobJSON, error) {
	ij, err := p.client.ImportJob(ctx, p.scope.Tenant, id)
	if err == nil {
		return fromImportJob(ij), nil
	}
	if !isNotFound(err) {
		return jobJSON{}, err
	}
	ej, err := p.client.ExportJob(ctx, p.scope.Tenant, id)
	if err != nil {
		return jobJSON{}, err
	}
	return fromExportJob(ej), nil
}

// jobGetter polls one job of a known direction.
func jobGetter(p *project, j jobJSON) func(context.Context) (jobJSON, error) {
	return func(ctx context.Context) (jobJSON, error) {
		if j.Direction == "import" {
			ij, err := p.client.ImportJob(ctx, p.scope.Tenant, j.ID)
			return fromImportJob(ij), err
		}
		ej, err := p.client.ExportJob(ctx, p.scope.Tenant, j.ID)
		return fromExportJob(ej), err
	}
}

func isNotFound(err error) bool {
	var ae *remote.APIError
	return errors.As(err, &ae) && ae.Status == 404
}

// integrationFixes explains the import/export API's problem codes.
var integrationFixes = map[string]string{
	"invalid_format":         "use --format xliff, json, po (import only), tmx or tbx",
	"invalid_mode":           "use --apply (merge), --overwrite or neither (a dry run)",
	"invalid_options":        "drop the options this format doesn't take (the README lists them per format)",
	"project_required":       "XLIFF, JSON and PO belong to a project: check project in glossa.yaml",
	"locale_not_found":       "add the locale to the project first (Studio), or check --locale (`glossa locales` lists them)",
	"file_too_large":         "split the file, or have the server's GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES raised",
	"empty_file":             "the file is empty: check the path",
	"upload_interrupted":     "the upload broke off: run the import again",
	"upload_not_expected":    "the job already has its file: run the import again for a new job",
	"job_not_cancellable":    "it has finished; `glossa jobs show <id>` shows how",
	"export_not_ready":       "wait for the export: `glossa jobs show <id> --wait`",
	"file_expired":           "files are kept for a while (7 days by default): export again",
	"storage_unavailable":    "the server's file storage is unavailable: retry",
	"idempotency_key_reused": "drop the reused Idempotency-Key",
}

// integrationError explains a failed import/export request: input the
// server rejects is a usage error (exit 2), a refusal (permissions,
// conflicts, an expired file) exit 3, both with the server's detail.
func (inv *invocation) integrationError(err error, what string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if fix, ok := integrationFixes[ae.Code]; ok {
		e.Fix = fix
	}
	switch {
	case ae.Status == 400 || ae.Status == 413 || ae.Status == 422 || ae.Code == "locale_not_found":
		e.Exit = ExitUsage
	case ae.Status == 403:
		e.Fix = "use a token with the needed scope, created in Studio: read for exports and jobs; write for imports — " +
			"TMX, TBX, source catalogs and --overwrite need integration.manage (a write token)"
	case ae.Status == 404 && ae.Code == "not_found":
		e.Fix = "check the job ID (`glossa jobs list`) and project in glossa.yaml"
	}
	if ae.Detail != "" && (contains([]string{"400", "409", "410", "413", "422"}, strconv.Itoa(ae.Status)) || ae.Code == "locale_not_found") {
		e.Why = ae.Detail
	}
	return e
}

// ── glossa jobs ─────────────────────────────────────────────────────

type jobsListJSON struct {
	Schema string    `json:"schema"`
	Jobs   []jobJSON `json:"jobs"`
}

type jobsShowJSON struct {
	Schema string  `json:"schema"`
	Job    jobJSON `json:"job"`
	// Results (imports): conflicts and invalid items, or every item
	// with --all-results (ResultsFilter says which).
	Results       []importResultJSON `json:"results"`
	ResultsFilter string             `json:"results_filter"`
}

type jobsCancelJSON struct {
	Schema string  `json:"schema"`
	Job    jobJSON `json:"job"`
}

const jobsUsage = `jobs <action> [flags]

Actions:
  list [--direction import|export] [--state STATE] [--all-projects] [--limit N]
                  import and export jobs, newest first (this project's and the workspace's TMX and TBX jobs;
                  --all-projects: every job of the tenant)
  show <id> [--all-results] [--wait [--timeout 30m] [--poll-interval 1s]]
                  one job; for imports its conflicts and invalid items (--all-results: every item).
                  With --wait it polls until the job finishes and exits like import and export
  cancel <id>     cancel a job (a running import stops after its current batch)`

type jobsArgs struct {
	action, id, direction, state string
	limit                        int
	allProjects, allResults      bool
	wait                         bool
	w                            waitArgs
}

func parseJobsArgs(inv *invocation, args []string) (jobsArgs, error) {
	fs := inv.flags(jobsUsage)
	var a jobsArgs
	fs.StringVar(&a.direction, "direction", "", "list: import or export (default both)")
	fs.StringVar(&a.state, "state", "", "list: awaiting_upload, queued, running, succeeded, failed or cancelled")
	fs.IntVar(&a.limit, "limit", 20, "list: at most this many jobs, newest first (0: all)")
	fs.BoolVar(&a.allProjects, "all-projects", false, "list: every job of the tenant, tenant-wide TMX and TBX jobs included")
	fs.BoolVar(&a.allResults, "all-results", false, "show: every result of an import, not only conflicts and invalid items")
	fs.BoolVar(&a.wait, "wait", false, "show: poll until the job finishes")
	fs.DurationVar(&a.w.timeout, "timeout", 30*time.Minute, "with --wait: give up after this long")
	fs.DurationVar(&a.w.interval, "poll-interval", time.Second, "with --wait: how often to ask for progress")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: list, show or cancel")
	}
	a.action, pos = pos[0], pos[1:]
	if a.w.timeout <= 0 || a.w.interval <= 0 {
		return a, usageError(inv.name, "--timeout and --poll-interval must be positive")
	}
	switch a.action {
	case "list":
		if a.direction != "" && a.direction != "import" && a.direction != "export" {
			return a, usageError(inv.name, "--direction must be import or export, not %q", a.direction)
		}
		if a.state != "" && !contains([]string{"awaiting_upload", "queued", "running", jobSucceeded, jobFailed, jobCancelled}, a.state) {
			return a, usageError(inv.name, "--state must be awaiting_upload, queued, running, succeeded, failed or cancelled, not %q", a.state)
		}
		if a.limit < 0 {
			return a, usageError(inv.name, "--limit must be 0 (all) or more")
		}
		return a, noMore(inv, pos)
	case "show", "cancel":
		if len(pos) != 1 {
			return a, usageError(inv.name, "%s takes the job's ID (`glossa jobs list` shows it)", a.action)
		}
		if !idPattern.MatchString(pos[0]) {
			return a, usageError(inv.name, "%q is not a job ID (`glossa jobs list` shows them)", pos[0])
		}
		a.id = pos[0]
		return a, nil
	}
	return a, usageError(inv.name, "unknown action %q (list, show, cancel)", a.action)
}

func runJobs(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseJobsArgs(inv, args)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	switch a.action {
	case "list":
		return inv.jobsList(ctx, p, a)
	case "show":
		return inv.jobsShow(ctx, p, a)
	}
	return inv.jobsCancel(ctx, p, a)
}

func (inv *invocation) jobsList(ctx context.Context, p *project, a jobsArgs) error {
	f := remote.IntegrationJobFilter{State: a.state}
	if !a.allProjects {
		f.Project = p.scope.Project
	}
	out := jobsListJSON{Schema: "glossa.cli.jobs.list/v1", Jobs: []jobJSON{}}
	if a.direction != "export" {
		js, err := p.client.ImportJobs(ctx, p.scope.Tenant, f, a.limit)
		if err != nil {
			return inv.integrationError(err, "can't list import jobs")
		}
		if !a.allProjects {
			// The workspace's memory and termbase jobs belong to every
			// project; --all-projects has them already.
			ws, err := p.client.WorkspaceImportJobs(ctx, p.scope.Tenant, a.state, a.limit)
			if err != nil {
				return inv.integrationError(err, "can't list the workspace's import jobs")
			}
			js = append(js, ws...)
		}
		for _, j := range js {
			out.Jobs = append(out.Jobs, fromImportJob(j))
		}
	}
	if a.direction != "import" {
		js, err := p.client.ExportJobs(ctx, p.scope.Tenant, f, a.limit)
		if err != nil {
			return inv.integrationError(err, "can't list export jobs")
		}
		if !a.allProjects {
			ws, err := p.client.WorkspaceExportJobs(ctx, p.scope.Tenant, a.state, a.limit)
			if err != nil {
				return inv.integrationError(err, "can't list the workspace's export jobs")
			}
			js = append(js, ws...)
		}
		for _, j := range js {
			out.Jobs = append(out.Jobs, fromExportJob(j))
		}
	}
	// Each listing holds its newest --limit jobs, so the newest --limit
	// of them all are among them.
	sort.SliceStable(out.Jobs, func(i, j int) bool { return out.Jobs[i].CreatedAt.After(out.Jobs[j].CreatedAt) })
	if a.limit > 0 && len(out.Jobs) > a.limit {
		out.Jobs = out.Jobs[:a.limit]
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Jobs) == 0 {
			pr.line("No import or export jobs.")
			return
		}
		rows := [][]string{{"ID", "DIRECTION", "FORMAT", "SCOPE", "MODE", "STATE", "FILE", "CREATED", "RESULT"}}
		for _, j := range out.Jobs {
			scope := "project"
			if j.ProjectID == nil {
				scope = "workspace"
			}
			rows = append(rows, []string{j.ID, j.Direction, j.Format, scope, dash(j.Mode), j.State, dash(j.FileName),
				j.CreatedAt.Local().Format("2006-01-02 15:04"), jobOutcome(j)})
		}
		pr.table(rows)
	})
}

// jobOutcome is "3 created · 1 conflict", "42 written" or the failure.
func jobOutcome(j jobJSON) string {
	switch {
	case j.State == jobFailed:
		return orDefault(j.FailureCode, "failed")
	case j.State != jobSucceeded:
		return "-"
	case j.Written != nil:
		return strconv.Itoa(*j.Written) + " written"
	case j.Summary != nil:
		return countsLine(j.Summary.importCountsJSON)
	}
	return "-"
}

// countsLine is "3 created · 1 conflict" ("nothing" when all are zero).
func countsLine(c importCountsJSON) string {
	var parts []string
	for _, x := range []struct {
		n     int
		label string
	}{{c.Created, "created"}, {c.Updated, "updated"}, {c.Unchanged, "unchanged"}, {c.Conflict, "conflict"}, {c.Invalid, "invalid"}} {
		if x.n > 0 {
			label := x.label
			if x.label == "conflict" && x.n > 1 {
				label = "conflicts"
			}
			parts = append(parts, fmt.Sprintf("%d %s", x.n, label))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " · ")
}

func (inv *invocation) jobsShow(ctx context.Context, p *project, a jobsArgs) error {
	j, err := getJob(ctx, p, a.id)
	if err != nil {
		return inv.integrationError(err, "can't read job "+a.id)
	}
	if a.wait {
		if j, err = inv.waitForJob(ctx, j, jobGetter(p, j), a.w); err != nil {
			return err
		}
	}
	out := jobsShowJSON{Schema: "glossa.cli.jobs.show/v1", Job: j, Results: []importResultJSON{}, ResultsFilter: resultsFilter(a.allResults)}
	if j.Direction == "import" && j.State != "awaiting_upload" {
		if out.Results, err = importResults(ctx, p, j.ID, j.FileName, a.allResults); err != nil {
			return inv.integrationError(err, "can't read the import's results")
		}
	}
	if err := inv.emit(out, func(pr *printer) { printJob(pr, out.Job, out.Results) }); err != nil {
		return err
	}
	if !a.wait {
		return nil
	}
	return jobExit(j)
}

// jobExit is how a finished job ends the command: 4 when it failed or
// was cancelled, 1 when an import has conflicts or invalid items.
func jobExit(j jobJSON) error {
	switch {
	case j.State == jobFailed || j.State == jobCancelled:
		return silentExit(ExitPartial, "job_"+j.State)
	case j.Summary != nil && j.Summary.Conflict+j.Summary.Invalid > 0:
		return silentExit(ExitCheckFailed, "import_problems")
	}
	return nil
}

func (inv *invocation) jobsCancel(ctx context.Context, p *project, a jobsArgs) error {
	var j jobJSON
	ij, err := p.client.CancelImportJob(ctx, p.scope.Tenant, a.id)
	if err == nil {
		j = fromImportJob(ij)
	} else if isNotFound(err) {
		var ej remote.ExportJob
		ej, err = p.client.CancelExportJob(ctx, p.scope.Tenant, a.id)
		j = fromExportJob(ej)
	}
	if err != nil {
		return inv.integrationError(err, "can't cancel job "+a.id)
	}
	out := jobsCancelJSON{Schema: "glossa.cli.jobs.cancel/v1", Job: j}
	return inv.emit(out, func(pr *printer) {
		if j.State == jobCancelled {
			pr.line("%s Cancelled %s job %s", pr.pass(), j.Direction, j.ID)
			return
		}
		pr.line("%s Cancelling %s job %s: it stops after its current batch (what it applied stays applied)", pr.pass(), j.Direction, j.ID)
	})
}

// printJob prints a job and an import's results.
func printJob(pr *printer, j jobJSON, results []importResultJSON) {
	pr.line("%s %s job %s", pr.bold(strings.ToUpper(j.Direction[:1])+j.Direction[1:]), j.Format, j.ID)
	scope := "project"
	if j.ProjectID == nil {
		scope = "tenant"
	}
	fields := []struct{ label, value string }{
		{"state", j.State}, {"mode", j.Mode}, {"kind", j.Kind + " (" + scope + ")"}, {"file", j.FileName},
		{"created", j.CreatedAt.Local().Format(time.RFC3339) + " by " + j.CreatedBy},
		{"expires", j.ExpiresAt.Local().Format(time.RFC3339)},
		{"reused", j.ReusedJobID}, {"failure", strings.TrimSpace(j.FailureCode + " " + j.FailureMessage)},
	}
	if j.File != nil {
		fields = append(fields, struct{ label, value string }{"sha256", j.File.SHA256})
	}
	for _, f := range fields {
		if f.value != "" {
			pr.line("  %-8s %s", f.label, f.value)
		}
	}
	switch {
	case j.Written != nil && j.State == jobSucceeded:
		pr.line("  %-8s %d", "written", *j.Written)
	case j.Summary != nil && j.State != "awaiting_upload":
		pr.line("  %-8s %s", "result", countsLine(j.Summary.importCountsJSON))
	}
	printResults(pr, results)
}

// maxResultsShown bounds the results human output lists.
const maxResultsShown = 50

// printResults lists results as "file:line:column: status kind key …".
func printResults(pr *printer, results []importResultJSON) {
	for i, r := range results {
		if i == maxResultsShown {
			pr.line("  %s", pr.dim(fmt.Sprintf("… and %d more (--json lists all)", len(results)-i)))
			return
		}
		status := r.Status
		switch r.Status {
		case "conflict":
			status = pr.warn(status)
		case "invalid":
			status = pr.bad(status)
		}
		subject := r.Key
		if r.Locale != "" {
			subject = r.Locale + " " + r.Key
		}
		msg := r.Code
		if r.Detail != "" && msg != "" {
			msg += ": "
		}
		msg += r.Detail
		where := ""
		if r.Location != "" {
			where = r.Location + ": "
		}
		pr.line("%s%s %s %s  %s", where, status, r.Kind, subject, msg)
	}
}
