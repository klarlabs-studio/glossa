# M4 exit test — report

Written by `TestM4Exit` (`make system-m4`, or `go test -tags=system ./internal/systemtest/m4/...` in
`platform/`) against a real glossa-server on Postgres and MinIO, a fake GitHub on loopback, a headless
Chrome the test starts, the Dart SDK, and Studio's own Playwright suite pointed at the same server.
Every number below comes from the public API, from GitHub's own view of the pull request, from the
browser, or from the runtime's own test suites. RFC 0005 §12.

## The verdict

**8 of the 8 exit criteria hold.**

| § | Criterion | Verdict |
|---|---|---|
| 12.1 | A fixture repository with real CI | met |
| 12.2 | Findings across layers | met |
| 12.3 | The PR check agrees | met |
| 12.4 | Waivers and policy rollout | met |
| 12.5 | Release gate | met |
| 12.6 | MCP | met |
| 12.7 | Flutter | met |
| 12.8 | Dashboard | met |

## §12.1 — a fixture repository with real CI

`testdata/repo` is the **M3 fixture application** — Brotwerk's shop, 150 messages over eight routes,
source `de`, targets `en`, `es`, `fr`, `ja`, with its committed Vite build — plus what M4 adds: a
`.github/workflows/quality.yml` job that runs `glossa push`, `glossa context push`, `glossa check --json`
and `glossa capture --check --upload`; a capture plan for `/kasse` in German and Japanese; and one
stylesheet that gives the checkout page a fixed-width pay button. The test materializes the repository,
runs the workflow's commands, and never writes to the committed M3 fixture.

The project's check policy is **v3**: `require_complete: [de, en]`, `terminology` an error in the
`legal` namespace, `visual` in `warn`, and a `production` environment that also requires `fr`.

`glossa push --translations` reported 784 created, 0 failed, 0 revised, 0 unchanged, 0 updated.

The seeded defects, and the layer each is for:

| Seed | Layer §12.2 names it under |
|---|---|
| `checkout.payment.activity`: the German source gains `{$amount}`; every locale but French follows it | `parity` |
| `checkout.roll.help`: the German source drops its `{#link}`; every locale but Japanese follows it | `parity` |
| three French translations that were never written (`cart.flour.title`, `cart.shipping.title`, `cart.tray.title`) | `completeness` |
| two German sources that move under their Japanese (`home.allergen.title`, `products.bun.title`) | `completeness` |
| `help.legacy.title`: its message is obsoleted while its translations stay, and the help page still asks for it at `src/pages/HelpPage.vue:23` | `completeness` |
| `checkout.pickup.reminder`, used at `src/pages/CheckoutPage.vue:31` and in no catalog — Context's unknown key, which `glossa context push` counts | — |
| `legal.privacy.notice` (namespace `legal`), whose French uses the forbidden `vie privée` | `terminology` |
| `cart.basket.title`, whose Spanish does not use the preferred `carrito` | `terminology` |
| `checkout.bagel.title` with `max_length: 20` and a French translation over it | `length` (see below) |
| the checkout pay button, 104 px wide and single-line, holding `checkout.crust.label` | `visual` |

And the pull request §12.4's impact preview has to name: #10 (`feature/opening-hours`), opened and checked by its CI
while `main` still held the default branch's catalogs. Its newest recorded run passes under v3; the one
finding v4 would fail it on is the Japanese pay button, which clips on every branch.

## §12.2 — findings across layers

`glossa check --terminology --explain-policy --json` graded the project against policy v3 from the server
and exited **1**: conclusion `failure`, **4 errors, 558 warnings, 0 waived** over 156 active messages.

