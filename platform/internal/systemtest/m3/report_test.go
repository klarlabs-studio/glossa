//go:build system

package m3_test

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m3/fixture"
)

var localeNames = map[string]string{"de": "German", "en": "English", "es": "Spanish", "fr": "French", "ja": "Japanese"}

// report renders REPORT.md: the fixture application, what the platform
// knows about where its messages appear, the pull request's timeline,
// what the overlay guards proved, and what the Go runtime rendered. It
// holds no timings and no IDs, so a run over the same fixture writes the
// same file.
func (s *scenario) report() []byte {
	var b bytes.Buffer
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	w("# M3 exit test — report\n\n")
	w("Written by `TestM3Exit` (`make system-m3`, or `go test -tags=system ./internal/systemtest/m3/...` in `platform/`)\n")
	w("against a real glossa-server on Postgres and MinIO, glossa-edge on the same bucket, a fake GitHub on loopback and\n")
	w("a headless Chrome the test starts. Every number below comes from the public API, from GitHub's own view of the\n")
	w("pull request, or from the browser; the test fails when an exit criterion does not hold. RFC 0004 §1, §12.\n\n")

	s.fixtureSection(&b)
	s.contextSection(&b)
	s.pullRequestSection(&b)
	s.guardSection(&b)
	s.documentSection(&b)
	return b.Bytes()
}

func (s *scenario) fixtureSection(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	f := s.f
	patterns := map[string]int{}
	files := map[string]bool{}
	for _, m := range f.Messages {
		patterns[m.Pattern]++
		files[m.Web.File] = true
	}
	w("## The application\n\n")
	w("- **%d messages** of Brotwerk's shop (seed %d, `internal/systemtest/m3/fixture`), source `%s`, complete `en`, `es`,\n",
		len(f.Messages), f.Seed, f.SourceLocale)
	w("  `fr` and `ja`: %d labels, %d rendered by `<GlossaText>`/`<T>`, %d plurals, %d with a `$name`, %d in a placeholder\n",
		patterns[fixture.PatternPlain], patterns[fixture.PatternComponent]+patterns[fixture.PatternMarkup],
		patterns[fixture.PatternPlural], patterns[fixture.PatternValues], patterns[fixture.PatternAttribute])
	w("  attribute, %d with markup.\n", patterns[fixture.PatternMarkup])
	w("- A **Vite + Vue** app over %d source files and **%d routes**, with **one React island** (`PayButton.tsx`) on\n",
		len(files), len(f.Pages))
	w("  `/kasse`, built by `@klarlabs-studio/glossa/unplugin`. A Go receipt renderer and its `text/template` reuse %d of the same\n", f.GoUsages())
	w("  messages, which is what `glossa extract` reads.\n")
	w("- `glossa push --translations` created %d messages and %d translations.\n\n", s.pushedMessages, s.pushedTranslations)

	w("| Route | Page | Messages |\n|---|---|---|\n")
	perRoute := map[string]int{}
	for _, m := range f.Messages {
		perRoute[m.Web.Route]++
	}
	for _, p := range f.Pages {
		w("| `%s` | `%s` | %d |\n", p.Route, p.File, perRoute[p.Route])
	}
	w("| _(every route)_ | `src/components/SiteHeader.vue`, `src/components/SiteFooter.vue` | %d |\n\n", perRoute[""])
}

func (s *scenario) contextSection(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	f, c := s.f, s.coverage
	w("## 1. Where every message appears\n\n")
	w("Three uploads for one commit (`%s`):\n\n", short(f.Commit))
	w("| Collector | Source | Usages | Unknown keys |\n|---|---|---|---|\n")
	w("| `@klarlabs-studio/glossa/unplugin` (the Vite build) | `plugin` | %d | %d |\n", s.pluginBuild.Usages, s.pluginBuild.UnknownKeys)
	w("| `glossa extract` (Go and templates) | `extract` | %d | %d |\n", s.extractBuild.Usages, s.extractBuild.UnknownKeys)
	w("| `glossa capture` (headless Chrome) | `capture` | %d captures | %d |\n\n",
		s.capture.Upload.Captures, len(s.capture.Upload.UnknownKeys))

	w("`glossa capture` drove the preview build over %d routes × %s × %s = **%d captures**, %d images stored and %d\n",
		len(f.Pages), localeList(f.CaptureLocales), viewportList(f.Viewports),
		s.capture.Upload.Captures, s.capture.Upload.ImagesStored, s.capture.Upload.ImagesDeduplicated)
	w("deduplicated.\n\n")

	w("### Coverage\n\n")
	w("| Criterion (RFC 0004 §1) | Result | Bar |\n|---|---|---|\n")
	w("| Active messages with ≥ 1 usage | %d / %d | all |\n", c.messages-len(c.noUsage), c.messages)
	w("| … naming a file, a line **and** a component | %d / %d | all |\n", c.messages-len(c.noComponent), c.messages)
	w("| … with ≥ 1 **visible** region | %d / %d | all |\n", c.messages-len(c.noRegion), c.messages)
	w("| Unused active messages | 0 | 0 |\n")
	w("| Messages `glossa capture` reported as not captured | %d | 0 |\n\n", len(s.capture.Coverage.NotCaptured))

	w("%d usages and %d regions in all (%d visible). By kind: %s. By collector: %s.\n\n",
		c.usages, c.regions, c.visible, counts(c.kinds), counts(c.sources))
	w("Visible regions per capture locale: %s. Per route: %s.\n\n", counts(c.locales), counts(c.routes))
	w("One capture's image was read back through the API (`…/captures/{capture}/image`): `image/png`, its digest as the\n")
	w("`ETag`, `Cache-Control: private`.\n\n")
}

