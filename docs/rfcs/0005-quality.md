# RFC 0005 — Quality (M4)

**Status:** Proposed — 2026-09-20
**Builds on:** [RFC 0002](./0002-platform-architecture.md) §4 (Quality context), §9 (MCP), §10, §13 (M4) · [RFC 0003](./0003-knowledge-and-intelligence.md) §2.2–§2.3, §3.3, §4 · [RFC 0004](./0004-context.md) §2–§3, §6.4, §11 · [`runtimes/SPEC.md`](../../runtimes/SPEC.md)
**Intent:** §6.5, §11, §14–§15, §28–§31, §41, §43, §45–§47, §50, §60 (Phase 3), §65, §70

## 1. Goal

M4 makes localization **fail like a test** (intent §30): one command, one verdict, one vocabulary, in the terminal and in the pull request. Every layer of QA the intent names produces the same kind of finding (intent §29). A project decides what those findings mean for its CI (intent §30: "projects should configure policies"). Quality becomes a number someone can look at (intent §43). Agents get the same platform people get, through MCP (intent §47). And the Flutter runtime joins the contract, so Pet Medical's app renders from the same catalog as its web (intent §11).

M4 is done when three things hold:
- **Gating.** `glossa check` runs every layer, reads the project's policy from the server, and fails a fixture repository's CI on real findings from at least five layers, one of them visual. The Glossa PR check on the same commit reaches the **same verdict** from the same policy.
- **Observable.** Studio shows, per project and locale: coverage, outstanding findings by layer, AI acceptance and edit distance, review-queue age and context coverage — all from the contexts that already own the data.
- **Complete surfaces.** An MCP client with a tenant API token can search the catalog, read context, run a check, propose a translation and publish, within its scopes and never outside its tenant. The Dart runtime passes the shared SPEC fixtures in CI.

The internal exit test is §12. The real exit (`glossa check` gating CI in every dogfood product) happens in the dogfood phase.

**What exists.** M1–M3 built most of one layer and none of the rest:
- `platform/internal/kernel/checkpolicy` is the whole policy: `RequireComplete []string` and `FailOn Severity`, with `Requires`, `Fails` and `Severity(locale)`. Two knobs. It is shared, deliberately, by `glossa check` (`platform/internal/cli/cmd_check.go`) and the PR check (`platform/internal/integration/app/check_report.go`), so the two can't disagree. On the server it is still the zero value: `sources.Checks.Policy()` returns `checkpolicy.Policy{}, nil` and calls itself "the seam, and it is the only one". A **parallel slice fills that seam with a per-project, server-stored policy**; this RFC assumes it and builds the policy *document* (§4) on top of it.
- **A finding has seven shapes.** `messageformat.Finding` (code, severity, subject, detail — no key, no locale); `localization/domain.QAResult` over the same type, which is where `max-length-exceeded` lives and is persisted per revision in `translations.warnings jsonb`; `knowledge/domain.TermFinding` (the only one with byte **spans**, concept and term ids); `intelligence/domain.TermFinding`, its own narrower copy; `cli/terminology.Finding`, the wire shape; `cli/qa.Finding{Check, Code, Severity, Locale, Key, Subject, Detail, Message, Where}`; and `integration/app.CheckFinding{Code, Severity, Locale, Key, Message, File, Line}`. The last two are unification attempts that disagree with each other, and both conversions lose data: spans and term ids on the way to the CLI, `Check`/`Subject`/`Detail` on the way to the server.
- Structural validity is `messageformat.CheckCompat` over source and translation; the codes stay the kernel's (`missing-argument` and friends). Glossa's own codes are the seven in `checkpolicy`.
- Terminology QA (`term_missing`, `term_forbidden`) exists as a stateless server query (`GET …/terminology-findings`) and as `glossa check --terminology`. Nothing stores a finding. Nothing can waive one.
- **Almost no finding can become a GitHub annotation.** `annotations()` keeps only findings where `File != "" && Line > 0`, and the only place `File` and `Line` are ever set is a back-fill from `Usages.Unknown` keyed by message key. Terminology, `max_length` and missing-translation findings are summary-only, in a product that knows exactly where every message appears (RFC 0004 §2).
- There is **no `platform/internal/quality` package and no Quality table.** RFC 0002 §4 reserved the context and named its aggregates (`CheckRun`, `Finding`, `Policy`); M4 is where it gets built. `integration_github_checks` is the PR-check *queue* row: conclusions, attempts and `AnnotationFingerprint`s, and no findings at all, so nothing has a history and a dashboard has nothing to read.
- Confidence and review routing (RFC 0003 §3.3) exist, and `GET …/ai-metrics` already answers acceptance rate and mean edit distance per locale.
- **There is no MCP server anywhere** — not in the platform, not in v0.3. `platform/go.mod` has no `mcp-go`. The only traces of MCP in the tree are three comments promising it. Backlog P1.11 was never built.
- **There is no `runtimes/dart/`.** The JS runtimes are `runtime`, `elements`, `vue`, `react`, `astro`, `unplugin`, `capture`, `overlay`; Go is `runtimes/go`. The shared fixtures are `runtimes/testdata/{scenarios,loading,edge,usages,markup.json,schemas}`.
- `glossa capture` drives `scout` (`go.klarlabs.de/scout v1.15.3`) over CDP and can attach to a browser the caller started (`--cdp`, `GLOSSA_CAPTURE_CDP`, refused off loopback without `--cdp-allow-remote`), because scout's launcher passes a fixed Chrome flag list and nothing can add `--no-sandbox` — the gap is filed in `platform/README.md` ("scout follow-ups") asking for `scout.WithExtraArgs`, and not worked around. `platform/internal/cli/capture/browser.go` already builds a `scout/agent` session alongside the page, so scout's higher-level helpers are reachable where they're wanted.
- Studio has no dashboard view of any kind. Its project routes are `translate`, `review`, `terms`, `style`, `ai`, `locales`, `releases`, `files`, `settings`.

## 2. The Quality context

### 2.1 One finding, everywhere

Intent §29 names six layers. Six layers must not mean seven shapes. The Quality context owns exactly one:

```json
{
  "schema": "glossa.finding/v1",
  "fingerprint": "f_7c1a…",
  "layer": "length", "code": "expansion-excessive", "severity": "warning",
  "locus": {
    "message": "0192f5a1-…", "key": "checkout.pay", "locale": "fr",
    "revision": "0192f5b0-…", "namespace": "checkout",
    "file": "src/checkout/PaymentFooter.vue", "line": 42,
    "route": "/checkout/payment", "component": "PaymentFooter",
    "capture": "0192f5c2-…", "region": "r_18",
    "span": { "side": "target", "start": 12, "end": 19 }
  },
  "message": "French is 74 % longer than the German source; the button is 148 px wide.",
  "detail": { "ratio": 1.74, "max_length": null, "region_width_px": 148 },
  "fix": { "kind": "shorten", "to": 28, "hint": "Payer maintenant" }
}
```

