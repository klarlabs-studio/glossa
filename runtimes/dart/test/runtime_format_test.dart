/// Glossa's own MF2 runtime cases
/// (`messageformat/testdata/glossa/runtime-format.json`) through this
/// package's interpreter (`runtimes/SPEC.md` §5): precompiled data model +
/// locale + values -> the reference formatter's output.
///
/// The Go driver is `runtimes/go/runtime_format_test.go` and the JS driver
/// is `runtimes/js/runtime/src/runtime-format.test.ts`.
///
/// `messageformat/testdata/glossa/README.md` sets the rule this file
/// follows: the fixture is generated on one ICU/CLDR version and its cases
/// are chosen so the output is the same in CLDR 47 and 48, so a mismatch is
/// never version drift. "An implementation that can't reproduce a case keeps
/// a skip list with the reason and the upstream gap, never a workaround."
/// [_skips] is that list, and `the skip list is honest` keeps it true in
/// both directions: an entry whose case has disappeared fails, and so does
/// one whose case would now pass.
library;

import 'package:glossa/glossa.dart';
import 'package:test/test.dart';

import 'fixtures.dart';

// Every gap below is in `package:intl`, which is the only CLDR data this
// package has (SPEC §5 lets a runtime document implementation-defined
// output). They are fixed upstream, or by a richer CLDR layer later - never
// by hand-written locale data here, which would make Dart the one runtime
// that answers differently.

const String _noOrdinal =
    'package:intl ships cardinal plural rules only (src/plural_rules.dart '
    'has no ordinal table), so `:number select=ordinal` cannot select';

const String _noUnits =
    'package:intl has no measurement-unit data, so `:unit` is not registered '
    'and the expression resolves as unknown-function';

const String _noTimeZones =
    "package:intl has no time-zone database and formats in the DateTime's "
    'own zone, so a named `timeZone` other than UTC cannot be applied';

const String _noCurrencyNames =
    'package:intl has no currency display names, only a symbol and the ISO '
    'code, so `currencyDisplay=name` cannot be honoured';

const String _noLocalCurrencySymbols =
    'package:intl looks a currency symbol up in one global table, so a '
    'currency foreign to the locale does not fall back to its ISO code '
    '(es/fr JPY) and a locale-specific form is missed (ja U+FFE5)';

const String _noMinGroupingDigits =
    'package:intl ignores CLDR minimumGroupingDigits=2 for es, so a '
    'four-digit integer part is grouped (1.234 where CLDR says 1234) - the '
    'same gap runtimes/go records for go-intl';

const String _roundedPluralOperandI =
    'package:intl computes the CLDR plural operand `i` as `_n.round()` '
    '(src/plural_rules.dart, startRuleEvaluation) instead of the integer '
    'part, so fr 1.5 gets i=2 and resolves as other rather than one';

const String _staleGermanDateTimeFormat =
    "package:intl carries '{1}, {0}' as the German dateTimeFormat at every "
    'length, where CLDR 48 has "{1} \'um\' {0}" for full and long';

