/// A release shipped inside a Flutter app's asset bundle
/// (`runtimes/SPEC.md` §3, step 4).
///
/// ## The layout
///
/// It is the one SPEC §3 fixes for every runtime and for the CLI — the
/// edge's URL space below `/v1/{deliveryKey}/`, and byte for byte what
/// `glossa pull --release` writes:
///
/// ```text
/// assets/glossa/manifest.json      the manifest, exactly as the edge serves it
/// assets/glossa/a/<sha256>.json    one artifact, exact bytes
/// ```
///
/// Declared in the app's `pubspec.yaml` as a directory, so adding a
/// locale or publishing a new release never edits it:
///
/// ```yaml
/// flutter:
///   assets:
///     - assets/glossa/
///     - assets/glossa/a/
/// ```
///
/// Flutter's asset declaration is not recursive, so `a/` is listed too.
/// The same directory is a valid persisted cache and a valid bundle, so a
/// release the app cached can be committed as its bundle unchanged
/// (`FileReleaseStore` in `package:glossa/io.dart`).
///
/// ## Finding it at runtime
///
/// [loadBundledRelease] reads `manifest.json`, then loads exactly the
/// artifacts that manifest names — no asset-manifest scan, no guessing.
/// An artifact the bundle doesn't carry is simply skipped: shipping a
/// subset of the locales is a supported choice, and the loader falls
/// through to the persisted cache or the network for the rest.
///
/// Bundled bytes are trusted like application code (SPEC §3): they ship
/// inside the build and never crossed a network, so they are neither
/// re-hashed nor signature-checked.
library;

import 'package:flutter/services.dart';
import 'package:glossa/glossa.dart';

/// Where a release lives in an app's assets unless the app says
/// otherwise.
const String defaultBundlePath = 'assets/glossa';

/// Read the release at [path] out of [bundle] (`rootBundle` by default).
///
/// Returns null when there is no release there, or when its manifest
/// can't be read — never throws, so a mis-declared asset degrades to "no
/// bundle" and the loader carries on with the next source.
///
/// ```dart
/// final glossa = GlossaClient(
///   edge: 'https://edge.example.com',
///   deliveryKey: 'pk_live_…',
///   locales: WidgetsBinding.instance.platformDispatcher.locales
///       .map((l) => l.toLanguageTag())
///       .toList(),
///   bundled: await loadBundledRelease(),
/// );
/// ```
Future<BundledRelease?> loadBundledRelease({
  AssetBundle? bundle,
  String path = defaultBundlePath,
}) async {
  final from = bundle ?? rootBundle;
  final dir = path.endsWith('/') ? path.substring(0, path.length - 1) : path;

  final manifestBytes = await _load(from, '$dir/manifest.json');
  if (manifestBytes == null) return null;
  final Manifest manifest;
  try {
    manifest = Manifest.decode(manifestBytes);
  } on SchemaException {
    // A bundle that can't be read is no bundle. The loader reports the
    // release it does activate; there is nothing to report about one
    // that was never a candidate.
    return null;
  }

  final artifacts = <String, String>{};
  for (final namespaces in manifest.artifacts.values) {
    for (final sha256 in namespaces.values) {
      if (artifacts.containsKey(sha256)) continue;
      final bytes = await _load(from, '$dir/a/$sha256.json');
      if (bytes != null) artifacts[sha256] = bytes;
    }
  }
  return BundledRelease(manifest: manifestBytes, artifacts: artifacts);
}

/// One asset, or null when the bundle doesn't carry it.
///
/// `cache: false` because the runtime keeps the bytes it needs and
/// decodes them off the UI isolate; caching them in the bundle as well
/// would hold a second copy of the catalog for the process's life, which
/// RFC 0005 §6.4's size budget has no room for.
Future<String?> _load(AssetBundle bundle, String key) async {
  try {
    return await bundle.loadString(key, cache: false);
  } on Object {
    // A missing asset is a FlutterError, which is an Error, not an
    // Exception; a custom bundle may throw anything at all.
    return null;
  }
}
