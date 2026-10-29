package fixture

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.klarlabs.de/glossa/messageformat"
)

// generated is the banner every generated source file of the app
// carries, so nobody edits one by hand. It deliberately avoids the
// "Code generated … DO NOT EDIT." line: both @klarlabs-studio/glossa/unplugin and
// `glossa extract` skip files that carry it (that is how the output of
// `glossa generate` stays out of the usages), and this application is
// the one the exit test wants usages from.
const generated = "Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file."

// AppFiles renders the fixture application: the catalogs the CLI
// pushes, the Vue app with its React island, the Go receipt renderer
// and its template, and the capture plan. Paths are relative to the
// app's directory, with forward slashes.
func AppFiles(f *Fixture) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, locale := range Locales {
		b, err := marshal(f.Catalog(locale, f.Messages))
		if err != nil {
			return nil, err
		}
		out["locales/"+locale+".json"] = b
	}
	catalogs, err := models(f)
	if err != nil {
		return nil, err
	}
	out["src/catalogs.json"] = catalogs

	out["package.json"] = []byte(packageJSON)
	out["index.html"] = []byte(indexHTML)
	out["glossa.yaml"] = []byte(glossaYAML(f))
	out["src/runtime.ts"] = []byte(runtimeTS)
	out["src/main.ts"] = []byte(mainTS(f))
	out["src/islands/mount.ts"] = []byte(mountTS)
	out["src/styles.css"] = []byte(stylesCSS)
	out["internal/receipt/receipt.go"] = []byte(receiptGo(f))
	out[goTemplateFile] = []byte(receiptTemplate(f))
	out["build.json"] = mustMarshal(buildInfo(f))

	for _, a := range areas {
		body := ""
		if a.island {
			body = island(f, a)
		} else {
			body = sfc(f, a)
		}
		out[a.file] = []byte(body)
	}
	// The pull request's version of the checkout page: the same file
	// with the five new keys on it. The build script overlays it to
	// collect the branch's usages (§12.2).
	out["branch/"+BranchFile] = []byte(branchSFC(f))
	return out, nil
}

// BranchFile is the one file the pull request changes.
const BranchFile = "src/pages/CheckoutPage.vue"

// branchSFC is the checkout page as the pull request leaves it: the five
// new keys rendered below the page's own copy.
func branchSFC(f *Fixture) string {
	var a area
	for _, c := range areas {
		if c.file == BranchFile {
			a = c
		}
	}
	body := sfc(f, a)
	var added strings.Builder
	for _, m := range f.NewKeys {
		added.WriteString("    " + line(m) + "\n")
	}
	marker := "    <div ref=\"island\"></div>\n"
	return strings.Replace(body, marker, added.String()+marker, 1)
}

func mustMarshal(v any) []byte {
	b, err := marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// buildInfo is what the JS build script needs: the build's identity, the
// route map the plugin fills usages from, and the Studio the overlay
// loader would talk to.
func buildInfo(f *Fixture) map[string]any {
	routes := map[string][]string{}
	for _, a := range areas {
		if a.route == "" {
			continue
		}
		routes[a.route] = append(routes[a.route], a.file)
	}
	for _, v := range routes {
		sort.Strings(v)
	}
	return map[string]any{
		"application": f.Application,
		"commit":      f.Commit,
		"branch":      f.Branch,
		"routes":      routes,
		"keys":        f.Keys(),
		"studio": map[string]string{
			// A stand-in Studio: nothing loads the script, the bundle
			// scan only has to find (or not find) these strings.
			"origin":    "https://studio.glossa.test",
			"integrity": "sha384-" + strings.Repeat("Q", 64),
			"tenant":    "ten_m3fixture",
			"project":   "prj_m3fixture",
		},
	}
}

// models renders every locale's catalog as the MF2 data model the
// runtime ships in an artifact, so the built app renders offline.
func models(f *Fixture) ([]byte, error) {
	out := map[string]map[string]json.RawMessage{}
	for _, locale := range Locales {
		byKey := map[string]json.RawMessage{}
		for key, text := range f.Catalog(locale, f.Messages) {
			msg, err := messageformat.ParseMF2(text)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", locale, key, err)
			}
			raw, err := json.Marshal(msg)
			if err != nil {
				return nil, err
			}
			byKey[key] = raw
		}
		out[locale] = byKey
	}
	return marshal(out)
}

