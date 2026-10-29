package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Defaults init writes.
const (
	defaultServer   = "http://localhost:8080"
	defaultCatalogs = "locales/{locale}.json"
)

var defaultExtract = config.Extract{
	Include:   []string{"**/*.{ts,tsx,js,jsx,mjs,vue,astro,svelte,html,go}"},
	Exclude:   []string{"**/*.test.*", "**/*_test.go", "**/*.d.ts"},
	Templates: []string{"**/*.{tmpl,gotmpl,gohtml}"},
}

type initJSON struct {
	Schema  string         `json:"schema"`
	Path    string         `json:"path"`
	Config  *config.Config `json:"config"`
	Checked bool           `json:"checked_with_server"`
}

type initFlags struct {
	server, tenant, project, sourceLocale, catalogs, ts, vue, react, goOut, dartOut string
	force                                                                           bool
}

func runInit(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("init [--server URL] [--project SLUG] [--source-locale en] [flags]")
	var f initFlags
	fs.StringVar(&f.server, "server", "", "glossa-server URL (default "+defaultServer+")")
	fs.StringVar(&f.tenant, "tenant", "", "tenant ID (default: the API token's tenant)")
	fs.StringVar(&f.project, "project", "", "project slug or ID")
	fs.StringVar(&f.sourceLocale, "source-locale", "", "the project's source locale (read from the server when a token is available)")
	fs.StringVar(&f.catalogs, "catalogs", "", "catalog file pattern (default "+defaultCatalogs+")")
	fs.StringVar(&f.ts, "typescript", "", "where `generate` writes the TypeScript module")
	fs.StringVar(&f.vue, "vue", "", "where `generate` writes the @klarlabs-studio/glossa/vue registration")
	fs.StringVar(&f.react, "react", "", "where `generate` writes the @klarlabs-studio/glossa/react registration")
	fs.StringVar(&f.goOut, "go", "", "where `generate` writes the Go accessors")
	fs.StringVar(&f.dartOut, "dart", "", "where `generate` writes the Dart accessors")
	fs.BoolVar(&f.force, "force", false, "overwrite an existing glossa.yaml")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	path := filepath.Join(inv.env.Dir, config.FileName)
	if _, err := os.Stat(path); err == nil && !f.force {
		return &Error{Exit: ExitUsage, Code: "config_exists", What: "glossa.yaml already exists", Where: path,
			Fix: "edit it, or pass --force to replace it"}
	}
	if err := inv.promptInit(&f); err != nil {
		return err
	}
	cfg := &config.Config{Version: 1, Server: strings.TrimRight(orDefault(f.server, defaultServer), "/"),
		Tenant: f.tenant, Project: f.project, SourceLocale: f.sourceLocale,
		Catalogs: config.Catalogs{Path: orDefault(f.catalogs, defaultCatalogs)},
		Extract:  defaultExtract,
		Generate: config.Generate{TypeScript: f.ts, Vue: f.vue, React: f.react, Go: f.goOut, Dart: f.dartOut},
		Path:     path}
	if cfg.Project == "" {
		return &Error{Exit: ExitUsage, Code: "invalid_usage", What: "which project?",
			Fix: "pass --project <slug> (the project's slug in Studio)"}
	}
	checked, err := inv.completeFromServer(ctx, cfg)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return &Error{Exit: ExitUsage, Code: "invalid_config", What: "can't write glossa.yaml", Why: err.Error(),
			Fix: "pass --source-locale (e.g. en) and --catalogs with {locale}"}
	}
	if err := cfg.Write(path, f.force); err != nil {
		return &Error{Exit: ExitUsage, Code: "write_failed", What: "can't write glossa.yaml", Where: path, Why: err.Error()}
	}
	out := initJSON{Schema: "glossa.cli.init/v1", Path: path, Config: cfg, Checked: checked}
	return inv.emit(out, func(p *printer) {
		p.line("%s Wrote %s", p.pass(), path)
		p.line("  project %s on %s, source locale %s, catalogs %s", cfg.Project, cfg.Server, cfg.SourceLocale, cfg.Catalogs.Path)
		if !checked {
			p.line("  %s not checked against the server (no token): run `glossa login`, then `glossa whoami`", p.caution())
		}
		p.line("Next: `glossa push` sends %s to the server.",
			strings.ReplaceAll(cfg.Catalogs.Path, config.LocalePlaceholder, cfg.SourceLocale))
	})
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// promptInit asks for what's missing on an interactive terminal.
func (inv *invocation) promptInit(f *initFlags) error {
	if !inv.env.Interactive || inv.json {
		return nil
	}
	r := bufio.NewReader(inv.env.Stdin)
	for _, q := range []struct {
		label string
		dst   *string
		def   string
	}{
		{"Server", &f.server, defaultServer},
		{"Project (slug)", &f.project, ""},
		{"Catalog files", &f.catalogs, defaultCatalogs},
	} {
		if *q.dst != "" {
			continue
		}
		v, err := inv.prompt(r, q.label, q.def)
		if err != nil {
			return usageError(inv.name, "no answer for %s", q.label)
		}
		*q.dst = v
	}
	return nil
}

// completeFromServer fills the tenant and source locale from the server
// when a token is at hand. It reports whether the server was consulted.
func (inv *invocation) completeFromServer(ctx context.Context, cfg *config.Config) (bool, error) {
	if _, _, err := inv.token(cfg.Server); err != nil {
		if cfg.SourceLocale == "" {
			return false, &Error{Exit: ExitUsage, Code: "invalid_usage", What: "which source locale?",
				Why: "there is no API token to read it from the server",
				Fix: "pass --source-locale (e.g. en), or run `glossa login --server " + cfg.Server + "` first"}
		}
		return false, nil
	}
	p, err := inv.connectWith(ctx, cfg)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == "project_not_found" {
			e.Where = "--project " + cfg.Project
			e.Fix = "create the project in Studio first, or pass the slug of an existing one"
		}
		return false, err
	}
	if cfg.SourceLocale != "" {
		want, err := bcp47.Parse(cfg.SourceLocale)
		if err != nil || want.String() != p.info.SourceLocale {
			return false, &Error{Exit: ExitUsage, Code: "source_locale_mismatch",
				What: fmt.Sprintf("the project's source locale is %s, not %s", p.info.SourceLocale, cfg.SourceLocale),
				Fix:  "drop --source-locale; init reads it from the server"}
		}
	}
	cfg.SourceLocale = p.info.SourceLocale
	if cfg.Tenant == "" {
		cfg.Tenant = p.scope.Tenant
	}
	return true, nil
}
