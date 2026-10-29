package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// Workflows (RFC 0006 §2): an organisation's localization process as a
// versioned `glossa.workflow/v1` document, linted and stored by the
// server, bound to projects, and run as instances whose transition log
// says what happened and as whom. This group is the CLI's side of the
// Workflow API, so a definition can live in a repository and reach the
// server from CI: lint it in a pull request, push it on merge.

// workflowSchema tags every `glossa workflow --json` document; `action`
// says which subcommand wrote it (cmd/glossa/README.md, "JSON output").
const workflowSchema = "glossa.cli.workflow/v1"

// ── output shapes ───────────────────────────────────────────────────

// workflowDefinitionJSON is a definition at a version. ProjectID is
// null for a tenant-wide definition, which every project may bind.
type workflowDefinitionJSON struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Subject   string     `json:"subject"`
	ProjectID *string    `json:"project_id"`
	Version   int        `json:"version"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type workflowFindingJSON struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	State    string `json:"state,omitempty"`
	Event    string `json:"event,omitempty"`
	Message  string `json:"message"`
}

type workflowBindingJSON struct {
	ID           string `json:"id"`
	DefinitionID string `json:"definition_id"`
	// Definition is the definition's name, when it could be read.
	Definition string    `json:"definition,omitempty"`
	Subject    string    `json:"subject"`
	Locales    []string  `json:"locales"`
	Namespace  string    `json:"namespace,omitempty"`
	Position   int       `json:"position"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

type workflowInstanceJSON struct {
	ID                string    `json:"id"`
	DefinitionID      string    `json:"definition_id"`
	Definition        string    `json:"definition,omitempty"`
	DefinitionVersion int       `json:"definition_version"`
	Subject           string    `json:"subject"`
	SubjectID         string    `json:"subject_id"`
	Locale            string    `json:"locale,omitempty"`
	State             string    `json:"state"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type workflowTransitionJSON struct {
	Seq     int64  `json:"seq"`
	From    string `json:"from"`
	Event   string `json:"event"`
	To      string `json:"to"`
	Outcome string `json:"outcome"`
	Guards  []struct {
		Guard  string `json:"guard"`
		Passed bool   `json:"passed"`
	} `json:"guards"`
	Actions []struct {
		Name    string `json:"name"`
		Outcome string `json:"outcome"`
		Detail  string `json:"detail,omitempty"`
	} `json:"actions"`
	Actor         string    `json:"actor"`
	OutboxEventID string    `json:"outbox_event_id,omitempty"`
	At            time.Time `json:"at"`
}

type workflowVersionJSON struct {
	Version   int       `json:"version"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type workflowLintDoc struct {
	Schema   string                `json:"schema"`
	Action   string                `json:"action"`
	File     string                `json:"file"`
	Valid    bool                  `json:"valid"`
	Errors   int                   `json:"errors"`
	Warnings int                   `json:"warnings"`
	Findings []workflowFindingJSON `json:"findings"`
}

type workflowPushDoc struct {
	Schema string `json:"schema"`
	Action string `json:"action"`
	File   string `json:"file"`
	// Result is created, saved (a new version), unchanged or invalid.
	Result     string                  `json:"result"`
	Definition *workflowDefinitionJSON `json:"definition"`
	Findings   []workflowFindingJSON   `json:"findings"`
}

type workflowPullDoc struct {
	Schema     string                 `json:"schema"`
	Action     string                 `json:"action"`
	File       string                 `json:"file"`
	Definition workflowDefinitionJSON `json:"definition"`
	Version    int                    `json:"version"`
}

type workflowListDoc struct {
	Schema      string                   `json:"schema"`
	Action      string                   `json:"action"`
	ProjectID   string                   `json:"project_id"`
	Definitions []workflowDefinitionJSON `json:"definitions"`
}

type workflowShowDoc struct {
	Schema     string                 `json:"schema"`
	Action     string                 `json:"action"`
	Definition workflowDefinitionJSON `json:"definition"`
	// Version is the version Document is, the latest unless --version.
	Version  int                   `json:"version"`
	Document map[string]any        `json:"document"`
	Versions []workflowVersionJSON `json:"versions"`
	// Bindings are the project's bindings of this definition.
	ProjectID string                `json:"project_id"`
	Bindings  []workflowBindingJSON `json:"bindings"`
}

type workflowBindDoc struct {
	Schema string `json:"schema"`
	Action string `json:"action"`
	// Result is created or unchanged (bind); removed (unbind).
	Result  string              `json:"result"`
	Binding workflowBindingJSON `json:"binding"`
}

type workflowBindingsDoc struct {
	Schema    string                `json:"schema"`
	Action    string                `json:"action"`
	ProjectID string                `json:"project_id"`
	Bindings  []workflowBindingJSON `json:"bindings"`
}

type workflowInstancesDoc struct {
	Schema    string                 `json:"schema"`
	Action    string                 `json:"action"`
	ProjectID string                 `json:"project_id"`
	Instances []workflowInstanceJSON `json:"instances"`
}

type workflowLogDoc struct {
	Schema      string                   `json:"schema"`
	Action      string                   `json:"action"`
	Instance    workflowInstanceJSON     `json:"instance"`
	Transitions []workflowTransitionJSON `json:"transitions"`
}

// ── arguments ───────────────────────────────────────────────────────

const workflowUsage = `workflow <action> [flags]

Actions:
  lint <file>                  lint a glossa.workflow/v1 document on the server; saves nothing,
                               exits 1 when a save would be refused
  push <file> [--project P] [--if-version N]
                               create the definition, or save its next version; an unchanged
                               document saves nothing. Without --project it is tenant-wide
  pull <name> [--version N] [-o file]
                               the document of the latest (or Nth) version, to stdout or a file
  export <name> [--version N] [-o file]
                               the same: the glossa.workflow/v1 document alone — no IDs, no
                               tenant — so another tenant or server can import it
  import <file> [--project P] [--if-version N]
                               the same as push: create the definition, or save its next version,
                               with the server's lint findings shown
  list                         the definitions the project may bind (the tenant's and its own)
  show <name> [--version N]    a definition, its versions and where the project binds it
  bind <name> [--locales de,fr] [--namespace ns]
                               bind a definition to the project, optionally narrowed
  unbind <name> [--locales …] [--namespace …] | unbind --binding <id>
                               remove a binding
  bindings                     the project's bindings, in creation order
  instances [--status active|finished] [--definition D] [--locale L] [--message key]
                               the project's workflow instances
  log <instance-id>            an instance's transition log, oldest first
  rebase <instance-id> [--version N]
                               move a running instance to the definition's latest (or Nth)
                               version, keeping its state; refused when that version lacks it

<file> is JSON, or YAML when it ends in .yaml or .yml; - is stdin. <name> is a definition's
name or ID. --project (slug or ID) is the project acted in; glossa.yaml's by default.

Saving, binding and rebasing need the workflows.manage permission: an API token holds it only with the
opt-in workflows scope, and a GitHub Actions credential never does.`

type workflowArgs struct {
	action, target                      string
	project, out, namespace, binding    string
	status, locale, message, definition string
	version, ifVersion                  int
	locales                             listFlag
}

func parseWorkflowArgs(inv *invocation, args []string) (workflowArgs, error) {
	fs := inv.flags(workflowUsage)
	var a workflowArgs
	fs.StringVar(&a.project, "project", "", "the project, by slug or ID (push: the project the definition belongs to; default tenant-wide)")
	fs.IntVar(&a.version, "version", 0, "pull, export, show: this version instead of the latest; rebase: the version to move to")
	fs.IntVar(&a.ifVersion, "if-version", 0, "push, import: save only if the latest version is still N (the one you edited)")
	fs.StringVar(&a.out, "out", "", "pull, export: write the document to this file (.yaml/.yml: YAML)")
	fs.StringVar(&a.out, "o", "", "pull, export: shorthand for --out")
	fs.Var(&a.locales, "locales", "bind, unbind: the binding's locales (repeatable or comma-separated)")
	fs.StringVar(&a.namespace, "namespace", "", "bind, unbind: the binding's namespace")
	fs.StringVar(&a.binding, "binding", "", "unbind: the binding's ID")
	fs.StringVar(&a.status, "status", "", "instances: active or finished")
	fs.StringVar(&a.definition, "definition", "", "instances: only this definition's (name or ID)")
	fs.StringVar(&a.locale, "locale", "", "instances: only this locale's")
	fs.StringVar(&a.message, "message", "", "instances: only this message's (by key)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: lint, push, pull, export, import, list, show, bind, unbind, bindings, instances, log or rebase")
	}
	a.action, pos = pos[0], pos[1:]
	one := func(what string) error {
		if len(pos) == 0 {
			return usageError(inv.name, "%s takes %s", a.action, what)
		}
		a.target = pos[0]
		return noMore(inv, pos[1:])
	}
	switch a.action {
	case "lint", "push", "import":
		return a, one("a file (`-` for stdin)")
	case "pull", "export", "show", "bind":
		return a, one("a definition's name or ID")
	case "log", "rebase":
		return a, one("an instance ID (`glossa workflow instances` lists them)")
	case "unbind":
		if a.binding != "" {
			return a, noMore(inv, pos)
		}
		return a, one("a definition's name or ID, or --binding <id>")
	case "list", "bindings":
		return a, noMore(inv, pos)
	case "instances":
		if a.status != "" && a.status != "active" && a.status != "finished" {
			return a, usageError(inv.name, "--status is active or finished, not %q", a.status)
		}
		if a.locale != "" {
			l, err := normalizeLocale(inv, "--locale", a.locale)
			if err != nil {
				return a, err
			}
			a.locale = l
		}
		return a, noMore(inv, pos)
	}
	return a, usageError(inv.name, "unknown action %q (lint, push, pull, export, import, list, show, bind, unbind, bindings, instances, log, rebase)", a.action)
}

