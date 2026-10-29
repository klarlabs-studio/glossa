package cli

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// `glossa export --format <f>`: an interchange file through the
// export-job API (RFC 0003 §6) — create the job, poll, download the
// file and check it against its SHA-256 (the ETag).

// exportJSON is glossa.cli.export/v1.
type exportJSON struct {
	Schema string  `json:"schema"`
	Format string  `json:"format"`
	Scope  string  `json:"scope"` // project, or tenant (TMX, TBX)
	Job    jobJSON `json:"job"`
	// Waited is false with --no-wait: the job was queued, nothing more.
	Waited bool `json:"waited"`
	// File is what was downloaded; null without it (--no-wait).
	File *downloadJSON `json:"file"`
	// Extracted are the files --unzip wrote.
	Extracted []extractedJSON `json:"extracted"`
}

type downloadJSON struct {
	// Path is where the file was written; empty when --unzip extracted
	// a zip (Extracted lists its files).
	Path        string `json:"path,omitempty"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
	// Verified: the SHA-256 of the bytes received matched the ETag and
	// the job's file.
	Verified bool `json:"verified"`
}

type extractedJSON struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// exportFormats are the formats `export --format` writes.
var exportFormats = []string{"xliff", "json", "tmx", "tbx"}

var exportFormatOptions = map[string][]string{
	"xliff": {"locale", "namespace", "state"},
	"json":  {"locale", "namespace", "state", "layout", "syntax"},
	"tmx":   {"locale", "source-locale", "scope"},
	"tbx":   {"scope"},
}

type exportArgs struct {
	format, layout, syntax, sourceLocale, scope, out, job string
	locales, namespaces, states                           listFlag
	unzip, wait, noWait                                   bool
	w                                                     waitArgs
}

const exportUsage = `export --format xliff|json|tmx|tbx [options] [-o PATH] [--unzip]

Creates an export job, waits for it, downloads the file and checks it against its SHA-256.
Options per format:
  xliff  --locale L... (one document per target locale; none: the source only) --namespace N... --state S...
  json   --locale L... (default the source locale) --namespace N... --state S... --layout flat|nested --syntax mf1|mf2
  tmx    --locale L... (targets) --source-locale L --scope project|tenant
  tbx    --scope project|tenant
--locale, --namespace and --state repeat or take commas; --state defaults to approved.
Several locales come as a zip; --unzip extracts it into -o (a directory).
Exit codes: 0 ok, 2 usage, 3 refused or a corrupted download, 4 the job failed.`

func parseExportArgs(inv *invocation, args []string, usage, format string) (exportArgs, error) {
	fs := inv.flags(usage)
	var a exportArgs
	fs.StringVar(&a.format, "format", "", "xliff, json, tmx or tbx")
	fs.Var(&a.locales, "locale", "xliff: target locales; json: the catalogs' locales (default the source); tmx: target locales")
	fs.Var(&a.namespaces, "namespace", "xliff, json: only these namespaces")
	fs.Var(&a.states, "state", "xliff, json: translations in these review states (default approved)")
	fs.StringVar(&a.layout, "layout", "", "json: flat (default) or nested")
	fs.StringVar(&a.syntax, "syntax", "", "json: mf1 or mf2 (default glossa.yaml's syntax)")
	fs.StringVar(&a.sourceLocale, "source-locale", "", "tmx: only units with this source locale")
	fs.StringVar(&a.scope, "scope", "project", "tmx, tbx: the project's units or concepts, or the whole tenant's (tenant)")
	fs.StringVar(&a.out, "o", "", "where to write the file (default: its name, here); with --unzip a directory")
	fs.StringVar(&a.out, "out", "", "the same as -o")
	fs.BoolVar(&a.unzip, "unzip", false, "extract a zip (several locales) into -o, a directory")
	fs.StringVar(&a.job, "job", "", "download an existing export job's file instead of creating one")
	fs.BoolVar(&a.wait, "wait", true, "wait for the job and download its file (the default)")
	fs.BoolVar(&a.noWait, "no-wait", false, "return once the job is queued; `glossa export --job <id>` downloads it later")
	fs.DurationVar(&a.w.timeout, "timeout", 30*time.Minute, "give up waiting after this long")
	fs.DurationVar(&a.w.interval, "poll-interval", time.Second, "how often to ask for progress")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if err := noMore(inv, pos); err != nil {
		return a, err
	}
	if format != "" {
		if a.format != "" && a.format != format {
			return a, usageError(inv.name, "%s export writes %s files; use `glossa export --format %s` for others", inv.name, format, a.format)
		}
		a.format = format
	}
	if a.noWait {
		a.wait = false
	}
	switch {
	case a.job != "":
		if !idPattern.MatchString(a.job) {
			return a, usageError(inv.name, "--job takes an export job's ID (`glossa jobs list --direction export` shows them)")
		}
		for _, name := range []string{"format", "locale", "namespace", "state", "layout", "syntax", "source-locale", "scope"} {
			if isSet(fs, name) {
				return a, usageError(inv.name, "--%s shapes a new export; --job downloads one that exists", name)
			}
		}
	case a.format == "po":
		return a, usageError(inv.name, "gettext PO is import only: export xliff or json instead")
	case !contains(exportFormats, a.format):
		return a, usageError(inv.name, "--format must be xliff, json, tmx or tbx, not %q", a.format)
	}
	for _, name := range []string{"locale", "namespace", "state", "layout", "syntax", "source-locale", "scope"} {
		if a.job == "" && isSet(fs, name) && !contains(exportFormatOptions[a.format], name) {
			return a, usageError(inv.name, "--%s doesn't apply to %s exports (it takes %s)", name, a.format, optionList(exportFormatOptions[a.format]))
		}
	}
	switch {
	case a.layout != "" && a.layout != "flat" && a.layout != "nested":
		return a, usageError(inv.name, "--layout must be flat or nested, not %q", a.layout)
	case a.syntax != "" && a.syntax != "mf1" && a.syntax != "mf2":
		return a, usageError(inv.name, "--syntax must be mf1 or mf2, not %q", a.syntax)
	case a.scope != "project" && a.scope != "tenant":
		return a, usageError(inv.name, "--scope must be project or tenant, not %q", a.scope)
	case a.w.timeout <= 0 || a.w.interval <= 0:
		return a, usageError(inv.name, "--timeout and --poll-interval must be positive")
	case a.unzip && a.out == "":
		return a, usageError(inv.name, "--unzip needs -o <directory> to extract into")
	case !a.wait && (a.out != "" || a.unzip):
		return a, usageError(inv.name, "--no-wait downloads nothing: drop -o and --unzip")
	}
	return a, nil
}

// options is the export's ExportOptions.
func (a exportArgs) options(inv *invocation, cfgSyntax string) (*remote.ExportOptions, error) {
	var o remote.ExportOptions
	set := false
	locales, err := normalizeLocales(inv, "--locale", a.locales)
	if err != nil {
		return nil, err
	}
	if len(locales) > 0 {
		o.Locales, set = &locales, true
	}
	if ns := splitList(a.namespaces); len(ns) > 0 {
		o.Namespaces, set = &ns, true
	}
	if ss := splitList(a.states); len(ss) > 0 {
		states := make([]remote.ReviewState, len(ss))
		for i, s := range ss {
			if !contains([]string{"draft", "needs_review", "approved", "rejected"}, s) {
				return nil, usageError(inv.name, "--state takes draft, needs_review, approved or rejected, not %q", s)
			}
			states[i] = remote.ReviewState(s)
		}
		o.States, set = &states, true
	}
	if a.layout != "" {
		l := remote.ExportLayout(a.layout)
		o.Layout, set = &l, true
	}
	syntax := a.syntax
	if syntax == "" && a.format == "json" {
		syntax = cfgSyntax
	}
	if syntax != "" {
		s := remote.Syntax(syntax)
		o.Syntax, set = &s, true
	}
	if a.sourceLocale != "" {
		l, err := normalizeLocale(inv, "--source-locale", a.sourceLocale)
		if err != nil {
			return nil, err
		}
		o.SourceLocale, set = &l, true
	}
	if !set {
		return nil, nil
	}
	return &o, nil
}

// splitList flattens repeated and comma-separated values.
func splitList(vs []string) []string {
	var out []string
	for _, v := range vs {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" && !contains(out, part) {
				out = append(out, part)
			}
		}
	}
	return out
}

func runExport(ctx context.Context, inv *invocation, args []string) error {
	return runExportAs(ctx, inv, args, exportUsage, "")
}

// runExportAs is `export` and the `tm export` / `terms export` aliases
// (format fixes the format).
func runExportAs(ctx context.Context, inv *invocation, args []string, usage, format string) error {
	a, err := parseExportArgs(inv, args, usage, format)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	var job jobJSON
	if a.job != "" {
		ej, err := p.client.ExportJob(ctx, p.scope.Tenant, a.job)
		if err != nil {
			return inv.integrationError(err, "can't read export job "+a.job)
		}
		job = fromExportJob(ej)
	} else {
		opts, err := a.options(inv, p.cfg.Syntax)
		if err != nil {
			return err
		}
		var ej remote.ExportJob
		if a.scope == "tenant" {
			// The workspace's own memory or termbase: tm-export-jobs,
			// termbase-export-jobs.
			ej, err = p.client.CreateKnowledgeExportJob(ctx, p.scope.Tenant, a.format, opts, newIdempotencyKey())
		} else {
			body := remote.ExportJobRequest{Format: remote.IntegrationFormat(a.format), Options: opts, ProjectId: &p.scope.Project}
			ej, err = p.client.CreateExportJob(ctx, p.scope.Tenant, body, newIdempotencyKey())
		}
		if err != nil {
			return inv.integrationError(err, "can't create the export job")
		}
		job = fromExportJob(ej)
	}
	out := exportJSON{Schema: "glossa.cli.export/v1", Format: job.Format, Scope: scopeOf(job.ProjectID), Job: job, Waited: a.wait,
		Extracted: []extractedJSON{}}
	if a.wait {
		if out.Job, err = inv.waitForJob(ctx, job, jobGetter(p, job), a.w); err != nil {
			return err
		}
		if out.Job.State == jobSucceeded {
			if out.File, out.Extracted, err = inv.download(ctx, p, out.Job, a); err != nil {
				return err
			}
		}
	}
	if err := inv.emit(out, func(pr *printer) { printExport(pr, out, p.info.Slug) }); err != nil {
		return err
	}
	if !a.wait {
		return nil
	}
	return jobExit(out.Job)
}

// download fetches the export's file into a temporary file next to its
// destination, checks its SHA-256 against the ETag and the job, and
// only then moves it into place (or extracts it with --unzip).
func (inv *invocation) download(ctx context.Context, p *project, j jobJSON, a exportArgs) (*downloadJSON, []extractedJSON, error) {
	name := filepath.Base(orDefault(j.FileName, "export"))
	dest, dir, err := inv.destination(a.out, name, a.unzip)
	if err != nil {
		return nil, nil, err
	}
	ref := j.downloadURL
	if ref == "" {
		ref = fmt.Sprintf("/v1/tenants/%s/export-jobs/%s/file", p.scope.Tenant, j.ID)
	}
	d, err := p.client.DownloadExportFile(ctx, ref)
	if err != nil {
		return nil, nil, inv.integrationError(err, "can't download the export")
	}
	defer d.Body.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't create the directory", Where: dir, Why: err.Error()}
	}
	tmp, err := os.CreateTemp(dir, ".glossa-export-*")
	if err != nil {
		return nil, nil, &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't write the file", Where: dir, Why: err.Error()}
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	progress := inv.transferProgress("Downloading", name, d.Size)
	n, err := io.Copy(io.MultiWriter(tmp, h), &countingReader{r: d.Body, report: progress.update})
	progress.done(err == nil)
	if err != nil {
		return nil, nil, &Error{Exit: ExitNetwork, Code: "download_interrupted", What: "the download broke off", Where: j.downloadURL,
			Why: err.Error(), Fix: fmt.Sprintf("download it again: `glossa export --job %s -o …`", j.ID)}
	}
	got := hex.EncodeToString(h.Sum(nil))
	file := &downloadJSON{Name: name, Size: n, SHA256: got}
	if j.File != nil {
		file.ContentType = j.File.ContentType
	}
	if err := verifyDownload(got, d.ETag, j); err != nil {
		return nil, nil, err
	}
	file.Verified = true
	if err := tmp.Close(); err != nil {
		return nil, nil, err
	}
	if a.unzip && isZip(file) {
		extracted, err := unzipInto(tmp.Name(), dir)
		for i := range extracted {
			extracted[i].Path = inv.display(extracted[i].Path)
		}
		return file, extracted, err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return nil, nil, &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't write the file", Where: dest, Why: err.Error()}
	}
	file.Path = inv.display(dest)
	return file, []extractedJSON{}, nil
}

// verifyDownload compares the bytes' SHA-256 with the ETag and the
// job's recorded digest; either may be missing, not both.
func verifyDownload(got, etag string, j jobJSON) error {
	var want []string
	if etag != "" {
		want = append(want, etag)
	}
	if j.File != nil && j.File.SHA256 != "" {
		want = append(want, j.File.SHA256)
	}
	if len(want) == 0 {
		return &Error{Exit: ExitNetwork, Code: "download_unverifiable", What: "the download can't be verified",
			Why: "the server sent no SHA-256 (ETag) for it", Fix: "check that glossa-server is up to date"}
	}
	for _, w := range want {
		if !strings.EqualFold(w, got) {
			return &Error{Exit: ExitNetwork, Code: "download_corrupted", What: "the downloaded file is corrupted",
				Why: fmt.Sprintf("its SHA-256 is %s, the server's is %s", got, w),
				Fix: fmt.Sprintf("nothing was written; download it again: `glossa export --job %s -o …`", j.ID)}
		}
	}
	return nil
}

func isZip(f *downloadJSON) bool {
	return f.ContentType == "application/zip" || strings.HasSuffix(strings.ToLower(f.Name), ".zip")
}

// destination is where the file goes and the directory it's written
// in: -o (a directory with --unzip, or an existing one, takes the file
// by its name), else the file's name in the working directory.
func (inv *invocation) destination(out, name string, unzip bool) (string, string, error) {
	if out == "" {
		return filepath.Join(inv.env.Dir, name), inv.env.Dir, nil
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(inv.env.Dir, out)
	}
	if info, err := os.Stat(out); unzip || (err == nil && info.IsDir()) {
		if err == nil && !info.IsDir() {
			return "", "", usageError(inv.name, "-o %s is a file; --unzip extracts into a directory", out)
		}
		return filepath.Join(out, name), out, nil
	}
	return out, filepath.Dir(out), nil
}

// display is path relative to the working directory when it's inside.
func (inv *invocation) display(path string) string {
	if rel, err := filepath.Rel(inv.env.Dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// unzipInto extracts the zip at path into dir; entries that would land
// outside it are refused.
func unzipInto(path, dir string) ([]extractedJSON, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, &Error{Exit: ExitNetwork, Code: "download_corrupted", What: "the download isn't a zip", Why: err.Error()}
	}
	defer zr.Close()
	out := []extractedJSON{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.FromSlash(f.Name)
		if !filepath.IsLocal(name) {
			return out, &Error{Exit: ExitNetwork, Code: "download_corrupted", What: "the zip names a file outside the directory", Where: f.Name}
		}
		dest := filepath.Join(dir, name)
		n, err := extractZipFile(f, dest)
		if err != nil {
			return out, &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't extract " + f.Name, Where: dest, Why: err.Error()}
		}
		out = append(out, extractedJSON{Path: dest, Size: n})
	}
	return out, nil
}

func extractZipFile(f *zip.File, dest string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	w, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, rc)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func printExport(pr *printer, out exportJSON, project string) {
	j := out.Job
	source := project
	if out.Scope == "tenant" {
		source = "the tenant"
	}
	switch {
	case !out.Waited:
		pr.line("%s Export job %s (%s from %s) is %s", pr.pass(), j.ID, j.Format, source, j.State)
		pr.line("  %s", pr.dim(fmt.Sprintf("`glossa export --job %s -o <path>` downloads it when it has finished", j.ID)))
		return
	case j.State == jobFailed:
		pr.line("%s Export job %s failed: %s", pr.fail(), j.ID, strings.TrimSpace(j.FailureCode+" "+j.FailureMessage))
		if j.FailureCode == "not_representable" {
			pr.line("  %s", pr.dim("the format can't express these messages: try --syntax mf2 (JSON) or other --locale values"))
		}
		return
	case j.State == jobCancelled:
		pr.line("%s Export job %s was cancelled", pr.fail(), j.ID)
		return
	}
	written := 0
	if j.Written != nil {
		written = *j.Written
	}
	f := out.File
	if len(out.Extracted) > 0 {
		pr.line("%s Exported %s from %s (%d written), SHA-256 verified; extracted %s:", pr.pass(), j.Format, source, written,
			plural(len(out.Extracted), "file", "files"))
		for _, e := range out.Extracted {
			pr.line("  %s (%s)", e.Path, humanBytes(int(e.Size)))
		}
		return
	}
	pr.line("%s Exported %s from %s (%d written) to %s (%s, SHA-256 verified)", pr.pass(), j.Format, source, written, f.Path, humanBytes(int(f.Size)))
}
