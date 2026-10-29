# `glossa` — the Dart runtime

The Dart implementation of [`runtimes/SPEC.md`](../SPEC.md), the contract every
Glossa runtime follows. It is **pure Dart**: no Flutter import, no FFI and no
reflection, and no `dart:io` in `package:glossa/glossa.dart`, so the core
compiles for the web and for AOT and can be tested with `dart test`. The pieces
that need a file system or a socket live in the separate entry point
`package:glossa/io.dart`. Designed in
[RFC 0005 §6](../../docs/rfcs/0005-quality.md).

The Flutter widgets are a separate package, [`flutter/`](./flutter)
(`glossa_flutter`), so that this one never depends on the Flutter SDK.

## What works today (waves 1 to 3)

| SPEC | Here |
|---|---|
| §1.1–1.2 manifest and artifact | `Manifest.decode`, `Artifact.decode`. Unknown fields ignored, a different `schema` major rejected, one unreadable message reported and skipped rather than failing the release |
| §1.3 integrity and signatures | `sha256Hex` over an artifact's exact bytes; `verifyManifestSignature` — **pure-Dart Ed25519** (`src/ed25519.dart`) over the **RFC 8785 (JCS)** form (`src/jcs.dart`) of the manifest without `signatures`, checked against the manifest's exact bytes. No keys configured means verification is off, which SPEC §1.3 permits and mobile OTA should not do |
| §1.4 staged rollout | `GlossaClient` reads a manifest's `rollout` after verifying it: the installation's cohort (`cohortOf`, checked against all 10,000 ids of [`cohorts.json`](../testdata/rollout/cohorts.json)), the candidate view when the cohort is inside `percent × 100`, and the stable view of the same manifest when the candidate can't activate. The installation id is random, 32 hex digits, kept by a store that is also an `InstallationIdStore` (`FileReleaseStore`, `MemoryReleaseStore`), in memory otherwise. `installationId:` replaces it; `rollout: false` ignores rollouts entirely. `explain().rollout` reports `{id, percent, cohort, side}` |
| §2 endpoints | `Transport`, a one-function seam. `ioTransport()` in `package:glossa/io.dart` is the `dart:io` one; it sends `If-None-Match` and never a credential |
| §3 loading | `GlossaClient`: memory → persisted → network → bundled → inline, with artifact *bytes* taken from memory → persisted → bundled → network. Atomic activation, `ReleaseStore` for the last-good release, and hashing plus decode in an isolate |
| §4.1 negotiation | `canonicalizeLocale` (RFC 5646 §4.5), `lookupLocale` (RFC 4647 §3.4), `resolveLocales`, `acceptLanguage` |
| §4.2 fallback chain | `fallbackChain`: explicit edges depth-first, truncations, `"*"`, the source locale, cycles stopped at the first repeat |
| §4.3 message resolution | `Localizer.t` formats with the locale the message was **found** in |
| §5 formatting | `formatMessage` / `formatToParts`: a MessageFormat 2 interpreter over the precompiled data model. No parser. Formatting never throws |
| §5 markup | `partsToTree` and `partsToHtml`: the shared safe-tag contract ([`markup.json`](../testdata/markup.json)). Only `safeTags` become elements, markup options are always dropped, so a translation can't add a link |
| §6 observability | `explain()` returns the SPEC document verbatim; `GlossaClient.errors` is the error channel with the six types and 60-second repeat suppression, and it spans every release of a runtime's life. **No telemetry** |

```dart
import 'package:glossa/glossa.dart';
import 'package:glossa/io.dart';

final glossa = GlossaClient(
  edge: 'https://edge.example.com',
  deliveryKey: 'pk_live_…',          // publishable by design (SPEC §2)
  locales: ['de-AT', 'en'],
  publicKeys: [GlossaPublicKey.parse('k_2026a', '…')],
  transport: ioTransport(),
  store: FileReleaseStore.scoped(
    supportDirectory,                 // path_provider, in a Flutter app
    deliveryKey: 'pk_live_…',
    environment: 'production',
  ),
  bundled: await readBundledRelease(Directory('assets/glossa')),
);
glossa.errors.listen((e) => log.warning('$e'));
await glossa.ready;                   // never throws; never rejects

glossa.t('cart.checkout');                    // "Zur Kassa"
glossa.t('cart.items', values: {'n': 3});     // "3 Artikel"
glossa.direction;                             // Direction.ltr
glossa.explain('cart.checkout').chain;        // [de-AT, de, en]
glossa.explain('cart.checkout').source;       // Source.persisted
```

