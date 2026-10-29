/// Locale identity and negotiation (`runtimes/SPEC.md` §4).
///
/// The cases mirror `runtimes/go/locale_test.go` and
/// `runtimes/js/runtime/src/locale.test.ts`, so a disagreement between the
/// runtimes shows up here rather than in a product.
library;

import 'package:glossa/glossa.dart';
import 'package:test/test.dart';

Manifest _manifest(
  Map<String, List<String>> fallback,
  List<String> codes, {
  String sourceLocale = 'de',
}) => Manifest(
  project: 'prj',
  environment: 'production',
  release: const ReleaseRef('rel', 1, null),
  sourceLocale: sourceLocale,
  locales: [for (final c in codes) LocaleEntry(c, null)],
  fallback: fallback,
  artifacts: const {},
);

void main() {
  group('canonicalizeLocale (RFC 5646 §4.5)', () {
    const cases = <String, String?>{
      'en': 'en',
      'EN_us': 'en-US',
      // A deprecated primary language subtag takes its preferred value.
      'iw': 'he',
      'in': 'id',
      'ji': 'yi',
      // A script the language implies is left out.
      'en-Latn-US': 'en-US',
      // A script the language does not imply stays.
      'zh-hant-tw': 'zh-Hant-TW',
      // Extensions and private use carry formatting preferences, not a
      // distinct body of translations.
      'de-DE-u-co-phonebk': 'de-DE',
      'en-a-bbb-x-foo': 'en',
      ' de ': 'de',
      // `language-extlang` collapses to the extlang's preferred value.
      'zh-cmn-Hans': 'cmn-Hans',
      // Grandfathered tags take their registered replacement.
      'i-klingon': 'tlh',
      'art-lojban': 'jbo',
      'x-foo': null,
      'und': null,
      '': null,
      '*': null,
      'not a tag': null,
      'i-default': null,
    };
    cases.forEach((input, want) {
      test(
        '${input.isEmpty ? '(empty)' : input} -> ${want ?? 'rejected'}',
        () => expect(canonicalizeLocale(input), want),
      );
    });
  });

  test('canonicalizeLocales drops malformed tags and repeats', () {
    expect(canonicalizeLocales(['de-AT', 'bogus tag', 'de_at', 'en']), [
      'de-AT',
      'en',
    ]);
    expect(
      canonicalizeLocales(['EN_us', 'iw', 'de-at', 'not a tag', '', 'en-US']),
      ['en-US', 'he', 'de-AT'],
    );
  });

  group('truncations (RFC 4647 §3.4)', () {
    const cases = <String, List<String>>{
      'zh-Hant-TW': ['zh-Hant', 'zh'],
      'en': [],
      'fr-CA': ['fr'],
      // A trailing single-character subtag goes with the one before it.
      'de-a-bbb-foo': ['de-a-bbb', 'de'],
      'sl-rozaj-a-x': ['sl-rozaj', 'sl'],
    };
    cases.forEach((input, want) {
      test(input, () => expect(truncations(input), want));
    });
  });

  group('lookupLocale (RFC 4647 §3.4)', () {
    const available = ['de', 'de-AT', 'en', 'zh-Hant', 'fr-CA', 'fr'];

    test('takes the first requested tag that matches, truncating', () {
      expect(lookupLocale(['en'], available), 'en');
      expect(lookupLocale(['en-GB'], available), 'en');
      expect(lookupLocale(['zh-Hant-TW'], available), 'zh-Hant');
      expect(lookupLocale(['ja', 'fr-CA'], available), 'fr-CA');
    });

    test('drops a trailing single-character subtag with the one before it', () {
      expect(lookupLocale(['de-AT-u-co-phonebk'], available), 'de-AT');
      expect(lookupLocale(['en-a-bbb-x-priv'], available), 'en');
    });

    test('answers null when nothing matches', () {
      expect(lookupLocale(['ja'], available), isNull);
      expect(lookupLocale([], available), isNull);
    });
  });

  group('fallbackChain (SPEC §4.2)', () {
    const codes = [
      'de',
      'de-AT',
      'de-CH',
      'en',
      'en-CA',
      'fr',
      'fr-CA',
      'zh-Hant',
      'pt-BR',
      'pt-PT',
    ];

    test('follows explicit edges, then "*", then the source locale', () {
      final m = _manifest({
        'de-AT': ['de'],
        '*': ['en'],
      }, codes);
      expect(fallbackChain('de-AT', m), ['de-AT', 'de', 'en']);
      expect(fallbackChain('de', m), ['de', 'en']);
      expect(fallbackChain('en', m), ['en', 'de']);
    });

    test('expands explicit edges depth-first', () {
      final m = _manifest({
        'fr-CA': ['fr', 'en-CA'],
        'fr': ['de'],
        'en-CA': ['en'],
        '*': ['zh-Hant'],
      }, codes);
      expect(fallbackChain('fr-CA', m), [
        'fr-CA',
        'fr',
        'de',
        'en-CA',
        'en',
        'zh-Hant',
      ]);
    });

    test('truncates only when the locale has no explicit entry', () {
      final m = _manifest({'*': <String>[]}, codes);
      expect(fallbackChain('de-AT', m), ['de-AT', 'de']);
      // de-CH has an entry, so truncation is not consulted.
      final explicit = _manifest({
        'de-CH': ['en'],
      }, codes);
      expect(fallbackChain('de-CH', explicit), ['de-CH', 'en', 'de']);
    });

    test('stops at the first repeat, so a cycle terminates', () {
      final m = _manifest(
        {
          'pt-BR': ['pt-PT'],
          'pt-PT': ['pt-BR'],
        },
        codes,
        sourceLocale: 'en',
      );
      expect(fallbackChain('pt-BR', m), ['pt-BR', 'pt-PT', 'en']);
      expect(fallbackChain('pt-PT', m), ['pt-PT', 'pt-BR', 'en']);
    });

    test('keeps a fallback target the manifest does not list', () {
      final m = _manifest({
        'de-AT': ['nl'],
      }, codes);
      expect(fallbackChain('de-AT', m), ['de-AT', 'nl', 'de']);
    });
  });

  group('directionOf', () {
    test('reads the explicit or likely script', () {
      expect(directionOf('ar'), Direction.rtl);
      expect(directionOf('he'), Direction.rtl);
      // The deprecated Hebrew code canonicalizes first.
      expect(directionOf('iw'), Direction.rtl);
      expect(directionOf('ar-Latn'), Direction.ltr);
      expect(directionOf('de'), Direction.ltr);
      expect(directionOf('zh-Hant'), Direction.ltr);
      expect(directionOf('not a tag'), Direction.ltr);
    });

    test('every RTL script is an ISO 15924 code', () {
      for (final s in rtlScripts) {
        expect(s, matches(RegExp(r'^[A-Z][a-z]{3}$')));
      }
    });
  });

  group('resolveLocales (intent §13)', () {
    test('runs the chain in priority order and canonicalizes', () {
      expect(
        resolveLocales([
          'de_at',
          null,
          ['EN_us', 'fr'],
          'de-AT',
        ]),
        ['de-AT', 'en-US', 'fr'],
      );
    });
  });

  group('acceptLanguage', () {
    test('sorts by quality, header order breaking ties', () {
      expect(acceptLanguage('de;q=0.8, en-GB, fr;q=0.9, *;q=0.1, ja;q=0'), [
        'en-GB',
        'fr',
        'de',
      ]);
      expect(acceptLanguage(null), isEmpty);
      expect(acceptLanguage('  '), isEmpty);
    });
  });
}