- **`layer`** is one of the eight in §3. **`code`** is stable and shared with the CLI and the PR check, exactly as `checkpolicy`'s codes already are. The MessageFormat kernel's codes keep their spelling.
- **`severity`** is `error` or `warning` as today, plus `waived` (§2.3), which is a rendering of a finding, not a third rank a layer may emit.
- **`locus`** is everything that locates a finding. Every field is optional, every field means the same thing in every layer, and the context fields (`file`, `line`, `route`, `component`, `capture`, `region`) come from Context (RFC 0004 §2–§3) **at report time**, never from the layer. A layer says *what is wrong with which message in which locale*; Context says *where you'd look*. That one change is what turns almost every finding into a GitHub annotation, instead of only the ones that happen to be unknown keys.
- **`span`** carries the byte offsets and side that `knowledge/domain.TermFinding` already computes and every conversion currently throws away. It is what lets Studio and the overlay underline the offending words instead of the whole string.
- **`fix`** is structured where a layer can produce one (`replace` with exact text, `shorten` to a length, `use-term` with the preferred term's id, `adopt-source-change`), and absent otherwise. It is a hint. Nothing applies it without a person or an explicit `--fix` (§9).
- **`fingerprint`** is `sha256(layer, code, message id, locale, normalized subject)`, truncated. It deliberately **excludes file, line and revision**, so a reformatted file or a new source revision doesn't silently re-open every waiver — and so the same finding keeps one identity across `glossa check`, the PR check and the server job. It generalizes `domain.AnnotationFingerprint`, which the PR check already computes for exactly this reason (not to re-send an annotation GitHub has), and replaces it.

**One implementation, three callers.** `platform/internal/cli/qa` grows up into `platform/internal/quality`: `quality/domain` (the finding, the policy, the check run, the waiver), `quality/layers` (one package per layer behind the existing `Checker` port) and `quality/app`. The CLI links `quality/layers` directly and runs the deterministic ones offline. The server runs all of them. The PR check drops `app.CheckFinding` and renders `domain.Finding`; `cli/terminology.Report.QA()` and `qa.fromKernel` stop being lossy conversions and become constructors. Seven shapes become one. A layer that disagreed with itself across surfaces would undo the one property `checkpolicy` was created to protect.

### 2.2 Where each layer runs

| | Authoring (Studio) | `glossa check` | PR check | Server job |
|---|---|---|---|---|
| Needs only the catalog | live, per message | yes, offline too | yes | on write |
| Needs the termbase or style guide | live, per message | yes (fetches) | yes | on write |
| Needs captures | — | yes (last capture) | yes | on capture ingest |
| Needs a provider | on request | **never** | no | on request / batch |

Three rules follow, and they are the shape of the milestone:
1. **Deterministic layers run everywhere, from the same code.** A finding a translator sees while typing is the finding that fails the build.
2. **`glossa check` never calls an AI provider.** A check must be cheap, offline-capable, deterministic and free. The linguistic layer (§3.8) is a server-side job whose findings `glossa check` *reports* if they're already stored, and never computes. Trade-off: a fresh linguistic finding can appear after a green local check. That is the right way round — a check that cost money and gave a different answer twice would stop being run.
3. **A check run is stored.** `CheckRun` (project, ref: branch or environment, trigger, policy version, layer set, counts, conclusion) plus its findings, so the PR check, Studio, the dashboard and `glossa findings` read one row rather than four recomputations. Runs are kept 90 days; findings live as long as their run.

Events: `quality.check.completed` (Integration refreshes the PR check, Insights rolls up) and `quality.finding.waived`.

### 2.3 Waivers: how a finding is accepted

Some findings are correct and still fine. "Login" is the German term. The Japanese button *is* two lines by design.

- A **Waiver** is (project, fingerprint, reason, actor, created_at, optional `expires_at`, scope `project | branch`). The **reason is required and non-empty**; a waiver without one is a `400`. That is the whole mechanism.
- A waived finding is **still computed and still reported**, at severity `waived`, counted separately in the run and never able to fail a check. Hiding it would make the number go down without the product getting better, which is the failure mode of every suppression system.
- A waiver is **invalidated when the source revision it was made against changes**, because the German the translator waived is not the German that now ships. It reappears as a normal finding with "waived against source revision 7, now at 9".
- **Rejected alternative: inline suppression comments in code** (`// glossa:ignore term-forbidden`). Most findings are about a *translation*, which has no line of code to put a comment on, so the mechanism would cover a third of the cases and teach everyone to reach for the wrong one. Waivers live with the message, where the finding lives.
- Waivers are listed, filterable and revocable in Studio and in `glossa waive --list`. A daily sweep expires the ones with an `expires_at`; the dashboard shows waivers older than 90 days as their own number, because an unexamined waiver is technical debt with a reason attached.

## 3. The layers

Eight. The first four exist in some form; four are new. Every one of them emits §2.1 findings and is selectable by name in the policy and on the command line.

| Layer | Runs on | Finds | State |
|---|---|---|---|
| `structure` | source, translation | invalid MF2, invalid data model (intent §29.1) | M1 |
| `parity` | source × translation | missing/extra arguments, wrong selectors, wrong plural categories for the target locale, markup mismatch — `messageformat.CheckCompat` | M1 (as `arguments`) |
| `completeness` | project × locale | missing, outdated, unknown key, missing locale, key conflict | M1 |
| `terminology` | translation | `term_missing`, `term_forbidden` (intent §29.3) | M2 |
| `style` | translation | mechanical style-guide breaches | **new** |
| `length` | translation (+ region) | `max_length`, expansion ratio, layout budget | **new** |
| `locale` | translation | locale-convention and bidi breaches (intent §14–§15) | **new** |
| `source` | source | problematic source copy (intent §28) | **new** |
| `visual` | capture region | overflow, overlap, mirroring, glyphs (intent §29.4) | **new**, §5 |
| `linguistic` | source × translation | likely mistranslation, tone, semantic divergence (intent §29.2) | **new**, advisory |

### 3.1 `parity` is split out of `arguments`

`CheckCompat` already answers placeholder and markup parity; M4 only renames the layer and keeps every code. Markup parity gains one case the elements and the Go runtime both need: a translation that introduces markup the source doesn't have, or nests it differently, is `markup-mismatch` at `error` — a translation that can add a `<a>` is the attack the runtimes already refuse (SPEC §5, `markup.json`), and the check should say so before the runtime silently drops it.

### 3.2 `style`

Only the **mechanical** fields of the effective style guide (RFC 0003 §2.3), resolved through the existing tenant → project → locale → namespace stack:
- formality: the guide's pronoun set appears and its opposite doesn't (de `du`/`Sie`, fr `tu`/`vous`) → `formality-mismatch`
- typography: quotation marks, dash style, ellipsis character, space before units → `typography-mismatch`
- number and date conventions the guide states beyond CLDR → `convention-mismatch`
- trailing/double spaces, a full stop the source doesn't have and the guide forbids → `punctuation-mismatch`

The guide's **prose rules** (rationale plus good/bad examples) are **not** checked here. They're prompt material for M2's agent and evidence for the linguistic layer. A regex over a rationale would be a lie about what the system knows.

### 3.3 `length`

- `max_length` on the message (M2) → `max-length-exceeded`, `error`.
- **Expansion ratio**: the target's rendered length against the source's, compared with the target locale's norm (a table per language pair in `platform/internal/quality/layers/length/testdata`, seeded from CLDR-adjacent published expansion ranges and refined from the tenant's own approved translations). Outside the norm → `expansion-excessive` / `expansion-suspicious`, `warning`.
- **Layout budget**: where M3 gave the message a visible region (RFC 0004 §3.1), the check has real pixels. A translation whose measured advance width would exceed the region's width is `layout-overflow-predicted`, `warning` — computed without a browser, from the region's font metrics recorded at capture time. It's the cheap, deterministic half of §5, and it runs offline.

### 3.4 `locale`

Intent §14 and §15 say locale correctness is more than strings, and §41 says not to pretend every language is equally supported. This layer checks what CLDR can decide:
- literal digits, decimal and grouping separators in the text that contradict the locale's (a French translation writing `1,234.50`) → `number-convention`
- a narrow no-break space missing before `;:!?` in fr, or a normal space where the locale wants U+202F → `spacing-convention`
- literal date or time formats typed into the text instead of a placeholder → `date-literal`
- **bidi**: stray U+200E/U+200F/U+202A–U+202E in a translation (the runtime adds isolation itself, SPEC §5) → `bidi-stray-control`; an RTL translation whose leading run is LTR with no isolate → `bidi-leading-run`; digits in a script the locale doesn't use → `digit-shaping`
- a translation that is byte-identical to the source in a locale whose script differs from the source's → `untranslated-suspected`, `warning`

Each finding names the **capability level** of the locale (intent §41): a code the layer can't decide for a language whose data it lacks is not emitted, and the dashboard says which layers are available per locale rather than reporting a false green.

### 3.5 `source`

Intent §28 asks for this before translation, and intent §30 puts it in CI. It runs on the **source locale only**, and every code is a `warning` by default, because source copy is the product team's call:
- `manual-plural`: `(s)`, `(n)`, `1 item/items` in a message with no selector
- `concatenation-suspected`: a message that is a fragment (leading/trailing space, ends in a colon or an opening quote) and is used adjacent to another message on the same route (Context knows, RFC 0004 §8)
- `ambiguous-short`: a one-word message with no description and more than one distinct usage component ("Open", "Save")
- `missing-description`: a message with a placeholder or a selector and no description, because a translator can't resolve `{$count}` without one
- `hardcoded-format`: a literal currency symbol, percent sign or date pattern next to a plain `{$value}` instead of a typed placeholder

`glossa check --layer source` is the IDE and pre-commit surface intent §28 asks for; the PR check reports it as warnings unless a project's policy says otherwise.

### 3.6 `terminology` and `completeness` are unchanged

They gain the shared shape and waivers, nothing else — except that since wave 7 `completeness` also sees the translations of obsolete messages, and reports them as `unknown-key` (§14 decision 13). Terminology moves from a stateless query to a stored layer so a finding can be waived and counted.

### 3.7 Runtime QA

Intent §29 lists a runtime layer. SPEC §6 already defines the error channel (`network`, `integrity`, `signature`, `schema`, `format`, `missing-message`) and SPEC §6 also says runtimes send **no telemetry unless the application enables it**. RFC 0004 §14.4 ruled out production runtime reporting for M3, and M4 does not reopen it.