| Locale | Required | Translated | Missing | Outdated | Errors | Warnings | Complete |
|---|---|---:|---:|---:|---:|---:|---|
| `de` _(source)_ | no | 156 | 0 | 0 | 0 | 40 | yes |
| `ar` | no | 1 | 155 | 0 | 0 | 156 | no |
| `de-AT` | no | 1 | 155 | 0 | 0 | 157 | no |
| `en` | yes | 156 | 0 | 0 | 0 | 28 | yes |
| `es` | no | 156 | 0 | 0 | 0 | 82 | yes |
| `fr` | no | 153 | 3 | 1 | 3 | 91 | no |
| `ja` | no | 156 | 0 | 3 | 1 | 4 | yes |

The layers the run computed: `completeness`, `length`, `locale`, `parity`, `source`, `structure`, `style`, `terminology`.

### The nine cases §12.2 names

| Layer | What §12.2 asks for | What this run produced | Codes |
|---|---|---|---|
| ✅ `structure` | one translation whose MF2 doesn't parse | 1 from `glossa check --offline` (`invalid-translation`); the server refused both writes of text that does not parse (`invalid_message`) and kept what it had, so no stored message can carry one | `invalid-translation` |
| ✅ `parity` | one French translation missing `{$amount}`, one Japanese one adding markup the source doesn't have | 2 findings (2 error, 0 warning) | `markup-extra`, `missing-argument` |
| ✅ `completeness` | three missing `fr`, two outdated `ja`, one unknown key with its file:line | 321 findings (0 error, 321 warning); `unknown-key` on `help.legacy.title` at `src/pages/HelpPage.vue:23` on the pull request | `missing-translation`, `outdated-translation`, `unknown-key` |
| ✅ `terminology` | one `term_forbidden` in `legal` (error), one `term_missing` elsewhere (warning) | 10 findings (1 error, 9 warning) | `term_forbidden`, `term_missing` |
| ✅ `style` | one German translation using `du` under a `Sie` guide | 1 findings (0 error, 1 warning) | `formality-mismatch` |
| ✅ `length` | one French button over its `max_length`, one over its region's width | 244 findings (1 error, 243 warning) | `expansion-excessive`, `expansion-suspicious`, `layout-overflow-predicted`, `max-length-exceeded` |
| ✅ `locale` | one French translation writing `1,234.50`, one Arabic one with a stray U+202B | 27 findings (0 error, 27 warning) | `bidi-stray-control`, `number-convention`, `spacing-convention` |
| ✅ `source` | one `3 item(s)` and one `ambiguous-short` | 40 findings (0 error, 40 warning) | `ambiguous-short`, `manual-plural`, `missing-description` |
| ✅ `visual` | a real one: Chrome over the fixture, the Japanese checkout button clips | 1 findings (0 error, 1 warning) | `text-clipped` |

**The `structure` layer** (§12.2 as amended in wave 7). `glossa check --offline` over a local catalog
holding text that does not parse produced **1** structure finding(s) (`invalid-translation`): the layer works where its
input can exist. A server project cannot hold that input, and the test asserts why rather than saying
so — every write path parses before it stores:

| Write | Answer |
|---|---|
| `PUT` a Spanish translation of `checkout.pay.label` with `.input {$name` | 400 `invalid_message`, and the stored text is unchanged |
| `PUT` the German source of `checkout.pay.label` with `.input {$name` | 400 `invalid_message`, and the stored text is unchanged |

**The visual layer.** `glossa capture --check` drove the headless Chrome this test started over `/kasse` in German and Japanese
at 1280×800, twice (the two-sighting rule of §5.2). The Japanese pay button clipped. The region the
finding names was read back through the API and the stored screenshot cropped to it:

| Message | Locale | Capture | Region | Box (CSS px) | Screenshot | Crop | Distinct colours |
|---|---|---|---|---|---|---|---:|
| `checkout.crust.label` | `ja` | `01a0f87` | `r_17` | 119×18 at (105, 675) | 1280×1303 (136301 bytes) | 119×18 px | 115 |