`Catalog.fromRelease` is still there for a host that already holds a release
and wants no loader at all.

### How the loader is safe

- **Atomic activation.** A release becomes visible only after its manifest has
  verified *and* every artifact its active fallback chain needs has loaded and
  verified. The switch is two field assignments with no `await` between them,
  so a reader sees the whole old release or the whole new one. Any failure
  leaves the previous release serving and goes to the error channel.
- **The manifest is written last.** `FileReleaseStore` writes every artifact
  first, each through a temporary file and a rename, and commits
  `manifest.json` last — the rule [`runtimes/go/store.go`](../go/store.go) set.
  Artifacts are content-addressed, so one written for a release that never
  activated is inert: nothing names it. A process killed at any point therefore
  leaves the previous manifest in place with all of its artifacts present, and
  a half-downloaded release can never be activated on the next start. The ETag
  lives in a sidecar that records the digest of the manifest it belongs to, so
  a torn write can never pair an ETag with a manifest it didn't come from.
- **Off the UI thread.** Hashing, parsing and building the data model are one
  pure function over sendable data, run through `Isolate.run` on the VM and
  AOT (so Flutter drops no frame) and inline on the web, which has no isolates.

## Flutter

[`flutter/`](./flutter) is the package `glossa_flutter`: `GlossaText`, the
safe tags as `InlineSpan`s, `explain()` and the error channel through a
`GlossaScope`, and the asset-bundle layout. Its README has the details.

It is separate rather than a `package:glossa/flutter.dart` entry point
because a Flutter dependency here would break `dart pub get`, `dart test`
and `dart compile js` for every host without the Flutter SDK — including
this package's CI job. `flutter/` is excluded from
`analysis_options.yaml` so `dart analyze` never tries to resolve
`package:flutter`; run `flutter analyze` from `flutter/` instead.

**The core is Flutter-free, and that is checked**: nothing below `lib/`
imports `package:flutter`, and `tool/web_compile.dart` compiles the
package for the web, which no Flutter import survives.

## Typed accessors

`glossa generate` writes them from the same argument metadata that feeds
TypeScript and Go, when `glossa.yaml` has `generate.dart`:

```yaml
generate:
  dart: lib/glossa/messages.dart
  # dart_runtime: package:glossa/glossa.dart   # the default
```

```dart
final messages = Messages.of(glossa);
messages.checkout.pay(amount: order.total);
MessageIds.checkoutPay;                        // for explain(), GlossaText
```

The generated file carries `// dart format off`, because the generator
(Go, in `platform/internal/cli/codegen/dart.go`) lays it out itself and
cannot run `dart_style`; a reformat would make `glossa generate --check`
fail for ever after.

## What is coming

- **A web store.** `ReleaseStore` is an interface and `MemoryReleaseStore`
  and `FileReleaseStore` implement it; an IndexedDB one for Flutter web is
  still missing, so a web build keeps its release in memory and reloads it
  from the edge on every start.

## Running the fixtures

The tests are a **driver over the shared fixtures**, not a copy of them
(RFC 0005 decision 9). They read `runtimes/testdata` and
`messageformat/testdata` from the repository and fail loudly if either is
missing, so they only run inside a checkout:

```sh
cd runtimes/dart
dart pub get
dart test                       # everything
dart test test/scenarios_test.dart
dart analyze --fatal-infos      # the CI bar

# RFC 0005 §6.4: the core must build for the web, so no dart:io in it
dart compile js -o .dart_tool/web_compile.js tool/web_compile.dart
```

CI runs the same commands on a pinned SDK in the `runtimes-dart` job,
which also compiles for the web and for AOT, runs the startup budgets
below, and runs `TestGeneratedDartAnalyzes` from the `platform` module —
the check that `glossa generate --lang dart` still emits code this
runtime accepts. The Flutter package has its own job, `runtimes-flutter`,
on a pinned Flutter release; see [`flutter/README.md`](./flutter).

| Suite | Source | Status |
|---|---|---|
| `test/scenarios_test.dart` | `runtimes/testdata/scenarios/*.json` | every case, **no skips** |
| `test/loading_test.dart` | `runtimes/testdata/loading/*.json` | every step of every sequence, **no skips** |
| `test/loader_test.dart` | the §3 cases a fixture can't ship: the bundle, the persisted store on disk, a torn write | — |
| `test/jcs_test.dart` | RFC 8785 §3.2.2–3.2.3 and Appendix B, plus number vectors from Node 22 (V8) | — |
| `test/ed25519_test.dart` | RFC 8032 §7.1, plus the rejections | — |
| `test/runtime_format_test.dart` | `messageformat/testdata/glossa/runtime-format.json` | 89 of 130; 41 skipped, each with its reason |
| `test/markup_test.dart` | `runtimes/testdata/markup.json` | every case, **no skips** |
| `test/locale_test.dart` | mirrors `runtimes/go/locale_test.go` and the JS locale suite | — |

