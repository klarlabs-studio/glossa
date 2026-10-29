/// The runtime contract's loading fixtures (`runtimes/SPEC.md` §1.3, §3,
/// §7), driven exactly as the JS driver
/// (`runtimes/js/runtime/src/contract.test.ts`) and the Go driver
/// (`runtimes/go/conformance_test.go`) drive them.
///
/// Every `runtimes/testdata/loading/*.json` file is one sequence. Each step
/// puts the step's responses behind a fake edge, settles one load — a fresh
/// [GlossaClient] on the first step and at every `restartBefore` index,
/// which drops memory and keeps the persisted store, `refresh()` otherwise
/// — and then reads. The step asserts the whole contract: the rendered
/// string, the active release afterwards, `explain().source`, and the error
/// types emitted during the step, in order. A `304` step also asserts that
/// the revalidation carried the previous `ETag` as `If-None-Match`.
///
/// A sequence's `installationId` and `rolloutSupport` configure staged
/// rollout (SPEC §1.4), and a step's `expRollout`, when present, is
/// asserted against `explain().rollout`.
///
/// [_skips] below names the loading cases this runtime doesn't pass, and
/// it is empty: every shared sequence runs, the staged-rollout ones
/// included. An entry would mean Dart disagrees with the contract, and
/// that is a bug, not a configuration. The group `the skip list is honest`
/// fails if an entry ever goes stale, so the list cannot quietly rot.
library;

import 'dart:convert';

import 'package:glossa/glossa.dart';
import 'package:test/test.dart';

import 'fixtures.dart';

const String _edge = 'https://edge.test';
const String _deliveryKey = 'pk_test';

/// Loading cases this runtime deliberately doesn't pass, keyed
/// `<file>: <step index> <description>`, with the reason. A sequence's
/// steps build on each other, so a sequence is skipped whole. Empty since
/// RFC 0006 wave 3 implemented SPEC §1.4 here: like the JS and Go
/// drivers, this one now runs every case.
const Map<String, String> _skips = {};

/// A fake `glossa-edge` (SPEC §2) behind the client's [Transport]: it
/// answers the manifest and artifact paths from whatever it was last told
/// to serve, and records every request.
class _FakeEdge {
  Map<String, Object?> _manifest = const {'status': 503};
  Map<String, String> _artifacts = const {};

  /// Every request, in order, as `(url, headers)`.
  final List<({String url, Map<String, String> headers})> requests = [];

  void serve(Map<String, Object?> edge) {
    _manifest =
        (edge['manifest'] as Map<String, Object?>?) ?? const {'status': 503};
    _artifacts = {
      for (final e
          in (edge['artifacts'] as Map<String, Object?>? ?? const {}).entries)
        e.key: e.value! as String,
    };
  }

  Transport get transport => (url, headers) async {
    requests.add((url: url, headers: Map.of(headers)));
    if (url == '$_edge/v1/$_deliveryKey/production/manifest.json') {
      final status = _manifest['status']! as int;
      if (status != 200) return EdgeResponse(status);
      return EdgeResponse(
        200,
        // `jsonEncode`, not the canonicalizer: the edge serves the
        // manifest in the order it was published, so verifying its
        // signature really does depend on the runtime's own JCS.
        body: jsonEncode(_manifest['body']),
        etag: _manifest['etag'] as String?,
      );
    }
    final match = RegExp(
      r'^' +
          RegExp.escape('$_edge/v1/$_deliveryKey/a/') +
          r'([0-9a-f]{64})\.json$',
    ).firstMatch(url);
    final body = match == null ? null : _artifacts[match.group(1)];
    return body == null
        ? const EdgeResponse(404)
        : EdgeResponse(200, body: body);
  };
}

