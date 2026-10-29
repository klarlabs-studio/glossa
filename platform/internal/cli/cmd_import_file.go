package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// `glossa import --format <f> <file>`: an interchange file through the
// import-job API (RFC 0003 §6) — create the job, PUT the file to its
// upload_url, poll, read the results.

// fileImportJSON is glossa.cli.import.job/v1.
type fileImportJSON struct {
	Schema string `json:"schema"`
	Format string `json:"format"`
	// Mode is dry_run (the default), merge (--apply) or overwrite.
	Mode   string        `json:"mode"`
	DryRun bool          `json:"dry_run"`
	Scope  string        `json:"scope"` // project, or tenant (TMX, TBX)
	File   localFileJSON `json:"file"`
	Job    jobJSON       `json:"job"`
	// Waited is false with --no-wait: the job was queued, nothing more.
	Waited bool `json:"waited"`
	// Results are the conflicts and invalid items (every item with
	// --all-results; ResultsFilter says which), in file order.
	Results       []importResultJSON `json:"results"`
	ResultsFilter string             `json:"results_filter"`
}

type localFileJSON struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// importFormats are the formats `import --format` reads.
var importFormats = []string{"xliff", "json", "po", "tmx", "tbx"}

// fileImportFlags are `import --format`'s flags.
type fileImportFlags struct {
	format, locale, namespace, syntax, state, pluralVariable, scope string
	apply, overwrite, dryRun, noWait, allResults                    bool
	wait                                                            bool
	w                                                               waitArgs
}

func (f *fileImportFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.format, "format", "", "the file's format: xliff, json, po, tmx or tbx")
	fs.BoolVar(&f.apply, "apply", false, "write the import (mode merge: never replaces an approved translation, a differing source or concept)")
	fs.BoolVar(&f.overwrite, "overwrite", false, "write the import, making the stored state the file's (needs integration.manage)")
	fs.StringVar(&f.locale, "locale", "", "json: the file's locale (default the source locale: a source catalog); po, xliff: the translations' locale (default its Language header, its trgLang)")
	fs.StringVar(&f.namespace, "namespace", "", "json, po: every message's namespace (default default)")
	fs.StringVar(&f.syntax, "syntax", "", "json: the messages' syntax, mf1 or mf2 (default glossa.yaml's syntax); xliff: mf1 reads other tools' plain units as ICU")
	fs.StringVar(&f.state, "state", "", "json, po: the review state of translations the file doesn't state (json default needs_review, po approved)")
	fs.StringVar(&f.pluralVariable, "plural-variable", "", "po: the count variable of plurals (default count)")
	fs.StringVar(&f.scope, "scope", "project", "tmx, tbx: import into the project or tenant-wide (tenant)")
	fs.BoolVar(&f.wait, "wait", true, "wait for the job to finish (the default)")
	fs.BoolVar(&f.noWait, "no-wait", false, "return once the file is uploaded; `glossa jobs show <id> --wait` follows it")
	fs.BoolVar(&f.allResults, "all-results", false, "report every item, not only conflicts and invalid ones")
	fs.DurationVar(&f.w.timeout, "timeout", 30*time.Minute, "give up waiting after this long")
	fs.DurationVar(&f.w.interval, "poll-interval", time.Second, "how often to ask for progress")
}

// fileImportFlagNames are the flags only `import --format` takes.
var fileImportFlagNames = []string{"format", "apply", "overwrite", "locale", "namespace", "syntax", "state", "plural-variable",
	"scope", "wait", "no-wait", "all-results", "timeout", "poll-interval"}

// formatOptions says which option flags each format takes.
var importFormatOptions = map[string][]string{
	"xliff": {"locale", "syntax"},
	"json":  {"locale", "namespace", "syntax", "state"},
	"po":    {"locale", "namespace", "state", "plural-variable"},
	"tmx":   {"scope"},
	"tbx":   {"scope"},
}

// fileImport is a validated `import --format` request.
type fileImport struct {
	fileImportFlags
	path, display string
	mode          string
}