func runWorkflow(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseWorkflowArgs(inv, args)
	if err != nil {
		return err
	}
	if a.version < 0 || a.ifVersion < 0 {
		return usageError(inv.name, "--version and --if-version are version numbers, 1 or more")
	}
	// Reading the file first: a typo in the path is a usage error, not
	// a round trip to the server.
	var doc map[string]any
	if a.action == "lint" || a.action == "push" || a.action == "import" {
		if doc, err = inv.readWorkflowFile(a.target); err != nil {
			return err
		}
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	switch a.action {
	case "lint":
		return inv.workflowLint(ctx, p, a, doc)
	case "push", "import":
		// Import is push by its portable name: a definition document
		// exported from another tenant or server is created here, or
		// saved as the next version of the one of its name.
		return inv.workflowPush(ctx, p, a, doc)
	}
	target, err := inv.workflowProject(ctx, p, a.project)
	if err != nil {
		return err
	}
	s := remote.Scope{Tenant: p.scope.Tenant, Project: target.Id}
	switch a.action {
	case "pull", "export":
		return inv.workflowPull(ctx, p, s, a)
	case "rebase":
		return inv.workflowRebase(ctx, p, s, a)
	case "list":
		return inv.workflowList(ctx, p, s)
	case "show":
		return inv.workflowShow(ctx, p, s, a)
	case "bind":
		return inv.workflowBind(ctx, p, s, a)
	case "unbind":
		return inv.workflowUnbind(ctx, p, s, a)
	case "bindings":
		return inv.workflowBindings(ctx, p, s)
	case "instances":
		return inv.workflowInstances(ctx, p, s, a)
	}
	return inv.workflowLog(ctx, p, s, a.target)
}

// workflowProject is --project, or glossa.yaml's project.
func (inv *invocation) workflowProject(ctx context.Context, p *project, ref string) (remote.Project, error) {
	target, err := inv.targetProject(ctx, p, ref)
	var ce *Error
	if err != nil && errors.As(err, &ce) && ce.Code != "project_not_found" && ce.Err != nil {
		return remote.Project{}, inv.workflowError(ce.Err, "can't read the project", "")
	}
	return target, err
}

// ── the document ────────────────────────────────────────────────────

// maxWorkflowFile is the API's limit on a document (256 KiB), with
// room for YAML's more verbose spelling.
const maxWorkflowFile = 1 << 20

// readWorkflowFile reads a glossa.workflow/v1 document: JSON, or YAML
// for a .yaml/.yml file. It checks only that it is one JSON object; the
// server's lint is the judge of everything else, so the CLI can never
// disagree with it.
func (inv *invocation) readWorkflowFile(file string) (map[string]any, error) {
	var (
		raw   []byte
		err   error
		where = "stdin"
	)
	if file == "-" {
		raw, err = io.ReadAll(io.LimitReader(inv.env.Stdin, maxWorkflowFile))
	} else {
		where = inv.resolvePath(file)
		raw, err = os.ReadFile(where) //nolint:gosec // a path the person named
	}
	if err != nil {
		return nil, &Error{Exit: ExitUsage, Code: "workflow_file_unreadable", What: "can't read the workflow file",
			Where: where, Why: err.Error(), Fix: "`glossa workflow pull <name> -o review.json` writes one to start from"}
	}
	doc, err := decodeWorkflowDocument(where, raw)
	if err != nil {
		return nil, &Error{Exit: ExitUsage, Code: "invalid_workflow_file", What: "the workflow file is not a document",
			Where: where, Why: err.Error(),
			Fix: "a glossa.workflow/v1 document is one JSON (or YAML) object: {schema, name, subject, chart, guards, actions}"}
	}
	return doc, nil
}

func decodeWorkflowDocument(where string, raw []byte) (map[string]any, error) {
	var v any
	if ext := strings.ToLower(filepath.Ext(where)); ext == ".yaml" || ext == ".yml" {
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		// Through JSON, so a YAML document is the same value a JSON one
		// is (numbers, nested maps) and compares equal to what the
		// server stored.
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more than one document")
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not an object")
	}
	return doc, nil
}

// documentName is the document's name, for finding the definition it
// is a version of.
func documentName(doc map[string]any) string {
	name, _ := doc["name"].(string)
	return name
}

// sameDocument reports whether two documents are the same value,
// whatever their key order and spacing.
func sameDocument(a, b map[string]any) bool {
	norm := func(m map[string]any) any {
		raw, _ := json.Marshal(m)
		var v any
		_ = json.Unmarshal(raw, &v)
		return v
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// ── lint and push ───────────────────────────────────────────────────

func (inv *invocation) workflowLint(ctx context.Context, p *project, a workflowArgs, doc map[string]any) error {
	res, err := p.client.LintWorkflow(ctx, p.scope.Tenant, doc)
	if err != nil {
		return inv.workflowError(err, "can't lint "+a.target, "")
	}
	out := workflowLintDoc{Schema: workflowSchema, Action: "lint", File: a.target, Valid: res.Valid,
		Findings: workflowFindings(res.Findings)}
	out.Errors, out.Warnings = countWorkflowFindings(out.Findings)
	if err := inv.emit(out, func(pr *printer) {
		if out.Valid {
			pr.line("%s %s is valid: a save would be accepted%s", pr.pass(), a.target, notesSuffix(len(out.Findings)))
		} else {
			pr.line("%s %s: %s, %s — a save would be refused", pr.fail(), a.target,
				plural(out.Errors, "error", "errors"), plural(out.Warnings, "warning", "warnings"))
		}
		printWorkflowFindings(pr, out.Findings)
	}); err != nil {
		return err
	}
	if !out.Valid {
		return silentExit(ExitCheckFailed, "invalid_workflow")
	}
	return nil
}

func notesSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return " (" + plural(n, "note", "notes") + ")"
}

func (inv *invocation) workflowPush(ctx context.Context, p *project, a workflowArgs, doc map[string]any) error {
	name := documentName(doc)
	if name == "" {
		return &Error{Exit: ExitUsage, Code: "invalid_workflow_file", What: "the workflow document has no name",
			Where: a.target, Why: "a definition is found, and created, by its name",
			Fix: "set name to the definition's name (lowercase letters, digits and hyphens)"}
	}
	var scope remote.Project
	if a.project != "" {
		var err error
		if scope, err = inv.workflowProject(ctx, p, a.project); err != nil {
			return err
		}
	}
	existing, found, err := inv.definitionInScope(ctx, p, scope.Id, name)
	if err != nil {
		return err
	}
	out := workflowPushDoc{Schema: workflowSchema, Action: a.action, File: a.target, Findings: []workflowFindingJSON{}}
	var saved remote.WorkflowDefinitionSaved
	switch {
	case !found && a.ifVersion > 0:
		return &Error{Exit: ExitNetwork, Code: "precondition_failed",
			What: fmt.Sprintf("workflow %q has no version %d", name, a.ifVersion),
			Why:  "--if-version names the version this document was edited from, and no definition of that name exists in this scope",
			Fix:  "drop --if-version to create it, or check --project"}
	case !found:
		saved, err = p.client.CreateWorkflowDefinition(ctx, p.scope.Tenant, scope.Id, doc)
		out.Result = "created"
	default:
		def, etag, err := p.client.WorkflowDefinition(ctx, p.scope.Tenant, existing.Id)
		if err != nil {
			return inv.workflowError(err, "can't read workflow "+name, name)
		}
		latest, err := p.client.WorkflowDefinitionVersion(ctx, p.scope.Tenant, def.Id, def.Version)
		if err != nil {
			return inv.workflowError(err, "can't read workflow "+name, name)
		}
		if sameDocument(doc, latest.Document) {
			out.Result, out.Definition = "unchanged", ptr(definitionJSON(def))
			return inv.emit(out, func(pr *printer) {
				pr.line("%s %s is unchanged: version %d is this document, nothing was saved", pr.pass(), name, def.Version)
			})
		}
		if a.ifVersion > 0 {
			etag = strconv.Quote(strconv.Itoa(a.ifVersion))
		}
		saved, err = p.client.SaveWorkflowDefinitionVersion(ctx, p.scope.Tenant, def.Id, etag, doc)
		out.Result = "saved"
		if err != nil {
			return inv.pushError(err, out, name, def.Version, a.ifVersion)
		}
	}
	if err != nil {
		return inv.pushError(err, out, name, 0, 0)
	}
	out.Definition = ptr(savedJSON(saved))
	out.Findings = workflowFindings(saved.Findings)
	return inv.emit(out, func(pr *printer) {
		d := out.Definition
		verb := "Saved " + d.Name + " as version " + strconv.Itoa(d.Version)
		if out.Result == "created" {
			verb = "Created " + d.Name + " (version 1)"
		}
		pr.line("%s %s, %s", pr.pass(), verb, definitionScope(d.ProjectID, scope))
		printWorkflowFindings(pr, out.Findings)
		pr.line("  %s", pr.dim("running instances stay on the version they started with; `glossa workflow bind "+d.Name+"` binds it to a project"))
	})
}

// pushError explains a refused save. A document the server refuses
// prints its findings (with --json, as the push document) and exits 2;
// a save another overtook says so, never overwriting it.
func (inv *invocation) pushError(err error, out workflowPushDoc, name string, latest, ifVersion int) error {
	var inval *remote.InvalidWorkflowError
	if errors.As(err, &inval) {
		out.Result, out.Findings = "invalid", workflowFindings(inval.Findings)
		errs, _ := countWorkflowFindings(out.Findings)
		if inv.json {
			if err := writeJSON(inv.env.Stdout, out); err != nil {
				return err
			}
			return silentExit(ExitUsage, "invalid_workflow")
		}
		printWorkflowFindings(&printer{w: inv.env.Stderr, color: inv.out.color}, out.Findings)
		return &Error{Exit: ExitUsage, Code: "invalid_workflow", What: "the server refused " + out.File,
			Why: fmt.Sprintf("%s in the document (above); nothing was saved", plural(max(errs, 1), "problem", "problems")),
			Fix: "fix them and push again; `glossa workflow lint " + out.File + "` checks without saving"}
	}
	var ae *remote.APIError
	if errors.As(err, &ae) && ae.Status == 412 {
		base := latest
		if ifVersion > 0 {
			base = ifVersion
		}
		return &Error{Exit: ExitNetwork, Code: ae.Code, What: fmt.Sprintf("can't save workflow %s: version %d is not the latest", name, base),
			Where: ae.Method + " " + ae.URL,
			Why:   "someone saved a newer version since the one this push is based on; saving would silently replace their change, so nothing was saved",
			Fix:   "`glossa workflow pull " + name + " -o <file>` fetches the latest, merge your change into it, and push again"}
	}
	return inv.workflowError(err, "can't save workflow "+name, name)
}

// definitionInScope finds the live definition named name in one scope:
// the project's own (project set) or the tenant's.
func (inv *invocation) definitionInScope(ctx context.Context, p *project, project, name string) (remote.WorkflowDefinition, bool, error) {
	defs, err := p.client.WorkflowDefinitions(ctx, p.scope.Tenant, project)
	if err != nil {
		return remote.WorkflowDefinition{}, false, inv.workflowError(err, "can't list the workflow definitions", "")
	}
	for _, d := range defs {
		if d.Name == name && derefStr(d.ProjectId) == project {
			return d, true, nil
		}
	}
	return remote.WorkflowDefinition{}, false, nil
}

// definitionNamed finds the definition ref names — an ID, or a name —
// among those the project may bind. A name both the tenant and the
// project define is ambiguous, and the ID decides.
func (inv *invocation) definitionNamed(ctx context.Context, p *project, s remote.Scope, ref string) (remote.WorkflowDefinition, []remote.WorkflowDefinition, error) {
	defs, err := p.client.WorkflowDefinitions(ctx, p.scope.Tenant, s.Project)
	if err != nil {
		return remote.WorkflowDefinition{}, nil, inv.workflowError(err, "can't list the workflow definitions", "")
	}
	var named []remote.WorkflowDefinition
	for _, d := range defs {
		if d.Id == ref {
			return d, defs, nil
		}
		if d.Name == ref {
			named = append(named, d)
		}
	}
	switch len(named) {
	case 1:
		return named[0], defs, nil
	case 0:
		names := make([]string, 0, len(defs))
		for _, d := range defs {
			names = append(names, d.Name)
		}
		fix := "`glossa workflow push <file>` creates one"
		if len(names) > 0 {
			fix = "pass one of: " + strings.Join(names, ", ") + " (or its ID)"
		}
		return remote.WorkflowDefinition{}, nil, &Error{Exit: ExitUsage, Code: "workflow_not_found",
			What: fmt.Sprintf("no workflow definition %q", ref),
			Why:  "the project may bind the tenant's definitions and its own, and none of them has that name or ID", Fix: fix}
	}
	ids := make([]string, 0, len(named))
	for _, d := range named {
		ids = append(ids, d.Id+" ("+definitionScope(d.ProjectId, remote.Project{})+")")
	}
	return remote.WorkflowDefinition{}, nil, &Error{Exit: ExitUsage, Code: "workflow_ambiguous",
		What: fmt.Sprintf("two definitions are named %q", ref),
		Why:  "the tenant and the project each define one: " + strings.Join(ids, ", "),
		Fix:  "pass the definition's ID instead of its name"}
}

func definitionScope(projectID *string, scope remote.Project) string {
	switch {
	case projectID == nil || *projectID == "":
		return "tenant-wide"
	case scope.Id == *projectID && scope.Slug != "":
		return "project " + string(scope.Slug)
	}
	return "project " + *projectID
}

// ── pull, list, show ────────────────────────────────────────────────

func (inv *invocation) workflowPull(ctx context.Context, p *project, s remote.Scope, a workflowArgs) error {
	def, _, err := inv.definitionNamed(ctx, p, s, a.target)
	if err != nil {
		return err
	}
	n := def.Version
	if a.version > 0 {
		n = a.version
	}
	v, err := p.client.WorkflowDefinitionVersion(ctx, p.scope.Tenant, def.Id, n)
	if err != nil {
		return inv.workflowError(err, fmt.Sprintf("can't read version %d of %s", n, def.Name), def.Name)
	}
	if a.out == "" || a.out == "-" {
		// The document itself, which is what push reads back.
		return writeJSON(inv.env.Stdout, v.Document)
	}
	path := inv.resolvePath(a.out)
	var raw []byte
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".yaml" || ext == ".yml" {
		raw, err = yaml.Marshal(v.Document)
	} else {
		var b bytes.Buffer
		err = writeJSON(&b, v.Document)
		raw = b.Bytes()
	}
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(path, raw, 0o644) //nolint:gosec // a definition is no secret
	}
	if err != nil {
		return &Error{Exit: ExitUsage, Code: "workflow_file_unwritable", What: "can't write the workflow file", Where: path, Why: err.Error()}
	}
	out := workflowPullDoc{Schema: workflowSchema, Action: a.action, File: a.out, Definition: definitionJSON(def), Version: n}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Wrote %s version %d to %s", pr.pass(), def.Name, n, a.out)
		if a.action == "export" {
			pr.line("  %s", pr.dim("`glossa workflow import "+a.out+"` creates it in another tenant or server, or saves it as the next version of the one called "+def.Name))
			return
		}
		pr.line("  %s", pr.dim("edit it and `glossa workflow push "+a.out+" --if-version "+strconv.Itoa(n)+"` saves the next version, unless someone else saved one first"))
	})
}