So M4's runtime QA is exactly this: the capture session (RFC 0004 §3.1, §5) already runs a real runtime, and the capture script **drains the error channel and uploads it with the capture**. `format` and `missing-message` errors become `visual` findings with a `runtime-*` code against the message that produced them. That is genuine runtime evidence, from an environment we control, with no new telemetry path. Sampled production error reporting stays a later RFC.

### 3.8 `linguistic`

The one layer that needs a model (intent §29.2). It is built on M2's machinery and inherits its rules wholesale: the provider port and routing policy (RFC 0003 §3.1), the per-tenant sending consent and the `sensitive` namespace rule (RFC 0003 §7), the budget cap, cassettes in CI and a golden set in `platform/internal/intelligence/evals` (RFC 0003 §4).

- It is a **job**, triggered explicitly or on a batch, never by a check.
- Its findings are `advisory`: they are `warning` and a policy **may not** raise them to `error`. A model's opinion does not fail a build. Trade-off: a real mistranslation only gates through a human in the review queue, which is where intent §24 wants it anyway.
- Its prompt is versioned like every other (RFC 0003 §3.2), and its output is structurally constrained to (code, span, explanation, optional suggestion), parsed and rejected on malformation, exactly as the translation agent's drafts are.
- Codes: `meaning-divergence`, `tone-mismatch`, `grammar-suspected`, `inconsistent-phrasing`.

## 4. CI policies

### 4.1 The policy document

Today's policy is two fields. Intent §30 asks for more, and different locales, namespaces and environments genuinely deserve different answers.

```yaml
schema: glossa.check-policy/v1
version: 7
fail_on: error            # lowest severity that fails a run
require_complete: [de, en]      # or: null = every locale
environments:
  production: { require_complete: [de, en, fr], require_review: approved }
  staging:    { require_complete: [de, en] }
rules:
  - { layer: source, severity: off }                       # advisory locally only
  - { layer: terminology, namespace: legal, severity: error }
  - { layer: length, locale: ja, code: expansion-excessive, severity: off }
  - { layer: visual, severity: warning, mode: warn }
```

- A **rule** selects on `layer`, `code`, `locale`, `namespace` and `environment` — any subset — and sets `severity` to `error`, `warning` or `off`. `off` means "don't compute", so a project doesn't pay for a layer it ignores.
- **Precedence is by specificity**: the rule matching on more fields wins; ties go to the later rule. It is stated in the schema and covered by a fixture table, because a policy nobody can predict is worse than no policy.
- A rule may not raise an `advisory` layer (§3.8) to `error`.
- `require_complete` keeps its meaning and gains a **per-environment** form. Release enforces it at publish: publishing to an environment whose completeness requirement isn't met fails with `policy_not_met` (409). It can be overridden with an explicit reason, which is audited — a hard block with no escape hatch gets routed around by disabling the check.

**The check policy is not the review policy.** M2's `ReviewPolicy` already routes on findings — its default `ForceReview` is `[term_forbidden, max_length]` — but it decides whether a *suggestion* needs a human, not whether a *build* fails. They stay separate instruments over the same findings, in separate contexts, with separate endpoints, because a project that auto-approves confidently and gates strictly is a perfectly sensible project. What they share is the finding.

### 4.2 One policy, two readers

The **server is the source of truth**, building on the per-project policy slice.
- `glossa check` fetches the policy and caches it at `.glossa/policy.json` with its version. A cached policy is used offline and the output says so. With no server and no cache, the built-in default applies (every locale required, `fail_on: error`) and the output says *that*, loudly — a check must never silently grade itself.
- `glossa.yaml`'s `check:` block and the flags (`--require-complete`, `--fail-on`, `--layer`) become **local overrides**. They are honoured for the local run, printed as `policy: server v7 + local overrides`, and **ignored by the PR check**. A developer can tighten or loosen their own loop; they cannot change what CI decides.
- Trade-off: a project that wants its policy in Git loses that. The alternative — the repository wins — means the same commit grades differently depending on which branch of `glossa.yaml` CI checked out, and makes a policy change a merge conflict. A policy is an organizational decision about a project, not a property of a commit. Export and import of the policy document keep it reviewable (§9).

### 4.3 Rolling out a stricter policy

The failure mode is obvious and avoidable: someone adds `terminology: error`, and forty open pull requests go red for something their authors didn't do.

- A policy has a monotonic `version`. Every `CheckRun` records the version it used.
- Every rule has a `mode`: `enforce` (default) or **`warn`**. In `warn` mode the rule computes and reports at its severity but cannot change the conclusion. `warn` is the on-ramp: ship the rule, watch the number, flip it.
- Saving a policy with a `grace_until` timestamp **pins open pull requests**: a check for a PR opened before the save keeps evaluating against the previous version until `grace_until` (default: 14 days), and its summary says which version it used and when that ends. New PRs get the new version immediately. Merging is never blocked by a rule the branch predates.
- `POST …/check-policy` with `dry_run: true` answers the **impact preview**: how many stored findings would change severity, and how many open pull requests would newly fail, per rule. Studio shows it before the save; `glossa policy diff` prints it.
- `glossa check --explain-policy` prints, per finding, the rule that decided its severity. "Why did this fail?" must have a mechanical answer.

### 4.4 Exit codes

Unchanged, and now documented as part of the contract (`0 ok · 1 check failed · 2 usage or config · 3 network or auth · 4 partial failure`):
- **0** — no finding at or above `fail_on` after waivers and policy.
- **1** — the policy failed the run. The only code CI should branch on.
- **2** — the policy couldn't be resolved, an unknown layer was named, or the configuration is wrong. Distinguishing this from 1 matters: a broken policy is not a clean build.
- **3** — the server or auth is unreachable *and* no cached policy exists. With a cache, the check runs offline and exits 0 or 1.
- **4** — some layers ran and some couldn't (the termbase was unreachable, the capture was missing). Findings are reported, the skipped layers are named, and CI can decide. Silently dropping a layer is the one behaviour a check may never have.

## 5. Visual QA via `scout`

### 5.1 Where it runs, and why there

**Visual QA runs where capture runs: in the product's CI** (RFC 0004 §3.2, §14.2). Glossa never navigates to a customer URL, needs no application credentials at rest and sees no production data. The same three reasons hold as in M3, and M4 adds a fourth: the checks below need **live layout**, not pixels. `scrollWidth`, `getClientRects()`, `getComputedStyle` and `document.fonts.check()` exist only while the page is open.

So `@klarlabs-studio/glossa/capture` grows a **probe pass**: `session.collect()` today returns `{renders, regions}`, and gains `probes`, computed after the regions and before the screenshot. It uploads with the capture as findings (§9). The server validates and stores them; it re-measures nothing. The package's size budget rises from **3 kB to 4 kB** (brotli) and the bundle test that proves no capture code reaches an application build (`runtimes/js/capture/src/bundle.test.ts`) keeps holding — the probes ship where the markers already ship, in a session, and nowhere else.

### 5.2 What it checks

Each check reads the regions M3 already produces — a message id, its boxes, its host element, whether it's visible (RFC 0004 §3.1) — plus a small amount of extra data the probe records.

| Code | How |
|---|---|
| `text-clipped` | the region's host has `overflow: hidden\|clip` or an ellipsizing `text-overflow`, and `scrollWidth > clientWidth + 1` or `scrollHeight > clientHeight + 1` |
| `region-overlap` | two message regions' rects intersect by more than 25 % of the smaller, and neither element contains the other |
| `region-not-visible` | M3's `visible: false` — zero-size, off-screen or `display: none` while the runtime rendered it |
| `line-growth` | the same (route, viewport, message) has more line boxes in the target locale than in the source capture, above a per-locale tolerance |
| `untranslated-on-screen` | the runtime's `explain()` (SPEC §6) says the region resolved from a fallback locale, or from the inline default, in a locale the manifest lists as translated |
| `mixed-locale` | two regions on the same screen resolved from different locales that aren't in one fallback chain |
| `rtl-not-mirrored` | in an RTL locale: the region's computed `direction` is `ltr`, or its host's inline-start padding/margin sits on the same physical side as in the LTR capture of the same route |
| `missing-glyph` | `document.fonts.check()` fails for the region's computed font and text, or the text measures at the `.notdef` advance |
| `runtime-format`, `runtime-missing-message` | drained from the runtime's error channel (§3.7) |

A visual finding carries `locus.capture` and `locus.region`, so Studio crops the stored image around the region and outlines it — the "Where it appears" pane from RFC 0004 §3.4, pointed at a finding instead of a message. The PR annotation uses the message's usage `file:line` and links to the finding in Studio, because GitHub can't show a screenshot on a diff line.

