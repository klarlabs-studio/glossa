package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	audit "go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// `glossa audit list` and `glossa audit export` (RFC 0006 §6.2, wave 6)
// read the tenant's audit API: entries, and export jobs whose two files
// verify offline with `glossa audit verify`.

// auditArgs are every flag of `glossa audit`; checkFlags says which a
// subcommand takes.
type auditArgs struct {
	keys                                  listFlag
	from, to, actor, action, project, src string
	limit                                 int
	asCSV                                 bool
	firstSeq, lastSeq                     int64
	out, job                              string
	wait, noWait, force                   bool
	w                                     waitArgs
}

func (a *auditArgs) register(fs *flag.FlagSet) {
	fs.Var(&a.keys, "public-key", "verify, export: a glossa.audit.keys/1 file, or keyId=base64 (repeatable)")
	fs.StringVar(&a.from, "from", "", "list, export: entries from this instant (RFC 3339, or a date, UTC)")
	fs.StringVar(&a.to, "to", "", "list, export: entries before this instant (RFC 3339, or a date, UTC)")
	fs.StringVar(&a.actor, "actor", "", "list: person:<id>, token:<id>, system:<name>, unknown")
	fs.StringVar(&a.action, "action", "", "list: an event type (release.published) or direct action (identity.person.signed_in)")
	fs.StringVar(&a.project, "project", "", "list: only this project's entries (slug or ID)")
	fs.StringVar(&a.src, "source", "", "list: outbox, direct or import")
	fs.IntVar(&a.limit, "limit", 100, "list: at most this many entries, newest first (0: all)")
	fs.BoolVar(&a.asCSV, "csv", false, "list: print CSV (columns: see `glossa audit csv`) instead of a table")
	fs.Int64Var(&a.firstSeq, "first-sequence", 0, "export: the chain's first entry to export")
	fs.Int64Var(&a.lastSeq, "last-sequence", 0, "export: the last entry (default: the chain's head when the job is made)")
	fs.StringVar(&a.out, "out", "", "export: the directory to write; csv: the file to write (default stdout)")
	fs.StringVar(&a.job, "job", "", "export: download an existing export job instead of creating one")
	fs.BoolVar(&a.wait, "wait", true, "export: wait for the job and download both files (the default)")
	fs.BoolVar(&a.noWait, "no-wait", false, "export: return once the job is queued; --job <id> downloads it later")
	fs.BoolVar(&a.force, "force", false, "export: overwrite files already in --out")
	fs.DurationVar(&a.w.timeout, "timeout", 30*time.Minute, "export: give up waiting after this long")
	fs.DurationVar(&a.w.interval, "poll-interval", time.Second, "export: how often to ask for progress")
}

var auditFlagsBySub = map[string][]string{
	"verify": {"public-key"},
	"list":   {"from", "to", "actor", "action", "project", "source", "limit", "csv"},
	"export": {"public-key", "from", "to", "first-sequence", "last-sequence", "out", "job", "wait", "no-wait", "force", "timeout", "poll-interval"},
	"csv":    {"out"},
}

var auditGlobalFlags = []string{"json", "quiet", "no-color", "config"}

// checkFlags refuses a flag that belongs to another subcommand.
func (a *auditArgs) checkFlags(fs *flag.FlagSet, sub string) error {
	allowed, ok := auditFlagsBySub[sub]
	if !ok {
		return nil
	}
	var bad error
	fs.Visit(func(f *flag.Flag) {
		if bad == nil && !contains(allowed, f.Name) && !contains(auditGlobalFlags, f.Name) {
			bad = fmt.Errorf("--%s doesn't apply to audit %s", f.Name, sub)
		}
	})
	return bad
}

// parseAuditTime reads --from and --to: RFC 3339, or a date (midnight UTC).
func parseAuditTime(inv *invocation, flagName, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			t = t.UTC()
			return &t, nil
		}
	}
	return nil, usageError(inv.name, "--%s takes an RFC 3339 time (2026-10-01T09:00:00Z) or a date (2026-10-01), not %q", flagName, v)
}