const packageJSON = `{
  "name": "@glossa/m3-fixture-shop",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "description": "Brotwerk's shop: the M3 exit test's fixture application. Generated; built by ` + "`pnpm --filter @klarlabs-studio/glossa-unplugin build:m3`" + `, never installed."
}
`

const indexHTML = `<!doctype html>
<html lang="de">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Brotwerk</title>
    <script type="module" src="/src/main.ts"></script>
  </head>
  <body>
    <div id="app"></div>
  </body>
</html>
`

const stylesCSS = `:root { color-scheme: light; }
body { margin: 0; font: 16px/1.5 Arial, Helvetica, sans-serif; color: #1b1b1b; background: #fff; }
main { padding: 16px; max-width: 1100px; margin: 0 auto; }
h1 { font-size: 24px; margin: 0 0 12px; }
nav, footer { padding: 12px 16px; background: #f4efe7; }
.line { margin: 0 0 10px; font-size: 15px; }
.field { width: 220px; padding: 4px; font: inherit; }
.island { padding: 12px; border: 1px solid #d8cfc0; margin-top: 12px; }
`

const runtimeTS = `// ` + generated + `
// The page's one runtime. The release is built in the page from the
// generated catalogs, so the app renders with no network and no server:
// ?env= picks the manifest's environment (preview by default), which is
// what the capture's production refusal and the overlay loader's guard
// read.
import { createRuntime } from "@klarlabs-studio/glossa";
import type { Artifact, BundledRelease, Manifest, Message } from "@klarlabs-studio/glossa";

import catalogs from "./catalogs.json";

const LOCALES = Object.keys(catalogs as Record<string, unknown>).sort();

function release(environment: string): BundledRelease {
  const artifacts: Record<string, Artifact> = {};
  const refs: Manifest["artifacts"] = {};
  LOCALES.forEach((locale, n) => {
    // Bundled artifacts are trusted as shipped; the digest only has to
    // match the manifest's.
    const sha = String(n + 1).repeat(64).slice(0, 64);
    const messages = (catalogs as Record<string, Record<string, Message>>)[locale]!;
    artifacts[sha] = { schema: "glossa.artifact/v1", locale, namespace: "default", messages };
    refs[locale] = { default: { sha256: sha, size: 1 } };
  });
  return {
    manifest: {
      schema: "glossa.manifest/v1",
      project: "prj_m3fixture",
      environment,
      release: { id: "rel_m3fixture", version: 1, createdAt: "2026-09-20T08:00:00Z" },
      sourceLocale: "de",
      locales: LOCALES.map((code) => ({ code, direction: "ltr" as const })),
      fallback: { "*": ["de"] },
      artifacts: refs,
    },
    artifacts,
  };
}

const query = new URLSearchParams(globalThis.location?.search ?? "");
export const environment = query.get("env") ?? "preview";
export const locale = query.get("lang") ?? "de";
export const runtime = createRuntime({
  bundled: release(environment),
  environment,
  locales: locale,
  storage: null,
});
`

const mountTS = `// ` + generated + `
// Mounts the one React island of the checkout page into an element of
// the Vue tree, sharing the page's runtime.
import { createElement } from "react";
import { createRoot } from "react-dom/client";
import { GlossaProvider, createGlossa } from "@klarlabs-studio/glossa/react";

import { runtime } from "../runtime";
import { PayButton } from "./PayButton";

export function mountPayButton(el: HTMLElement): () => void {
  const glossa = createGlossa({ runtime });
  const root = createRoot(el);
  root.render(createElement(GlossaProvider, { glossa }, createElement(PayButton)));
  return () => root.unmount();
}
`

// mainTS renders the app's entry: the Vue plugin over the shared
// runtime, and a router that shows one page per route pattern.
func mainTS(f *Fixture) string {
	var b strings.Builder
	b.WriteString("// " + generated + "\n")
	b.WriteString(`import { createApp, h } from "vue";
import { createGlossa } from "@klarlabs-studio/glossa/vue";

import "./styles.css";
import { runtime } from "./runtime";
import SiteHeader from "./components/SiteHeader.vue";
import SiteFooter from "./components/SiteFooter.vue";
`)
	for _, p := range f.Pages {
		b.WriteString("import " + p.Component + " from \"./pages/" + p.Component + ".vue\";\n")
	}
	b.WriteString("\n/** Route pattern → page, longest concrete path first. */\nconst routes = [\n")
	for _, p := range f.Pages {
		b.WriteString("  { route: " + quote(p.Route) + ", match: " + matchExpr(p.Route) + ", page: " + p.Component + " },\n")
	}
	b.WriteString(`] as const;

function current() {
  const path = location.pathname.replace(/\/+$/, "") || "/";
  return routes.find((r) => r.match(path)) ?? routes[0];
}

const app = createApp({
  render: () => h("div", [h(SiteHeader), h("main", [h(current().page)]), h(SiteFooter)]),
});
app.use(createGlossa({ runtime }));
app.mount("#app");
`)
	return b.String()
}

