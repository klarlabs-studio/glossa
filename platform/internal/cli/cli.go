// Package cli is the glossa command: the developer and CI interface to
// Glossa (product intent §46, RFC 0002 §9). platform/cmd/glossa is only
// its entry point.
//
// Every command:
//
//   - reads glossa.yaml (found by walking up from the working directory),
//   - talks to glossa-server through the generated /v1 client with an
//     API token (GLOSSA_TOKEN, else the one `glossa login` stored),
//   - prints human output, or exactly one JSON document with --json
//     (shapes in cmd/glossa/README.md, each tagged with a "schema"),
//   - exits with a documented code (ExitCode).
//
// The MessageFormat kernel (messageformat) runs locally, so `check
// --offline`, `extract` and `generate` work without a server.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
)

// Env is everything the CLI touches outside itself, so tests can run
// every command in-process.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// Dir is the working directory.
	Dir string
	// Interactive is true when stdin is a terminal: prompts are allowed.
	Interactive bool
	// ColorOutput is true when stdout is a terminal that takes color.
	ColorOutput bool
	// HTTP is the transport for glossa-server and v0.3 (nil: default).
	HTTP *http.Client
	// Credentials stores API tokens (nil: the OS keychain or the file).
	Credentials credentials.Store
	// ReadSecret reads a secret from the terminal without echo (nil:
	// read a line from Stdin).
	ReadSecret func(prompt string) (string, error)
	// Version is the CLI's version, for --version and the User-Agent.
	Version string
	// Sleep waits between device sign-in polls (nil: sleep for real,
	// until ctx ends).
	Sleep func(ctx context.Context, d time.Duration) error
}

func (e *Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// command is one subcommand.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, inv *invocation, args []string) error
}

func commands() []command {
	return []command{
		{"init", "Create glossa.yaml for this project", runInit},
		{"login", "Store an API token for the server, or sign in with --device", runLogin},
		{"logout", "Forget the stored credential (and end a device sign-in)", runLogout},
		{"whoami", "Show the server, token and tenant in use", runWhoami},
		{"push", "Send the source catalog's messages to the server", runPush},
		{"pull", "Write translations to local catalogs", runPull},
		{"extract", "Find message usages in source code", runExtract},
		{"context", "Upload a usages document (from @klarlabs-studio/glossa/unplugin or extract)", runContext},
		{"capture", "Screenshot the app's pages with where each message renders", runCaptureCmd},
		{"generate", "Generate typed message accessors (TypeScript, Vue, React, Go)", runGenerate},
		{"check", "Check the project: structure, arguments, completeness", runCheck},
		{"findings", "List the findings the server stored, with the QA filters", runFindings},
		{"waive", "Accept a finding, with a reason; list and revoke waivers", runWaive},
		{"policy", "The check policy: show, diff (impact preview), export, import", runPolicy},
		{"status", "Show translation coverage per locale, or --quality for the seven numbers", runStatus},
		{"diff", "Compare local catalogs with the server", runDiff},
		{"projects", "List the tenant's projects; create one (--name --slug --source-locale)", runProjects},
		{"tenants", "List the tenants the credential can act in", runTenants},
		{"locales", "List the project's locales", runLocales},
		{"messages", "List the project's messages", runMessages},
		{"namespaces", "List the project's namespaces with their message counts", runNamespaces},
		{"import", "Import XLIFF, JSON, PO, TMX or TBX files (--format), or Glossa v0.3 (--from v0)", runImport},
		{"export", "Export XLIFF, JSON, TMX or TBX files, verified by SHA-256", runExport},
		{"jobs", "List, show and cancel import and export jobs", runJobs},
		{"release", "Publish, promote, roll back and roll out releases; release requests", runRelease},
		{"branch", "Show or close a feature branch's proposals", runBranch},
		{"preview", "Register where CI deployed a branch's preview", runPreview},
		{"github", "List, add and remove Git connections (repository → project)", runGitHub},
		{"tm", "Search the translation memory; list and retire units; TMX export and import", runTM},
		{"terms", "Manage the termbase; check translations against it; TBX export and import", runTerms},
		{"style", "Show the effective style guide; edit one from YAML", runStyle},
		{"translate", "Fill locales with AI suggestions", runTranslate},
		{"review", "Review AI suggestions: list, accept, reject", runReview},
		{"translations", "Review translations: review key@locale --state approved|rejected|draft|needs_review", runTranslations},
		{"workflow", "Lint, push, pull and bind workflow definitions; list instances and their log", runWorkflow},
		{"assignments", "My work: list, show, accept, complete, decline; create assignments", runAssignments},
		{"approve", "Approve a release request or a translation (a person's session); list what waits", runApprove},
		{"deny", "Deny a release request or a translation, with a reason", runDeny},
		{"ai", "Show AI consent, budget, providers and project policy", runAI},
		{"audit", "The audit trail: list, export, CSV; verify an export offline (glossa.audit/v1)", runAudit},
		{"mcp", "Speak MCP on stdin/stdout, proxying to the server's /mcp endpoint", runMCP},
	}
}

