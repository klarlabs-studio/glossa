/// The runtime contract's resolution fixtures (`runtimes/SPEC.md` §7),
/// driven exactly as the JS driver (`runtimes/js/runtime/src/contract.test.ts`)
/// and the Go driver (`runtimes/go/conformance_test.go`) drive them.
///
/// Every case of every `runtimes/testdata/scenarios/*.json` file runs, and
/// every case asserts the same fields those drivers assert: the rendered
/// string, the active locale and direction, and the whole `explain()`
/// document (chain, `resolvedFrom`, release, source and steps). There is no
/// skip list — an entry here would mean the Dart runtime disagrees with the
/// contract, and that is a bug, not a configuration.
library;

import 'package:glossa/glossa.dart';
import 'package:test/test.dart';

import 'fixtures.dart';

void main() {
  final scenarios = loadFixtures('scenarios');

  test('the scenario fixtures are present', () {
    expect(scenarios, isNotEmpty);
  });

  scenarios.forEach((name, fixture) {
    group('runtimes/testdata/scenarios/$name', () {
      final manifest = Manifest.fromJson(fixture['manifest']);
      final artifacts = {
        for (final e in (fixture['artifacts']! as Map<String, Object?>).entries)
          e.key: e.value! as String,
      };
      final cases = (fixture['cases']! as List<Object?>)
          .cast<Map<String, Object?>>();

      late Catalog catalog;
      late List<GlossaError> errors;

      setUp(() {
        errors = [];
        catalog = Catalog.fromRelease(
          manifest: manifest,
          artifacts: artifacts,
          // The scenarios are loaded from the edge in the other drivers.
          source: Source.network,
          // Cases repeat message ids on purpose; report every miss rather
          // than once a minute.
          errorInterval: Duration.zero,
        );
        catalog.errors.listen(errors.add);
      });

      tearDown(() async => catalog.dispose());

      test('description', () {
        expect(fixture['description'], isA<String>());
      });

      for (var i = 0; i < cases.length; i++) {
        final c = cases[i];
        final requested = (c['requested']! as List<Object?>).cast<String>();
        final id = c['id']! as String;
        final expResolvedFrom = c['expResolvedFrom'] as String?;

        test('case $i: $requested $id', () {
          final localizer = catalog.forLocales(requested);
          final rendered = localizer.t(
            id,
            values: (c['values'] as Map<String, Object?>?) ?? const {},
            defaultText: c['default'] as String?,
            bidiIsolation: c['bidiIsolation'] != 'none',
          );

          expect(rendered, c['exp'], reason: 'rendered string');
          expect(localizer.locale, c['expLocale'], reason: 'active locale');
          if (c['expDirection'] != null) {
            expect(
              '${localizer.direction}',
              c['expDirection'],
              reason: 'direction',
            );
          }

          final x = localizer.explain(id);
          expect(x.locale, c['expLocale']);
          expect(x.chain, c['expChain']);
          expect(x.resolvedFrom, expResolvedFrom);
          expect(x.release?.id, manifest.release.id);
          expect(x.release?.version, manifest.release.version);
          expect('${x.source}', expResolvedFrom == null ? 'inline' : 'network');

          // Explaining the requested locales directly answers the same,
          // without switching.
          expect(localizer.explain(id, requested).toJson(), x.toJson());

          // The chain is walked to the locale that answered, and no further.
          final walked = expResolvedFrom == null
              ? x.chain
              : x.chain.sublist(0, x.chain.indexOf(expResolvedFrom) + 1);
          expect(
            [for (final s in x.steps) s.toJson()],
            [
              for (final locale in walked)
                {
                  'locale': locale,
                  'outcome': locale == expResolvedFrom ? 'found' : 'missing',
                },
            ],
          );

          // A message found in the chain renders without errors; a miss is
          // reported once, as `missing-message`.
          expect([
            for (final e in errors) '${e.type}',
          ], expResolvedFrom == null ? ['missing-message'] : <String>[]);
        });
      }
    });
  });
}
