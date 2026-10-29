/// SPEC rules the shared scenarios don't reach: schema rejection, an
/// unreadable message, the "never render the empty string" rule, and the
/// error channel's repeat suppression (`runtimes/SPEC.md` §1.1, §3, §6).
library;

import 'dart:convert';

import 'package:glossa/glossa.dart';
import 'package:test/test.dart';

const String _manifestJson = '''
{
  "schema": "glossa.manifest/v1",
  "project": "prj_test",
  "environment": "production",
  "release": {"id": "rel_1", "version": 3, "createdAt": "2026-09-01T08:00:00Z"},
  "sourceLocale": "de",
  "locales": [{"code": "de", "direction": "ltr"}, {"code": "ar", "direction": "rtl"}],
  "fallback": {},
  "artifacts": {
    "de": {"default": {"sha256": "aaa", "size": 1}},
    "ar": {"default": {"sha256": "bbb", "size": 1}}
  },
  "unknownFutureField": {"ignored": true}
}
''';

String _artifact(String locale, Map<String, Object?> messages) => jsonEncode({
  'schema': 'glossa.artifact/v1',
  'locale': locale,
  'namespace': 'default',
  'messages': messages,
});

Map<String, Object?> _text(String value) => {
  'type': 'message',
  'declarations': <Object?>[],
  'pattern': [value],
};

void main() {
  group('Manifest (SPEC §1.1)', () {
    test('reads a v1 manifest and ignores unknown top-level fields', () {
      final m = Manifest.decode(_manifestJson);
      expect(m.release.id, 'rel_1');
      expect(m.release.version, 3);
      expect(m.localeCodes, ['de', 'ar']);
      expect(m.directionOfLocale('ar'), 'rtl');
    });

    test('rejects a different schema major, and malformed JSON', () {
      expect(
        () => Manifest.decode(
          _manifestJson.replaceAll('manifest/v1', 'manifest/v2'),
        ),
        throwsA(isA<SchemaException>()),
      );
      expect(() => Manifest.decode('{'), throwsA(isA<SchemaException>()));
    });
  });

  group('Catalog (SPEC §3, §6)', () {
    Catalog build(Map<String, String> artifacts) => Catalog.fromRelease(
      manifest: Manifest.decode(_manifestJson),
      artifacts: artifacts,
      source: Source.bundled,
      errorInterval: Duration.zero,
    );

    test(
      'an unreadable message is a schema error and resolves as missing',
      () async {
        final catalog = build({
          'aaa': _artifact('de', {
            'ok': _text('Gut'),
            // `type` is not a data-model message type.
            'broken': {'type': 'nonsense', 'declarations': <Object?>[]},
          }),
          'bbb': _artifact('ar', const {}),
        });
        final errors = <GlossaError>[];
        catalog.errors.listen(errors.add);

        final t = catalog.forLocales(['de']);
        // The release still serves: one bad message doesn't block it.
        expect(t.t('ok'), 'Gut');
        // The bad one resolves as missing and falls through to the id.
        expect(t.t('broken'), 'broken');
        expect(t.explain('broken').resolvedFrom, isNull);
        expect(errors.map((e) => '${e.type}'), contains('missing-message'));
        await catalog.dispose();
      },
    );

    test(
      'a message that renders empty falls through to the default, never ""',
      () async {
        final catalog = build({
          'aaa': _artifact('de', {'blank': _text('')}),
          'bbb': _artifact('ar', const {}),
        });
        final t = catalog.forLocales(['de']);
        expect(t.t('blank'), 'blank');
        expect(t.t('blank', defaultText: 'Etwas'), 'Etwas');
        await catalog.dispose();
      },
    );

    test('the direction comes from the manifest, not the script', () async {
      final catalog = build({
        'aaa': _artifact('de', const {}),
        'bbb': _artifact('ar', const {}),
      });
      expect(catalog.forLocales(['ar']).direction, Direction.rtl);
      expect(catalog.forLocales(['de']).direction, Direction.ltr);
      await catalog.dispose();
    });

    test('explain has no side effects: it reports nothing', () async {
      final catalog = build({
        'aaa': _artifact('de', const {}),
        'bbb': _artifact('ar', const {}),
      });
      final errors = <GlossaError>[];
      catalog.errors.listen(errors.add);
      final x = catalog.forLocales(['de']).explain('nope');
      expect(x.resolvedFrom, isNull);
      expect(x.source, Source.inline);
      expect(x.release?.id, 'rel_1');
      expect(errors, isEmpty);
      await catalog.dispose();
    });
  });

  group('ErrorChannel (SPEC §6)', () {
    test('drops a repeat of the same error within the interval', () async {
      final channel = ErrorChannel(const Duration(seconds: 60));
      final seen = <GlossaError>[];
      channel.stream.listen(seen.add);

      const a = GlossaError(ErrorType.missingMessage, 'x', messageId: 'a');
      const b = GlossaError(ErrorType.missingMessage, 'x', messageId: 'b');
      channel
        ..report(a)
        ..report(a)
        ..report(b);
      // Errors are the same when all five fields are equal.
      expect(seen.map((e) => e.messageId), ['a', 'b']);
      await channel.close();
    });

    test('a zero interval suppresses nothing', () async {
      final channel = ErrorChannel(Duration.zero);
      final seen = <GlossaError>[];
      channel.stream.listen(seen.add);
      const e = GlossaError(ErrorType.format, 'x');
      channel
        ..report(e)
        ..report(e);
      expect(seen, hasLength(2));
      await channel.close();
    });

    test('the wire spelling of every type matches the SPEC', () {
      expect(ErrorType.values.map((t) => '$t'), [
        'network',
        'integrity',
        'signature',
        'schema',
        'format',
        'missing-message',
      ]);
    });
  });
}