// matchExpr compiles a route pattern into a path test.
func matchExpr(route string) string {
	if !strings.Contains(route, "[") {
		return "(p: string) => p === " + quote(route)
	}
	prefix := route[:strings.Index(route, "[")]
	prefix = strings.TrimSuffix(prefix, "/")
	return "(p: string) => p.startsWith(" + quote(prefix+"/") + ")"
}

// sfc renders one Vue single-file component: one line per message, in
// the order the fixture lists them, so a usage's line is predictable.
func sfc(f *Fixture, a area) string {
	messages := messagesOf(f, a.file)
	needsText := false
	for _, m := range messages {
		if m.Web.Kind == KindComponent {
			needsText = true
		}
	}
	var b strings.Builder
	b.WriteString("<!-- " + generated + " -->\n")
	b.WriteString("<script setup lang=\"ts\">\n")
	if needsText {
		b.WriteString("import { GlossaText } from \"@klarlabs-studio/glossa/vue\";\n")
	}
	if a.component == "CheckoutPage" {
		b.WriteString("import { onBeforeUnmount, onMounted, ref } from \"vue\";\n\nimport { mountPayButton } from \"../islands/mount\";\n\nconst island = ref<HTMLElement | null>(null);\nlet unmount: (() => void) | undefined;\nonMounted(() => {\n  if (island.value) unmount = mountPayButton(island.value);\n});\nonBeforeUnmount(() => unmount?.());\n")
	}
	b.WriteString("</script>\n\n<template>\n")
	b.WriteString("  <" + tagOf(a) + " class=\"" + sectionClass(a) + "\">\n")
	if a.title != "" {
		b.WriteString("    <h1>" + a.title + "</h1>\n")
	}
	for _, m := range messages {
		b.WriteString("    " + line(m) + "\n")
	}
	if a.component == "CheckoutPage" {
		b.WriteString("    <div ref=\"island\"></div>\n")
	}
	b.WriteString("  </" + tagOf(a) + ">\n</template>\n")
	return b.String()
}

func tagOf(a area) string {
	switch a.component {
	case "SiteHeader":
		return "nav"
	case "SiteFooter":
		return "footer"
	default:
		return "section"
	}
}

func sectionClass(a area) string {
	if a.page {
		return "page"
	}
	return "chrome"
}

// line renders one message in a Vue template.
func line(m Message) string {
	switch m.Pattern {
	case PatternPlural:
		return `<p class="line">{{ $t("` + m.Key + `", { count: 3 }) }}</p>`
	case PatternValues:
		return `<p class="line">{{ $t("` + m.Key + `", { name: "Lina" }) }}</p>`
	case PatternAttribute:
		return `<p class="line"><input class="field" :placeholder="$t('` + m.Key + `')" /></p>`
	case PatternComponent, PatternMarkup:
		return `<p class="line"><GlossaText id="` + m.Key + `" /></p>`
	default:
		return `<p class="line">{{ $t("` + m.Key + `") }}</p>`
	}
}

// island renders the React island: the component is the nearest
// enclosing capitalized function, so every usage names PayButton.
func island(f *Fixture, a area) string {
	messages := messagesOf(f, a.file)
	var b strings.Builder
	b.WriteString("// " + generated + "\n")
	b.WriteString("import { T, useGlossa } from \"@klarlabs-studio/glossa/react\";\n\nexport function PayButton() {\n  const { t } = useGlossa();\n  return (\n    <div className=\"island\">\n")
	for _, m := range messages {
		switch m.Pattern {
		case PatternPlural:
			b.WriteString("      <p className=\"line\">{t(" + quote(m.Key) + ", { count: 3 })}</p>\n")
		case PatternValues:
			b.WriteString("      <p className=\"line\">{t(" + quote(m.Key) + ", { name: \"Lina\" })}</p>\n")
		case PatternComponent, PatternMarkup:
			b.WriteString("      <p className=\"line\"><T id=" + quote(m.Key) + " /></p>\n")
		default:
			b.WriteString("      <p className=\"line\">{t(" + quote(m.Key) + ")}</p>\n")
		}
	}
	b.WriteString("    </div>\n  );\n}\n")
	return b.String()
}