void main() {
  final sequences = loadFixtures('loading');

  test('the loading fixtures are present', () {
    expect(sequences, isNotEmpty);
  });

  sequences.forEach((name, fixture) {
    final steps = (fixture['steps']! as List<Object?>)
        .cast<Map<String, Object?>>();
    final restartBefore =
        ((fixture['restartBefore'] as List<Object?>?) ?? const [])
            .cast<int>()
            .toSet();
    final publicKeys = [
      for (final k in (fixture['publicKeys'] as List<Object?>?) ?? const [])
        GlossaPublicKey.parse(
          (k! as Map<String, Object?>)['keyId']! as String,
          (k as Map<String, Object?>)['key']! as String,
        ),
    ];

    final installationId = fixture['installationId'] as String?;
    final rolloutSupport = fixture['rolloutSupport'] as bool? ?? true;

    test('runtimes/testdata/loading/$name', () async {
      final edge = _FakeEdge();
      // The store outlives a restart, exactly as a cache directory or
      // IndexedDB would; memory does not.
      final store = MemoryReleaseStore();
      final errors = <String>[];
      GlossaClient? client;
      String? lastEtag;

      for (var i = 0; i < steps.length; i++) {
        final step = steps[i];
        final where = 'step $i: ${step['description']}';
        final key = '$name: $i ${step['description']}';
        final skip = _skips[key];
        if (skip != null) continue;

        final read = step['read']! as Map<String, Object?>;
        final requested = (read['requested']! as List<Object?>).cast<String>();
        final manifest = step['edge'] is Map<String, Object?>
            ? (step['edge']! as Map<String, Object?>)['manifest']
                  as Map<String, Object?>?
            : null;

        edge.serve((step['edge'] as Map<String, Object?>?) ?? const {});
        final seenErrors = errors.length;
        final seenRequests = edge.requests.length;

        if (client == null || restartBefore.contains(i)) {
          await client?.dispose();
          client = GlossaClient(
            edge: _edge,
            deliveryKey: _deliveryKey,
            transport: edge.transport,
            store: store,
            publicKeys: publicKeys,
            locales: requested,
            refreshInterval: Duration.zero,
            installationId: installationId,
            rollout: rolloutSupport,
          );
          client.errors.listen((e) => errors.add('${e.type}'));
          await client.ready;
        } else {
          await client.refresh();
        }

        if (manifest?['status'] == 304) {
          final revalidation = edge.requests
              .skip(seenRequests)
              .firstWhere((r) => r.url.endsWith('manifest.json'));
          expect(
            revalidation.headers['If-None-Match'],
            lastEtag,
            reason: '$where: revalidated with the previous ETag',
          );
        }
        final body = manifest?['body'] as Map<String, Object?>?;
        // The manifest became active, on either side of a rollout it
        // carries.
        final candidate = switch (body?['rollout']) {
          {'candidate': {'release': {'id': final String id}}} => id,
          _ => null,
        };
        if (body != null &&
            [
              (body['release']! as Map<String, Object?>)['id'],
              candidate,
            ].contains(client.release?.id)) {
          lastEtag = manifest!['etag'] as String?;
        }

        if (client.explain(read['id']! as String).requested.join() !=
            requested.join()) {
          await client.setLocales(requested);
        }

        expect(client.t(read['id']! as String), step['exp'], reason: where);
        expect(
          client.release?.id,
          step['expActiveRelease'],
          reason: '$where: active release',
        );
        expect(
          '${client.explain(read['id']! as String).source}',
          step['expSource'],
          reason: '$where: explain().source',
        );
        expect(
          errors.sublist(seenErrors),
          step['expErrors'],
          reason: '$where: errors emitted during the step',
        );
        if (step.containsKey('expRollout')) {
          expect(
            client.explain(read['id']! as String).rollout?.toJson(),
            step['expRollout'],
            reason: '$where: explain().rollout',
          );
        }
      }
      await client?.dispose();
    });
  });

  group('the skip list is honest', () {
    final known = {
      for (final entry in sequences.entries)
        for (
          var i = 0;
          i < (entry.value['steps']! as List<Object?>).length;
          i++
        )
          '${entry.key}: $i '
              '${((entry.value['steps']! as List<Object?>)[i] as Map<String, Object?>)['description']}',
    };
    for (final key in _skips.keys) {
      test('$key is still a case', () {
        expect(known, contains(key), reason: 'stale skip entry');
      });
    }
    test('every loading case is accounted for', () {
      expect(known, isNotEmpty);
      // Every case either runs or is named in _skips with a reason.
      for (final key in _skips.keys) {
        expect(_skips[key], isNotEmpty, reason: '$key needs a reason');
      }
    });
  });
}
