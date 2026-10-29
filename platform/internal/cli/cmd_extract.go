package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/codegen"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/extract"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

type extractFlags struct {
	strict, upload              bool
	application, commit, branch string
}

// extractReport is what extract found, beyond the document: the human
// output's unknown and unused lists.
type extractReport struct {
	doc     extract.Document
	files   int
	skipped []extract.Skipped
	// unknown are keys used in code that the source catalog lacks, with
	// their first location and how many more there are.
	unknown []unknownKey
	// unused are catalog keys no code uses (dynamic keys can't be seen).
	unused      []string
	catalogSize int
	uploaded    *remote.UploadedBuild
}

type unknownKey struct {
	key   string
	first extract.Usage
	more  int
}

func runExtract(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("extract [--strict] [--upload] [--application SLUG] [--commit SHA] [--branch NAME]")
	var f extractFlags
	fs.BoolVar(&f.strict, "strict", false, "exit 1 when code uses message keys the catalog doesn't have")
	fs.BoolVar(&f.upload, "upload", false, "send the usages to the server (the Context API)")
	fs.StringVar(&f.application, "application", "", "the application's slug (default: extract.application, GLOSSA_APPLICATION)")
	fs.StringVar(&f.commit, "commit", "", "the commit the usages are from (default: CI, else git HEAD)")
	fs.StringVar(&f.branch, "branch", "", "the branch the usages are from (default: CI, else git)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	rep, err := scanProject(cfg)
	if err != nil {
		return err
	}
	if inv.json || f.upload {
		if rep.doc, err = inv.usagesHeader(ctx, cfg, &f, rep.doc); err != nil {
			return err
		}
	}
	if f.upload {
		if rep.uploaded, err = inv.uploadUsages(ctx, cfg, rep.doc); err != nil {
			return err
		}
	}
	if err := inv.emit(rep.doc, func(p *printer) { printExtract(p, rep) }); err != nil {
		return err
	}
	if f.strict && len(rep.unknown) > 0 {
		return silentExit(ExitCheckFailed, "unknown_messages")
	}
	return nil
}

// scanProject scans the files glossa.yaml selects and compares the
// usages with the source catalog.
func scanProject(cfg *config.Config) (extractReport, error) {
	local, err := loadLocal(cfg)
	if err != nil {
		return extractReport{}, err
	}
	keys := make([]string, 0, len(local.Messages))
	known := map[string]bool{}
	for _, m := range local.Messages {
		keys = append(keys, m.Key)
		known[m.Key] = true
	}
	opts := extract.Options{Include: cfg.Extract.Include, Exclude: cfg.Extract.Exclude, Templates: cfg.Extract.Templates,
		Accessors: extract.Accessors{TS: codegen.TSNames(keys), Go: codegen.GoNames(keys)}}
	if len(opts.Include) == 0 {
		opts.Include, opts.Exclude = defaultExtract.Include, append(opts.Exclude, defaultExtract.Exclude...)
	}
	if opts.Templates == nil {
		opts.Templates = extract.DefaultTemplates
	}
	res, err := extract.Scan(cfg.Dir(), opts)
	if err != nil {
		return extractReport{}, &Error{Exit: ExitUsage, Code: "scan_failed", What: "can't scan the source tree", Where: cfg.Dir(), Why: err.Error(),
			Fix: "check extract.include, extract.exclude and extract.templates in glossa.yaml"}
	}
	rep := extractReport{doc: extract.NewDocument(extract.Header{}, res.Usages), files: res.Files, skipped: res.Skipped, catalogSize: len(keys)}
	used := map[string]bool{}
	for _, u := range res.Usages {
		used[u.Key] = true
		if known[u.Key] {
			continue
		}
		if n := len(rep.unknown); n > 0 && rep.unknown[n-1].key == u.Key {
			rep.unknown[n-1].more++
			continue
		}
		rep.unknown = append(rep.unknown, unknownKey{key: u.Key, first: u})
	}
	for _, k := range keys {
		if !used[k] {
			rep.unused = append(rep.unused, k)
		}
	}
	return rep, nil
}

// usagesHeader fills in the document's application, commit, branch and
// tool: flags first, then the environment and glossa.yaml, then CI and
// git.
func (inv *invocation) usagesHeader(ctx context.Context, cfg *config.Config, f *extractFlags, doc extract.Document) (extract.Document, error) {
	app := firstOf(f.application, inv.env.getenv("GLOSSA_APPLICATION"), cfg.Extract.Application)
	h, err := inv.buildHeader(ctx, cfg, "usages", app, "extract.application", f.commit, f.branch)
	if err != nil {
		return doc, err
	}
	doc.Application, doc.Commit, doc.Branch, doc.Tool = h.Application, h.Commit, h.Branch, h.Tool
	return doc, nil
}

// buildHeader is a build's identity (application, commit, branch, tool)
// for an upload of what (usages, captures): application as the caller
// resolved it (appField names its glossa.yaml field), commit and branch
// from the flags, then CI and git.
func (inv *invocation) buildHeader(ctx context.Context, cfg *config.Config, what, application, appField, commit, branch string) (extract.Header, error) {
	h := extract.Header{Application: application}
	if h.Application == "" {
		return h, &Error{Exit: ExitUsage, Code: "application_required", What: "which application do these " + what + " belong to?",
			Why: "a " + what + " document is one build of one application",
			Fix: "pass --application <slug>, or set " + appField + " in glossa.yaml"}
	}
	if !config.ValidApplication(h.Application) {
		return h, usageError(inv.name, "--application %q is not an application slug (lowercase letters, digits and -, e.g. web)", h.Application)
	}
	detected := buildRef{}
	if commit == "" || branch == "" {
		detected = detectBuild(ctx, inv.env.getenv, cfg.Dir())
	}
	h.Commit = strings.ToLower(firstOf(commit, detected.Commit))
	h.Branch = strings.TrimPrefix(firstOf(branch, detected.Branch), "refs/heads/")
	switch {
	case h.Commit == "":
		return h, &Error{Exit: ExitUsage, Code: "commit_unknown", What: "which commit are these " + what + " from?",
			Why: "no CI commit variable is set and git can't read HEAD here",
			Fix: "run inside the git checkout, or pass --commit <sha> (GLOSSA_COMMIT)"}
	case !commitPattern.MatchString(h.Commit):
		return h, usageError(inv.name, "--commit %q is not a full commit ID (40 or 64 hex digits)", h.Commit)
	case h.Branch == "":
		return h, &Error{Exit: ExitUsage, Code: "branch_unknown", What: "which branch are these " + what + " from?",
			Why: "no CI branch variable is set and HEAD is detached",
			Fix: "pass --branch <name> (GLOSSA_BRANCH)"}
	case !validBranch(h.Branch):
		return h, usageError(inv.name, "--branch %q is not a branch name", h.Branch)
	}
	h.Tool = extract.Tool{Name: "glossa", Version: semver(inv.env.Version)}
	return h, nil
}

var semverPattern = regexp.MustCompile(`^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// semver is the CLI's version as the document's semantic version; a
// development build is 0.0.0-dev.
func semver(v string) string {
	v = strings.TrimPrefix(v, "v")
	if semverPattern.MatchString(v) {
		return v
	}
	return "0.0.0-dev"
}

// uploadUsages sends the document to the project's context builds as an
// extract build, like `glossa context push` would.
func (inv *invocation) uploadUsages(ctx context.Context, cfg *config.Config, doc extract.Document) (*remote.UploadedBuild, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return inv.pushUsages(ctx, cfg, raw, remote.SourceExtract)
}

func printExtract(p *printer, rep extractReport) {
	usages := rep.doc.Usages
	distinct := map[string]bool{}
	for _, u := range usages {
		distinct[u.Key] = true
	}
	p.line("%s %s of %s in %s", p.pass(), plural(len(usages), "usage", "usages"),
		plural(len(distinct), "message", "messages"), plural(rep.files, "file", "files"))
	for _, s := range rep.skipped {
		p.line("%s skipped %s: %s", p.caution(), s.File, s.Reason)
	}
	if len(rep.unknown) == 0 {
		p.line("%s every used key is in the catalog", p.pass())
	} else {
		p.line("%s %s not in the catalog:", p.fail(), plural(len(rep.unknown), "key", "keys"))
		for _, u := range rep.unknown {
			more := ""
			if u.more > 0 {
				more = p.dim(fmt.Sprintf(" (+%d more)", u.more))
			}
			p.line("  %s  %s:%d%s", u.key, u.first.File, u.first.Line, more)
		}
	}
	if len(rep.unused) > 0 {
		p.line("%s %d of %d catalog messages look unused (dynamic keys aren't detected):", p.caution(), len(rep.unused), rep.catalogSize)
		for i, k := range rep.unused {
			if i == maxFindingsPerGroup {
				p.line("  %s", p.dim(fmt.Sprintf("… and %d more", len(rep.unused)-i)))
				break
			}
			p.line("  %s", k)
		}
	}
	if up := rep.uploaded; up != nil {
		printUpload(p, rep.doc.Application, rep.doc.Commit, rep.doc.Branch, up)
	}
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
