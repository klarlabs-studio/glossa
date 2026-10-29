package cli

import (
	"bytes"
	"context"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/codegen"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
)

type generatedFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Changed bool   `json:"changed"`
}

type generateJSON struct {
	Schema   string            `json:"schema"`
	Source   string            `json:"source"`
	Messages int               `json:"messages"`
	Check    bool              `json:"check"`
	Files    []generatedFile   `json:"files"`
	Warnings []codegen.Warning `json:"warnings"`
}

func runGenerate(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("generate [--from-server] [--check]")
	fromServer := fs.Bool("from-server", false, "generate from the server's messages instead of the local source catalog")
	checkOnly := fs.Bool("check", false, "write nothing; exit 1 if the generated files are stale (CI)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	g := cfg.Generate
	if g.TypeScript == "" && g.Go == "" && g.Dart == "" {
		return &Error{Exit: ExitUsage, Code: "nothing_to_generate", What: "no generate outputs configured",
			Where: cfg.Path + " (generate)", Fix: "set generate.typescript (and generate.vue or generate.react), generate.go and/or generate.dart, e.g. generate:\n    typescript: src/glossa/messages.ts"}
	}
	s, label, err := inv.generateSource(ctx, cfg, *fromServer)
	if err != nil {
		return err
	}
	entries, warnings := generateEntries(s)
	out := generateJSON{Schema: "glossa.cli.generate/v1", Source: label, Messages: len(entries), Check: *checkOnly,
		Files: []generatedFile{}, Warnings: warnings}
	files, more, err := renderGenerated(cfg, entries, label)
	if err != nil {
		return err
	}
	out.Warnings = append(out.Warnings, more...)
	stale := false
	for _, f := range files {
		changed, err := writeGenerated(f.path, f.data, *checkOnly)
		if err != nil {
			return &Error{Exit: ExitUsage, Code: "write_failed", What: "can't write generated code", Where: f.path, Why: err.Error()}
		}
		stale = stale || changed
		out.Files = append(out.Files, generatedFile{Path: relPath(cfg, f.path), Kind: f.kind, Changed: changed})
	}
	if err := inv.emit(out, func(p *printer) { printGenerate(p, out) }); err != nil {
		return err
	}
	if *checkOnly && stale {
		return silentExit(ExitCheckFailed, "generated_stale")
	}
	return nil
}

func (inv *invocation) generateSource(ctx context.Context, cfg *config.Config, fromServer bool) (*snapshot.Snapshot, string, error) {
	if fromServer {
		s, label, err := inv.snapshot(ctx, cfg, false, snapshot.Options{SkipTranslations: true})
		return s, label, err
	}
	s, err := loadLocal(cfg)
	if err != nil {
		return nil, "", err
	}
	return s, relPath(cfg, s.Locales[0].File), nil
}

func generateEntries(s *snapshot.Snapshot) ([]codegen.Entry, []codegen.Warning) {
	var (
		entries  []codegen.Entry
		warnings = []codegen.Warning{}
	)
	for _, m := range s.Messages {
		if m.Invalid != nil {
			warnings = append(warnings, codegen.Warning{Key: m.Key, Reason: "invalid message (" + m.Invalid.Code + "); fix it, then generate again"})
			continue
		}
		entries = append(entries, codegen.Entry{Key: m.Key, Text: m.Text, Description: m.Description, Arguments: m.Arguments})
	}
	return entries, warnings
}

type renderedFile struct {
	path, kind string
	data       []byte
}

func renderGenerated(cfg *config.Config, entries []codegen.Entry, source string) ([]renderedFile, []codegen.Warning, error) {
	var (
		files    []renderedFile
		warnings []codegen.Warning
	)
	g := cfg.Generate
	if g.TypeScript != "" {
		ts, w := codegen.TypeScript(entries, codegen.TSOptions{Source: source})
		warnings = append(warnings, w...)
		files = append(files, renderedFile{cfg.Resolve(g.TypeScript), "typescript", ts})
		// Framework registrations import the typed module relative to themselves.
		for _, r := range []struct {
			out, kind string
			render    func(modulePath string) []byte
		}{{g.Vue, "vue", codegen.Vue}, {g.React, "react", codegen.React}} {
			if r.out == "" {
				continue
			}
			path := cfg.Resolve(r.out)
			rel, err := filepath.Rel(filepath.Dir(path), cfg.Resolve(g.TypeScript))
			if err != nil {
				return nil, nil, err
			}
			files = append(files, renderedFile{path, r.kind, r.render(filepath.ToSlash(rel))})
		}
	}
	if g.Go != "" {
		path := cfg.Resolve(g.Go)
		pkg := g.GoPackage
		if pkg == "" {
			pkg = goPackageName(filepath.Base(filepath.Dir(path)))
		}
		src, w, err := codegen.Go(entries, codegen.GoOptions{Package: pkg, Runtime: orDefault(g.GoRuntime, config.DefaultGoRuntime), Source: source})
		if err != nil {
			return nil, nil, err
		}
		if len(g.TypeScript) == 0 {
			warnings = append(warnings, w...)
		}
		files = append(files, renderedFile{path, "go", src})
	}
	if g.Dart != "" {
		src, w := codegen.Dart(entries, codegen.DartOptions{Runtime: orDefault(g.DartRuntime, config.DefaultDartRuntime), Source: source})
		// The accessor paths are TypeScript's, so its warnings are the
		// same ones; report them once.
		if g.TypeScript == "" {
			warnings = append(warnings, w...)
		}
		files = append(files, renderedFile{cfg.Resolve(g.Dart), "dart", src})
	}
	return files, dedupeWarnings(warnings), nil
}

func dedupeWarnings(ws []codegen.Warning) []codegen.Warning {
	seen := map[string]bool{}
	out := []codegen.Warning{}
	for _, w := range ws {
		if !seen[w.Key+w.Reason] {
			seen[w.Key+w.Reason] = true
			out = append(out, w)
		}
	}
	return out
}

// goPackageName derives a package name from a directory name.
func goPackageName(dir string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(dir) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" || !token.IsIdentifier(name) || token.IsKeyword(name) {
		return "messages"
	}
	return name
}

// writeGenerated writes data unless path already holds it; with
// checkOnly it only reports whether it would change.
func writeGenerated(path string, data []byte, checkOnly bool) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if checkOnly {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, data, 0o644)
}

func printGenerate(p *printer, out generateJSON) {
	for _, f := range out.Files {
		switch {
		case out.Check && f.Changed:
			p.line("%s %s is stale (%s)", p.fail(), f.Path, f.Kind)
		case f.Changed:
			p.line("%s wrote %s (%s)", p.pass(), f.Path, f.Kind)
		default:
			p.line("%s %s is up to date (%s)", p.pass(), f.Path, f.Kind)
		}
	}
	p.line("  %s", p.dim(plural(out.Messages, "message", "messages")+" from "+out.Source))
	for _, w := range out.Warnings {
		p.line("%s %s: no accessor, %s", p.caution(), w.Key, w.Reason)
	}
	if out.Check {
		for _, f := range out.Files {
			if f.Changed {
				p.line("Run `glossa generate` and commit the result.")
				return
			}
		}
	}
}