// Main runs the CLI with args (without the program name) and returns the
// process exit code.
func Main(ctx context.Context, args []string, env Env) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) > 1 {
			return Main(ctx, []string{args[1], "--help"}, env)
		}
		printHelp(env.Stdout)
		return int(ExitOK)
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintf(env.Stdout, "glossa %s\n", versionOr(env.Version))
		return int(ExitOK)
	}
	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}
		inv := &invocation{env: env, name: c.name}
		err := c.run(ctx, inv, args[1:])
		return inv.finish(err)
	}
	inv := &invocation{env: env, name: "glossa"}
	inv.setupOutput()
	return inv.finish(&Error{Exit: ExitUsage, Code: "unknown_command",
		What: fmt.Sprintf("unknown command %q", args[0]),
		Fix:  "run `glossa help` for the list of commands"})
}

func versionOr(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `glossa — localization infrastructure for developers and CI

Usage: glossa <command> [flags]

Commands:
`)
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-11s %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, `
Flags every command takes:
  --json       print one JSON document (stable shapes; see the README)
  --quiet      print nothing but errors
  --no-color   never color output (also NO_COLOR=1)
  --config     path to glossa.yaml (default: nearest one up from here)

Exit codes: 0 ok · 1 check failed · 2 usage or config · 3 network or auth · 4 partial failure · 5 held for approval
Run "glossa <command> --help" for a command's flags.
`)
}

// invocation is one command run: its flags, output and lazily loaded
// config and client.
type invocation struct {
	env  Env
	name string

	json, quiet, noColor bool
	configPath           string

	out *printer
}

// flags returns a FlagSet with the global flags registered.
func (inv *invocation) flags(usage string) *flag.FlagSet {
	fs := flag.NewFlagSet("glossa "+inv.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&inv.json, "json", false, "print one JSON document")
	fs.BoolVar(&inv.quiet, "quiet", false, "print nothing but errors")
	fs.BoolVar(&inv.noColor, "no-color", false, "never color output")
	fs.StringVar(&inv.configPath, "config", "", "path to glossa.yaml")
	fs.Usage = func() {
		fmt.Fprintf(inv.env.Stdout, "Usage: glossa %s\n\n", usage)
		fs.SetOutput(inv.env.Stdout)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
	return fs
}

// errHelp ends a run after --help was printed.
var errHelp = errors.New("help requested")

// parse parses args (flags may follow positional arguments) and sets up
// output. It returns the positional arguments.
func (inv *invocation) parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			inv.setupOutput()
			if errors.Is(err, flag.ErrHelp) {
				// flag already printed the usage.
				return nil, errHelp
			}
			return nil, usageError(inv.name, "%v", err)
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	inv.setupOutput()
	return positional, nil
}

func (inv *invocation) setupOutput() {
	color := inv.env.ColorOutput && !inv.noColor && !inv.json && inv.env.getenv("NO_COLOR") == ""
	inv.out = &printer{w: inv.env.Stdout, color: color, quiet: inv.quiet || inv.json}
}

// emit prints the result: v as JSON with --json, else human(out).
func (inv *invocation) emit(v any, human func(p *printer)) error {
	if inv.json {
		return writeJSON(inv.env.Stdout, v)
	}
	human(inv.out)
	return nil
}

// finish turns the command's error into output and an exit code.
func (inv *invocation) finish(err error) int {
	if err == nil || errors.Is(err, errHelp) {
		return int(ExitOK)
	}
	if inv.out == nil {
		inv.setupOutput()
	}
	e := asError(err)
	if e.Silent {
		return int(e.Exit)
	}
	if inv.json {
		_ = writeJSON(inv.env.Stdout, map[string]any{"schema": "glossa.cli.error/v1", "error": e.json()})
		return int(e.Exit)
	}
	p := &printer{w: inv.env.Stderr, color: inv.out.color}
	printError(inv.env.Stderr, p, e)
	return int(e.Exit)
}