- `glossa context push` uploaded 152 usages, one of them `checkout.pickup.reminder` at `src/pages/CheckoutPage.vue:31` — a key no catalog has.
- The head commit revises 4 German sources; the French `checkout.payment.activity` keeps its text and loses `{$amount}`, and the Japanese `checkout.roll.help` keeps the link the source dropped.
- `de-AT`'s effective style guide asks for the formal form of address (`Sie`).
- The termbase holds `Datenschutz` → forbidden French `vie privée` (legal) and `Warenkorb` → preferred Spanish `carrito`.
- `glossa check --json` exited 1 with conclusion `failure`: 4 errors, 558 warnings, 0 waived, graded against policy v3 from the server.
- All 562 findings validate against `glossa.finding/v1` (schema, fingerprint, layer, code, severity, locus).
- The visual layer is real: `glossa capture --check` drove Chrome over `/kasse` in German and Japanese at 1280×800, and the Japanese pay button clipped — 1 `text-clipped` finding(s) on the Japanese capture (0 on the German one), and the server stored 1 probe findings with the upload.
- The region the finding names (`r_17` on capture `01a0f87`) was read back through the API and the stored screenshot cropped to it: 119×18 CSS px at (105, 675) → 119×18 pixels of a 1280×1303 screenshot, 115 distinct colours.
- No single command computes all nine layers: `glossa check --terminology` has the terminology layer and no browser, `glossa capture --check` has the visual layer and takes no `--terminology` flag. The table below counts both runs of the workflow together.
- `help.legacy.title` was obsoleted while its translations stayed: `glossa check` reported `unknown-key` in `en`, `es`, `fr`, `ja`, each a warning against the obsolete message's ID (`01a0f87`), and the pull request annotated it at `src/pages/HelpPage.vue:23` — the line the help page still asks for the key on, from the build's usages.

## §12.3 — the PR check agrees (the exit criterion)

Pull request #11 on `acme/shop`, head `6b28d05`, opened by a signed `pull_request.opened` webhook the fake GitHub's
fixtures sign. Nothing creates a check run through `/v1` — `openCheck` is reached only from webhook
processing — so the test does what a product does and assumes no API shortcut.

The check run: `Glossa`, completed/`failure`, "3 problems and 633 warnings", 200 annotations over 5 PATCHes, 1 sticky comment.

The terminal's side is `glossa capture --check` on this commit — the workflow's run that carries every layer the server can have.

| | the terminal | the pull request's check run | |
|---|---|---|---|
| conclusion | failure | failure | ✅ |
| errors | 3 | 3 | ✅ |
| warnings | 633 | 633 | ✅ |
| waived | 0 | 0 | ✅ |
| layer `completeness` (e/w/x) | 0/321/0 | 0/321/0 | ✅ |
| layer `length` (e/w/x) | 1/243/0 | 1/243/0 | ✅ |
| layer `locale` (e/w/x) | 0/27/0 | 0/27/0 | ✅ |
| layer `parity` (e/w/x) | 2/0/0 | 2/0/0 | ✅ |
| layer `source` (e/w/x) | 0/40/0 | 0/40/0 | ✅ |
| layer `style` (e/w/x) | 0/1/0 | 0/1/0 | ✅ |
| layer `visual` (e/w/x) | 0/1/0 | 0/1/0 | ✅ |

Two surfaces, one verdict.

## §12.4 — waivers and the policy rollout

### The waiver

| What | What happened |
|---|---|
| a waiver with a blank reason | 400 `waiver_reason_required` — the reason is required and non-empty |
| waive `term_missing` (`f_afd30`) with a reason | waiver `01a0f87`, scope `project`, against source revision 1 |
| re-run `glossa check` | the finding is `waived`; 558→557 warnings, 0→1 waived, conclusion `failure`→`failure` |
| change the German source behind it | the finding is `warning` again: the waiver was made against source revision 1 and the finding is now at 2 |
| waive the `number-convention` on `checkout.total.label` (`f_b696a`) | `waived`, against source revision 1 |
| change the German source behind **that** one | `warning` again: the waiver was made against source revision 1 and the finding is now at 2 — waived against a German that no longer ships |

### The rollout