// ── rebase ──────────────────────────────────────────────────────────

type workflowRebaseDoc struct {
	Schema      string               `json:"schema"`
	Action      string               `json:"action"`
	FromVersion int                  `json:"from_version"`
	Instance    workflowInstanceJSON `json:"instance"`
}

// workflowRebase moves a running instance to a newer version of its
// definition (RFC 0006 §2.3). The If-Match is the version the instance
// runs on as read here, so a rebase someone else made in between is
// refused, never repeated.
func (inv *invocation) workflowRebase(ctx context.Context, p *project, s remote.Scope, a workflowArgs) error {
	cur, err := p.client.WorkflowInstance(ctx, s, a.target)
	if err != nil {
		return inv.workflowError(err, "can't read instance "+a.target, "")
	}
	it, err := p.client.RebaseWorkflowInstance(ctx, s, a.target, cur.DefinitionVersion, a.version)
	if err != nil {
		return inv.rebaseError(err, cur)
	}
	names := inv.definitionNames(ctx, p, s)
	out := workflowRebaseDoc{Schema: workflowSchema, Action: "rebase", FromVersion: cur.DefinitionVersion, Instance: instanceJSON(it, names)}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Rebased %s from version %d to %d, in %s", pr.pass(), it.Id, cur.DefinitionVersion, it.DefinitionVersion, stateLabel(it.State))
		pr.line("  %s", pr.dim("nothing ran again: what the state asked for stands, and the next event moves it on the new version"))
	})
}