A bug found here becomes a fixture first (SPEC §7).

## Budgets (RFC 0005 §6.4)

```sh
cd runtimes/dart
dart compile exe tool/startup_budget.dart -o .dart_tool/startup_budget
.dart_tool/startup_budget
```

AOT, not `dart run`, because AOT is what ships. The tool verifies a
200 kB manifest and its signature, starts a client from a warm persisted
cache of 500 messages, renders the first message, and watches the event
loop for a stall longer than a frame.

**What it enforces, and what it only records.** §6.4's three numbers —
30 ms to verify, 5 ms to the first `t()`, no jank frame — are *device*
budgets: it names a mid-range Android phone. A CI runner is not one, and
neither is a developer's laptop, so the tool prints those three against
their budgets and does not fail on them. The device numbers belong to the
M4 exit report (§12.7), which runs on a device.

What it does fail on are two properties that hold on every machine, and
that a regression breaks on all of them at once:

- canonicalization stays **linear** in the size of the manifest;
- the first `t()` after a warm cache costs a small fraction of one
  signature verification — i.e. the runtime verifies a release when it
  activates it, and not again per message.

The size half of §6.4 belongs to the Flutter package: see
[`flutter/README.md`](./flutter). The web and AOT half is
`tool/web_compile.dart`, `dart compile exe` and
[`test/purity_test.dart`](./test/purity_test.dart), which fails on an
import of `package:flutter`, `dart:io` outside `lib/io.dart`,
`dart:mirrors` or `dart:ffi`.

## What the host's CLDR decides

Numbers, dates and plural categories come from
[`package:intl`](https://pub.dev/packages/intl), which carries its **own** CLDR
snapshot — currently CLDR 48, the version `runtime-format.json` was generated on
(`generatedWith.cldr`). Glossa ships no locale data of its own, so the host's
`intl` version decides the output, and an app that pins a different `intl` can
see different strings.

`messageformat/testdata/glossa/README.md` sets the rule, and this package
follows it rather than inventing one:

> An implementation that can't reproduce a case keeps a skip list with the
> reason and the upstream gap, never a workaround.

The fixture's cases are chosen so their output is identical in CLDR 47 and 48,
so a mismatch is never version drift — it is a gap, and it gets fixed upstream.
`_skips` in `test/runtime_format_test.dart` is the list, kept honest in both
directions: an entry whose case has disappeared fails the suite, and so does one
whose case would now pass. The gaps today, all in `package:intl`:

| Gap | Cases | Effect |
|---|---|---|
| No ordinal plural rules | 15 | `:number select=ordinal` can't select; `bad-selector` |
| No measurement-unit data | 7 | `:unit` is **not registered**, so it is `unknown-function` and renders its MF2 fallback — visible, rather than silently wrong |
| No time-zone database | 9 | a named `timeZone` other than `UTC` reports `unsupported-operation` and formats in the value's own zone |
| One global currency-symbol table | 4 | a currency foreign to the locale keeps its symbol instead of falling back to the ISO code (es/fr `JPY`), and `ja` misses U+FFE5 |
| No currency display names | 1 | `currencyDisplay=name` reports `unsupported-operation` |
| CLDR `minimumGroupingDigits` ignored for `es` | 3 | `1.234` where CLDR says `1234`. The same gap `runtimes/go` records for go-intl |
| Plural operand `i` is `round()`, not the integer part | 1 | `fr` 1.5 gets `i=2`, so *other* instead of *one* |
| Stale German `dateTimeFormat` | 1 | `'{1}, {0}'` at every length, where CLDR 48 has `{1} 'um' {0}` for full and long |

Locale **identity** does not depend on any of this. BCP 47 canonicalization is
RFC 5646 §4.5 against the IANA Language Subtag Registry, whose tables
`tool/gen_subtags.dart` generates into `lib/src/subtags.g.dart` — Dart has
neither ICU's `getCanonicalLocales` nor `golang.org/x/text`, so the data is
generated rather than guessed, and the generated file records the registry's
`File-Date`. Regenerate with the command at the top of the generator.
