/// A tiny release the widget tests render from, in the SPEC §1 shapes.
///
/// It is built here rather than read from `runtimes/testdata` on purpose:
/// the shared fixtures are the *contract's*, and the core package drives
/// them (`../test/scenarios_test.dart`, `../test/markup_test.dart`). What
/// is left for this package is the rendering, which needs markup the
/// scenario fixtures don't carry.
library;

import 'dart:convert';

import 'package:glossa_flutter/glossa_flutter.dart';

/// `{#name …}`, `{/name}` or `{#name/}` as data-model JSON.
Map<String, Object?> markup(
  String kind,
  String name, [
  Map<String, String> options = const {},
]) => {
  'type': 'markup',
  'kind': kind,
  'name': name,
  if (options.isNotEmpty)
    'options': {
      for (final o in options.entries)
        o.key: {'type': 'literal', 'value': o.value},
    },
};

/// `{$name}` as data-model JSON.
Map<String, Object?> variable(String name) => {
  'type': 'expression',
  'arg': {'type': 'variable', 'name': name},
};

Map<String, Object?> _message(List<Object?> pattern) => {
  'type': 'message',
  'declarations': <Object?>[],
  'pattern': pattern,
};

/// The German artifact: plain text, safe markup, unsafe markup, a void
/// tag and a placeholder.
final String deArtifact = jsonEncode({
  'schema': 'glossa.artifact/v1',
  'locale': 'de',
  'namespace': 'default',
  'messages': {
    'cart.checkout': _message(['Zur Kasse']),
    'cart.hint': _message([
      'Tippe ',
      markup('open', 'b'),
      'hier ',
      markup('open', 'em'),
      'jetzt',
      markup('close', 'em'),
      markup('close', 'b'),
      '.',
    ]),
    'cart.lines': _message(['eins', markup('standalone', 'br'), 'zwei']),
    'legal.accept': _message([
      'Mit dem Fortfahren akzeptierst du die ',
      markup('open', 'link', {'href': 'javascript:alert(1)'}),
      'AGB',
      markup('close', 'link'),
      '.',
    ]),
    'cart.greeting': _message(['Hallo ', variable('name'), '!']),
  },
});

/// The English artifact: only one of the messages, so the fallback chain
/// has something to do.
final String enArtifact = jsonEncode({
  'schema': 'glossa.artifact/v1',
  'locale': 'en',
  'namespace': 'default',
  'messages': {
    'cart.checkout': _message(['Checkout']),
  },
});

/// The Arabic artifact, for the right-to-left direction.
final String arArtifact = jsonEncode({
  'schema': 'glossa.artifact/v1',
  'locale': 'ar',
  'namespace': 'default',
  'messages': {
    'cart.checkout': _message(['\u0625\u062a\u0645\u0627\u0645']),
  },
});

/// The Austrian artifact: one regional message, so `de-AT` is a real
/// first step of the chain and everything else falls back to `de`.
final String atArtifact = jsonEncode({
  'schema': 'glossa.artifact/v1',
  'locale': 'de-AT',
  'namespace': 'default',
  'messages': {
    'cart.regional': _message(['Servus']),
  },
});

/// The manifest naming both artifacts by the SHA-256 of their bytes.
final String manifestBytes = jsonEncode({
  'schema': 'glossa.manifest/v1',
  'project': 'prj_test',
  'environment': 'production',
  'release': {'id': 'rel_1', 'version': 1, 'createdAt': '2026-09-19T08:00:00Z'},
  'sourceLocale': 'de',
  'locales': [
    {'code': 'de', 'direction': 'ltr'},
    {'code': 'en', 'direction': 'ltr'},
    {'code': 'ar', 'direction': 'rtl'},
    {'code': 'de-AT', 'direction': 'ltr'},
  ],
  'fallback': {
    'de-AT': ['de'],
    '*': ['en'],
  },
  'artifacts': {
    'de': {
      'default': {'sha256': deSha, 'size': deArtifact.length},
    },
    'en': {
      'default': {'sha256': enSha, 'size': enArtifact.length},
    },
    'ar': {
      'default': {'sha256': arSha, 'size': arArtifact.length},
    },
    'de-AT': {
      'default': {'sha256': atSha, 'size': atArtifact.length},
    },
  },
});

/// The German artifact's SHA-256, as the manifest names it.
final String deSha = sha256Hex(deArtifact);

/// The English artifact's SHA-256.
final String enSha = sha256Hex(enArtifact);

/// The Arabic artifact's SHA-256.
final String arSha = sha256Hex(arArtifact);

/// The Austrian artifact's SHA-256.
final String atSha = sha256Hex(atArtifact);

/// The release as a bundle: what [loadBundledRelease] returns, and what a
/// `GlossaClient` takes as `bundled`.
BundledRelease bundledRelease() => BundledRelease(
  manifest: manifestBytes,
  artifacts: {
    deSha: deArtifact,
    enSha: enArtifact,
    arSha: arArtifact,
    atSha: atArtifact,
  },
);

/// The same release laid out as Flutter assets, the SPEC §3 way.
Map<String, String> bundleAssets({String path = defaultBundlePath}) => {
  '$path/manifest.json': manifestBytes,
  '$path/a/$deSha.json': deArtifact,
  '$path/a/$enSha.json': enArtifact,
  '$path/a/$arSha.json': arArtifact,
  '$path/a/$atSha.json': atArtifact,
};