// ── list ────────────────────────────────────────────────────────────

// auditListJSON is glossa.cli.audit.list/v1: the API's entries, as they
// are (content-free by construction).
type auditListJSON struct {
	Schema  string              `json:"schema"`
	Tenant  string              `json:"tenant"`
	Filter  auditFilterJSON     `json:"filter"`
	Count   int                 `json:"count"`
	Limit   int                 `json:"limit"`
	More    bool                `json:"more"`
	Entries []remote.AuditEntry `json:"entries"`
}

type auditFilterJSON struct {
	From    *time.Time `json:"from"`
	To      *time.Time `json:"to"`
	Actor   string     `json:"actor,omitempty"`
	Action  string     `json:"action,omitempty"`
	Project string     `json:"project,omitempty"`
	Source  string     `json:"source,omitempty"`
}

func (inv *invocation) auditList(ctx context.Context, a auditArgs, pos []string) error {
	if err := noMore(inv, pos); err != nil {
		return err
	}
	switch {
	case a.limit < 0:
		return usageError(inv.name, "--limit takes a number of entries, 0 for all")
	case a.asCSV && inv.json:
		return usageError(inv.name, "--csv and --json are two output formats: pick one")
	case a.src != "" && !contains([]string{"outbox", "direct", "import"}, a.src):
		return usageError(inv.name, "--source is outbox, direct or import, not %q", a.src)
	}
	from, err := parseAuditTime(inv, "from", a.from)
	if err != nil {
		return err
	}
	to, err := parseAuditTime(inv, "to", a.to)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	f := remote.AuditFilter{From: from, To: to, Actor: a.actor, Action: a.action, Source: a.src}
	if a.project != "" {
		target, err := inv.workflowProject(ctx, p, a.project)
		if err != nil {
			return err
		}
		f.Project = target.Id
	}
	// One more than asked for tells whether the trail goes on.
	want := a.limit
	if want > 0 {
		want++
	}
	entries, err := p.client.AuditEntries(ctx, p.scope.Tenant, f, want)
	if err != nil {
		return inv.auditError(err, "can't read the audit trail", "audit.read", "owner and admin")
	}
	more := a.limit > 0 && len(entries) > a.limit
	if more {
		entries = entries[:a.limit]
	}
	if entries == nil {
		entries = []remote.AuditEntry{}
	}
	if a.asCSV {
		return writeAuditEntriesCSV(inv.env.Stdout, entries)
	}
	out := auditListJSON{Schema: "glossa.cli.audit.list/v1", Tenant: p.scope.Tenant, Count: len(entries), Limit: a.limit, More: more, Entries: entries,
		Filter: auditFilterJSON{From: from, To: to, Actor: a.actor, Action: a.action, Project: f.Project, Source: a.src}}
	return inv.emit(out, func(pr *printer) { printAuditList(pr, out) })
}

func printAuditList(pr *printer, d auditListJSON) {
	if len(d.Entries) == 0 {
		pr.line("No audit entries match.")
		return
	}
	rows := [][]string{{"SEQ", "WHEN (UTC)", "ACTOR", "ACTION", "ABOUT", "PROJECT"}}
	for _, e := range d.Entries {
		project := ""
		if e.ProjectId != nil {
			project = *e.ProjectId
		}
		rows = append(rows, []string{fmt.Sprint(e.Sequence), e.OccurredAt.UTC().Format("2006-01-02 15:04:05"), e.Actor, e.Action,
			e.AggregateType + ":" + e.AggregateId, project})
	}
	pr.table(rows)
	if d.More {
		pr.line("%s more entries match; raise --limit (0: all) or narrow the filter", pr.caution())
	}
}

// ── export ──────────────────────────────────────────────────────────

// auditExportJSON is glossa.cli.audit.export/v1.
type auditExportJSON struct {
	Schema string         `json:"schema"`
	Job    auditJobJSON   `json:"job"`
	Waited bool           `json:"waited"`
	Dir    string         `json:"dir,omitempty"`
	Files  []auditFileOut `json:"files"`
	// Verification is `audit verify`'s document when --public-key was
	// given; null otherwise (the export is downloaded, not yet trusted).
	Verification *auditVerifyJSON `json:"verification"`
}