func stateLabel(state string) string {
	if state == "" {
		return "no state yet (it starts on the new version at its next event)"
	}
	return "state " + state
}

// rebaseError explains a refused rebase: the server's reason is precise
// (the state the target lacks, a finished instance), so it is passed on
// with what to do about it.
func (inv *invocation) rebaseError(err error, cur remote.WorkflowInstance) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return inv.workflowError(err, "can't rebase instance "+cur.Id, "")
	}
	e := &Error{Exit: ExitNetwork, Code: ae.Code, What: "can't rebase instance " + cur.Id, Where: ae.Method + " " + ae.URL, Why: ae.Detail}
	switch ae.Code {
	case "workflow_rebase_state_missing", "workflow_rebase_state_final":
		e.Exit = ExitUsage
		e.Fix = "keep a waiting state called " + cur.State + " in the version you rebase onto, or pick one that has it with --version"
	case "invalid_workflow_rebase":
		e.Exit = ExitUsage
		e.Fix = fmt.Sprintf("it runs on version %d; `glossa workflow show <definition>` lists the newer ones", cur.DefinitionVersion)
	case "workflow_instance_finished":
		e.Exit = ExitUsage
		e.Fix = "a finished instance stays on the version it finished on"
	case "precondition_failed":
		e.Why = "someone rebased it since it was read; nothing was changed"
		e.Fix = "run it again to rebase from the version it runs on now"
	default:
		return inv.workflowError(err, "can't rebase instance "+cur.Id, "")
	}
	return e
}

