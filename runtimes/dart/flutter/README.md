# `glossa_flutter` — the Flutter layer

`GlossaText` and everything else Flutter needs, on top of the pure-Dart
runtime in [`..`](../README.md). Designed in
[RFC 0005 §6](../../../docs/rfcs/0005-quality.md).

It is a **separate package on purpose**. `package:glossa` must not import
Flutter — that is what lets it be tested with `dart test`, compiled for the
web with `dart compile js`, and used from a server or a CLI. Declaring
Flutter as a dependency there would break `dart pub get` for everyone
without the Flutter SDK, including the `runtimes-dart` CI job. So the core
stays Flutter-free and this package is the only place `package:flutter`
appears. `flutter/` is excluded from the core's `analysis_options.yaml`
for the same reason: `dart analyze` in `runtimes/dart` must not try to
resolve Flutter.

```yaml
dependencies:
  glossa_flutter:
    path: ../glossa/runtimes/dart/flutter   # until it is published (§15.2)
```

`package:glossa_flutter/glossa_flutter.dart` re-exports the whole core, so
an application imports one library.

## What it adds

| | |
|---|---|
| `GlossaScope` | binds a `GlossaClient` to a widget subtree, subscribes to the SPEC §6 error channel, rebuilds when a release activates or the locale changes, and revalidates the manifest on app resume (SPEC §3) |
| `GlossaText` | a `Text` that renders a message by id and turns its MessageFormat 2 markup into styles instead of escaping it |
| `glossaSpan`, `GlossaTagStyles`, `GlossaTagBuilder` | formatted parts as `InlineSpan`s, under the shared safe-tag list |
| `loadBundledRelease` | the SPEC §3 bundle, read out of the app's assets |
| `Glossa` | `t`, `parts`, `span`, `explain`, `errors`, `locale`, `textDirection`, `availableLocales`, `setLocales`, `refresh` |

```dart
final glossa = GlossaClient(
  edge: 'https://edge.example.com',
  deliveryKey: 'pk_live_…',          // publishable by design (SPEC §2)
  locales: ['de-AT'],
  publicKeys: [GlossaPublicKey.parse('k_2026a', '…')],
  transport: ioTransport(),           // package:glossa/io.dart
  store: FileReleaseStore.scoped(
    await getApplicationSupportDirectory(),   // package:path_provider
    deliveryKey: 'pk_live_…',
    environment: 'production',
  ),
  bundled: await loadBundledRelease(),
);

runApp(
  GlossaScope(
    client: glossa,
    onError: (e) => logger.warning('$e'),
    child: const App(),
  ),
);

// anywhere below it
GlossaText('cart.items', values: {'n': cart.length});
GlossaScope.of(context).explain('cart.items').chain;   // [de-AT, de, en]
```

`path_provider` is deliberately **not** a dependency of this package: the
application decides where its cache lives, and a plugin dependency we
don't need costs size no one asked for (RFC 0005 §6.4).

**Configure signing keys.** SPEC §1.3 lets a runtime without public keys
skip signature verification; mobile OTA should not. Pass `publicKeys`.

## Markup, and why a translation can't add a link

MF2 markup reaches the widget as parts, and `partsToTree` in the core
turns them into the tree every runtime shares
([`runtimes/testdata/markup.json`](../../testdata/markup.json), SPEC §5).
Only names on `safeTags` become elements, and **markup options are
dropped** — a translation that writes `{#link href=|javascript:…|}` gets
its text rendered and nothing else.

`GlossaTagStyles.standard()` gives the unambiguous tags a style: `b` and
`strong` bold, `i`, `em`, `var`, `cite` and `dfn` italic, `u` and `ins`
underlined, `s` and `del` struck through, `code`, `kbd` and `samp`
monospace. `{#br/}` is a line break and `{#wbr/}` a zero-width space. The
tags that need a size or a colour (`small`, `sub`, `sup`, `mark`) carry no
default, because only the application's theme knows: give them one with
`GlossaTagStyles.standard().merge({...})`, on the scope or on one
`GlossaText`.

A tappable link is markup the **application** renders:

```dart
GlossaText(
  'legal.accept',
  builders: {
    'link': (context, children) => TextSpan(
      children: children,
      style: const TextStyle(decoration: TextDecoration.underline),
      recognizer: TapGestureRecognizer()..onTap = () => open(termsUrl),
    ),
  },
)
```

The builder gets the children and the context, and nothing from the
translation: the URL is the call site's, always.

## Shipping a release in the app

The layout is the one SPEC §3 fixes for every runtime and for
`glossa pull --release` — the edge's URL space below `/v1/{deliveryKey}/`:

```text
assets/glossa/manifest.json      the manifest, exactly as the edge serves it
assets/glossa/a/<sha256>.json    one artifact, exact bytes
```

```yaml
flutter:
  assets:
    - assets/glossa/
    - assets/glossa/a/      # Flutter's asset directories are not recursive
```