// messagesOf are the messages a file uses, in fixture order.
func messagesOf(f *Fixture, file string) []Message {
	var out []Message
	for _, m := range f.Messages {
		if m.Web.File == file {
			out = append(out, m)
		}
	}
	return out
}

// receiptGo renders the server-side receipt: Go calls the extractor
// reads with go/ast, whose component is receipt.Render.
func receiptGo(f *Fixture) string {
	var b strings.Builder
	b.WriteString("// " + generated + "\n//\n// Package receipt renders Brotwerk's receipt. It reuses the invoice\n// page's copy, so `glossa extract` reports a second, server-side usage\n// for those messages (RFC 0004 §2.1).\npackage receipt\n\nimport (\n\tglossa \"go.klarlabs.de/glossa/runtimes/go\"\n)\n\n// Render writes the receipt's lines for one reader.\nfunc Render(l *glossa.Localizer) []string {\n\treturn []string{\n")
	for _, m := range f.Messages {
		for _, g := range m.Go {
			if g.File == goCallsFile {
				b.WriteString("\t\tl.T(" + quote(m.Key) + ", nil),\n")
			}
		}
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

// receiptTemplate renders the Go template: {{t "…"}} calls, which the
// extractor reads with text/template/parse. They carry no component.
func receiptTemplate(f *Fixture) string {
	var b strings.Builder
	b.WriteString("{{/* " + generated + " */}}\n")
	b.WriteString("{{define \"receipt\"}}<!doctype html>\n<html lang=\"{{lang}}\" dir=\"{{dir}}\">\n  <body>\n")
	for _, m := range f.Messages {
		for _, g := range m.Go {
			if g.File == goTemplateFile {
				b.WriteString("    <p>{{t " + quote(m.Key) + "}}</p>\n")
			}
		}
	}
	b.WriteString("  </body>\n</html>{{end}}\n")
	return b.String()
}

// glossaYAML renders the project file the CLI reads: the catalogs it
// pushes, what `glossa extract` scans (the server side only — the web
// side comes from the bundler plugin), and the capture plan of §3.2.
func glossaYAML(f *Fixture) string {
	var b strings.Builder
	b.WriteString("# " + generated + "\n")
	b.WriteString("# The server and the project come from GLOSSA_SERVER and GLOSSA_PROJECT;\n# the base URL from `glossa capture --base-url`.\n")
	b.WriteString("version: 1\nserver: http://127.0.0.1:1\nproject: " + Application + "\nsource_locale: " + SourceLocale + "\nsyntax: mf2\ncatalogs:\n  path: locales/{locale}.json\nextract:\n  application: " + Application + "\n  include:\n    - \"internal/**/*.go\"\n  templates:\n    - \"templates/**/*.tmpl\"\ncapture:\n  application: " + Application + "\n  base_url: http://127.0.0.1:1\n  locales: [" + strings.Join(f.CaptureLocales, ", ") + "]\n  locale:\n    query: lang\n  viewports:\n")
	for _, v := range f.Viewports {
		mobile := ""
		if v[0] < 700 {
			mobile = ", mobile: true"
		}
		b.WriteString("    - { width: " + strconv.Itoa(v[0]) + ", height: " + strconv.Itoa(v[1]) + mobile + " }\n")
	}
	b.WriteString("  routes:\n")
	for _, p := range f.Pages {
		if p.Route == p.URL {
			b.WriteString("    - { route: " + quote(p.Route) + " }\n")
			continue
		}
		b.WriteString("    - { route: " + quote(p.Route) + ", url: " + quote(p.URL) + " }\n")
	}
	return b.String()
}

// setLines records where the pull request's new keys are used, so the
// test can check the check's annotations against lines it predicted.
func setLines(f *Fixture) error {
	body := strings.Split(branchSFC(f), "\n")
	for i := range f.NewKeys {
		want := line(f.NewKeys[i])
		at := 0
		for n, l := range body {
			if strings.TrimSpace(l) == want {
				at = n + 1
				break
			}
		}
		if at == 0 {
			return fmt.Errorf("%s is not on the branch's checkout page", f.NewKeys[i].Key)
		}
		f.NewKeys[i].Line = at
	}
	f.InvalidLine = f.NewKeys[invalidIndex].Line
	return nil
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