func (inv *invocation) workflowList(ctx context.Context, p *project, s remote.Scope) error {
	defs, err := p.client.WorkflowDefinitions(ctx, p.scope.Tenant, s.Project)
	if err != nil {
		return inv.workflowError(err, "can't list the workflow definitions", "")
	}
	out := workflowListDoc{Schema: workflowSchema, Action: "list", ProjectID: s.Project, Definitions: []workflowDefinitionJSON{}}
	for _, d := range defs {
		out.Definitions = append(out.Definitions, definitionJSON(d))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Definitions) == 0 {
			pr.line("No workflow definitions: projects behave as they did before workflows (no instances).")
			pr.line("  %s", pr.dim("`glossa workflow push review.json` saves one"))
			return
		}
		rows := [][]string{{"NAME", "VERSION", "SUBJECT", "SCOPE", "ID"}}
		for _, d := range out.Definitions {
			rows = append(rows, []string{d.Name, "v" + strconv.Itoa(d.Version), d.Subject, definitionScope(d.ProjectID, remote.Project{}), d.ID})
		}
		pr.table(rows)
	})
}

func (inv *invocation) workflowShow(ctx context.Context, p *project, s remote.Scope, a workflowArgs) error {
	def, _, err := inv.definitionNamed(ctx, p, s, a.target)
	if err != nil {
		return err
	}
	versions, err := p.client.WorkflowDefinitionVersions(ctx, p.scope.Tenant, def.Id)
	if err != nil {
		return inv.workflowError(err, "can't read the versions of "+def.Name, def.Name)
	}
	n := def.Version
	if a.version > 0 {
		n = a.version
	}
	out := workflowShowDoc{Schema: workflowSchema, Action: "show", Definition: definitionJSON(def), Version: n,
		Versions: []workflowVersionJSON{}, ProjectID: s.Project, Bindings: []workflowBindingJSON{}}
	for _, v := range versions {
		out.Versions = append(out.Versions, workflowVersionJSON{Version: v.Version, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt})
		if v.Version == n {
			out.Document = v.Document
		}
	}
	if out.Document == nil {
		// Past the first page, or a version number the list didn't
		// have: ask for it, and let the server say whether it exists.
		v, err := p.client.WorkflowDefinitionVersion(ctx, p.scope.Tenant, def.Id, n)
		if err != nil {
			return inv.workflowError(err, fmt.Sprintf("can't read version %d of %s", n, def.Name), def.Name)
		}
		out.Document = v.Document
	}
	bindings, err := p.client.WorkflowBindings(ctx, s)
	if err != nil {
		return inv.workflowError(err, "can't list the project's bindings", "")
	}
	for _, b := range bindings {
		if b.DefinitionId == def.Id {
			out.Bindings = append(out.Bindings, bindingJSON(b, map[string]string{def.Id: def.Name}))
		}
	}
	return inv.emit(out, func(pr *printer) {
		d := out.Definition
		pr.line("%s v%d · %s · %s", pr.bold(d.Name), d.Version, d.Subject, definitionScope(d.ProjectID, remote.Project{}))
		pr.line("  id %s", d.ID)
		if d.DeletedAt != nil {
			pr.line("  %s", pr.warn("deleted "+d.DeletedAt.Format(time.RFC3339)+": it is never selected again; its versions stay readable"))
		}
		pr.line("")
		rows := [][]string{{"VERSION", "SAVED BY", "SAVED AT"}}
		for _, v := range out.Versions {
			rows = append(rows, []string{"v" + strconv.Itoa(v.Version), v.CreatedBy, v.CreatedAt.Format(time.RFC3339)})
		}
		pr.table(rows)
		pr.line("")
		if len(out.Bindings) == 0 {
			pr.line("Not bound in this project (`glossa workflow bind %s` binds it).", d.Name)
		} else {
			pr.line("Bound in this project:")
			for _, b := range out.Bindings {
				pr.line("  %s  %s  (%s)", selectorLabel(b.Locales, b.Namespace), pr.dim("position "+strconv.Itoa(b.Position)), b.ID)
			}
		}
		pr.line("  %s", pr.dim("`glossa workflow pull "+d.Name+"` prints version "+strconv.Itoa(out.Version)+"'s document"))
	})
}