func (s *scenario) pullRequestSection(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	f := s.f
	w("## 2. The pull request\n\n")
	w("`%s`, pull request #%d on `%s`, head `%s`. A fake GitHub signs the webhooks and serves the App's endpoints;\n",
		fixture.PRBranch, fixture.PRNumber, repositoryName, short(fixture.BranchCommit))
	w("everything else is the real server.\n\n")
	w("| Step | What the platform did |\n|---|---|\n")
	for _, e := range s.timeline {
		w("| %s | %s |\n", e.What, e.Then)
	}
	w("\n### The check\n\n")
	w("| Run | Head | Status | Conclusion | Title | Annotations |\n|---|---|---|---|---|---|\n")
	for i, c := range s.checks {
		w("| %d | %s | %s | `%s` | %s | %s |\n", i+1, short(c.HeadSHA), c.Status, c.Conclusion, c.Title, plural(c.Annotations, "annotation"))
	}
	w("\nThe first run refused `%s`: its plural has no catch-all variant, so the push never stored it, its usage stayed an\n", f.InvalidKey)
	w("unknown key, and the check annotated the line the product's own source uses it on — `%s:%d`. The repair was an\n",
		f.InvalidFile, f.InvalidLine)
	w("ordinary CI push; the %d missing Spanish, French and Japanese translations were written through an **in-context\n", s.inContextEdits)
	w("grant** (`glossa_ctx_…`, 15 minutes, bound to the project and to the preview deployment's origin, with\n")
	w("`translations.write` among its permissions) — the same call the in-product editor makes. The same grant was\n")
	w("refused on the tenant's API tokens and from another origin.\n\n")
	w("One sticky comment existed throughout: it was updated in place, never duplicated, and no annotation was sent twice.\n\n")
	w("### The branch environment\n\n")
	w("| Delivery key | Environment | Through glossa-edge | When |\n|---|---|---|---|\n")
	for _, r := range s.edgeReads {
		w("| %s | `%s` | %d | %s |\n", r.Key, r.Environment, r.Status, r.When)
	}
	w("\nThe default-branch push that follows the merge activated all %d proposed messages; `pull_request.closed` destroyed\n", s.activated)
	w("`%s`, and the manifest the preview key had just read became a 404.\n\n", fixture.PREnvironment)
}

func (s *scenario) guardSection(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## 3. The in-product editor is never in production\n\n")
	w("| Build | Files | Bytes | Loader markers found |\n|---|---|---|---|\n")
	for _, scan := range s.bundles {
		found := "none"
		if len(scan.Found) > 0 {
			found = "`" + strings.Join(scan.Found, "`, `") + "`"
		}
		w("| `%s` | %d | %d | %s |\n", scan.Build, scan.Files, scan.Bytes, found)
	}
	w("\nThe markers are what only the loader puts into a bundle: the attribute it sets on the script it adds, the\n")
	w("overlay's path on the Studio origin, that origin, and the SRI hash pinned at build time. The preview build holds\n")
	w("all four — otherwise the production scan would prove nothing — and the production build none.\n\n")
	w("The same preview build, the one that **does** carry the loader, was then driven in a real browser with the\n")
	w("`?glossa=edit` gesture:\n\n")
	w("| Release the runtime activated | Loader scripts on the page | Overlay registered |\n|---|---|---|\n")
	w("| `%s` | %d | — |\n", s.overlay.PreviewEnvironment, s.overlay.PreviewScripts)
	w("| `%s` | %d | no |\n", s.overlay.ProductionEnvironment, s.overlay.ProductionScripts)
	w("\nAgainst the production manifest the loader added nothing and said so: %q.\n\n", s.overlay.Warning)
}

func (s *scenario) documentSection(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	d := s.documents
	w("## 4. Documents in five locales\n\n")
	w("The Go runtime's own document tests and the fpdf example ran against `runtimes/go/testdata/documents`, which owns\n")
	w("the fixture and its goldens; the exit test runs them rather than keeping a second copy.\n\n")
	w("- Documents: %s.\n", code(d.Documents))
	w("- Locales: %s.\n", code(documentLocales))
	w("- Goldens compared: %d — one HTML file per document and locale, plus one `Runs` file per locale.\n", len(d.Goldens))
	w("- `examples/pdf` rendered %d PDFs with the Noto fonts (%d documents × %d locales).\n\n",
		d.PDFs, len(d.Documents), len(documentLocales))
	w("| Test | Documents | Locales |\n|---|---|---|\n")
	names := make([]string, 0, len(d.Tests))
	for n := range d.Tests {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w("| `%s` | %d | %d |\n", n, len(d.Documents), len(documentLocales))
	}
	w("\n")
}

// plural renders a count with its noun.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// counts renders a count map as `a 1, b 2`, sorted by name.
func counts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("`%s` %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func code(items []string) string { return "`" + strings.Join(items, "`, `") + "`" }

func localeList(locales []string) string {
	var parts []string
	for _, l := range locales {
		parts = append(parts, localeNames[l])
	}
	return strings.Join(parts, " and ")
}

func viewportList(viewports [][2]int) string {
	var parts []string
	for _, v := range viewports {
		parts = append(parts, fmt.Sprintf("%d×%d", v[0], v[1]))
	}
	return strings.Join(parts, " and ")
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