// parseFileImport validates the flags and the file argument.
func parseFileImport(inv *invocation, fs *flag.FlagSet, f fileImportFlags, pos []string) (fileImport, error) {
	in := fileImport{fileImportFlags: f}
	if !contains(importFormats, f.format) {
		return in, usageError(inv.name, "--format must be xliff, json, po, tmx or tbx, not %q", f.format)
	}
	for _, name := range []string{"locale", "namespace", "syntax", "state", "plural-variable", "scope"} {
		if isSet(fs, name) && !contains(importFormatOptions[f.format], name) {
			return in, usageError(inv.name, "--%s doesn't apply to %s imports (it takes %s)", name, f.format, optionList(importFormatOptions[f.format]))
		}
	}
	switch {
	case len(pos) == 0:
		return in, usageError(inv.name, "import --format %s takes the file to import", f.format)
	case len(pos) > 1:
		return in, usageError(inv.name, "import takes one file, got %d", len(pos))
	case f.dryRun && (f.apply || f.overwrite):
		return in, usageError(inv.name, "--dry-run and --apply/--overwrite exclude each other")
	case f.w.timeout <= 0 || f.w.interval <= 0:
		return in, usageError(inv.name, "--timeout and --poll-interval must be positive")
	case f.syntax != "" && f.syntax != "mf1" && (f.syntax != "mf2" || f.format == "xliff"):
		if f.format == "xliff" {
			return in, usageError(inv.name, "--syntax for XLIFF must be mf1, not %q", f.syntax)
		}
		return in, usageError(inv.name, "--syntax must be mf1 or mf2, not %q", f.syntax)
	case f.state != "" && !contains([]string{"draft", "needs_review", "approved", "rejected"}, f.state):
		return in, usageError(inv.name, "--state must be draft, needs_review, approved or rejected, not %q", f.state)
	case f.scope != "project" && f.scope != "tenant":
		return in, usageError(inv.name, "--scope must be project or tenant, not %q", f.scope)
	}
	var err error
	if f.locale != "" {
		if in.locale, err = normalizeLocale(inv, "--locale", f.locale); err != nil {
			return in, err
		}
	}
	in.mode = "dry_run"
	switch {
	case f.overwrite:
		in.mode = "overwrite"
	case f.apply:
		in.mode = "merge"
	}
	if f.noWait {
		in.wait = false
	}
	in.display = pos[0]
	in.path = pos[0]
	if !filepath.IsAbs(in.path) {
		in.path = filepath.Join(inv.env.Dir, in.path)
	}
	return in, nil
}

func optionList(names []string) string {
	flags := make([]string, len(names))
	for i, n := range names {
		flags[i] = "--" + n
	}
	return strings.Join(flags, ", ")
}

// runFileImport is `import --format` and the `tm import` / `terms
// import` aliases (format fixes the format).
func runFileImport(ctx context.Context, inv *invocation, args []string, usage, format string) error {
	fs := inv.flags(usage)
	var f fileImportFlags
	f.register(fs)
	fs.BoolVar(&f.dryRun, "dry-run", false, "check the file and report what an import would do (the default)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if f.format != "" && f.format != format {
		return usageError(inv.name, "%s import reads %s files; use `glossa import --format %s` for others", inv.name, format, f.format)
	}
	f.format = format
	in, err := parseFileImport(inv, fs, f, pos)
	if err != nil {
		return err
	}
	return inv.importFile(ctx, in)
}