**Flake control.** A visual finding is a `warning` on first sighting and becomes eligible for `error` only when the **same fingerprint appears in two consecutive captures of the same (route, viewport, locale)**. Headless Chrome's text metrics move with font availability, and scout can pass no extra Chrome flags (the filed gap), so tolerances are in CSS pixels, never device pixels, and every threshold above is a policy-visible constant.

### 5.3 What is not attempted

- **Pixel diffing whole pages.** It answers "did this page change", not "is this localization wrong". Fonts, GPU rasterization and antialiasing differ between runners, so the signal-to-noise is poor and the maintenance is a baseline per route per locale per viewport. Every check above is a *semantic* assertion about a known region, and those don't flake on a driver update. Ruled out, not deferred.
- **OCR** of screenshots, and **screenshots as model input**. RFC 0004 §14.4 stands: images never go to an AI provider.
- **Contrast and general accessibility.** Contrast is a property of the product's design, identical in every locale; localization changes text, not colour. `glossa capture` already holds a `scout/agent` session, so `AriaViolations()` is one call away — which is exactly why this has to be a decision rather than an omission. Products get a11y from that call in their *own* suites. Reporting it as a Glossa finding would make Glossa's number go red for something Glossa cannot fix, and a quality number nobody can act on is a number people learn to ignore. (Direction-dependent a11y — a mirrored focus order — is genuinely localization's, and is filed for a later milestone.)
- **Native and mobile surfaces.** The probe is DOM-based. The Flutter runtime (§6) ships without capture, and visual QA stays web-only in M4.

## 6. Flutter runtime

### 6.1 Shape and shipping

`runtimes/dart/` holds **two** packages: `glossa`, pure Dart, and `glossa_flutter` in `runtimes/dart/flutter/`, which path-depends on it and re-exports it. The core stays testable with `dart test` and usable off Flutter.

*Amended during wave 3.* This section first called for one package with an optional Flutter layer at `glossa/flutter.dart`. That cannot be built: pub has no optional dependencies, so a single package with a Flutter layer must declare `flutter: {sdk: flutter}`, which makes the Flutter SDK a hard requirement of the whole package and breaks `dart pub get`, `dart test` and `dart compile js` on every host without it — including the `runtimes-dart` CI job, which installs Dart and not Flutter. Declaring nothing and importing `package:flutter` anyway fails `dart analyze`, and the only way to silence that is to exclude the file from analysis, i.e. never check the new code at all. Two packages serve this section's stated reason; one package defeats it. `runtimes/dart/analysis_options.yaml` excludes `flutter/**` so the core's analyzer and test run see only pure Dart, and `test/purity_test.dart` fails if anything under `lib/` imports `package:flutter`, or if anything but `lib/io.dart` imports `dart:io`. It is developed in the monorepo and consumed by Pet Medical from a path/git dependency until it is published; **whether it goes to pub.dev is an owner decision** (§15), and nothing in the design depends on the answer.

### 6.2 Satisfying `runtimes/SPEC.md`

| SPEC | Dart |
|---|---|
| §1.1–1.2 manifest and artifact schemas | decoded with `dart:convert`, validated against the same `testdata/schemas/*.json`; unknown fields ignored, a different `schema` major rejected |
| §1.3 integrity | SHA-256 over artifact bytes via `package:crypto`; **Ed25519** over the **RFC 8785 (JCS)** canonicalization of the manifest without `signatures`, via `package:cryptography` (pure Dart, no FFI, so it works on web and in AOT). JCS is implemented in Dart and covered by the shared fixtures — it is the one piece with no Dart prior art, so it gets its own test vectors. A runtime with configured keys rejects an unsigned manifest; mobile OTA **SHOULD** configure keys, per SPEC §1.3, and the Flutter README says so plainly |
| §2 endpoints | `package:http` with `If-None-Match`; no credentials, ever |
| §3 load order | memory → persisted (`path_provider`'s application-support directory: `manifest.json` plus `a/<sha256>.json`, the exact bundle layout SPEC §3.4 fixes) → network → **bundled** (the same layout as a Flutter asset, declared in the app's `pubspec.yaml`) → inline default. The persisted store follows the Go runtime's rule exactly (`runtimes/go/store.go`): every write is a rename of a fully written temporary file, **and the manifest is written last**, so a crash never leaves a manifest pointing at artifacts that aren't there. Verification and decode run in an isolate via `compute`, and the new release is swapped in with a single reference assignment, so activation is atomic and the UI never renders a half-loaded release |
| §4 negotiation | BCP 47 canonicalization in Dart (`ui.Locale` and `package:intl` don't canonicalize: `en_us` → `en-US`, `iw` → `he`), then RFC 4647 §3.4 lookup and the §4.2 chain, both driven by the shared scenarios |
| §5 formatting | an MF2 interpreter over the data model, with `package:intl` for numbers, currency, dates and plural categories. Markup goes through `parts()`; the Flutter layer's `GlossaText` builds a `Text.rich` where `safeTags` map to `TextStyle` and a link tag gets a recognizer **the application supplies** — markup options never become attributes, per SPEC §5 and `markup.json`. Formatting never throws: a failing expression renders its MF2 fallback |
| §6 observability | `explain(id, {locales})` returning the SPEC §6 document verbatim, and `Stream<GlossaError>` with the six types and the 60-second repeat suppression. **No telemetry**, no opt-in switch in M4 |

`glossa generate` gains `generate.dart`: typed accessors (`messages.checkout.pay(amount: …)`) from the same argument metadata that feeds TS and Go (intent §10).

### 6.3 Tested against the shared fixtures

The Dart tests are a **driver**, not a reimplementation: they read `runtimes/testdata/scenarios/*.json` and `runtimes/testdata/loading/*.json` from the repository and assert the same fields the JS driver (`runtimes/js/runtime/src/contract.test.ts`) and the Go driver (`runtimes/go/conformance_test.go`) assert — including the loading suite's whole contract: `publicKeys` as base64url raw Ed25519 (empty means verification off), the per-step edge responses with deliberately mismatched artifact bytes, `expActiveRelease`, `expSource`, `expErrors` in order, and `restartBefore`, which drops memory and keeps persisted storage. Plus `messageformat/testdata` runtime cases and `markup.json`. The fixtures are generated by `runtimes/testdata/gen/generate.py` and CI already fails on drift; Dart adds a consumer, not a generator. A new required CI job, `runtimes-dart`, runs them on the Flutter stable channel. A bug found in Dart becomes a fixture first, like every other runtime (SPEC §7).

*Amended during wave 4.* The two packages of §6.1 need two jobs, for the reason §6.1 gives: `runtimes-dart` installs the plain Dart SDK, which is what proves the core does not need Flutter, and `runtimes-flutter` installs the Flutter stable channel for `glossa_flutter` and the §6.4 size budget. `runtimes-dart` also runs `TestGeneratedDartAnalyzes` from the `platform` module, because it is the only job with a Dart SDK and that test had been skipping silently everywhere; with `CI=true` a missing SDK now fails it rather than passing it.

### 6.4 Budgets

- **Size**: the package's contribution to a release build, measured with `flutter build --analyze-size` against a fixture app with and without it, **≤ 150 kB** excluding `package:intl` — *superseded in wave 4 by **≤ 400 kB** over a baseline that already has `intl` and an HTTP client; see the amendment below, and do not quote the 150 kB.* `intl` carries its own CLDR and is measured and reported separately, because it is the app's dependency as much as ours — and reported honestly, because intent §33 says localization must not cost performance and a hidden megabyte is a lie.
- **Startup**: verifying a 200 kB manifest and its signature **≤ 30 ms** on a mid-range Android device; the first `t()` after a warm persisted cache **≤ 5 ms**; no jank frame on activation (the isolate is the reason).
- **AOT and web**: no `dart:mirrors`, no reflection, no `dart:io` in the core; a compile test proves the package builds for web.

*Amended during wave 4, where the budgets were built.* Two of the three are enforced in CI; one cannot be, and saying otherwise would be the lie this section exists to prevent. **The size budget's number changed: 150 kB is replaced by 400 kB.** That is the thing to read first:

- **The method is the delta, and the delta is what is enforced.** This section names its method — measured "against a fixture app **with and without it**" — and `runtimes/dart/flutter/tool/size_budget.dart` in the `runtimes-flutter` job gates exactly that. A byte count is deterministic, so a runner is as good as a phone for it. The gated number is the whole-app delta **over a realistic baseline**: a fixture that already carries `package:intl` formatting numbers, currency, percentages, dates and plurals, an `HttpClient` call and a file read and write — what an app that would adopt Glossa already has. Measured on macOS arm64 with Flutter 3.47.5: **343.1 kB per architecture, against a budget of ≤ 400 kB.**
- **Why 400 kB and not 150 kB.** 150 kB was this section's original figure and **nothing meets it by this section's own method**: the delta over that realistic baseline is 343 kB, and over a *bare* Flutter app 1048.8 kB (879.8 kB of it outside `package:intl`). Wave 4 measured first and then replaced the number deliberately, set above today's figure with room rather than at a comfortable distance. The alternative on the table was to keep 150 kB and gate `--analyze-size`'s per-library figure for our two packages alone (**133.7 kB**, which does fit) — rejected, because this section exists to stop a hidden megabyte and a gate that omits ~880 kB of what an app actually pays works against that purpose however honestly the rest is printed. Raising 400 kB again is an RFC change.
- **What an app with neither pays, reported on every run.** The delta over a bare Flutter app is **1048.8 kB per architecture**; that is what an app with no networking and no `intl` would pay, and it is the number intent §33 is really about. The gap between it and the gated 343 kB is not mostly our code: the `dart:io` HTTP client and `dart:_http` a transport needs (229 kB), `package:intl` (**169.0 kB**, reported separately as this section requires), the `dart:core` BigInt arithmetic the pure-Dart Ed25519 verifier uses (82 kB), `package:crypto` (16 kB). The baseline carries those because an app of that kind already does. The tool prints all four figures — gated delta, bare-app delta, bare-app delta excluding `intl`, and the attributed per-library figure — plus the full per-library decomposition, on every run, and its verdict line carries the assumption it rests on, so a green job cannot be quoted as "Glossa costs 343 kB" without it.
- **Bringing the number down** is a separate question, carried to the M4 exit measurement: the two candidates are the hand-written BigInt Ed25519 verifier and the non-optional `dart:io` transport.
- **The fixture has to be a realistic client, or the number is fiction.** Dart's AOT tree shaker is a global fixpoint: a `GlossaClient` with no transport, no bundle and an empty in-memory store can never activate a release, so the compiler proves the catalog, the formatter and all of `package:intl` unreachable and drops them. Such a fixture measures **17 kB** and proves nothing. The tool therefore refuses *either* baseline build carrying a single byte of ours, and refuses a Glossa build carrying too few.
- **AOT and web are enforced**: `dart compile js` and `dart compile exe` in `runtimes-dart`, plus `test/purity_test.dart`, which now also fails on `dart:mirrors` and `dart:ffi`.
- **Startup is measured and recorded, not gated.** `runtimes/dart/tool/startup_budget.dart` prints all three numbers against their budgets on every CI run and keeps them as an artifact, and fails on neither. The budgets are written *on a mid-range Android device*; a GitHub runner is not one, and neither is a laptop. Gating 30 ms on hardware several times faster than the device would pass a build that misses the budget on the phone — an enforcement the numbers do not support. What the tool does fail on are two properties that hold on any machine: canonicalization stays linear in manifest size, and the first `t()` after a warm cache costs a small fraction of one signature verification, i.e. a release is verified when it activates and not again per message. The device numbers are §12.7's, measured where the budget is written. For the record, AOT on an Apple M-series laptop: **≈10 ms** to verify a 202 kB manifest, **≈0.05 ms** to the first `t()`, **≈2.5 ms** longest event-loop stall on activation — and, on one run with the machine busy, 29 ms for the first of those, which is how much a wall-clock gate would be measuring the room rather than the code. That first number also has by far the least headroom against its 30 ms budget, and the reason is the hand-written BigInt Ed25519 verifier, which is the on-thread half of activation. Whether it needs replacing is a question for the device measurement, not for this one.

## 7. MCP

Intent §6.5 makes agents first-class users and §47 asks for MCP. RFC 0002 §9 named the surface and the library. Nothing was built. M4 builds it.

### 7.1 Shape

- **`glossa-server` serves MCP at `/mcp`** over streamable HTTP, on `mcp-go`, in the same process, behind the same middleware, tenancy and RLS as REST. Every tool is a thin call into an application port — never a second implementation of a rule, the same discipline that made `checkpolicy` shared. Intent §45 already requires that every capability be an API; MCP is a second façade on it, not a second platform.
- **`glossa mcp`** in the CLI is a **stdio proxy** to that endpoint, using the token `glossa login` already stored. Editors that speak only stdio work without a second server, and a self-hosted or air-gapped user gets the same tools. Trade-off: two transports to keep working; the proxy is thin enough (framing only) that it can't diverge.

### 7.2 Auth

**No new credential type.** A session presents an existing **tenant API token** (`glossa_api_…`) as a bearer token. `identity/app` already enforces authorization itself "so every adapter — REST today, MCP later — gets the same rules", so MCP inherits `Scope` (`read`, `write`, `publish`, `admin`) → `GrantForScopes` → the 23 `Permission` strings unchanged. Nothing about authorization is written twice.
- The token's **tenant is the session's tenant**. No tool takes a tenant argument, so there is nothing to confuse and nothing to escalate — the same binding the in-context grant uses for its project (RFC 0004 §5.2). Tokens are never locale-scoped, which is already true and stays true.
- **CI tokens (`glossa_ci_…`) and in-context grants (`glossa_ctx_…`) are refused.** Each is minted for one job or one origin; lending either to a long-lived agent session would widen it.
- **Writes need two locks**: the token must carry `write`, *and* the client must have opened the session with the write toolset (`--allow-write` on the proxy). A token is long-lived and an agent is not a person; one accidental tool call should not be able to rewrite a catalog.
- `admin` is not exposed at all in M4: no member, token, connection or tenant management, and **no delete tool of any kind**. Obsoleting a message is a state change and is available; destroying data is not.
- Every tool call is written to the audit ledger with the token id (`token:<uuid>` as `Actor` already spells it), the tool, the arguments' shape and the affected ids.

### 7.3 Tools

| Tool | Scope | Returns |
|---|---|---|
| `catalog_search` | read | messages by key prefix, namespace, state, text, `unused`, `not_captured` |
| `message_get` | read | source, arguments, description, `max_length`, neighbours, **usages and capture count** (RFC 0004 §2.2) |
| `translation_get` | read | a translation with its provenance, review state and outdated flag |
| `usages_get` | read | `file:line (component, route)` for a message |
| `tm_search` | read | exact and fuzzy TM matches with scores and provenance |
| `term_lookup` | read | concepts and terms recognized in a text, with status |
| `style_rules` | read | the effective style guide for a locale and namespace |
| `check_run` | read | runs the deterministic layers and returns findings and the policy verdict |
| `findings_list` | read | stored findings by layer, locale, severity, waived |
| `explain_delivery` | read | what a release and environment currently serve, and a message's resolution — `explain()` for the platform side |
| `message_upsert` | write | creates or revises source text |
| `translation_propose` | write | writes a translation revision with provenance `agent`, **always routed to review**, never auto-approved |
| `locale_add` | write | adds a target locale |
| `translate` | write | starts an M2 fill job and returns its id |
| `release_publish` / `release_promote` / `release_rollback` | publish | the existing Release operations |

Every result carries a short human explanation alongside the structured payload (intent §47: "structured, explainable answers"). Resources: the project's catalog and effective style guide. No prompts in M4.

### 7.4 Boundaries

- **No AI provider keys, in either direction.** `translate` runs as a server-side job under the tenant's own provider configuration and budget (RFC 0003 §3.1). The MCP client never sees a key, cannot set one, and `ai-providers` has no tool.
- **Agent writes are never auto-approved**, and this needs no new rule: `translations.review` is in no scope's permission set, because "review is a human decision and no scope grants it". `translation_propose` therefore writes a revision that enters review whatever the project's routing policy says, for the same reason a CI token can't approve one. M4 only makes it explicit in the tool's contract.
- The `sensitive` namespace rule (RFC 0003 §7) holds: `translate` refuses those namespaces, and `catalog_search` returns their messages only with `read` on the tenant, like any other reader.
- Per-tenant rate limits and the AI budget apply to MCP exactly as to REST; a runaway agent hits the same wall a runaway script does.

## 8. Quality dashboards

Intent §43 wants measurable operational health; intent §70 names the metrics. M4 shows **seven numbers**, per project and per locale, and no more, because a dashboard nobody reads is worse than a check that fails.

| Number | From |
|---|---|
| Coverage: translated / outdated / missing | Localization, `GET …/translation-stats` (exists) |
| Outstanding findings by layer and severity, plus waived | Quality, new |
| AI acceptance rate and mean edit distance per locale | Intelligence, `GET …/ai-metrics` (exists, RFC 0003 §3.3; Studio already renders it in `InsightsCard.vue`) |
| Review queue depth and **age** (p50/p90 of waiting items) | Intelligence, queue exists; age is new |
| Context coverage: share of active messages with a usage, and with a visible region | Context. Today `ContextCoverage` is three integers (`current_builds`, `active_messages`, `unused_messages`) and says nothing about captures; the ratios exist only as Prometheus gauges (RFC 0004 §11) and become a query |
| Lead time: source change → published translation, p50/p90 | Localization revisions × Release, new query |
| Check health: PR-check pass rate and median PR-event-to-conclusion | Integration, already a metric; new as a query |

- **No new time-series store in M4.** Everything above is computed from the owning context's own tables on read, cached 60 s, behind one endpoint. `chronos` and the Insights context (RFC 0002 §4) stay for M5. Trade-off: no arbitrary history. The one trend worth having now — findings by layer per day — is a small daily rollup in Quality's own table, because Quality owns the data and the rollup is three columns.
- **Studio**: a new child route of `ProjectLayout.vue` at `t/:tenant/p/:project/quality` — Studio has no dashboard route of any kind today, and its only quantitative surfaces are `ContextCoverage` and `InsightsCard`, both buried in other panes. The view has a health header, findings grouped by layer with the §2.1 filters, the visual findings rendered as cropped, outlined screenshots, and the waiver list; the existing presentational `QaFindings.vue` becomes the list component for `domain.Finding`. The project navigation shows one number: open errors. The projects list gains a per-locale row (coverage, open errors, queue age). Intent §41: a locale shows which layers are *available* for it, so an unsupported layer never reads as a green one.
- **CLI**: `glossa status --quality` prints the same seven numbers with `--json`, because intent §46 says the CLI is first-class and a dashboard that only exists in a browser breaks intent §45.

## 9. Surfaces

- **API** (one slice per wave touches `platform/api/openapi.yaml`, §13):
  - check runs (create, read, list) and findings (list, filter by layer, severity, locale, waived)
  - waivers (create with a required reason, list, revoke)
  - the check policy (read, write with `dry_run` impact preview, version history, export and import)
  - visual findings, as part of the existing capture upload
  - linguistic-QA jobs (create, read, cancel)
  - the quality summary (§8)
  - MCP at `/mcp`, documented in the OpenAPI description but not as REST paths
- **Studio:** the `quality` view, the policy editor with the impact preview, the waiver list, the project health header and the per-locale row.
- **CLI:**
  - `glossa check` on the layered library: `--layer`, `--explain-policy`, `--offline` against the cached policy, `--fix` for findings whose `fix` is exact and unambiguous (a preferred term, a typographic character), never for anything a model produced
  - `glossa findings [--layer --locale --severity --waived]`, `glossa waive <fingerprint> --reason`, `glossa policy show|diff|export|import`
  - `glossa capture --check` to run the visual probes with a capture
  - `glossa mcp` (stdio proxy)
  - all with `--json`
- **Runtimes:** `glossa` for Dart/Flutter; `glossa generate` gains `generate.dart`; the capture package gains the probe pass and the error-channel drain.

## 10. Privacy and security (intent §50)

- **Visual QA runs where capture runs** (§5.1): in the product's CI, against preview environments with fixture data, with `data-glossa-redact` blacked out before the screenshot and server-side re-encoding on upload (RFC 0004 §3.3, §10). M4 adds no server-side browser and no outbound navigation from the platform.
- **Findings can contain product copy.** They are tenant data under forced RLS in Quality's own tables, and they never go to the edge. The `sensitive` namespace rule holds for the linguistic layer exactly as it does for translation (RFC 0003 §7).
- **MCP tokens** are ordinary tenant API tokens, so revocation, expiry, listing and last-used are what already exists. No new long-lived credential is minted. CI tokens and in-context grants are refused (§7.2). Writes need both the scope and an explicit write session; `admin` and deletion are not exposed; every call is audited.
- **No AI provider key ever crosses MCP**, in either direction (§7.4).
- **Limits:** a check run holds at most 10,000 findings and a capture at most 500 visual findings; the PR check keeps its 200-annotation cap (`MaxAnnotations`). Per-tenant rate limits on the check, findings and MCP endpoints. The linguistic layer is bounded by the existing per-tenant AI budget.

## 11. Observability

- **Metrics** (Prometheus). Nothing counts a finding today — there is no `glossa_check_*` or `glossa_quality_*` metric anywhere. M4 adds, through a `quality/adapters/metrics` package built the way every other context's is (a `New(reg prometheus.Registerer)` constructor, the `register[C]` helper that tolerates `AlreadyRegisteredError`, an app-layer `Metrics` port with a no-op, and allowlisted label values to bound cardinality): `glossa_quality_findings_total{layer,code,severity}`, `glossa_quality_check_runs_total{trigger,conclusion}`, `glossa_quality_check_duration_seconds{layer}` (so a slow layer is visible before it is unbearable), `glossa_quality_waivers_total{outcome}`, `glossa_quality_visual_probes_total{code,outcome}`, `glossa_quality_policy_version{project}`, `glossa_mcp_tool_calls_total{tool,scope,outcome}` and `glossa_mcp_sessions_total{transport}`. The Dart runtime's budget measurements are CI build artifacts, not server metrics.
- **Traces** (OpenTelemetry): one trace per check run spanning every layer; one per MCP tool call, joined to the REST operation it wraps.
- **Logs** (`bolt`): check run ids, policy versions, fingerprints, token ids. Never message text, never translation text, never image bytes.

## 12. Exit test

`platform/internal/systemtest/m4` (`make system-m4`, Docker: Postgres, MinIO, headless Chrome) runs against a real `glossa-server` and writes `REPORT.md`, like M2 and M3.

1. **A fixture repository with real CI.** `testdata/repo` reuses the M3 fixture application and adds a `.github/workflows` job that runs `glossa check`. The test runs that job's commands against the real server. The project's policy (version 3) requires `de` and `en` complete, makes `terminology` an error in the `legal` namespace, and puts `visual` in `warn`.
2. **Findings across layers.** The fixture is seeded so the check produces, at minimum:
   - `structure`: one translation whose MF2 doesn't parse — *amended in wave 7, below*
   - `parity`: one French translation missing `{$amount}`, one Japanese one adding a `<a>` the source doesn't have
   - `completeness`: three missing `fr` translations, two outdated `ja` ones, one unknown key with its `file:line` — *amended in wave 7, below*
   - `terminology`: one `term_forbidden` in `legal` (error, by the policy) and one `term_missing` elsewhere (warning)
   - `style`: one German translation using `du` under a `Sie` guide
   - `length`: one French button over its `max_length`, one over its region's width
   - `locale`: one French translation writing `1,234.50` and one Arabic one with a stray U+202B
   - `source`: one `3 item(s)` and one `ambiguous-short`
   - `visual`: **a real one** — `glossa capture` drives Chrome over the fixture, and the Japanese checkout button clips (`text-clipped`) with a region the test reads back and crops
   `glossa check` exits **1**, and `--json` validates against `glossa.finding/v1`.
3. **The PR check agrees.** The same commit, through the fake GitHub of RFC 0004 §12, produces a check run whose conclusion, error count and per-layer counts **equal the CLI's**, with annotations on the located findings and one sticky comment. This is the exit criterion: two surfaces, one verdict.
4. **Waivers and policy rollout.** The `term_missing` is waived with a reason: it moves to `waived`, the counts change, the conclusion does not. The source revision behind it is then changed, and it reappears. A policy v4 promoting `visual` from `warn` to `enforce` is saved with `dry_run` first (the preview names the one PR that would newly fail), then saved with a `grace_until`: the open PR keeps grading against v3 and says so, a new PR grades against v4 and fails.
5. **Release gate.** Publishing to `production` with `fr` incomplete fails with `policy_not_met`; the forced publish succeeds and writes an audit entry with its reason.
6. **MCP.** An `mcp-go` client with a read-only token lists tools, searches the catalog, reads a message with its usages, runs a check and is **refused** on `translation_propose`. With a `write` token and a write session it proposes a translation, which lands in review and not as an approved revision. A second tenant's token sees none of the first tenant's project. A CI token and an in-context grant are both refused at connect.
7. **Flutter.** `runtimes/dart` passes every `runtimes/testdata/scenarios` and `loading` case, the MessageFormat runtime cases and `markup.json`; `explain()` matches the SPEC §6 document field for field; an unsigned manifest is rejected with keys configured; the size and startup budgets of §6.4 are measured and recorded in the report.
8. **Dashboard.** A Playwright test opens Studio's `quality` view against the same server and asserts the seven numbers match the API.

*Amended during wave 7 (§14 decision 12).* §12.2's `structure` case read "one translation whose MF2 doesn't parse", on the assumption that the fixture could seed one for the server-backed check to find. Building the exit test showed that no server project can hold one. Every write path parses before it stores: `localization/app.Service.prepare` parses each translation and refuses text that does not parse with `400 invalid_message` (and `QAResult.Gate` refuses one with error-severity findings), and a source revision — through the API or `glossa push` — is refused the same way. A check against the server therefore has no input for the layer, and a fixture that seeded one would be seeding a state the platform cannot reach. That is a stronger property than the criterion assumed, not a gap in the product. The case now reads: **`structure`: `glossa check --offline` over a local catalog holding a translation whose MF2 doesn't parse reports `invalid-translation`; and the server refuses to store that text — a translation write and a source revision carrying it are each answered `400 invalid_message`, and the stored text is unchanged.** The exit test asserts both halves; neither is argued in prose.

*Amended during wave 7 (§14 decision 13).* §12.2's `completeness` case read "one unknown key with its `file:line`" without saying which unknown key, or on which surface the line appears. Building the exit test showed that, against a server project, the case could not occur at all: the completeness layer emits `unknown-key` for a translation whose key no active message has, and both snapshots — the server's (`quality/adapters/snapshot`) and the CLI's (`cli/snapshot.FromServer`) — read only the translations of active messages, so a translation whose message the catalog obsoleted silently vanished from every check. A usage of a key no catalog has ever held is a different thing: Context counts it at `glossa context push` (`unknown_keys`), and only the pull request's fallback view (§14 decision 11) turns it into a finding. The owner decided to report orphaned translations. The case now reads: **`completeness`: a translation whose message the catalog has obsoleted is `unknown-key` — a warning, one per translation left behind, whose locus carries the obsolete message's catalog ID so its fingerprint is the one every surface computes; `glossa check` reports it with no location, because a catalog is not a file; where the product still asks for the key, the pull request renders that run with the finding located at the usage's `file:line` from Context (`integration/app.locate`), and annotates it there.** The exit test obsoletes `help.legacy.title` while the help page still asks for it and asserts both: the terminal's warnings against the obsolete message's ID in every locale that had a translation, and the pull request's annotation at `src/pages/HelpPage.vue:23`. Proving it end to end surfaced one more fix: the pull request's 200-annotation budget was spent before repeats were removed, so a finding repeated on one line (a missing translation in six locales) could crowd out every distinct annotation after it; the budget now counts distinct annotations, which is what GitHub is sent. And a check reads obsolete state from Localization's projection, like every bulk translation read, so an obsoletion is reported once the projection has handled Catalog's event.

**Dogfood exit, in the dogfood phase:** `glossa check` gates CI in every dogfood product, and Pet Medical's Flutter app renders from a Glossa release.

## 13. Work breakdown

Each slice is about an hour of agent work. At most one slice per wave edits `platform/api/openapi.yaml`, and slices in the same wave don't depend on each other.

| Wave | Task | Touches the API spec |
|---|---|---|
| 1 | Quality context: domain (Finding, CheckRun, Waiver, Policy), tables and migration, fingerprinting | no |
| 1 | `glossa.finding/v1` schema; `internal/cli/qa` → `internal/quality/layers` behind the `Checker` port; `structure`, `parity`, `completeness` move unchanged | no |
| 1 | `length` layer: `max_length`, expansion norms per language pair, layout budget from region metrics; fixtures | no |
| 1 | `locale` layer: number, spacing and date conventions, bidi and digit shaping; fixtures | no |
| 1 | `source` layer: manual plural, concatenation, ambiguous-short, missing description, hard-coded format; fixtures | no |
| 1 | MCP server skeleton on `mcp-go`: `/mcp` transport, API-token auth, tenant binding, session toolsets, audit | no |
| 1 | Dart core 1: BCP 47 canonicalization, RFC 4647 lookup, fallback chain, MF2 interpreter over the data model; scenarios fixtures pass | no |
| 2 | Quality API: check runs, findings list and filters, waivers (create with reason, list, revoke) | yes |
| 2 | `style` layer over the effective style guide (mechanical fields only); `terminology` becomes a stored layer | no |
| 2 | Policy document: schema, selectors, specificity precedence, versions, `mode: warn`, grace; `checkpolicy` becomes its evaluator | no |
| 2 | Dart core 2: loader (memory → persisted → network → bundled → inline), isolate decode, atomic activation, SHA-256, Ed25519 + JCS; loading fixtures pass | no |
| 2 | MCP read tools: `catalog_search`, `message_get`, `translation_get`, `usages_get`, `tm_search`, `term_lookup`, `style_rules`, `findings_list`, `explain_delivery` | no |
| 3 | Policy API: per-project read and write, `dry_run` impact preview, version history, export and import | yes |
| 3 | Visual probe pass in `@klarlabs-studio/glossa/capture`: `session.collect()` gains `probes` — clipping, overlap, line growth, RTL mirroring, glyphs, `explain()`-based untranslated and mixed locale, error-channel drain; fixtures, the 4 kB budget and the bundle test | no |
| 3 | `glossa check` on the Quality library: layers, `--layer`, policy fetch and `.glossa/policy.json` cache, `--explain-policy`, exit codes 1–4 | no |
| 3 | Dart Flutter layer: `GlossaText`, `parts()` with `safeTags`, `explain()`, error channel, asset-bundle layout; `glossa generate` Dart accessors | no |
| 3 | MCP write tools: `message_upsert`, `translation_propose` (always to review), `locale_add`, `check_run`, `translate`; the two-lock write gate | no |
| 4 | Capture API: visual findings on the capture upload, validation and storage, findings by capture and region | yes |
| 4 | PR check on the layered report: `domain.Finding` replaces `app.CheckFinding`, per-layer grouping, annotations, waived shown separately, policy version in the summary | no |
| 4 | `glossa capture --check`, the two-sighting promotion rule, the visual layer's thresholds as policy constants | no |
| 4 | `runtimes-dart` CI job; the §6.4 size and startup budgets, measured and enforced | no |
| 4 | Studio: the `quality` view — findings by layer, filters, cropped visual findings, the waiver flow | no (consume) |
| 5 | Quality summary API: the seven numbers per project and locale, cached, plus the findings-by-day rollup | yes |
| 5 | Release publish gate on the environment's completeness requirement (`policy_not_met`, forced with an audited reason) | no |
| 5 | MCP release tools behind `publish`; `glossa mcp` stdio proxy; the MCP README | no |
| 5 | Studio: project health header, the per-locale quality row, per-locale layer availability | no (consume) |
| 6 | Linguistic-QA jobs API: create, read, cancel; advisory severity enforced server-side | yes |
| 6 | The `linguistic` layer: prompt version, constrained output, cassettes, golden set, budget | no |
| 6 | CLI: `glossa findings`, `glossa waive`, `glossa policy show\|diff\|export\|import`, `glossa status --quality`, `glossa check --fix` | no |
| 6 | Studio: the policy editor with the impact preview, waiver management | no (consume) |
| 7 | Waiver expiry sweep, check-run retention, fingerprint-stability tests, the §11 metrics | no |
| 7 | M4 exit system test (`make system-m4`) and CI job | no |

**Deferred from the RFC 0002 M4 row, explicitly:**
- **Semantic QA as its own layer.** Intent §29 lists it beside linguistic QA; M4 folds `meaning-divergence` into `linguistic` rather than building a second model-backed layer. Splitting them is a later decision that needs eval evidence, not a guess.
- **Production runtime telemetry.** RFC 0004 §14.4 stands: runtime QA in M4 is capture-session evidence only (§3.7).
- **Pixel diffing, OCR, contrast and general a11y** (§5.3), and visual QA on native surfaces.
- **A time-series store.** `chronos` and the Insights context stay M5 (§8).
- **Regional adaptation checks** (intent §40) and the vendor/workflow side of quality (intent §42), which belong to M5's workflow engine.

## 14. Decisions

These are decisions this RFC takes, each with what it costs.

1. **One finding model, one implementation, three callers.** `internal/quality` replaces all seven shapes a finding has today, and the locus carries the spans and the `file:line` that every current conversion drops. Cost: a refactor of working code in wave 1. Benefit: the property `checkpolicy` was created to protect — the terminal and the pull request cannot disagree — extends to every layer.
2. **`glossa check` never calls an AI provider.** The linguistic layer is a server-side job whose stored findings a check reports but never computes. Cost: a linguistic finding can appear after a green local check. Benefit: the check stays free, offline-capable and deterministic, which is what makes people run it.
3. **The server owns the check policy; `glossa.yaml` can only override locally**, and the PR check ignores local overrides (§4.2). Cost: a project can't keep its policy in Git. Benefit: one commit has one verdict, and a policy change isn't a merge conflict.
4. **A stricter policy rolls out with `mode: warn`, a version, a `dry_run` impact preview and a `grace_until` that pins open pull requests.** Cost: two policy versions can be live at once, and the check has to say which it used. Benefit: nobody wakes up to forty red pull requests.
5. **Findings are waived with a required reason, never hidden.** A waived finding still computes, still shows, still counts separately, and comes back when its source revision changes. No inline code suppression. Cost: the number never goes to a comfortable zero. Benefit: the number is true.
6. **Visual QA is semantic assertions on known regions, measured live in the page during capture, in the product's CI.** No pixel diffing, no OCR, no contrast, no server-side browser. Cost: it can't catch a purely visual regression nobody described. Benefit: it doesn't flake on a driver update, and Glossa never navigates to a customer URL.
7. **No new time-series store in M4.** Seven numbers, computed from the owning contexts on read, plus one daily findings rollup in Quality's own table. Cost: no arbitrary history until M5. Benefit: no premature infrastructure for a dashboard whose shape isn't proven.
8. **MCP reuses tenant API tokens and mints no new credential.** CI tokens and in-context grants are refused. Writes need the scope *and* an explicit write session. No `admin`, no delete tools, no provider keys, and agent-written translations always enter review. Cost: a slightly clumsier first-run for an agent. Benefit: MCP adds no new way to lose a catalog.
9. **The Dart runtime is a driver over the shared fixtures, not a reimplementation of the test suite.** It ships pure-Dart Ed25519 and JCS so it works on web and in AOT with no FFI. Cost: JCS in Dart has no prior art and needs its own vectors. Benefit: one contract, four runtimes, one place a bug is fixed.
10. **The `parity`, `length`, `locale` and `source` layers are deterministic and offline.** Everything a model decides is `advisory` and cannot be raised to `error` by policy. Cost: a genuine mistranslation only gates through a human. Benefit: a build never fails on an opinion.
11. **The pull-request check renders the run CI recorded; it does not compute one of its own** (§12.3, decided by the owner in wave 7 after the exit test measured the divergence). `glossa check` — and `glossa capture --check`, the same check with the visual layer — records what it found through `createCheckRun`, and the check finds that run **by the commit** and renders it: conclusion, per-layer counts, annotations, waived apart, policy version. The commit's recorded run joins the push and the usages upload as a third thing the check waits for — **but only in a project known to record runs**, one with at least one reported run (`cli`, `pull_request`, `api`; not `capture` or `write`) on the record. A project that has never recorded one is not waited for: its check reports as soon as the push and the usages arrive, as it did before this decision. (The first cut waited in every project, and left every pull request of a repository without `glossa check` in CI — the dogfood products today — silent for `CheckWait`, thirty minutes; M3's exit test caught it.) Where no run was recorded, the check falls back to Integration's own narrower view of the branch and **says so on the summary's first line and in the comment**, naming the layers it does not cover. "Known to record" is learned from the record, not configured, and it has one honest edge: a repository's first pull request after adding `glossa check` to its CI still has no run on the record, so it is not waited for — it reports the labelled reduced view, and when its run lands `quality.check_run.recorded` re-renders it from the run, which can move the verdict (green, then red). From the next pull request on, the check waits. Symmetrically, a project whose reported runs have all been swept by the 90-day retention (the newest run of each ref is kept) stops being waited for. We accept the edge: it happens once per repository, it is labelled while it lasts, and the alternative — a per-connection "CI runs `glossa check`" setting — is a second source of truth that goes stale the day someone removes the step, which is exactly the silent half-hour this amendment removes. A stronger signal exists if the edge ever matters: CI declaring it at push time (`glossa push` saying the pipeline will record a check), which the server could trust per commit; it is not built because the history signal needs nothing new from CI. Costs: a repository that does not run `glossa check` in CI gets the reduced view, labelled, and its first pull request after adopting it may flip once; the check can only re-grade what the policy decides by *rule*, so a severity the completeness layer took from `require_complete` rides with the run it was recorded in (the report re-emits that one against the version grading the pull request, and nothing else). Benefit: the two surfaces cannot disagree by construction rather than being tested for agreement and found wanting — one computation, two presentations.
12. **Unparseable text is refused at write, so `structure` is proven offline** (§12.2, decided by the owner in wave 7). No write path stores a message or translation that does not parse, which makes the layer unreachable against a server project; the exit test proves the layer through `glossa check --offline` and proves the refusal by sending unparseable text through the write paths and reading back the unchanged text. Cost: a server-backed check never reports `structure`, so a reader who sees zero there has to know it means "cannot happen", not "was not checked". Benefit: a stored catalog is always one every runtime can load, which is worth more than a finding about one that is not.
13. **A translation whose message was obsoleted is reported, as `unknown-key`** (§12.2, decided by the owner in wave 7). Both snapshots carry the translations of obsolete messages apart from the catalog's translations — never among them, where `glossa pull` would write a dead translation back into a catalog file — and the completeness layer reports each by the obsolete message's ID. **Severity: `warning`**, like every `unknown-key` before it. §3.6 keeps the completeness layer's severities as they were, and the layer cannot tell a key the code still uses from one nobody references: that is Context's, and it arrives after the run, when the pull request locates the finding — which may place a finding and never regrade one, or the two surfaces would stop agreeing (§14 decision 11). A project that wants a still-used key to fail the build says so by rule, `{layer: completeness, code: unknown-key, severity: error}`. **Bound:** each reader reads one page of `layers.MaxOrphans` (100, the listing's largest page) per twenty locales from the same translation listing in the same (key, message ID, locale) order, never follows it past the first page, and the layer sorts, reports the first 100 and adds one project-wide `unknown-key` saying there are more; the server's bulk read of live translations (`localization/app.LiveTranslations`) no longer loads the obsolete messages' rows at all. Cost: a project that obsoleted many messages carries up to a hundred standing warnings until it restores those messages or waives the findings, and past a hundred a still-used key sorted late is only in the roll-up. Benefit: text the product may be showing for a key the catalog says does not exist is no longer invisible, and a check's cost does not grow with a project's history.

## 15. Open questions for the owner

1. **Is the MCP server hosted, local-only, or both?** This RFC builds both (a `/mcp` endpoint on `glossa-server` and a `glossa mcp` stdio proxy over it) and assumes the hosted endpoint is **off by default and enabled per tenant**. If it should be on by default, or if only the local proxy should ship in M4 while the hosted endpoint waits for Glossa's product status (RFC 0002 §16), the wave-1 and wave-5 MCP slices change shape.
2. **Does the Flutter runtime ship to pub.dev in M4?** The package is developed at `runtimes/dart/` either way. Publishing means a public package name (`glossa` is unclaimed) and a public release cadence, which is the same open question as npm scope and the Go module path (RFC 0002 §16) — and it is the first Glossa artifact that would be public before that question is settled. Until it's answered, Pet Medical consumes it by path.
3. **Should `glossa check` default to failing on `warning`?** Today's default is `fail_on: error`, and four of the new layers are warnings by default. A project that never changes its policy therefore gets more information and no more gating. That is deliberate; whether the dogfood products should instead start at `fail_on: warning` with `source` and `visual` explicitly off is a policy call, not an engineering one.
4. **How long is a waiver allowed to live unexamined?** The design has an optional `expires_at` and a dashboard number for waivers older than 90 days. If waivers should instead *require* an expiry — say, at most 180 days — that is a one-line change now and a painful one later.
5. **Does the release publish gate get an escape hatch?** §4.1 says publishing to an environment whose completeness requirement isn't met can be forced with an audited reason. The alternative is a hard block. A hard block is safer and, in our experience of gates without escape hatches, gets routed around by turning the requirement off.
6. ~~**Which number is the 150 kB size budget about?**~~ **Decided in wave 4: the delta, at ≤ 400 kB over a realistic baseline.** Raised by the wave-4 measurement, which found that §6.4's own method — a fixture app built with and without the package — gives about **880 kB excluding `package:intl`** over a bare Flutter app and **343 kB** over one that already has `intl` and an HTTP client, while `--analyze-size`'s per-library figure for the two packages gives **133.7 kB**. Only the last meets 150 kB. The choice was between gating the per-library figure at 150 kB and gating the delta at a number the runtime can hold. **The delta was chosen, and 150 kB replaced by 400 kB**, because this section exists to stop a hidden megabyte: gating a number that omits ~880 kB of what an app actually pays works against that purpose however transparently the rest is printed, and enforcing something other than the method the section names invites exactly the drift §6.4 was written to prevent. 400 kB sits above today's 343 kB with room and not at a comfortable distance. Bringing the number down is carried forward as a risk against the M4 exit measurement rather than as a budget change: the two candidates are the hand-written BigInt Ed25519 verifier — also the startup budget's tightest number — and the non-optional `dart:io` transport.