// selectorLabel says what a binding selects.
func selectorLabel(locales []string, namespace string) string {
	parts := []string{"every locale"}
	if len(locales) > 0 {
		parts[0] = strings.Join(locales, ", ")
	}
	if namespace != "" {
		parts = append(parts, "namespace "+namespace)
	} else {
		parts = append(parts, "every namespace")
	}
	return strings.Join(parts, " · ")
}

// ── bindings ────────────────────────────────────────────────────────

func (inv *invocation) workflowBind(ctx context.Context, p *project, s remote.Scope, a workflowArgs) error {
	def, _, err := inv.definitionNamed(ctx, p, s, a.target)
	if err != nil {
		return err
	}
	locales, err := normalizeLocales(inv, "--locales", a.locales)
	if err != nil {
		return err
	}
	names := map[string]string{def.Id: def.Name}
	out := workflowBindDoc{Schema: workflowSchema, Action: "bind", Result: "created"}
	b, err := p.client.BindWorkflow(ctx, s, def.Id, locales, a.namespace)
	var ae *remote.APIError
	if errors.As(err, &ae) && ae.Status == 409 && ae.Code == "workflow_binding_exists" {
		// Binding the same thing twice is what a CI job re-run does:
		// the binding already there is the answer. A selector bound to
		// another definition is a real conflict.
		existing, ok, lerr := inv.bindingWithSelector(ctx, p, s, locales, a.namespace)
		if lerr != nil {
			return lerr
		}
		if ok && existing.DefinitionId == def.Id {
			out.Result, out.Binding = "unchanged", bindingJSON(existing, names)
			return inv.emit(out, func(pr *printer) {
				pr.line("%s %s is already bound here for %s", pr.pass(), def.Name, selectorLabel(existing.Locales, derefStr(existing.Namespace)))
			})
		}
		e := asError(inv.workflowError(err, "can't bind "+def.Name, def.Name))
		e.Why = "this project already binds another definition for exactly that selector"
		if ok {
			e.Why = fmt.Sprintf("this project already binds definition %s for %s", existing.DefinitionId, selectorLabel(existing.Locales, derefStr(existing.Namespace)))
		}
		e.Fix = "`glossa workflow bindings` lists them; unbind that one first (`glossa workflow unbind --binding <id>`), or narrow this one with --locales or --namespace"
		return e
	}
	if err != nil {
		return inv.workflowError(err, "can't bind "+def.Name, def.Name)
	}
	out.Binding = bindingJSON(b, names)
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Bound %s for %s (binding %s)", pr.pass(), def.Name, selectorLabel(b.Locales, derefStr(b.Namespace)), b.Id)
		pr.line("  %s", pr.dim("new work under this selector starts instances on its latest version; the more specific binding wins, a tie goes to the later one"))
	})
}

// bindingWithSelector finds the project's binding for exactly these
// locales and namespace.
func (inv *invocation) bindingWithSelector(ctx context.Context, p *project, s remote.Scope, locales []string, namespace string) (remote.WorkflowBinding, bool, error) {
	bindings, err := p.client.WorkflowBindings(ctx, s)
	if err != nil {
		return remote.WorkflowBinding{}, false, inv.workflowError(err, "can't list the project's bindings", "")
	}
	for _, b := range bindings {
		if sameSelector(b, locales, namespace) {
			return b, true, nil
		}
	}
	return remote.WorkflowBinding{}, false, nil
}

func sameSelector(b remote.WorkflowBinding, locales []string, namespace string) bool {
	if derefStr(b.Namespace) != namespace || len(b.Locales) != len(locales) {
		return false
	}
	want := slices.Clone(locales)
	slices.Sort(want)
	have := slices.Clone(b.Locales)
	slices.Sort(have)
	for i := range want {
		if !strings.EqualFold(want[i], have[i]) {
			return false
		}
	}
	return true
}

func (inv *invocation) workflowUnbind(ctx context.Context, p *project, s remote.Scope, a workflowArgs) error {
	bindings, err := p.client.WorkflowBindings(ctx, s)
	if err != nil {
		return inv.workflowError(err, "can't list the project's bindings", "")
	}
	names := map[string]string{}
	var candidates []remote.WorkflowBinding
	if a.binding != "" {
		for _, b := range bindings {
			if b.Id == a.binding {
				candidates = append(candidates, b)
			}
		}
	} else {
		def, _, err := inv.definitionNamed(ctx, p, s, a.target)
		if err != nil {
			return err
		}
		names[def.Id] = def.Name
		locales, err := normalizeLocales(inv, "--locales", a.locales)
		if err != nil {
			return err
		}
		narrowed := len(locales) > 0 || a.namespace != ""
		for _, b := range bindings {
			if b.DefinitionId == def.Id && (!narrowed || sameSelector(b, locales, a.namespace)) {
				candidates = append(candidates, b)
			}
		}
	}
	switch len(candidates) {
	case 0:
		what := "no binding " + a.binding + " in this project"
		if a.binding == "" {
			what = fmt.Sprintf("%s is not bound in this project%s", a.target, selectorSuffix(a))
		}
		return &Error{Exit: ExitUsage, Code: "workflow_not_bound", What: what,
			Fix: "`glossa workflow bindings` lists the project's bindings"}
	case 1:
	default:
		ids := make([]string, 0, len(candidates))
		for _, b := range candidates {
			ids = append(ids, b.Id+" ("+selectorLabel(b.Locales, derefStr(b.Namespace))+")")
		}
		return &Error{Exit: ExitUsage, Code: "binding_ambiguous",
			What: fmt.Sprintf("%s is bound %d times in this project", a.target, len(candidates)),
			Why:  strings.Join(ids, "; "),
			Fix:  "name the one to remove with --locales and --namespace, or --binding <id>"}
	}
	b := candidates[0]
	if err := p.client.UnbindWorkflow(ctx, s, b.Id); err != nil {
		return inv.workflowError(err, "can't remove binding "+b.Id, "")
	}
	out := workflowBindDoc{Schema: workflowSchema, Action: "unbind", Result: "removed", Binding: bindingJSON(b, names)}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Removed binding %s (%s)", pr.pass(), b.Id, selectorLabel(b.Locales, derefStr(b.Namespace)))
		pr.line("  %s", pr.dim("running instances stay on the version they started with; new work resolves without it"))
	})
}

