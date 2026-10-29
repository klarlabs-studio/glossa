/// Staged rollout (`runtimes/SPEC.md` §1.4) beyond the loading sequences.
///
/// The cohort function is checked against
/// `runtimes/testdata/rollout/cohorts.json` — the shared file, read from
/// the checkout — id for id: 10,000 installation ids, the candidate counts
/// at each percentage and the SPEC's vectors. The generator computes them
/// with `hashlib` and shares no code with any runtime, so agreeing with it
/// is agreeing with the SPEC.
///
/// The rest is what `loading/*.json` can't express: where the installation
/// id comes from and where it is kept, and a candidate that fails the
/// schema.
library;

import 'dart:convert';
import 'dart:io';

import 'package:glossa/glossa.dart';
import 'package:glossa/io.dart';
import 'package:test/test.dart';

import 'fixtures.dart';

const String _edge = 'https://edge.test';
const String _key = 'pk_test';

/// In the candidate of `rollout-candidate-side.json`'s rollout (cohort 390).
const String _inside = 'e56569efb77a68930c4817af70197f0b';

/// Outside it at 10 % and 50 % (cohort 8164).
const String _outside = '4a8e596b4aa7b02507e9eeebe6cb5751';

/// Step 0 of `rollout-candidate-side.json`: rel_1 stable, a rollout of
/// rel_2 at 10 %, and the artifacts of both.
({Map<String, Object?> manifest, Map<String, String> artifacts}) _fixture() {
  final steps =
      loadFixtures('loading')['rollout-candidate-side.json']!['steps']!
          as List<Object?>;
  final edge =
      (steps[0]! as Map<String, Object?>)['edge']! as Map<String, Object?>;
  return (
    manifest:
        (edge['manifest']! as Map<String, Object?>)['body']!
            as Map<String, Object?>,
    artifacts: (edge['artifacts']! as Map<String, Object?>)
        .cast<String, String>(),
  );
}

/// An edge serving [manifest] and [artifacts]; [fetched] records every
/// artifact hash asked for.
Transport _transport(
  Map<String, Object?> manifest,
  Map<String, String> artifacts, [
  List<String>? fetched,
]) => (url, headers) async {
  if (url.endsWith('/manifest.json')) {
    return EdgeResponse(200, body: jsonEncode(manifest), etag: '"m"');
  }
  final sha = url.split('/').last.replaceAll('.json', '');
  fetched?.add(sha);
  final body = artifacts[sha];
  return body == null ? const EdgeResponse(404) : EdgeResponse(200, body: body);
};

GlossaClient _client(
  Map<String, Object?> manifest,
  Map<String, String> artifacts, {
  ReleaseStore? store,
  String? installationId,
  bool rollout = true,
  List<String>? fetched,
}) => GlossaClient(
  edge: _edge,
  deliveryKey: _key,
  locales: ['en'],
  transport: _transport(manifest, artifacts, fetched),
  store: store,
  installationId: installationId,
  rollout: rollout,
  refreshInterval: Duration.zero,
);

/// A store that keeps a release and nothing else: written before SPEC §1.4.
class _ReleaseOnlyStore implements ReleaseStore {
  final MemoryReleaseStore _inner = MemoryReleaseStore();

  @override
  Future<String?> artifact(String sha256) => _inner.artifact(sha256);

  @override
  Future<void> putArtifact(String sha256, String bytes) =>
      _inner.putArtifact(sha256, bytes);

  @override
  Future<StoredManifest?> manifest() => _inner.manifest();

  @override
  Future<void> putManifest(String bytes, {String? etag}) =>
      _inner.putManifest(bytes, etag: etag);

  @override
  Future<void> prune(Set<String> keep) => _inner.prune(keep);
}

