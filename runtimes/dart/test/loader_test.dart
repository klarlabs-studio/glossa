/// The parts of `runtimes/SPEC.md` §3 the shared loading fixtures don't
/// reach.
///
/// `loading_test.dart` drives the contract's own sequences, which cover
/// memory, the persisted last-good release, the network and the error
/// channel. They have no bundled release and no file system, because a
/// fixture can't ship one. What is left is here, built from the very same
/// fixture data so the manifests and artifacts are the contract's, not
/// this file's invention:
///
/// * the inline default, with nothing configured at all;
/// * the bundled release — activated without a network, trusted like
///   application code (not re-hashed), and the `release.version` race
///   against a persisted release, in both directions;
/// * a manifest published for another environment;
/// * a torn write: artifacts on disk for a release whose manifest never
///   landed, which must leave the previous release serving;
/// * that decode really does leave the calling isolate.
library;

import 'dart:convert';
import 'dart:io';
import 'dart:isolate';

import 'package:glossa/glossa.dart';
import 'package:glossa/io.dart';
import 'package:glossa/src/worker.dart';
import 'package:test/test.dart';

import 'fixtures.dart';

/// One release out of `loading/last-good.json`: the manifest bytes as the
/// edge serves them, and the artifacts it names.
typedef Release = ({String manifest, Map<String, String> artifacts});

Release _release(int step) {
  final fixture = loadFixtures('loading')['last-good.json']!;
  final steps = (fixture['steps']! as List<Object?>)
      .cast<Map<String, Object?>>();
  final edge = steps[step]['edge']! as Map<String, Object?>;
  final manifest = (edge['manifest']! as Map<String, Object?>)['body']!;
  final served = (edge['artifacts']! as Map<String, Object?>)
      .cast<String, String>();
  final named = <String>{
    for (final namespaces
        in ((manifest as Map<String, Object?>)['artifacts']!
                as Map<String, Object?>)
            .values)
      for (final ref in (namespaces as Map<String, Object?>).values)
        (ref! as Map<String, Object?>)['sha256']! as String,
  };
  return (
    manifest: jsonEncode(manifest),
    artifacts: {for (final sha in named) sha: served[sha]!},
  );
}

/// Runs in another isolate; see the last test.
String _whereAmI(int _) => Isolate.current.debugName ?? '';

