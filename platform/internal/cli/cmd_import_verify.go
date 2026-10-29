package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
)

// verifyFlags are `import --from v0 --verify`'s own flags.
type verifyFlags struct {
	verify                      bool
	edge, environment, keyEnv   string
	formatModule, runtimeModule string
	node                        string
}

// verifyFlagNames are the flags only --verify takes.
var verifyFlagNames = []string{"edge", "environment", "delivery-key-env", "format-module", "runtime-module", "node"}

func (v *verifyFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&v.verify, "verify", false, "--from v0: render every key both ways and compare; imports nothing")
	fs.StringVar(&v.edge, "edge", "", "--verify: glossa-edge's URL (default $GLOSSA_EDGE)")
	fs.StringVar(&v.environment, "environment", "", "--verify: the environment whose release the runtime loads")
	fs.StringVar(&v.keyEnv, "delivery-key-env", "GLOSSA_DELIVERY_KEY", "--verify: environment variable holding a delivery key for it")
	fs.StringVar(&v.formatModule, "format-module", "", "--verify: the @felixgeelhaar/glossa-format package directory (default: found from here)")
	fs.StringVar(&v.runtimeModule, "runtime-module", "", "--verify: the @klarlabs-studio/glossa package directory (default: found from here)")
	fs.StringVar(&v.node, "node", "node", "--verify: the Node.js (22 or later) to render with")
}

// verifyRow is one rendering in the report.
type verifyRow = v0.VerifyRow

// verifyJSON is `glossa.cli.import-verify/v1`.
type verifyJSON struct {
	Schema      string            `json:"schema"`
	From        string            `json:"from"`
	Source      map[string]string `json:"source"`
	Edge        string            `json:"edge"`
	Environment string            `json:"environment"`
	Release     string            `json:"release"`
	// Summary counts renderings by verdict: match, known_defect,
	// mismatch; renderings, keys and locales are what was compared.
	Summary map[string]int `json:"summary"`
	// KnownDefects are the renderings that differ only by v0.3's known
	// apostrophe defect, each with its evidence; Mismatches every other
	// difference. Neither is ever left out.
	KnownDefects  []verifyRow `json:"known_defects"`
	Mismatches    []verifyRow `json:"mismatches"`
	RuntimeErrors []string    `json:"runtime_errors,omitempty"`
}

// importV0Verify is `glossa import --from v0 --verify` (RFC 0006 §7.3):
// it reads v0.3's text — from a restored backup (--v0-db) or the v0.3
// API (--v0-url) — and renders every key in every locale twice, with
// v0.3's own formatter and with @klarlabs-studio/glossa over the release the
// edge serves, comparing the outputs. It writes nothing anywhere. Exit
// 0 when every rendering matches or differs only by v0.3's known
// apostrophe defect (reported and counted), 1 on any other difference.
func (inv *invocation) importV0Verify(ctx context.Context, fs *flag.FlagSet, f importFlags, vf verifyFlags) error {
	for _, name := range []string{"dry-run", "invite", "history"} {
		if isSet(fs, name) {
			return usageError(inv.name, "--%s belongs to an import; --verify only compares, and writes nothing", name)
		}
	}
	cfg, err := inv.verifyConfig(vf)
	if err != nil {
		return err
	}
	only, err := localeSet(inv, f.locales)
	if err != nil {
		return err
	}
	if f.project == "" {
		if c, err := inv.loadConfig(); err == nil {
			f.project = c.Project
		}
	}
	if f.project == "" {
		return usageError(inv.name, "--verify needs --v0-project (the v0.3 project's slug)")
	}
	texts, source, err := inv.verifyTexts(ctx, f, only)
	if err != nil {
		return err
	}
	res, err := v0.Verify(ctx, cfg, texts)
	if err != nil {
		e := &Error{Exit: ExitNetwork, Code: "verify_failed", What: "can't render the comparison", Why: err.Error(),
			Fix: "check --edge, --environment and the delivery key; the renderer's own output is in why"}
		if errors.Is(err, v0.ErrNoNode) {
			e.Exit, e.Code, e.Fix = ExitUsage, "node_not_found", "install Node.js 22 or later, or pass --node <path>"
		}
		return e
	}
	out := verifyJSON{Schema: "glossa.cli.import-verify/v1", From: "v0", Source: source, Edge: cfg.EdgeURL,
		Environment: cfg.Environment, Release: res.Release, KnownDefects: []verifyRow{}, Mismatches: []verifyRow{},
		RuntimeErrors: res.RuntimeErrors}
	keys, locales := map[string]bool{}, map[string]bool{}
	for _, r := range res.Rows {
		keys[r.Key], locales[r.Locale] = true, true
		switch r.Verdict {
		case v0.VerdictKnownDefect:
			out.KnownDefects = append(out.KnownDefects, r)
		case v0.VerdictMismatch:
			out.Mismatches = append(out.Mismatches, r)
		}
	}
	out.Summary = map[string]int{
		"renderings": len(res.Rows), "keys": len(keys), "locales": len(locales),
		v0.VerdictMatch: res.Count(v0.VerdictMatch), v0.VerdictKnownDefect: len(out.KnownDefects), v0.VerdictMismatch: len(out.Mismatches),
	}
	if err := inv.emit(out, func(pr *printer) { printVerify(pr, out) }); err != nil {
		return err
	}
	if len(out.Mismatches) > 0 {
		return silentExit(ExitCheckFailed, "renderings_differ")
	}
	return nil
}