| What | What happened |
|---|---|
| `POST …/check-policy` with `dry_run: true` | 1263 stored findings examined over 2 runs: 2 raised, 0 lowered, 0 silenced; 2 findings newly failing, on `feature/opening-hours`; **1 open pull requests** would newly fail: #10 `feature/opening-hours` <http://127.0.0.1:57167/acme/shop/pull/10> |
| save v4 with a 14-day grace | v3 keeps grading the pull requests opened before it, until 2026-10-15T17:12:11.950834Z |
| the open pull request (`feature/checkout-copy`, opened under v3) | still graded against v3, and its summary says so: “Graded against the project's check policy v3 — the version this pull request was opened under.” |
| the pull request the preview named (#10, `feature/opening-hours`, opened under v3) | a new commit after the save: still graded against v3, and still `success` — the grace keeps it green until it closes or the grace runs out |
| a pull request opened after the save (`feature/pickup-copy`) | graded against v4 and `failure`: the visual layer carries 1 errors |

Pull request #10 (`feature/opening-hours`) was opened and checked while `main` still held the default branch's catalogs: its newest recorded run (`e58b21c`) concluded `success` under v3 with 0 errors and 629 warnings, one of them the Japanese pay button's `text-clipped` — a warning under `visual: warn`, an error under `enforce`.

## §12.5 — the release gate

| What | What happened |
|---|---|
| publish to `production` with `fr` incomplete | 409 `policy_not_met` — release: the environment's check policy is not met: production: fr must be complete and is 3 of 156 messages short |
| force it with no reason | 400 `force_reason_required` |
| force it with a reason | 201 — release `01a0f87`, version 1 |
| the record | deployment #1 by `person:01a0f873-a740-7b42-801c-6c1cfaaf5d76`, `forced: true`, with the reason it was forced for |

## §12.6 — MCP

An `mcp-go` client (`github.com/modelcontextprotocol/go-sdk`) over streamable HTTP at `/mcp`, on the same
server, with `GLOSSA_MCP_ENABLED=true` — the endpoint is off by default and this deployment turns it on.

| Credential | Session | Call | Outcome |
|---|---|---|---|
| a CI token (`glossa_ci_…`) | read (asked for) | `connect` | **refused at connect** — HTTP 401:  invalid token: mcp: credential not accepted: a CI token is minted for one workflow run; present a tenant API token (glossa_api_…) instead |
| a live in-context grant (`glossa_ctx_…`) | read (asked for) | `connect` | **refused at connect** — HTTP 401:  invalid token: mcp: credential not accepted: an in-context grant is minted for one project and one browser origin; present a tenant API token (glossa_api_…) instead |
| read-only API token | read | `tools/list` | 11 tools, none of them a write tool |
| read-only API token | read | `catalog_search` | the project's `checkout.` messages |
| read-only API token | read | `message_get` | the message with its usages and capture count |
| read-only API token | read | `check_run` | findings and the policy verdict, computed and stored nowhere |
| read-only API token | read | `translation_propose` | **refused** — calling "tools/call": unknown tool "translation_propose" |
| read-only API token | write (asked for) | `connect` | **refused** — a write session needs the `write` scope |
| write API token | write | `translation_propose` | written, state `needs_review`, origin `agent` — in review, not an approved revision |
| another tenant's API token | read | `catalog_search (the first tenant's project)` | **refused** — mcp: not found |

The CI token above is a well-formed `glossa_ci_…` secret rather than one minted through the GitHub OIDC
exchange: the refusal is by credential kind in `mcp/adapters/identity.Authenticate`, before any lookup, so
that is the path being exercised. The in-context grant **is** a live one, minted through
`POST …/in-context-grants` for the fixture deployment's origin and refused all the same.

## §12.7 — the Flutter runtime

`Dart SDK version: 3.13.4 (stable) (Tue Sep 15 01:01:15 2026 -0700) on "macos_arm64"` · Flutter 3.47.5 • channel stable • https://github.com/flutter/flutter.git.

| Suite | Fixtures | Passed | Failed | Skipped |
|---|---|---:|---:|---:|
| `catalog_test.dart` | `explain()` has no side effects; the SPEC's wire spellings | 9 | 0 | 0 |
| `ed25519_test.dart` | RFC 8032 signature vectors and their rejections | 9 | 0 | 0 |
| `jcs_test.dart` | RFC 8785 canonicalization vectors | 52 | 0 | 0 |
| `loader_test.dart` | the §3 loading cases a fixture cannot ship | 12 | 0 | 0 |
| `loading_test.dart` | runtimes/testdata/loading — including `signatures.json`: an unsigned manifest with keys configured | 9 | 0 | 0 |
| `locale_test.dart` | the BCP 47 suites the Go and JS runtimes share | 37 | 0 | 0 |
| `markup_test.dart` | runtimes/testdata/markup.json | 22 | 0 | 0 |
| `purity_test.dart` | §6.4's AOT and web rules: no `dart:mirrors`, no `dart:ffi`, no `dart:io` in the core | 4 | 0 | 0 |
| `runtime_format_test.dart` | messageformat/testdata/glossa/runtime-format.json | 131 | 0 | 41 |
| `scenarios_test.dart` | runtimes/testdata/scenarios — and `explain()` field for field (SPEC §6) | 24 | 0 | 0 |
| **Total** | | **309** | **0** | **41** |

`explain()` is asserted field for field against SPEC §6 inside `scenarios_test.dart` — locale, chain,
`resolvedFrom`, release id and version, source and every step — and the unsigned manifest is
`runtimes/testdata/loading/signatures.json`'s "release 2 unsigned" step, which runs with public keys
configured and expects a `signature` error and the previous release still serving.

### The §6.4 budgets

**Size** — `flutter build --analyze-size` against a fixture app with and without the package, which is
the method §6.4 names; gated on the delta over a realistic baseline at ≤ 400 kB (§15 question 6).

```
Glossa Flutter runtime — RFC 0005 §6.4 size budget
target: macos
  plain: 3046.6 kB of Dart code
  host: 3752.4 kB of Dart code
  glossa: 4095.4 kB of Dart code

