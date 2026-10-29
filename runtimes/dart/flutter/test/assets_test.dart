/// The asset-bundle layout (SPEC §3, step 4): a release shipped inside
/// the app and found at runtime.
library;

import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:glossa_flutter/glossa_flutter.dart';

import 'release.dart';

/// An asset bundle holding exactly the given keys, and failing for every
/// other one the way Flutter's own does.
class _Bundle extends AssetBundle {
  _Bundle(this.assets);

  final Map<String, String> assets;
  final List<String> loaded = [];

  @override
  Future<ByteData> load(String key) async {
    loaded.add(key);
    final asset = assets[key];
    if (asset == null) throw FlutterError('Unable to load asset: "$key".');
    return ByteData.sublistView(Uint8List.fromList(utf8.encode(asset)));
  }
}

void main() {
  test('the bundle is manifest.json plus a/<sha256>.json', () async {
    final bundle = _Bundle(bundleAssets());

    final release = await loadBundledRelease(bundle: bundle);

    expect(release, isNotNull);
    expect(release!.manifest, manifestBytes);
    expect(
      release.artifacts.keys,
      unorderedEquals([deSha, enSha, arSha, atSha]),
    );
    expect(release.artifacts[deSha], deArtifact);
    expect(bundle.loaded, [
      '$defaultBundlePath/manifest.json',
      '$defaultBundlePath/a/$deSha.json',
      '$defaultBundlePath/a/$enSha.json',
      '$defaultBundlePath/a/$arSha.json',
      '$defaultBundlePath/a/$atSha.json',
    ], reason: 'only what the manifest names is read; no directory scan');
  });

  test('the asset path is configurable', () async {
    final bundle = _Bundle(bundleAssets(path: 'assets/i18n'));

    final release = await loadBundledRelease(
      bundle: bundle,
      path: 'assets/i18n/',
    );

    expect(release?.artifacts, hasLength(4));
  });

  test('no release there is null, not a throw', () async {
    expect(await loadBundledRelease(bundle: _Bundle({})), isNull);
  });

  test('a manifest that cannot be read is no bundle', () async {
    final bundle = _Bundle({'$defaultBundlePath/manifest.json': '{"schema":'});

    expect(await loadBundledRelease(bundle: bundle), isNull);
  });

  test('an artifact the bundle lacks is skipped, not fatal', () async {
    final assets = bundleAssets()..remove('$defaultBundlePath/a/$enSha.json');

    final release = await loadBundledRelease(bundle: _Bundle(assets));

    expect(release!.artifacts.keys, [deSha, arSha, atSha]);
  });

  test('the bundle renders, offline, with nothing else configured', () async {
    final release = await loadBundledRelease(bundle: _Bundle(bundleAssets()));
    final client = GlossaClient(locales: ['de-AT'], bundled: release);
    addTearDown(client.dispose);
    await client.ready;

    expect(client.t('cart.checkout'), 'Zur Kasse');
    expect(client.source, Source.bundled);
    expect(client.release?.version, 1);
  });
}