func selectorSuffix(a workflowArgs) string {
	if len(a.locales) == 0 && a.namespace == "" {
		return ""
	}
	return " for " + selectorLabel(splitList(a.locales), a.namespace)
}

func (inv *invocation) workflowBindings(ctx context.Context, p *project, s remote.Scope) error {
	bindings, err := p.client.WorkflowBindings(ctx, s)
	if err != nil {
		return inv.workflowError(err, "can't list the project's bindings", "")
	}
	names := inv.definitionNames(ctx, p, s)
	out := workflowBindingsDoc{Schema: workflowSchema, Action: "bindings", ProjectID: s.Project, Bindings: []workflowBindingJSON{}}
	for _, b := range bindings {
		out.Bindings = append(out.Bindings, bindingJSON(b, names))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Bindings) == 0 {
			pr.line("No bindings: this project has no workflow, and behaves as it did before workflows.")
			return
		}
		rows := [][]string{{"DEFINITION", "LOCALES", "NAMESPACE", "POSITION", "ID"}}
		for _, b := range out.Bindings {
			loc := "every locale"
			if len(b.Locales) > 0 {
				loc = strings.Join(b.Locales, ",")
			}
			rows = append(rows, []string{orDefault(b.Definition, b.DefinitionID), loc, orDefault(b.Namespace, "every"), strconv.Itoa(b.Position), b.ID})
		}
		pr.table(rows)
	})
}

// definitionNames names the definitions the project may bind, by ID.
// Best effort: a label, never a reason to fail.
func (inv *invocation) definitionNames(ctx context.Context, p *project, s remote.Scope) map[string]string {
	names := map[string]string{}
	defs, err := p.client.WorkflowDefinitions(ctx, p.scope.Tenant, s.Project)
	if err != nil {
		return names
	}
	for _, d := range defs {
		names[d.Id] = d.Name
	}
	return names
}

// ── instances and their log ─────────────────────────────────────────

func (inv *invocation) workflowInstances(ctx context.Context, p *project, s remote.Scope, a workflowArgs) error {
	f := remote.InstanceFilter{Status: a.status, Locale: a.locale, Message: a.message}
	if a.definition != "" {
		def, _, err := inv.definitionNamed(ctx, p, s, a.definition)
		if err != nil {
			return err
		}
		f.Definition = def.Id
	}
	items, err := p.client.WorkflowInstances(ctx, s, f)
	if err != nil {
		return inv.workflowError(err, "can't list the workflow instances", "")
	}
	names := inv.definitionNames(ctx, p, s)
	out := workflowInstancesDoc{Schema: workflowSchema, Action: "instances", ProjectID: s.Project, Instances: []workflowInstanceJSON{}}
	for _, it := range items {
		out.Instances = append(out.Instances, instanceJSON(it, names))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Instances) == 0 {
			pr.line("No workflow instances match: an instance exists only while work is in flight under a binding, or once it finished.")
			return
		}
		rows := [][]string{{"ID", "DEFINITION", "SUBJECT", "LOCALE", "STATE", "STATUS", "UPDATED"}}
		for _, it := range out.Instances {
			rows = append(rows, []string{it.ID, orDefault(it.Definition, shortID(it.DefinitionID)) + " v" + strconv.Itoa(it.DefinitionVersion),
				it.Subject + " " + shortID(it.SubjectID), dash(it.Locale), it.State, it.Status, it.UpdatedAt.Format(time.RFC3339)})
		}
		pr.table(rows)
		pr.line("  %s", pr.dim("`glossa workflow log <id>` shows how an instance got where it is"))
	})
}

func (inv *invocation) workflowLog(ctx context.Context, p *project, s remote.Scope, id string) error {
	it, err := p.client.WorkflowInstance(ctx, s, id)
	if err != nil {
		return inv.workflowError(err, "can't read workflow instance "+id, "")
	}
	ts, err := p.client.WorkflowTransitions(ctx, s, id)
	if err != nil {
		return inv.workflowError(err, "can't read the transition log of "+id, "")
	}
	out := workflowLogDoc{Schema: workflowSchema, Action: "log", Instance: instanceJSON(it, inv.definitionNames(ctx, p, s)),
		Transitions: []workflowTransitionJSON{}}
	for _, t := range ts {
		out.Transitions = append(out.Transitions, transitionJSON(t))
	}
	return inv.emit(out, func(pr *printer) {
		in := out.Instance
		pr.line("%s · %s v%d · %s %s%s · %s in %s", pr.bold(in.ID), orDefault(in.Definition, in.DefinitionID), in.DefinitionVersion,
			in.Subject, in.SubjectID, localeSuffix(in.Locale), in.Status, in.State)
		if len(out.Transitions) == 0 {
			pr.line("  no transitions yet")
			return
		}
		for _, t := range out.Transitions {
			mark := pr.pass()
			switch t.Outcome {
			case "ignored":
				mark = pr.dim("·")
			case "refused":
				mark = pr.fail()
			}
			pr.line("%s #%d %s  %s --%s--> %s  %s  by %s", mark, t.Seq, t.At.Format(time.RFC3339), orDefault(t.From, "(start)"), t.Event, t.To, t.Outcome, t.Actor)
			for _, g := range t.Guards {
				res := "passed"
				if !g.Passed {
					res = "failed"
				}
				pr.line("      guard %s %s", g.Guard, res)
			}
			for _, act := range t.Actions {
				detail := ""
				if act.Detail != "" {
					detail = ": " + act.Detail
				}
				pr.line("      action %s %s%s", act.Name, act.Outcome, detail)
			}
		}
	})
}

func localeSuffix(l string) string {
	if l == "" {
		return ""
	}
	return " " + l
}

// ── conversions ─────────────────────────────────────────────────────

func ptr[T any](v T) *T { return &v }