`loadBundledRelease()` reads `manifest.json` and then exactly the
artifacts that manifest names — no asset-manifest scan. An artifact the
bundle doesn't carry is skipped, so shipping a subset of the locales is a
supported choice and the loader falls through to the persisted cache or
the network for the rest. Bundled bytes are trusted like application code
(SPEC §3): they are neither re-hashed nor signature-checked.

The same directory is a valid persisted cache (`FileReleaseStore`), so a
release the app cached can be committed as its bundle unchanged.

## Typed accessors

`glossa generate` writes Dart accessors when `glossa.yaml` has
`generate.dart`:

```yaml
generate:
  dart: lib/glossa/messages.dart
  # dart_runtime: package:glossa_flutter/glossa_flutter.dart   # optional
```

```dart
final messages = Messages(GlossaScope.of(context).t);
messages.checkout.pay(amount: order.total);
GlossaText(MessageIds.checkoutPay, values: {'amount': order.total});
```

The generated file carries `// dart format off`: it is laid out by the Go
generator, which cannot run `dart_style`, so `dart format` must leave it
alone — otherwise every run of it would make `glossa generate --check`
fail.

## Running the tests

The `runtimes-flutter` CI job runs exactly these, on a pinned Flutter
release:

```sh
cd runtimes/dart/flutter
flutter pub get
dart format --output=none --set-exit-if-changed .
flutter analyze --fatal-infos
flutter test
```

The core's own suites stay on the plain Dart SDK, from `runtimes/dart`:
`dart analyze --fatal-infos` and `dart test`. Neither suite touches the
network.

A widget test's body runs against a fake clock, so the loader's first pass
has to happen inside `tester.runAsync` — `test/glossa_text_test.dart`'s
`offlineClient` does that, and every test here goes through it.

## The size budget (RFC 0005 §6.4)

```sh
cd runtimes/dart/flutter
dart run tool/size_budget.dart            # host desktop target
dart run tool/size_budget.dart --platform linux --report size.txt
```

It scaffolds a throwaway Flutter app in a temporary directory and builds
it three times from one pubspec:

- `plain.dart` — bare Flutter, one hard-coded string;
- `host.dart` — the same app plus what a Glossa client leans on that an
  app of this kind already has: `package:intl` formatting numbers,
  currency, percentages, dates and plurals, an `HttpClient` call, a file
  read and write;
- `glossa.dart` — a realistic client: edge, delivery key, persisted
  store, HTTP transport, signing keys, bundled release, `GlossaScope`,
  `GlossaText`, `explain()` and the error channel.

§6.4 names the delta as its method, and the delta is what is gated:
**`glossa` minus `host`**, what adopting Glossa costs an app that was
already making network calls and already formatting numbers and dates.

The realism of the third entry point is load-bearing. Dart's AOT tree
shaker is a global fixpoint: a client with no transport, no bundle and an
empty in-memory store can never activate a release, so the compiler
proves the catalog, the formatter and `package:intl` unreachable and
drops all of them. A fixture like that measures **17 kB** and means
nothing. The tool therefore refuses *either* baseline that carries a byte
of ours, and refuses a Glossa build that carries too few.

Measured on macOS arm64, Flutter 3.47.5, per architecture:

| | |
|---|---|
| **adopting Glossa, over a realistic baseline** | **343.1 kB** of 400 kB — the gated number |
| over a bare Flutter app (no `intl`, no networking) | 1048.8 kB |
| the same, excluding `package:intl` | 879.8 kB |
| `package:intl` and its CLDR data | 169.0 kB |
| `package:glossa` + `package:glossa_flutter`, attributed | 133.7 kB |

**Read the second row beside the first.** The gated number assumes a
baseline. An app with no networking and no `intl` pays 1048.8 kB, and
that is the figure intent §33 is really about. The gap is mostly not our
code: the `dart:io` HTTP client a transport needs (229 kB of `dart:io`
and `dart:_http`), `package:intl` (169 kB), the `dart:core` BigInt
arithmetic the pure-Dart Ed25519 verifier needs (82 kB), `package:crypto`
(16 kB). The baseline carries those because an app of that kind already
does. The tool prints all five figures, the full per-library
decomposition, and the assumption its verdict rests on, on every run.

**150 kB was §6.4's original number and nothing meets it by this method**
— not 343 kB, not 880 kB. Wave 4 measured first and replaced it with
400 kB deliberately; the rejected alternative was to keep 150 kB and gate
the attributed 133.7 kB instead, which would have left ~880 kB outside
the gate in a section written to stop exactly that. RFC 0005 §15,
question 6 records the decision and its reasoning. Bringing the number
down is a separate thread against the M4 exit measurement: the two
candidates are the BigInt Ed25519 verifier and the non-optional `dart:io`
transport.

The startup half of §6.4 lives in the core package —
[`../tool/startup_budget.dart`](../tool/startup_budget.dart) — because
none of it needs Flutter.

## Not here yet

- An IndexedDB `ReleaseStore` for Flutter web; a web build keeps its
  release in memory and reloads it from the edge on every start.
