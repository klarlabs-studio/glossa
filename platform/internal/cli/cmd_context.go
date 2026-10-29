package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

const contextUsage = `context push <file> [--source plugin|extract|runtime|capture]

Uploads a glossa.usages/v1 document — @klarlabs-studio/glossa/unplugin's .glossa/usages.json, or
` + "`glossa extract --json`" + `'s output — to the project's context builds (RFC 0004 §2).
--source is the collector that wrote it: by default extract for a document whose tool is
glossa, otherwise plugin. Uploading the same document again changes nothing.
Whether the build is of the default branch is the project's setting (default_branch).`

// contextPushJSON is `glossa context push --json`.
type contextPushJSON struct {
	Schema   string              `json:"schema"`
	File     string              `json:"file"`
	Source   string              `json:"source"`
	Replayed bool                `json:"replayed"`
	Build    remote.ContextBuild `json:"build"`
}

// usagesHead is what context push reads of a document before sending it
// (the server validates the rest).
type usagesHead struct {
	Schema      string `json:"schema"`
	Application string `json:"application"`
	Commit      string `json:"commit"`
	Branch      string `json:"branch"`
	Tool        struct {
		Name string `json:"name"`
	} `json:"tool"`
}

func runContext(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(contextUsage)
	source := fs.String("source", "", "the collector: plugin, extract, runtime or capture (default: from the document's tool)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 || pos[0] != "push" {
		return usageError(inv.name, "context takes one action: push <file> (`glossa context push .glossa/usages.json`)")
	}
	path, file := pos[1], pos[1]
	if !filepath.IsAbs(file) {
		file = filepath.Join(inv.env.Dir, file)
	}
	raw, err := os.ReadFile(file) //nolint:gosec // the user names the file
	if err != nil {
		return &Error{Exit: ExitUsage, Code: "file_unreadable", What: "can't read the usages document", Where: path, Why: err.Error(),
			Fix: "build with @klarlabs-studio/glossa/unplugin (it writes .glossa/usages.json) or run `glossa extract --json > usages.json`"}
	}
	var head usagesHead
	if err := json.Unmarshal(raw, &head); err != nil || head.Schema != "glossa.usages/v1" {
		return &Error{Exit: ExitUsage, Code: "invalid_usages", What: "not a glossa.usages/v1 document", Where: path,
			Why: "its schema member isn't glossa.usages/v1", Fix: "upload the file @klarlabs-studio/glossa/unplugin or `glossa extract --json` wrote"}
	}
	if *source == "" {
		*source = sourceOf(head.Tool.Name)
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	up, err := inv.pushUsages(ctx, cfg, raw, *source)
	if err != nil {
		return err
	}
	out := contextPushJSON{Schema: "glossa.cli.context.push/v1", File: path, Source: *source, Replayed: up.Replayed, Build: up.Build}
	return inv.emit(out, func(p *printer) {
		printUpload(p, head.Application, head.Commit, head.Branch, up)
	})
}

// sourceOf is the collector a document's tool names: glossa is
// `glossa extract`; anything else (@klarlabs-studio/glossa/unplugin, @klarlabs-studio/glossa/astro) the
// bundler plugin.
func sourceOf(tool string) string {
	if tool == "glossa" {
		return remote.SourceExtract
	}
	return remote.SourcePlugin
}

// pushUsages uploads a glossa.usages/v1 document to the project's context
// builds: `glossa extract --upload` and `glossa context push` share it.
// The server refusing the document is a usage error (exit 2).
func (inv *invocation) pushUsages(ctx context.Context, cfg *config.Config, raw []byte, source string) (*remote.UploadedBuild, error) {
	p, err := inv.connectWith(ctx, cfg)
	if err != nil {
		return nil, err
	}
	up, err := p.client.UploadUsages(ctx, p.scope, raw, source)
	if err != nil {
		err = inv.apiError(err, "can't upload the usages")
		var e *Error
		var ae *remote.APIError
		if errors.As(err, &e) && errors.As(err, &ae) && (ae.Status == 400 || ae.Status == 413) {
			e.Exit = ExitUsage
			e.Fix = "fix the document (schema: runtimes/testdata/schemas/usages.v1.schema.json), or create the application in the project"
		}
		return nil, err
	}
	return &up, nil
}

// printUpload is the human line for an upload.
func printUpload(p *printer, application, commit, branch string, up *remote.UploadedBuild) {
	what := "Uploaded"
	if up.Replayed {
		what = "Already uploaded"
	}
	p.line("%s %s the usages of %s at %s (%s) as build %s", p.pass(), what, application, shortCommit(commit), branch, up.Build.Id)
	if n := up.Build.UnknownKeys; n > 0 {
		p.line("%s %s the catalog doesn't know (stored without a message: `glossa push` the catalog before uploading)",
			p.caution(), plural(n, "usage names a key", "usages name keys"))
	}
	if up.Build.OnDefaultBranch {
		p.line("  %s", p.dim("on the default branch: the current usages of every view"))
	}
}