func nonEmptyPtr(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

func definitionJSON(d remote.WorkflowDefinition) workflowDefinitionJSON {
	return workflowDefinitionJSON{ID: d.Id, Name: d.Name, Subject: string(d.Subject), ProjectID: nonEmptyPtr(d.ProjectId),
		Version: d.Version, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, DeletedAt: d.DeletedAt}
}

func savedJSON(d remote.WorkflowDefinitionSaved) workflowDefinitionJSON {
	return workflowDefinitionJSON{ID: d.Id, Name: d.Name, Subject: string(d.Subject), ProjectID: nonEmptyPtr(d.ProjectId),
		Version: d.Version, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt}
}

func workflowFindings(fs []remote.WorkflowFinding) []workflowFindingJSON {
	out := make([]workflowFindingJSON, 0, len(fs))
	for _, f := range fs {
		out = append(out, workflowFindingJSON{Rule: f.Rule, Severity: string(f.Severity), Path: derefStr(f.Path),
			State: derefStr(f.State), Event: derefStr(f.Event), Message: f.Message})
	}
	return out
}

// countFindings counts what refuses a save: errors, and warnings,
// which refuse it too (only info is a note).
func countWorkflowFindings(fs []workflowFindingJSON) (errs, warnings int) {
	for _, f := range fs {
		switch f.Severity {
		case "error":
			errs++
		case "warning":
			warnings++
		}
	}
	return errs, warnings
}

// printFindings prints one line per finding: severity, rule, where,
// and what.
func printWorkflowFindings(pr *printer, fs []workflowFindingJSON) {
	for _, f := range fs {
		sev := f.Severity
		switch sev {
		case "error":
			sev = pr.bad(fmt.Sprintf("%-7s", sev))
		case "warning":
			sev = pr.warn(fmt.Sprintf("%-7s", sev))
		default:
			sev = pr.dim(fmt.Sprintf("%-7s", sev))
		}
		where := f.Path
		if where == "" {
			where = "(document)"
		}
		var ctx []string
		if f.State != "" {
			ctx = append(ctx, "state "+f.State)
		}
		if f.Event != "" {
			ctx = append(ctx, "event "+f.Event)
		}
		extra := ""
		if len(ctx) > 0 {
			extra = " [" + strings.Join(ctx, ", ") + "]"
		}
		fmt.Fprintf(pr.w, "  %s %s  %s%s: %s\n", sev, f.Rule, where, extra, f.Message)
	}
}

func bindingJSON(b remote.WorkflowBinding, names map[string]string) workflowBindingJSON {
	locales := b.Locales
	if locales == nil {
		locales = []string{}
	}
	return workflowBindingJSON{ID: b.Id, DefinitionID: b.DefinitionId, Definition: names[b.DefinitionId], Subject: string(b.Subject),
		Locales: locales, Namespace: derefStr(b.Namespace), Position: b.Position, CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt}
}

func instanceJSON(it remote.WorkflowInstance, names map[string]string) workflowInstanceJSON {
	return workflowInstanceJSON{ID: it.Id, DefinitionID: it.DefinitionId, Definition: names[it.DefinitionId],
		DefinitionVersion: it.DefinitionVersion, Subject: string(it.Subject), SubjectID: it.SubjectId, Locale: derefStr(it.Locale),
		State: it.State, Status: string(it.Status), CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
}

func transitionJSON(t remote.WorkflowTransition) workflowTransitionJSON {
	out := workflowTransitionJSON{Seq: t.Seq, From: t.From, Event: t.Event, To: t.To, Outcome: string(t.Outcome),
		Actor: t.Actor, OutboxEventID: derefStr(t.OutboxEventId), At: t.At}
	out.Guards = make([]struct {
		Guard  string `json:"guard"`
		Passed bool   `json:"passed"`
	}, len(t.Guards))
	for i, g := range t.Guards {
		out.Guards[i].Guard, out.Guards[i].Passed = g.Guard, g.Passed
	}
	out.Actions = make([]struct {
		Name    string `json:"name"`
		Outcome string `json:"outcome"`
		Detail  string `json:"detail,omitempty"`
	}, len(t.Actions))
	for i, act := range t.Actions {
		out.Actions[i].Name, out.Actions[i].Outcome, out.Actions[i].Detail = act.Name, string(act.Outcome), derefStr(act.Detail)
	}
	return out
}

// ── errors ──────────────────────────────────────────────────────────

// workflowFixes explains the Workflow API's problem codes (RFC 0006 §2).
var workflowFixes = map[string]string{
	"workflow_definition_exists":       "a live definition of that name exists in this scope: `glossa workflow push` saves a new version of it",
	"workflow_limit_reached":           "the tenant has its 50 live definitions; delete one in Studio before creating another",
	"invalid_workflow_binding":         "--locales takes BCP 47 tags (de, pt-BR) and --namespace a namespace name",
	"workflow_definition_out_of_scope": "that definition belongs to another project; push a tenant-wide one (no --project) or one of this project's own",
	"workflow_instances_unavailable":   "this server runs no workflow instance store; ask its operator, or check the deployment's configuration",
	"invalid_query":                    "check --status (active, finished), --locale (BCP 47) and --message (a key)",
}

// workflowError explains a failed Workflow request. Input the server
// refuses is a usage error (exit 2), the server refusing the operation
// a network error (exit 3). A token without the opt-in workflows scope
// is told exactly that.
func (inv *invocation) workflowError(err error, what, name string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if fix, ok := workflowFixes[ae.Code]; ok {
		e.Fix = fix
	}
	switch {
	case ae.Status == 403 && strings.Contains(ae.Detail, "workflows.manage"):
		e.Code = "workflows_scope_required"
		e.Why = "the credential lacks the `workflows` scope: saving and binding workflow definitions, and rebasing instances, takes workflows.manage, " +
			"which an API token holds only with that opt-in scope (no other scope implies it), and a GitHub Actions credential never holds"
		e.Fix = "create an API token with the `workflows` scope in Studio (Settings → API tokens) and set GLOSSA_TOKEN to it for this step"
	case ae.Status == 403 && strings.Contains(ae.Detail, "workflows.read"):
		e.Why = "the credential may not read workflows (" + ae.Detail + ")"
		e.Fix = "use an API token with the read scope; a GitHub Actions credential only reads and writes its project's catalog"
	case ae.Status == 404:
		e.Why = orDefault(ae.Detail, "not found")
		e.Fix = "check the name or ID: `glossa workflow list`, `bindings` and `instances` show them"
		if name != "" {
			e.Fix = "`glossa workflow list` shows the definitions this project may bind"
		}
	case ae.Status == 400 || ae.Status == 422:
		e.Exit = ExitUsage
		e.Why = orDefault(ae.Detail, e.Why)
	case ae.Status == 409 || ae.Status == 503:
		e.Why = orDefault(ae.Detail, e.Why)
	}
	return e
}