// verifyTexts reads v0.3's text from the restore or the API.
func (inv *invocation) verifyTexts(ctx context.Context, f importFlags, only map[string]bool) ([]v0.VerifyText, map[string]string, error) {
	if f.db != "" {
		snap, err := v0.ReadRestore(ctx, f.db, f.tenant, f.project)
		if err != nil {
			return nil, nil, v0DBError(err, f.db)
		}
		texts, err := snap.VerifyTexts(only)
		if err != nil {
			return nil, nil, &Error{Exit: ExitUsage, Code: "invalid_locale", What: err.Error()}
		}
		return texts, map[string]string{"db": v0.DescribeDSN(f.db), "tenant": snap.Tenant.Slug, "project": snap.Project.Slug,
			"restore": snap.Restore.DumpName}, nil
	}
	if f.url == "" {
		return nil, nil, usageError(inv.name, "--verify reads v0.3's text from --v0-db (a restored backup) or --v0-url (its API)")
	}
	key := strings.TrimSpace(inv.env.getenv(f.keyEnv))
	if key == "" {
		return nil, nil, &Error{Exit: ExitUsage, Code: "no_v0_key", What: "no v0.3 API key", Why: "$" + f.keyEnv + " is empty",
			Fix: "export " + f.keyEnv + "=glossa_… (a read key of the v0.3 project)"}
	}
	src := v0.New(f.url, key, inv.env.HTTP, inv.userAgent())
	codes, err := src.Locales(ctx, f.project)
	if err != nil {
		return nil, nil, &Error{Exit: ExitNetwork, Code: "v0_request_failed", What: "can't list the v0.3 project's locales",
			Where: src.Base(), Why: err.Error(), Fix: "check --v0-url, --v0-project and the v0.3 key"}
	}
	var texts []v0.VerifyText
	for _, code := range codes {
		canon, err := v0.Canonical(code)
		if err != nil {
			return nil, nil, &Error{Exit: ExitUsage, Code: "invalid_locale", What: fmt.Sprintf("v0.3 locale %q is not BCP 47", code)}
		}
		if only != nil && !only[canon] {
			continue
		}
		b, err := src.Bundle(ctx, f.project, code)
		if err != nil {
			return nil, nil, &Error{Exit: ExitNetwork, Code: "v0_request_failed", What: "can't read v0.3 locale " + code,
				Where: src.Base(), Why: err.Error(), Fix: "check --v0-url, --v0-project and the v0.3 key"}
		}
		for k, text := range b.Messages {
			if text != "" {
				texts = append(texts, v0.VerifyText{Key: k, Locale: canon, V0Locale: code, Text: text})
			}
		}
	}
	return texts, map[string]string{"url": src.Base(), "project": f.project}, nil
}