— the fixture is honest ————————————————————————————————————
  bytes of ours in plain, which does not use us: 0
  bytes of ours in host, which does not use us: 0
  bytes of ours in glossa, which does: 133656

— enforced: §6.4's own method, the delta ————————————————————
  adopting Glossa, over a realistic baseline   343.1 kB
    budget 400.0 kB (§6.4, set in wave 4)
    The baseline already has package:intl formatting numbers and
    dates and an HttpClient making a call — what most apps that
    would adopt Glossa already carry.

— reported, because a hidden megabyte is a lie ————————————————
  over a bare Flutter app (no intl, no networking)  1048.8 kB
  the same, excluding package:intl             879.8 kB
  package:intl (its own CLDR data)             169.0 kB
  package:glossa + package:glossa_flutter, attributed   133.7 kB
    The first of these is what an app with neither would pay, and
    it is the number intent §33 is about. The gap between it and
    the gated one is the dart:io HTTP client a transport needs,
    package:intl, the dart:core BigInt arithmetic behind the
    pure-Dart Ed25519 verifier, and package:crypto — carried by
    the baseline above because an app of this kind already has
    them. Per architecture: one slice, whatever the bundle packs.

  where the growth over a bare app went, by library:
      239.6 kB  @shared
      169.0 kB  package:intl
      124.6 kB  package:glossa
      120.7 kB  dart:io
      108.8 kB  dart:_http
       82.2 kB  dart:core
       74.0 kB  @unknown
       41.5 kB  dart:async
       17.9 kB  dart:typed_data
       15.6 kB  package:crypto
       12.2 kB  package:flutter
       10.4 kB  dart:convert
        9.8 kB  dart:_internal
        9.1 kB  package:glossa_flutter/package:glossa_flutter/src
        5.0 kB  @stubs
        2.3 kB  dart:collection
        1.8 kB  dart:isolate
        1.7 kB  package:glossa_size_fixture/package:glossa_size_fixture/glossa.dart
        1.4 kB  dart:_compact_hash