void main() {
  group('runtimes/testdata/rollout/cohorts.json', () {
    final table = loadRuntimeFixture('rollout/cohorts.json');
    final salt = table['salt']! as String;
    final installations = (table['installations']! as List<Object?>)
        .cast<List<Object?>>();

    test('every one of the 10,000 installation ids gets its cohort', () {
      expect(installations, hasLength(10000));
      final wrong = <String>[];
      for (final row in installations) {
        final id = row[0]! as String;
        final got = cohortOf(salt, id);
        if (got != row[1]) wrong.add('$id: $got, want ${row[1]}');
      }
      expect(wrong, isEmpty);
    });

    test('the candidate holds as many at every percentage', () {
      final expected = (table['expCandidates']! as Map<String, Object?>);
      expect(expected, isNotEmpty);
      for (final e in expected.entries) {
        final percent = int.parse(e.key);
        final inside = installations
            .where((r) => cohortOf(salt, r[0]! as String) < percent * 100)
            .length;
        expect(inside, e.value, reason: '$percent %');
      }
    });

    final vectors = (table['vectors']! as List<Object?>)
        .cast<Map<String, Object?>>();
    test('there are the 14 vectors', () => expect(vectors, hasLength(14)));
    for (final v in vectors) {
      test('vector ${jsonEncode(v['key'])}: ${v['note']}', () {
        expect(
          cohortOf(v['salt']! as String, v['key']! as String),
          v['cohort'],
        );
      });
    }
  });

  group('the installation id', () {
    test('is created once, as 32 lowercase hex digits, and kept in a '
        'MemoryReleaseStore across restarts', () async {
      final f = _fixture();
      final store = MemoryReleaseStore();
      final first = _client(f.manifest, f.artifacts, store: store);
      await first.ready;
      final id = await store.installationId();
      expect(id, matches(RegExp(r'^[0-9a-f]{32}$')));
      final cohort = first.explain('hello').rollout!.cohort;
      expect(cohort, cohortOf('c3RhZ2VkLXJvbGxvdXQtMQ', id!));
      await first.dispose();

      final second = _client(f.manifest, f.artifacts, store: store);
      await second.ready;
      expect(await store.installationId(), id);
      expect(second.explain('hello').rollout!.cohort, cohort);
      await second.dispose();
    });

    test('is kept beside the release by FileReleaseStore', () async {
      final dir = Directory.systemTemp.createTempSync('glossa-rollout-');
      addTearDown(() => dir.deleteSync(recursive: true));
      final f = _fixture();
      final first = _client(
        f.manifest,
        f.artifacts,
        store: FileReleaseStore(dir),
      );
      await first.ready;
      final id = File('${dir.path}/installation-id').readAsStringSync();
      expect(id, matches(RegExp(r'^[0-9a-f]{32}$')));
      await first.dispose();

      final store = FileReleaseStore(dir);
      expect(await store.installationId(), id);
      final second = _client(f.manifest, f.artifacts, store: store);
      await second.ready;
      expect(
        second.explain('hello').rollout!.cohort,
        cohortOf('c3RhZ2VkLXJvbGxvdXQtMQ', id),
      );
      await second.dispose();
    });

    test('a FileReleaseStore ignores an id it did not write', () async {
      final dir = Directory.systemTemp.createTempSync('glossa-rollout-');
      addTearDown(() => dir.deleteSync(recursive: true));
      File('${dir.path}/installation-id').writeAsStringSync('NOT-AN-ID');
      expect(await FileReleaseStore(dir).installationId(), isNull);
    });

    test('lives in memory with a store that cannot keep it', () async {
      final f = _fixture();
      final client = _client(
        f.manifest,
        f.artifacts,
        store: _ReleaseOnlyStore(),
      );
      await client.ready;
      final rollout = client.explain('hello').rollout!;
      await client.refresh();
      expect(client.explain('hello').rollout!.cohort, rollout.cohort);
      await client.dispose();
    });

    test(
      'is the installationId option when given, and not persisted',
      () async {
        final f = _fixture();
        final store = MemoryReleaseStore();
        final client = _client(
          f.manifest,
          f.artifacts,
          store: store,
          installationId: _inside,
        );
        await client.ready;
        expect(client.explain('hello').rollout!.toJson(), {
          'id': 'ro_fixture',
          'percent': 10,
          'cohort': 390,
          'side': 'candidate',
        });
        expect(client.release?.id, 'rel_2');
        expect(await store.installationId(), isNull);
        await client.dispose();
      },
    );

    test('is not created for a manifest without a rollout', () async {
      final f = _fixture();
      final manifest = Map.of(f.manifest)..remove('rollout');
      final store = MemoryReleaseStore();
      final client = _client(manifest, f.artifacts, store: store);
      await client.ready;
      expect(client.explain('hello').rollout, isNull);
      expect(await store.installationId(), isNull);
      await client.dispose();
    });
  });

  test('the stable side never fetches a candidate artifact', () async {
    final f = _fixture();
    final fetched = <String>[];
    final client = _client(
      f.manifest,
      f.artifacts,
      installationId: _outside,
      fetched: fetched,
    );
    await client.ready;
    expect(client.release?.id, 'rel_1');
    expect(client.explain('hello').rollout?.side, RolloutSide.stable);
    final candidate =
        ((f.manifest['rollout']! as Map<String, Object?>)['candidate']!
                as Map<String, Object?>)['artifacts']!
            as Map<String, Object?>;
    final candidateShas = {
      for (final ns in candidate.values)
        for (final ref in (ns! as Map<String, Object?>).values)
          (ref! as Map<String, Object?>)['sha256'],
    };
    expect(fetched.where(candidateShas.contains), isEmpty);
    await client.dispose();
  });

  test('a candidate that fails the schema falls back to the stable view of '
      'the same manifest, with a schema error', () async {
    final f = _fixture();
    final manifest = jsonDecode(jsonEncode(f.manifest)) as Map<String, Object?>;
    ((manifest['rollout']! as Map<String, Object?>)['candidate']!
            as Map<String, Object?>)['locales'] =
        'not an array';
    final client = _client(manifest, f.artifacts, installationId: _inside);
    final errors = <GlossaError>[];
    client.errors.listen(errors.add);
    await client.ready;
    expect(client.release?.id, 'rel_1');
    expect(client.t('hello'), 'Hello v1');
    expect(client.explain('hello').rollout!.toJson(), {
      'id': 'ro_fixture',
      'percent': 10,
      'cohort': 390,
      'side': 'stable',
    });
    expect(errors.map((e) => e.type), [ErrorType.schema]);
    expect(errors.single.releaseId, 'rel_2');
    await client.dispose();
  });

  test('with rollout support off, explain() reports no rollout', () async {
    final f = _fixture();
    final client = _client(
      f.manifest,
      f.artifacts,
      installationId: _inside,
      rollout: false,
    );
    await client.ready;
    expect(client.release?.id, 'rel_1');
    expect(client.explain('hello').toJson()['rollout'], isNull);
    await client.dispose();
  });

  test('bundled and persisted releases race by the version of the view each '
      'would activate (SPEC §1.4)', () async {
    final f = _fixture();
    // Persisted: rel_1 (version 1), no rollout.
    final stable = Map.of(f.manifest)..remove('rollout');
    final store = MemoryReleaseStore();
    for (final e in f.artifacts.entries) {
      await store.putArtifact(e.key, e.value);
    }
    await store.putManifest(jsonEncode(stable));
    // Bundled: rel_1 (version 1) too, but with a rollout of rel_2 (version
    // 2) this installation is in. Compared as top-level releases the two
    // tie and the persisted one wins; compared as views the bundle's
    // candidate is newer.
    final client = GlossaClient(
      locales: ['en'],
      store: store,
      bundled: BundledRelease(
        manifest: jsonEncode(f.manifest),
        artifacts: f.artifacts,
      ),
      installationId: _inside,
      refreshInterval: Duration.zero,
    );
    await client.ready;
    expect(client.release?.id, 'rel_2');
    expect(client.t('hello'), 'Hello v2');
    expect(client.explain('hello').source, Source.bundled);
    expect(client.explain('hello').rollout?.side, RolloutSide.candidate);
    await client.dispose();
  });
}
