package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/catalog"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/release"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

type pulledLocale struct {
	Locale   string `json:"locale"`
	Path     string `json:"path"`
	Messages int    `json:"messages"`
	// Skipped counts translations left out by review state.
	Skipped  map[string]int `json:"skipped"`
	Outdated int            `json:"outdated"`
	Changed  bool           `json:"changed"`
}

type pullJSON struct {
	Schema  string          `json:"schema"`
	States  []string        `json:"states"`
	Locales []pulledLocale  `json:"locales"`
	Release *release.Bundle `json:"release,omitempty"`
}

var reviewStates = []string{"approved", "needs_review", "draft", "rejected"}

const pullUsage = `pull [--states approved,needs_review|all] [--locales de,fr]
       glossa pull --release <id|v<N>|latest> [--environment NAME] [--out DIR]

With --release, writes the release's bundle (manifest.json + a/<sha256>.json,
runtimes/SPEC.md §3.4) as --environment serves it. "latest" is the release
--environment serves now; for a release ID the default environment is the one
it was published to.`

func runPull(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(pullUsage)
	states := fs.String("states", "", "review states to pull (comma-separated, or all; default glossa.yaml's pull.states, else approved)")
	locales := fs.String("locales", "", "only these locales (comma-separated)")
	releaseRef := fs.String("release", "", "write this release's bundle: an ID, v<N>, or latest (what --environment serves)")
	environment := fs.String("environment", "", "with --release: the environment the bundle's manifest is for")
	outDir := fs.String("out", "glossa-release", "with --release: the bundle directory")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if err := noMore(inv, pos); err != nil {
		return err
	}
	switch {
	case *releaseRef == "" && *environment != "":
		return usageError(inv.name, "--environment only applies with --release")
	case *releaseRef == "latest" && *environment == "":
		return usageError(inv.name, "--release latest needs --environment <name>: latest is the release that environment serves")
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	if *releaseRef != "" {
		return inv.pullRelease(ctx, cfg, *releaseRef, *environment, cfg.Resolve(*outDir))
	}
	want, err := pullStates(inv, *states, cfg.Pull.States)
	if err != nil {
		return err
	}
	only, err := localeSet(inv, *locales)
	if err != nil {
		return err
	}
	s, _, err := inv.snapshot(ctx, cfg, false, snapshot.Options{})
	if err != nil {
		return err
	}
	style := catalog.Flat
	if cfg.Catalogs.Style == "nested" {
		style = catalog.Nested
	}
	out := pullJSON{Schema: "glossa.cli.pull/v1", States: want, Locales: []pulledLocale{}}
	for _, l := range s.TargetLocales() {
		if only != nil && !only[l.Code] {
			continue
		}
		pl, err := writePulled(s, l.Code, cfg.PullPath(l.Code), want, style)
		if err != nil {
			return &Error{Exit: ExitUsage, Code: "write_failed", What: "can't write a catalog", Where: cfg.PullPath(l.Code), Why: err.Error()}
		}
		pl.Path = relPath(cfg, pl.Path)
		out.Locales = append(out.Locales, pl)
	}
	return inv.emit(out, func(p *printer) {
		for _, l := range out.Locales {
			note := ""
			if !l.Changed {
				note = p.dim(" (unchanged)")
			}
			p.line("%s %s: %s → %s%s", p.pass(), l.Locale, plural(l.Messages, "message", "messages"), l.Path, note)
		}
		if len(out.Locales) == 0 {
			p.line("No locales to pull: the project has only its source locale.")
		}
	})
}

func pullStates(inv *invocation, flagValue string, fromConfig []string) ([]string, error) {
	list := fromConfig
	if flagValue != "" {
		list = strings.Split(flagValue, ",")
	}
	if len(list) == 0 {
		return []string{"approved"}, nil
	}
	if len(list) == 1 && list[0] == "all" {
		return []string{"approved", "needs_review", "draft"}, nil
	}
	for _, s := range list {
		ok := false
		for _, known := range reviewStates {
			ok = ok || s == known
		}
		if !ok {
			return nil, usageError(inv.name, "unknown review state %q (approved, needs_review, draft, rejected, or all)", s)
		}
	}
	return list, nil
}

func localeSet(inv *invocation, flagValue string) (map[string]bool, error) {
	if flagValue == "" {
		return nil, nil
	}
	set := map[string]bool{}
	for _, l := range strings.Split(flagValue, ",") {
		tag, err := bcp47.Parse(strings.TrimSpace(l))
		if err != nil {
			return nil, usageError(inv.name, "%q is not a locale", l)
		}
		set[tag.String()] = true
	}
	return set, nil
}

func writePulled(s *snapshot.Snapshot, locale, path string, states []string, style catalog.Style) (pulledLocale, error) {
	pl := pulledLocale{Locale: locale, Path: path, Skipped: map[string]int{}}
	entries := map[string]string{}
	for key, t := range s.Translations[locale] {
		if !contains(states, t.State) {
			pl.Skipped[t.State]++
			continue
		}
		entries[key] = t.Text
		if t.Outdated {
			pl.Outdated++
		}
	}
	pl.Messages = len(entries)
	changed, err := catalog.Write(path, entries, style)
	pl.Changed = changed
	return pl, err
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pullRelease writes a release's bundle for environment: "latest" is
// what the environment serves now; for a release the default
// environment is the one it was published to.
func (inv *invocation) pullRelease(ctx context.Context, cfg *config.Config, ref, environment, dir string) error {
	p, err := inv.connectWith(ctx, cfg)
	if err != nil {
		return err
	}
	rc := inv.releaseClient(p)
	var id string
	if ref == "latest" {
		env, err := rc.svc.Environment(ctx, rc.scope, environment)
		if err != nil {
			return inv.releaseError(err, "can't read environment "+environment)
		}
		if env.CurrentReleaseID == "" {
			return &Error{Exit: ExitNetwork, Code: "no_release", What: environment + " serves no release yet",
				Fix: fmt.Sprintf("publish one with `glossa release publish --environment %s`, or promote one with `glossa release promote`", environment)}
		}
		id = env.CurrentReleaseID
	} else {
		rel, err := rc.resolve(ctx, inv, ref)
		if err != nil {
			return err
		}
		id, environment = rel.ID, orDefault(environment, rel.Environment)
	}
	b, err := release.WriteBundle(ctx, rc.svc.BundleSource(rc.scope, id, environment), dir, release.BundleRef{ReleaseID: id, Environment: environment})
	if err != nil {
		var ae *remote.APIError
		if errors.As(err, &ae) {
			return inv.releaseError(err, "pull --release failed")
		}
		return &Error{Exit: ExitNetwork, Code: "bundle_failed", What: "can't write the release bundle", Where: dir, Why: err.Error(),
			Fix: "retry; if the bytes keep failing their hash, report it: storage or a proxy is altering them"}
	}
	b.Dir = relPath(cfg, dir)
	out := pullJSON{Schema: "glossa.cli.pull/v1", Locales: []pulledLocale{}, Release: &b}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s release v%d (%s): %s, %s → %s", pr.pass(), b.Version, b.Environment,
			strings.Join(b.Locales, ", "), plural(b.Artifacts, "artifact", "artifacts"), b.Dir)
		detail := fmt.Sprintf("%s, release %s; runtimes load it as their bundled release for environment %q (runtimes/SPEC.md §3.4)",
			humanBytes(b.Bytes), b.ReleaseID, b.Environment)
		if b.Removed > 0 {
			detail += fmt.Sprintf("; removed %s of an earlier bundle", plural(b.Removed, "artifact", "artifacts"))
		}
		pr.line("  %s", pr.dim(detail))
	})
}