OK — 343.1 kB of 400.0 kB, the delta over a baseline that already has package:intl and an HTTP client. That verdict does not stand alone:
  What it assumes: an app that already makes HTTP calls and already formats numbers and dates. An app with neither pays 1048.8 kB — 879.8 kB of it outside package:intl.
  What the budget is: 150 kB was §6.4's original figure and nothing meets it by this method. Wave 4 replaced it with 400.0 kB deliberately, after measuring. See RFC 0005 §6.4 and §15, question 6, which records the decision and its reasoning.
```

Measured in 54 s on this machine.

**Startup** — measured and recorded, not gated on wall clock: §6.4's numbers are written for a mid-range
Android device and neither a laptop nor a CI runner is one. What the tool does enforce are the two
properties that hold on any machine.

```
Glossa Dart runtime — RFC 0005 §6.4 startup budgets
host: macos 3.13.4
mode: AOT (release)

manifest under test: 202594 bytes (control: 19621 bytes)
the shared fixture verifies against its own key: yes

warm cache: 500 messages, 87360 artifact bytes on disk
activation from the persisted store: 2.432 ms

— §6.4 device budgets, recorded ————————————————————————————
  verify 200 kB manifest + signature         9.29 ms   (within the 30 ms budget)
  first t() after a warm cache               0.05 ms   (within the 5 ms budget)
  longest event-loop stall on activation     2.44 ms   (within the 16.7 ms budget)
  (steady state after warm-up: verification 9.08 ms, canonicalization 4.30 ms of it.)
  These are wall clock on this machine. §6.4 names a mid-range Android device; neither a laptop nor a CI runner is one, so none of the three fails this program. The device numbers belong to the M4 exit report (§12).

— enforced, because they hold on every machine ——————————————
  canonicalization cost per byte, 202594 B over 19621 B  ×0.94 (linear is ×1.00, ceiling ×3.00)
  first t() ÷ one verification  0.5 % (ceiling 25 %)

OK — both enforced properties hold.
```

Measured in 2 s on this machine.

## §12.8 — the dashboard

| Number (RFC 0005 §8) | What the API reported |
|---|---|
| Coverage: translated / outdated / missing | {messages:936,missing:313,outdated:4,translated:623} |
| Outstanding findings by layer and severity, plus waived | {errors:4,waived:0,warnings:635} |
| AI acceptance rate and mean edit distance | {acceptance_rate:0,accepted:0,decisions:0,edited:0,mean_edit_distance:0,rejected:0} |
| Review queue depth and age | {depth:0} |
| Context coverage: usages and visible regions | {active_messages:156,with_region:0,with_usage:150} |
| Lead time, p50/p90 | {p50_seconds:11.178263,p90_seconds:11.195435,samples:305} |
| Check health: pass rate and median time to a conclusion | {failed:2,latency:{p50_seconds:5.406886,p90_seconds:8.2987772,samples:3},median_seconds:5.406886,neutral:0,pass_rate:0.3… |

`studio/e2e/m4/quality-exit.spec.ts` signed in against **this** server, opened `/t/…/p/…/quality`, found seven stats in the
health header, and asserted each rendered value against the summary above — computing the expected
rendering from the API's JSON with plain `Intl` rather than by calling Studio's own formatters, so the
comparison cannot agree with itself. A number the API did not measure has to read "Not measured" and
carry `data-measured="false"`; a zero there would be a claim the API never made. (3 s)

## No provider, no network

RFC 0005 §14 decision 2: a check never calls an AI provider, and none of §12's nine layers is the
model-backed one. The tenant's only configured provider is a loopback endpoint that refuses every
request and counts it. **It was asked 0 times.**

The GitHub is the fake of RFC 0004 §12, on loopback; Postgres and MinIO are testcontainers; the browser
is a headless Chrome the test starts and attaches to over CDP. Nothing in this run reaches the network.