func (inv *invocation) importFile(ctx context.Context, in fileImport) error {
	info, err := os.Stat(in.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &Error{Exit: ExitUsage, Code: "file_not_found", What: "no such file", Where: in.display, Fix: "check the path"}
	case err != nil:
		return &Error{Exit: ExitUsage, Code: "file_unreadable", What: "can't read the file", Where: in.display, Why: err.Error()}
	case !info.Mode().IsRegular():
		return &Error{Exit: ExitUsage, Code: "file_unreadable", What: "not a regular file", Where: in.display}
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	body := in.request(p, filepath.Base(in.path))
	created, err := in.create(ctx, p, body)
	if err != nil {
		return inv.integrationError(err, "can't create the import job")
	}
	local, job, err := inv.upload(ctx, p, in, created, info.Size())
	if err != nil {
		// The job would wait for its file for a day; nothing depends on it.
		_, _ = p.client.CancelImportJob(context.WithoutCancel(ctx), p.scope.Tenant, created.Id)
		return err
	}
	if in.wait {
		if job, err = inv.waitForJob(ctx, job, jobGetter(p, job), in.w); err != nil {
			return err
		}
	}
	out := fileImportJSON{Schema: "glossa.cli.import.job/v1", Format: in.format, Mode: in.mode, DryRun: in.mode == "dry_run",
		Scope: scopeOf(body.ProjectId), File: local, Job: job, Waited: in.wait, Results: []importResultJSON{},
		ResultsFilter: resultsFilter(in.allResults)}
	if in.wait {
		if out.Results, err = importResults(ctx, p, job.ID, in.display, in.allResults); err != nil {
			return inv.integrationError(err, "can't read the import's results")
		}
	}
	if err := inv.emit(out, func(pr *printer) { printFileImport(pr, out, p.info.Slug) }); err != nil {
		return err
	}
	if !in.wait {
		return nil
	}
	return jobExit(job)
}

func scopeOf(project *string) string {
	if project == nil {
		return "tenant"
	}
	return "project"
}

// create creates the import job: a tenant-wide TMX or TBX file goes
// to the workspace's own routes (tm-import-jobs, termbase-import-jobs),
// everything else to import-jobs.
func (in fileImport) create(ctx context.Context, p *project, body remote.ImportJobRequest) (remote.ImportJob, error) {
	if body.ProjectId == nil {
		return p.client.CreateKnowledgeImportJob(ctx, p.scope.Tenant, in.format,
			remote.KnowledgeImportJobRequest{Mode: body.Mode, FileName: body.FileName}, newIdempotencyKey())
	}
	return p.client.CreateImportJob(ctx, p.scope.Tenant, body, newIdempotencyKey())
}

// request is the import job to create.
func (in fileImport) request(p *project, name string) remote.ImportJobRequest {
	mode := remote.ImportMode(in.mode)
	body := remote.ImportJobRequest{Format: remote.IntegrationFormat(in.format), Mode: &mode, FileName: &name}
	if in.scope != "tenant" {
		body.ProjectId = &p.scope.Project
	}
	var o remote.ImportOptions
	set := false
	str := func(v string) *string {
		if v == "" {
			return nil
		}
		set = true
		return &v
	}
	o.Locale = str(in.locale)
	o.Namespace = str(in.namespace)
	o.PluralVariable = str(in.pluralVariable)
	syntax := in.syntax
	if syntax == "" && in.format == "json" {
		syntax = p.cfg.Syntax // the project's local catalogs' syntax, when glossa.yaml says
	}
	if s := str(syntax); s != nil {
		st := remote.Syntax(*s)
		o.Syntax = &st
	}
	if s := str(in.state); s != nil {
		st := remote.ReviewState(*s)
		o.State = &st
	}
	if set {
		body.Options = &o
	}
	return body
}

// upload PUTs the file to the job's upload_url with progress on
// stderr, and checks that the server stored what was sent.
func (inv *invocation) upload(ctx context.Context, p *project, in fileImport, created remote.ImportJob, size int64) (localFileJSON, jobJSON, error) {
	local := localFileJSON{Path: in.display, Size: size}
	file, err := os.Open(in.path)
	if err != nil {
		return local, jobJSON{}, &Error{Exit: ExitUsage, Code: "file_unreadable", What: "can't read the file", Where: in.display, Why: err.Error()}
	}
	defer file.Close()
	h := sha256.New()
	progress := inv.transferProgress("Uploading", filepath.Base(in.path), size)
	body := &countingReader{r: io.TeeReader(file, h), report: progress.update}
	ref := derefStr(created.UploadUrl)
	if ref == "" {
		ref = fmt.Sprintf("/v1/tenants/%s/import-jobs/%s/file", p.scope.Tenant, created.Id)
	}
	uploaded, err := p.client.UploadImportFile(ctx, ref, body, size)
	progress.done(err == nil)
	if err != nil {
		return local, jobJSON{}, inv.integrationError(err, "can't upload "+in.display)
	}
	local.SHA256 = hex.EncodeToString(h.Sum(nil))
	job := fromImportJob(uploaded)
	if job.File != nil && job.File.SHA256 != "" && !strings.EqualFold(job.File.SHA256, local.SHA256) {
		return local, job, &Error{Exit: ExitNetwork, Code: "upload_corrupted", What: "the server stored a different file",
			Where: in.display, Why: fmt.Sprintf("sent SHA-256 %s, the server has %s", local.SHA256, job.File.SHA256),
			Fix: "run the import again; the job was cancelled"}
	}
	return local, job, nil
}

func printFileImport(pr *printer, out fileImportJSON, project string) {
	j := out.Job
	target := project
	if out.Scope == "tenant" {
		target = "the tenant"
	}
	head := fmt.Sprintf("%s (%s, %s) into %s", out.File.Path, out.Format, humanBytes(int(out.File.Size)), target)
	switch {
	case !out.Waited:
		pr.line("%s Uploaded %s: job %s is %s", pr.pass(), head, j.ID, j.State)
		pr.line("  %s", pr.dim(fmt.Sprintf("`glossa jobs show %s --wait` follows it", j.ID)))
		return
	case out.DryRun:
		pr.line("Dry run: %s — nothing was changed", head)
	case j.Mode == "overwrite":
		pr.line("Imported %s, overwriting", head)
	default:
		pr.line("Imported %s", head)
	}
	if j.ReusedJobID != "" {
		pr.line("  %s", pr.dim(fmt.Sprintf("the same file and options were imported by job %s: its result stands, nothing was applied twice", j.ReusedJobID)))
	}
	switch j.State {
	case jobFailed:
		pr.line("%s job %s failed: %s", pr.fail(), j.ID, strings.TrimSpace(j.FailureCode+" "+j.FailureMessage))
		if j.FailureCode == "target_locale_mismatch" {
			pr.line("  %s", pr.dim("import it as one of the project's locales with --locale (`glossa locales` lists them)"))
		}
	case jobCancelled:
		pr.line("%s job %s was cancelled; what it applied before stays applied", pr.fail(), j.ID)
	}
	if j.Summary != nil && j.State != jobFailed {
		kinds := make([]string, 0, len(j.Summary.ByKind))
		for _, k := range []string{"message", "translation", "tm_unit", "concept"} {
			if _, ok := j.Summary.ByKind[k]; ok {
				kinds = append(kinds, k)
			}
		}
		for _, k := range kinds {
			c := j.Summary.ByKind[k]
			mark := pr.pass()
			if c.Conflict+c.Invalid > 0 {
				mark = pr.fail()
			}
			label := map[string]string{"message": "messages", "translation": "translations", "tm_unit": "TM units", "concept": "concepts"}[k]
			verb := ""
			if out.DryRun {
				verb = "would be "
			}
			pr.line("%s %s: %s%s", mark, label, verb, countsLine(c))
		}
		if len(kinds) == 0 {
			pr.line("%s nothing in the file", pr.pass())
		}
	}
	printResults(pr, out.Results)
	if out.DryRun && j.State == jobSucceeded {
		pr.line("")
		pr.line("%s", pr.dim("Run again with --apply to import it (--overwrite to replace what differs)."))
	}
}

// ── transfer progress ───────────────────────────────────────────────

// countingReader reports how much has been read.
type countingReader struct {
	r      io.Reader
	n      int64
	report func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if n > 0 && c.report != nil {
		c.report(c.n)
	}
	return n, err
}

// progress prints a transfer's progress on stderr: updated in place on
// a terminal, a line at the start and the end otherwise; nothing with
// --json or --quiet.
type progress struct {
	inv        *invocation
	verb, name string
	size       int64
	last       time.Time
	lastN      int64
}

func (inv *invocation) transferProgress(verb, name string, size int64) *progress {
	return &progress{inv: inv, verb: verb, name: name, size: size}
}

func (p *progress) on() bool { return !p.inv.json && !p.inv.quiet }

func (p *progress) update(n int64) {
	p.lastN = n
	if !p.on() || !p.inv.env.ColorOutput || time.Since(p.last) < 100*time.Millisecond {
		return
	}
	p.last = time.Now()
	p.print(n)
}

func (p *progress) print(n int64) {
	line := fmt.Sprintf("  %s %s %s", p.verb, p.name, humanBytes(int(n)))
	if p.size > 0 {
		line += fmt.Sprintf(" / %s (%d%%)", humanBytes(int(p.size)), n*100/p.size)
	}
	fmt.Fprintf(p.inv.env.Stderr, "\r\x1b[K%s", line)
}

func (p *progress) done(ok bool) {
	if !p.on() {
		return
	}
	if p.inv.env.ColorOutput {
		p.print(p.lastN)
		fmt.Fprintln(p.inv.env.Stderr)
		return
	}
	if ok {
		fmt.Fprintf(p.inv.env.Stderr, "  %s %s (%s)\n", p.pastVerb(), p.name, humanBytes(int(p.lastN)))
	}
}

// pastVerb is "uploaded" for "Uploading".
func (p *progress) pastVerb() string {
	return strings.ToLower(strings.TrimSuffix(p.verb, "ing")) + "ed"
}