void main() {
  final v1 = _release(0); // rel_1, version 1, "Hello v1"
  final v2 = _release(3); // rel_2, version 2, "Hello v2"

  test(
    'with nothing configured, the inline default renders (SPEC §3.5)',
    () async {
      final client = GlossaClient(locales: ['en']);
      await client.ready;

      expect(client.t('hello'), 'hello', reason: 'the id, never blank');
      expect(client.t('hello', defaultText: 'Hi'), 'Hi');
      expect(client.release, isNull);
      final explanation = client.explain('hello');
      expect(explanation.toJson(), {
        'id': 'hello',
        'requested': ['en'],
        'locale': null,
        'chain': <String>[],
        'resolvedFrom': null,
        'release': null,
        'source': 'inline',
        'steps': <Object?>[],
        'rollout': null,
      });
      await client.dispose();
    },
  );

  test('a bundled release renders with no network at all', () async {
    final client = GlossaClient(
      bundled: BundledRelease(manifest: v1.manifest, artifacts: v1.artifacts),
      locales: ['en'],
    );
    await client.ready;

    expect(client.t('hello'), 'Hello v1');
    expect(client.release?.id, 'rel_1');
    expect('${client.explain('hello').source}', 'bundled');
    await client.dispose();
  });

  test(
    'bundled bytes are trusted like application code, not re-hashed',
    () async {
      // SPEC §3: "Bundled artifacts and manifests are trusted like
      // application code and aren't re-hashed or signature-checked."
      final tampered = {
        for (final e in v1.artifacts.entries)
          e.key: e.value.replaceAll('Hello v1', 'Hello, bundle'),
      };
      final errors = <GlossaError>[];
      final client = GlossaClient(
        bundled: BundledRelease(manifest: v1.manifest, artifacts: tampered),
        locales: ['en'],
      );
      client.errors.listen(errors.add);
      await client.ready;

      expect(client.t('hello'), 'Hello, bundle');
      expect(errors, isEmpty, reason: 'no integrity error for a bundle');
      await client.dispose();
    },
  );

  group('bundled against persisted, the higher release.version wins', () {
    Future<GlossaClient> start(Release bundled, Release persisted) async {
      final store = MemoryReleaseStore();
      for (final e in persisted.artifacts.entries) {
        await store.putArtifact(e.key, e.value);
      }
      await store.putManifest(persisted.manifest);
      final client = GlossaClient(
        bundled: BundledRelease(
          manifest: bundled.manifest,
          artifacts: bundled.artifacts,
        ),
        store: store,
        locales: ['en'],
      );
      await client.ready;
      return client;
    }

    test('an app update ships a newer catalog than the cache', () async {
      final client = await start(v2, v1);
      expect(client.release?.id, 'rel_2');
      expect('${client.explain('hello').source}', 'bundled');
      await client.dispose();
    });

    test('the cache is newer than the build it came with', () async {
      final client = await start(v1, v2);
      expect(client.release?.id, 'rel_2');
      expect('${client.explain('hello').source}', 'persisted');
      await client.dispose();
    });
  });

  test('a manifest for another environment is rejected (SPEC §3)', () async {
    final errors = <GlossaError>[];
    final client = GlossaClient(
      environment: 'staging',
      bundled: BundledRelease(manifest: v1.manifest, artifacts: v1.artifacts),
      locales: ['en'],
    );
    client.errors.listen(errors.add);
    await client.ready;

    expect(client.release, isNull);
    expect(client.t('hello'), 'hello');
    expect([for (final e in errors) '${e.type}'], ['schema']);
    expect(errors.single.detail, contains('production'));
    await client.dispose();
  });

  group('FileReleaseStore', () {
    late Directory dir;
    setUp(() => dir = Directory.systemTemp.createTempSync('glossa-store'));
    tearDown(() => dir.deleteSync(recursive: true));

    Future<void> persist(Release release, {String? etag}) async {
      final store = FileReleaseStore(dir);
      for (final e in release.artifacts.entries) {
        await store.putArtifact(e.key, e.value);
      }
      await store.putManifest(release.manifest, etag: etag);
    }

    test('a release survives a restart, ETag and all', () async {
      await persist(v1, etag: '"m1"');

      final client = GlossaClient(
        store: FileReleaseStore(dir),
        locales: ['en'],
      );
      await client.ready;
      expect(client.t('hello'), 'Hello v1');
      expect('${client.explain('hello').source}', 'persisted');
      expect((await FileReleaseStore(dir).manifest())?.etag, '"m1"');
      await client.dispose();
    });

    test('the directory is a bundle, and reads back as one', () async {
      await persist(v1);
      final bundle = await readBundledRelease(dir);

      expect(bundle, isNotNull);
      expect(bundle!.manifest, v1.manifest);
      expect(bundle.artifacts.keys, containsAll(v1.artifacts.keys));
    });

    test('a torn write cannot activate a half-downloaded release', () async {
      await persist(v1, etag: '"m1"');
      // Now a v2 download that dies before the manifest is committed: its
      // artifacts are on disk, and nothing names them.
      final store = FileReleaseStore(dir);
      for (final e in v2.artifacts.entries) {
        await store.putArtifact(e.key, e.value);
      }

      final client = GlossaClient(
        store: FileReleaseStore(dir),
        locales: ['en'],
      );
      await client.ready;
      expect(
        client.release?.id,
        'rel_1',
        reason: 'the last release that committed',
      );
      expect(client.t('hello'), 'Hello v1');
      await client.dispose();
    });

    test('an ETag from another manifest is never reused', () async {
      await persist(v1, etag: '"m1"');
      // Replace only the manifest, as a torn write between the two files
      // would: the sidecar still holds v1's ETag.
      await File('${dir.path}/manifest.json').writeAsString(v2.manifest);

      expect((await FileReleaseStore(dir).manifest())?.etag, isNull);
    });

    test(
      'prune drops artifacts the last-good release no longer names',
      () async {
        await persist(v1);
        final store = FileReleaseStore(dir);
        for (final e in v2.artifacts.entries) {
          await store.putArtifact(e.key, e.value);
        }
        await store.prune(v2.artifacts.keys.toSet());

        for (final sha in v1.artifacts.keys) {
          expect(await store.artifact(sha), isNull, reason: sha);
        }
        for (final sha in v2.artifacts.keys) {
          expect(await store.artifact(sha), isNotNull, reason: sha);
        }
      },
    );
  });

  test('decode runs off the calling isolate (RFC 0005 §6.2)', () async {
    expect(await runOffThread(_whereAmI, 0), isNot(Isolate.current.debugName));
  });
}