/// Cases this runtime cannot reproduce, keyed by `"<description> [<params>]"`.
const Map<String, String> _skips = {
  'de EUR spelled out [total=1234.5]': _noCurrencyNames,
  'de datetime with seconds [d=2026-09-19T14:05:00Z]':
      _staleGermanDateTimeFormat,
  'de kilometers [km=12.5]': _noUnits,
  'en kilometers, long [km=12.5]': _noUnits,
  'en ordinal suffixes [place=101]': _noOrdinal,
  'en ordinal suffixes [place=111]': _noOrdinal,
  'en ordinal suffixes [place=11]': _noOrdinal,
  'en ordinal suffixes [place=12]': _noOrdinal,
  'en ordinal suffixes [place=13]': _noOrdinal,
  'en ordinal suffixes [place=1]': _noOrdinal,
  'en ordinal suffixes [place=21]': _noOrdinal,
  'en ordinal suffixes [place=22]': _noOrdinal,
  'en ordinal suffixes [place=23]': _noOrdinal,
  'en ordinal suffixes [place=2]': _noOrdinal,
  'en ordinal suffixes [place=3]': _noOrdinal,
  'en ordinal suffixes [place=4]': _noOrdinal,
  'en selectordinal from ICU MF1 [n=13]': _noOrdinal,
  'en selectordinal from ICU MF1 [n=1]': _noOrdinal,
  'en selectordinal from ICU MF1 [n=22]': _noOrdinal,
  'es EUR total [total=1234.5]': _noMinGroupingDigits,
  'es JPY without fraction digits [total=98765.4]': _noLocalCurrencySymbols,
  'es grouping and negative numbers [n=-1234.5]': _noMinGroupingDigits,
  'es grouping and negative numbers [n=1234]': _noMinGroupingDigits,
  'es kilometers, long [km=12.5]': _noUnits,
  'es long date in Madrid [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'es short date in Madrid [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'es time in Madrid [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'fr JPY without fraction digits [total=98765.4]': _noLocalCurrencySymbols,
  'fr kilometers [km=12.5]': _noUnits,
  'fr kilometers, long [km=12.5]': _noUnits,
  'fr long date in Paris [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'fr plural with one and many [count=1.5]': _roundedPluralOperandI,
  'fr short date in Paris [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'fr time in Paris [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'ja JPY without fraction digits [total=1234567.4]': _noLocalCurrencySymbols,
  'ja JPY without fraction digits [total=1234]': _noLocalCurrencySymbols,
  'ja kilometers [km=12.5]': _noUnits,
  'ja kilometers, long [km=12.5]': _noUnits,
  'ja long date in Tokyo [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'ja short date in Tokyo [d=2026-09-19T20:30:00Z]': _noTimeZones,
  'ja time in Tokyo [d=2026-09-19T20:30:00Z]': _noTimeZones,
};

void main() {
  final fixture = loadMessageFormatFixture('glossa/runtime-format.json');
  final tests = (fixture['tests']! as List<Object?>)
      .cast<Map<String, Object?>>();
  final generatedWith = fixture['generatedWith']! as Map<String, Object?>;
  final byKey = {for (final tc in tests) _key(tc): tc};

  test('the fixture is present and records the CLDR it was generated on', () {
    expect(tests, isNotEmpty);
    expect(generatedWith['cldr'], isA<String>());
  });

  for (final tc in tests) {
    final key = _key(tc);
    final skip = _skips[key];
    test(
      key,
      () => _check(tc),
      skip: skip == null ? null : 'package:intl gap - $skip',
    );
  }

  group('the skip list is honest', () {
    for (final key in _skips.keys) {
      test('$key is still a case, and still fails', () {
        final tc = byKey[key];
        expect(tc, isNotNull, reason: 'stale skip entry: no such case');
        expect(
          () => _check(tc!),
          throwsA(isA<TestFailure>()),
          reason: 'this case passes now; remove it from _skips',
        );
      });
    }
  });
}

/// Run one case: render it and compare the string and the reported errors.
void _check(Map<String, Object?> tc) {
  final errors = <String>[];
  final rendered = formatMessage(
    Message.fromJson(tc['message']),
    tc['locale']! as String,
    values: _values(tc['params']),
    options: FormatOptions(
      bidiIsolation: tc['bidiIsolation'] != 'none',
      onError: (e) => errors.add(e.type),
    ),
  );
  expect(rendered, tc['exp'], reason: 'rendered string');
  expect(errors, [
    for (final e in (tc['expErrors'] as List<Object?>?) ?? const [])
      (e! as Map<String, Object?>)['type'],
  ], reason: 'reported errors');
}

String _key(Map<String, Object?> tc) {
  final params = [
    for (final p in (tc['params'] as List<Object?>?) ?? const [])
      '${(p! as Map<String, Object?>)['name']}=${p['value']}',
  ].join(' ');
  return '${tc['description']} [$params]';
}

Map<String, Object?> _values(Object? params) => {
  for (final p in (params as List<Object?>?) ?? const [])
    (p! as Map<String, Object?>)['name']! as String: p['type'] == 'datetime'
        ? DateTime.parse(p['value']! as String)
        : p['value'],
};