// verifyConfig resolves where the release and the two formatters come
// from.
func (inv *invocation) verifyConfig(vf verifyFlags) (v0.VerifyConfig, error) {
	cfg := v0.VerifyConfig{Node: vf.node, EdgeURL: strings.TrimRight(orDefault(vf.edge, inv.env.getenv("GLOSSA_EDGE")), "/"),
		Environment: vf.environment, DeliveryKey: strings.TrimSpace(inv.env.getenv(vf.keyEnv))}
	switch {
	case cfg.EdgeURL == "":
		return cfg, usageError(inv.name, "--verify needs --edge (glossa-edge's URL) or $GLOSSA_EDGE")
	case cfg.Environment == "":
		return cfg, usageError(inv.name, "--verify needs --environment: the release it compares against is the one the edge serves there")
	case cfg.DeliveryKey == "":
		return cfg, &Error{Exit: ExitUsage, Code: "no_delivery_key", What: "no delivery key", Why: "$" + vf.keyEnv + " is empty",
			Fix: "create one with `glossa release keys create <name>` and export " + vf.keyEnv}
	}
	var err error
	if cfg.FormatModule, err = findModule(inv.env.Dir, vf.formatModule, "@felixgeelhaar/glossa-format", "packages/format", "dist/index.js"); err != nil {
		return cfg, err
	}
	if cfg.RuntimeModule, err = findModule(inv.env.Dir, vf.runtimeModule, "@klarlabs-studio/glossa", "runtimes/js/glossa", "dist/runtime/index.js"); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// findModule finds a built JS package: the flag's directory, else the
// nearest node_modules/<pkg> from dir upwards, else <repoPath> of a
// Glossa checkout dir is inside. The directory must be the package
// (its package.json names it) and hold its built entry (for
// @klarlabs-studio/glossa the runtime's, dist/runtime/index.js).
func findModule(dir, flagValue, pkg, repoPath, entry string) (string, error) {
	var candidates []string
	if flagValue != "" {
		if !filepath.IsAbs(flagValue) {
			flagValue = filepath.Join(dir, flagValue)
		}
		candidates = []string{flagValue}
	} else {
		for d := dir; ; d = filepath.Dir(d) {
			candidates = append(candidates, filepath.Join(d, "node_modules", filepath.FromSlash(pkg)), filepath.Join(d, filepath.FromSlash(repoPath)))
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	for _, c := range candidates {
		raw, err := os.ReadFile(filepath.Join(c, "package.json"))
		if err != nil {
			continue
		}
		var meta struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &meta) != nil || meta.Name != pkg {
			continue
		}
		if _, err := os.Stat(filepath.Join(c, filepath.FromSlash(entry))); err != nil {
			return "", &Error{Exit: ExitUsage, Code: "module_not_built", What: pkg + " is not built", Where: c,
				Fix: "build it (`pnpm --filter " + pkg + "... build`), or point --" + moduleFlag(pkg) + " at a built copy"}
		}
		return c, nil
	}
	return "", &Error{Exit: ExitUsage, Code: "module_not_found", What: "can't find " + pkg, Where: dir + " and its parents",
		Why: "--verify renders with it and the CLI does not bundle it",
		Fix: "install it (`npm install " + pkg + "`), run from a Glossa checkout, or pass --" + moduleFlag(pkg) + " <dir>"}
}

func moduleFlag(pkg string) string {
	if pkg == "@klarlabs-studio/glossa" {
		return "runtime-module"
	}
	return "format-module"
}

func printVerify(p *printer, out verifyJSON) {
	s := out.Summary
	p.line("%s rendered %d keys in %d locales both ways — %d renderings against release %s in %s",
		p.pass(), s["keys"], s["locales"], s["renderings"], orDefault(out.Release, "(none)"), out.Environment)
	p.line("  %d match, %d differ only by v0.3's known apostrophe defect, %d mismatch", s[v0.VerdictMatch],
		s[v0.VerdictKnownDefect], s[v0.VerdictMismatch])
	rows := func(mark string, rs []verifyRow) {
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Key < rs[j].Key })
		for _, r := range rs {
			args, _ := json.Marshal(r.Args)
			p.line("  %s %s %s %s: v0.3 %s, runtime %s", mark, r.Key, r.Locale, args, quoted(r.V0, r.V0Error), quoted(r.Runtime, r.RuntimeError))
		}
	}
	if len(out.KnownDefects) > 0 {
		p.line("%s known v0.3 defect (%s): a bare apostrophe opens a quoted run in v0.3; the runtime reads it as ICU does",
			p.caution(), v0.DefectBareApostrophe)
		rows("~", out.KnownDefects)
	}
	if len(out.Mismatches) > 0 {
		p.line("%s %d renderings differ", p.fail(), len(out.Mismatches))
		rows("✗", out.Mismatches)
	}
	for _, e := range out.RuntimeErrors {
		p.line("  %s runtime: %s", p.caution(), e)
	}
}

func quoted(s *string, err string) string {
	if err != "" {
		return "error: " + err
	}
	if s == nil {
		return "nothing"
	}
	return fmt.Sprintf("%q", *s)
}