type auditJobJSON struct {
	ID            string     `json:"id"`
	State         string     `json:"state"`
	From          *time.Time `json:"from,omitempty"`
	To            *time.Time `json:"to,omitempty"`
	FirstSequence *int64     `json:"first_sequence,omitempty"`
	LastSequence  *int64     `json:"last_sequence,omitempty"`
	EntryCount    *int64     `json:"entry_count,omitempty"`
	KeyID         string     `json:"key_id,omitempty"`
	FailureCode   string     `json:"failure_code,omitempty"`
	FailureReason string     `json:"failure_message,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
}

type auditFileOut struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
}

func auditJob(j remote.AuditExportJob) auditJobJSON {
	out := auditJobJSON{ID: j.Id, State: string(j.State), From: j.From, To: j.To, FirstSequence: j.FirstSequence, LastSequence: j.LastSequence,
		EntryCount: j.EntryCount, ExpiresAt: j.ExpiresAt}
	if j.KeyId != nil {
		out.KeyID = *j.KeyId
	}
	if j.FailureCode != nil {
		out.FailureCode = *j.FailureCode
	}
	if j.FailureMessage != nil {
		out.FailureReason = *j.FailureMessage
	}
	return out
}

func (a auditArgs) exportBody(inv *invocation) (remote.AuditExportJobInput, error) {
	var body remote.AuditExportJobInput
	from, err := parseAuditTime(inv, "from", a.from)
	if err != nil {
		return body, err
	}
	to, err := parseAuditTime(inv, "to", a.to)
	if err != nil {
		return body, err
	}
	timeRange := from != nil || to != nil
	seqRange := a.firstSeq != 0 || a.lastSeq != 0
	switch {
	case timeRange && seqRange:
		return body, usageError(inv.name, "an export is a time range (--from, --to) or a sequence range (--first-sequence, --last-sequence), not both")
	case !timeRange && !seqRange:
		return body, usageError(inv.name, "audit export needs a range: --from and --to, or --first-sequence (and --last-sequence)")
	case timeRange && (from == nil || to == nil):
		return body, usageError(inv.name, "a time range needs both --from and --to")
	case timeRange && !to.After(*from):
		return body, usageError(inv.name, "--to must be after --from")
	case seqRange && a.firstSeq < 1:
		return body, usageError(inv.name, "--first-sequence takes the chain position of the first entry (1 or more)")
	case seqRange && a.lastSeq != 0 && a.lastSeq < a.firstSeq:
		return body, usageError(inv.name, "--last-sequence must not be before --first-sequence")
	}
	if timeRange {
		body.From, body.To = from, to
		return body, nil
	}
	body.FirstSequence = &a.firstSeq
	if a.lastSeq != 0 {
		body.LastSequence = &a.lastSeq
	}
	return body, nil
}

func (inv *invocation) auditExport(ctx context.Context, a auditArgs, pos []string) error {
	if err := noMore(inv, pos); err != nil {
		return err
	}
	if a.noWait {
		a.wait = false
	}
	switch {
	case a.w.timeout <= 0 || a.w.interval <= 0:
		return usageError(inv.name, "--timeout and --poll-interval must be positive")
	case a.wait && a.out == "":
		return usageError(inv.name, "audit export needs --out <directory> to write entries.jsonl and manifest.json into (or --no-wait)")
	case !a.wait && (a.out != "" || len(a.keys) > 0):
		return usageError(inv.name, "--no-wait downloads nothing: drop --out and --public-key")
	}
	var body remote.AuditExportJobInput
	if a.job != "" {
		if !idPattern.MatchString(a.job) {
			return usageError(inv.name, "--job takes an audit export job's ID")
		}
		for _, name := range []string{"from", "to", "first-sequence", "last-sequence"} {
			if a.flagSet(name) {
				return usageError(inv.name, "--%s shapes a new export; --job downloads one that exists", name)
			}
		}
	} else {
		var err error
		if body, err = a.exportBody(inv); err != nil {
			return err
		}
	}
	// Keys are read before anything is created: a mistake there should
	// not leave a job behind.
	var trusted []audit.PublicKey
	if len(a.keys) > 0 {
		var err error
		if trusted, err = inv.auditKeys(a.keys); err != nil {
			return err
		}
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	var job remote.AuditExportJob
	if a.job != "" {
		job, err = p.client.AuditExportJob(ctx, p.scope.Tenant, a.job)
	} else {
		job, err = p.client.CreateAuditExportJob(ctx, p.scope.Tenant, body, newIdempotencyKey())
	}
	if err != nil {
		return inv.auditError(err, "can't create the audit export", "audit.export", "owner")
	}
	out := auditExportJSON{Schema: "glossa.cli.audit.export/v1", Waited: a.wait, Files: []auditFileOut{}}
	if a.wait {
		if job, err = inv.waitForAuditJob(ctx, p, job, a.w); err != nil {
			return err
		}
	}
	out.Job = auditJob(job)
	if a.wait {
		if job.State == remote.AuditExportJobStateFailed {
			return auditJobFailed(job)
		}
		if out.Dir, out.Files, err = inv.downloadAuditExport(ctx, p, job, a); err != nil {
			return err
		}
		if len(trusted) > 0 {
			report, err := verifyExportDir(inv.resolvePath(a.out), trusted)
			if err != nil {
				return err
			}
			v := auditVerifyDoc(out.Dir, report)
			out.Verification = &v
			if err := inv.emit(out, func(pr *printer) { printAuditExport(pr, out, p.client.Server(), &report) }); err != nil {
				return err
			}
			if !report.OK {
				return silentExit(ExitCheckFailed, "audit_verify_failed")
			}
			return nil
		}
	}
	return inv.emit(out, func(pr *printer) { printAuditExport(pr, out, p.client.Server(), nil) })
}

// flagSet reports whether a flag was given (by its zero-value check;
// the range flags have no meaningful zero).
func (a auditArgs) flagSet(name string) bool {
	switch name {
	case "from":
		return a.from != ""
	case "to":
		return a.to != ""
	case "first-sequence":
		return a.firstSeq != 0
	case "last-sequence":
		return a.lastSeq != 0
	}
	return false
}

func printAuditExport(pr *printer, d auditExportJSON, server string, report *audit.ExportReport) {
	j := d.Job
	if !d.Waited {
		pr.line("%s audit export job %s is %s", pr.pass(), j.ID, j.State)
		pr.line("  download it when it has succeeded: glossa audit export --job %s --out <dir>", j.ID)
		return
	}
	pr.line("%s audit export %s: %s, sequences %s–%s, signed with %s", pr.pass(), j.ID,
		plural(int(derefInt64(j.EntryCount)), "entry", "entries"), seqText(j.FirstSequence), seqText(j.LastSequence), j.KeyID)
	for _, f := range d.Files {
		pr.line("  %s  %s  %s", f.Path, plural(int(f.Size), "byte", "bytes"), pr.dim("sha256 "+f.SHA256))
	}
	if report != nil {
		printAuditVerify(pr, d.Dir, *report, *d.Verification)
		return
	}
	pr.line("%s the files match the server's digests, but nothing here says they are authentic yet", pr.caution())
	pr.line("  save the deployment's audit keys once and pin them:")
	pr.line("    curl -fsS %s/.well-known/glossa-audit-keys.json > audit-keys.json", strings.TrimSuffix(server, "/"))
	pr.line("  then verify offline:")
	pr.line("    glossa audit verify %s --public-key audit-keys.json", d.Dir)
	pr.line("  files are kept until %s", j.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC"))
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func seqText(p *int64) string {
	if p == nil {
		return "?"
	}
	return fmt.Sprint(*p)
}

// waitForAuditJob polls until the job succeeded or failed.
func (inv *invocation) waitForAuditJob(ctx context.Context, p *project, job remote.AuditExportJob, w waitArgs) (remote.AuditExportJob, error) {
	deadline := time.Now().Add(w.timeout)
	progress := !inv.json && !inv.quiet
	last := ""
	for {
		if progress && string(job.State) != last {
			last = string(job.State)
			fmt.Fprintf(inv.env.Stderr, "  audit export %s: %s\n", job.Id, job.State)
		}
		if job.State == remote.AuditExportJobStateSucceeded || job.State == remote.AuditExportJobStateFailed {
			return job, nil
		}
		if time.Now().After(deadline) {
			return job, &Error{Exit: ExitNetwork, Code: "wait_timeout",
				What: fmt.Sprintf("the audit export didn't finish within %s", w.timeout),
				Why:  fmt.Sprintf("job %s is %s", job.Id, job.State),
				Fix:  fmt.Sprintf("it keeps running on the server: `glossa audit export --job %s --out <dir>` downloads it when it has succeeded", job.Id)}
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-time.After(w.interval):
		}
		var err error
		if job, err = p.client.AuditExportJob(ctx, p.scope.Tenant, job.Id); err != nil {
			return job, inv.auditError(err, "can't read the export job's progress", "audit.export", "owner")
		}
	}
}

// auditJobFailed explains a job that ended failed.
func auditJobFailed(j remote.AuditExportJob) error {
	code, msg := "internal", ""
	if j.FailureCode != nil {
		code = *j.FailureCode
	}
	if j.FailureMessage != nil {
		msg = *j.FailureMessage
	}
	e := &Error{Exit: ExitPartial, Code: code, What: "the audit export failed", Where: "export job " + j.Id, Why: msg}
	switch code {
	case "range_not_contiguous":
		e.Exit = ExitUsage
		e.What = "that time range is not one unbroken segment of the audit chain"
		e.Why = "imported v0.3 history was appended to the chain inside it, with older timestamps; an export is always one verifiable segment, so the server won't skip or include entries outside the range"
		e.Fix = "export by sequence: `glossa audit list --json` shows each entry's sequence; then `glossa audit export --first-sequence N --last-sequence M --out <dir>`"
	default:
		e.Fix = "retry; if it persists, check the server's logs for this job"
	}
	return e
}

// downloadAuditExport writes entries.jsonl and manifest.json into
// a.out, each checked against the job's digest and the ETag before it
// is moved into place.
func (inv *invocation) downloadAuditExport(ctx context.Context, p *project, job remote.AuditExportJob, a auditArgs) (string, []auditFileOut, error) {
	dir := inv.resolvePath(a.out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't create the directory", Where: dir, Why: err.Error()}
	}
	files := []struct {
		name string
		meta *remote.AuditExportFile
		ref  string
	}{
		{audit.EntriesFile, job.Entries, fmt.Sprintf("/v1/tenants/%s/audit-export-jobs/%s/file", p.scope.Tenant, job.Id)},
		{audit.ManifestFile, job.Manifest, fmt.Sprintf("/v1/tenants/%s/audit-export-jobs/%s/manifest", p.scope.Tenant, job.Id)},
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f.name)); err == nil && !a.force {
			return "", nil, &Error{Exit: ExitUsage, Code: "output_exists", What: f.name + " is already in " + a.out,
				Why: "an export is evidence; it isn't overwritten silently", Fix: "choose another --out, or pass --force"}
		}
	}
	var out []auditFileOut
	for _, f := range files {
		if f.meta == nil {
			return "", nil, &Error{Exit: ExitNetwork, Code: "file_expired", What: "the export's files are gone",
				Why: "the job has no " + f.name + "; the server deletes them after GLOSSA_AUDIT_EXPORT_RETENTION",
				Fix: "make a new export"}
		}
		ref := f.ref
		if f.meta.DownloadUrl != nil && *f.meta.DownloadUrl != "" {
			ref = *f.meta.DownloadUrl
		}
		got, err := inv.fetchAuditFile(ctx, p, ref, dir, f.name, f.meta.Sha256, job.Id)
		if err != nil {
			return "", nil, err
		}
		out = append(out, got)
	}
	for i := range out {
		out[i].Path = inv.display(filepath.Join(dir, out[i].Name))
	}
	return inv.display(dir), out, nil
}

func (inv *invocation) fetchAuditFile(ctx context.Context, p *project, ref, dir, name, wantSHA, jobID string) (auditFileOut, error) {
	d, err := p.client.DownloadExportFile(ctx, ref)
	if err != nil {
		return auditFileOut{}, inv.auditError(err, "can't download "+name, "audit.export", "owner")
	}
	defer d.Body.Close()
	tmp, err := os.CreateTemp(dir, ".glossa-audit-*")
	if err != nil {
		return auditFileOut{}, &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't write the file", Where: dir, Why: err.Error()}
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), d.Body)
	if err != nil {
		return auditFileOut{}, &Error{Exit: ExitNetwork, Code: "download_interrupted", What: "the download of " + name + " broke off", Why: err.Error(),
			Fix: fmt.Sprintf("download it again: `glossa audit export --job %s --out … --force`", jobID)}
	}
	got := hex.EncodeToString(h.Sum(nil))
	for _, want := range []string{d.ETag, wantSHA} {
		if want != "" && !strings.EqualFold(want, got) {
			return auditFileOut{}, &Error{Exit: ExitNetwork, Code: "download_corrupted", What: name + " is corrupted",
				Why: fmt.Sprintf("its SHA-256 is %s, the server's is %s", got, want),
				Fix: "nothing was written for it; download it again"}
		}
	}
	if d.ETag == "" && wantSHA == "" {
		return auditFileOut{}, &Error{Exit: ExitNetwork, Code: "download_unverifiable", What: name + " can't be verified",
			Why: "the server sent no SHA-256 for it", Fix: "check that glossa-server is up to date"}
	}
	if err := tmp.Close(); err != nil {
		return auditFileOut{}, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return auditFileOut{}, &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't write " + name, Where: dir, Why: err.Error()}
	}
	return auditFileOut{Name: name, Size: n, SHA256: got, Verified: true}, nil
}

// ── errors ──────────────────────────────────────────────────────────

// auditError explains a refused audit request: perm is the permission
// the operation needs and who holds it.
func (inv *invocation) auditError(err error, what, perm, holders string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	switch {
	case ae.Status == 403:
		e.Why = "the credential may not use the audit trail (" + ae.Code + detail(ae) + ")"
		e.Fix = "it needs " + perm + " (" + holders + "), held by a person's session or an owner-issued credential; no API token scope grants it. " +
			"A credential limited to some projects can't export, and lists only its projects' entries"
	case ae.Code == "audit_export_unavailable":
		e.Why = "this deployment has no audit signing key, or has exports switched off"
		e.Fix = "an operator sets GLOSSA_AUDIT_SIGNING_KEY and GLOSSA_AUDIT_EXPORTS_ENABLED=true (platform/README.md, Audit export format); " +
			"verifying an export you already have still works offline"
	case ae.Status == 400 || ae.Status == 422:
		e.Exit = ExitUsage
		if ae.Detail != "" {
			e.Why = ae.Detail
		}
		switch ae.Code {
		case "range_too_long":
			e.Fix = "a time range is at most 31 days and a sequence range 1,000,000 entries; split it into several exports (each one's last_hash is the next one's first_prev_hash)"
		case "sequence_out_of_range":
			e.Fix = "that sequence is past the chain's head; `glossa audit list --limit 1` shows the newest"
		}
	case ae.Code == "export_not_ready":
		e.Why = "the export job hasn't succeeded"
		e.Fix = "wait for it: `glossa audit export --job <id> --out <dir>` waits and downloads"
	case ae.Status == 410:
		e.Why = "the export's files were deleted after the server's retention period (the job stays)"
		e.Fix = "make a new export"
	}
	return e
}
