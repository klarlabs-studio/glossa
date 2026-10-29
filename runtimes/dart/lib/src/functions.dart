/// The default MessageFormat 2 functions, over `package:intl`'s CLDR data:
/// `:string :number :integer :percent :currency :offset :date :time
/// :datetime`.
///
/// What `package:intl` cannot do, this package does not pretend to do. A
/// function it can't implement is simply not registered, so the expression
/// resolves as `unknown-function` and renders its MF2 fallback; an option it
/// can't honour reports `unsupported-operation`. See `README.md`, "What the
/// host's CLDR decides", for the list and the fixture skips that follow
/// from it.
library;

import 'package:intl/date_symbol_data_local.dart';
import 'package:intl/intl.dart';

import 'locale.dart';
import 'parts.dart';

/// A hard failure inside a function: the expression falls back and [type] is
/// reported on the error channel.
class FunctionError implements Exception {
  /// Creates a function error of the given MF2 error [type].
  FunctionError(this.type);

  /// The MF2 error type, e.g. `bad-operand`.
  final String type;

  @override
  String toString() => 'FunctionError($type)';
}

/// What a function may read about the call site.
class FunctionContext {
  /// Creates a function context.
  FunctionContext({
    required this.locale,
    required this.literals,
    required this.onError,
    this.dir,
  });

  /// The locale the message was found in (SPEC §4.3).
  final String locale;

  /// Set by the `u:dir` option.
  final String? dir;

  /// Names of the options given as literals rather than variables.
  final Set<String> literals;

  /// Report a non-fatal error; formatting continues.
  final void Function(String type) onError;
}

/// A resolved value, as a function returns it.
class MessageValue {
  /// Creates a resolved value.
  const MessageValue({
    required this.type,
    required this.value,
    this.dir,
    this.options,
    this.selector,
    this.formatter,
  });

  /// The value's MF2 type (`string`, `number`, `datetime`, `fallback`, …).
  final String type;

  /// The underlying Dart value.
  final Object? value;

  /// Text direction of the formatted value; drives bidi isolation.
  final String? dir;

  /// Resolved options, inherited when this value is another function's
  /// operand.
  final Map<String, Object?>? options;

  /// The variant keys this value matches, most preferred first. Null when
  /// the value can't select.
  final List<String> Function(List<String> keys)? selector;

  /// Null when the value can't be formatted.
  final List<Part> Function()? formatter;

  /// A copy with [dir], [options] or the source replaced.
  MessageValue copyWith({String? dir, Map<String, Object?>? options}) =>
      MessageValue(
        type: type,
        value: value,
        dir: dir ?? this.dir,
        options: options ?? this.options,
        selector: selector,
        formatter: formatter,
      );
}

/// A function handler. Throw a [FunctionError] to make the expression fall
/// back.
typedef MessageFunction = MessageValue Function(
  FunctionContext ctx,
  Map<String, Object?> options,
  Object? operand,
  bool hasOperand,
);

/// Unwrap a resolved value to its raw value and the options it carries.
(Object?, Map<String, Object?>?) unwrap(Object? v) =>
    v is MessageValue ? (v.value, v.options) : (v, null);

String _asString(Object? v) {
  final (raw, _) = unwrap(v);
  if (raw is String) return raw;
  throw FunctionError('bad-option');
}

int _asDigitSize(Object? v) {
  final (raw, _) = unwrap(v);
  final n = raw is String ? int.tryParse(raw) : (raw is int ? raw : null);
  if (n == null || n < 0) throw FunctionError('bad-option');
  return n;
}

(num, Map<String, Object?>?) _numeric(Object? operand) {
  var (raw, opts) = unwrap(operand);
  if (raw is String) raw = num.tryParse(raw);
  if (raw is! num) throw FunctionError('bad-operand');
  return (raw, opts);
}

/// The default text direction of a locale's script, for values the runtime
/// formats itself.
String _dirOf(String locale) => directionOf(locale).name;

// ---------------------------------------------------------------------------
// :string
// ---------------------------------------------------------------------------

/// MF2 compares a `:string` selector's value with the variant keys in NFC.
/// The Dart SDK has no Unicode normalization, so the comparison is on the
/// strings as given. Keys come from the data model, which the platform
/// already stores in NFC, so this only differs for an application value
/// that is decomposed — a gap to close with a normalization package rather
/// than a hand-rolled table.
MessageValue _string(
  FunctionContext ctx,
  Map<String, Object?> options,
  Object? operand,
  bool hasOperand,
) {
  final value = hasOperand ? '${unwrap(operand).$1}' : '';
  return MessageValue(
    type: 'string',
    value: value,
    dir: ctx.dir ?? 'auto',
    selector: (keys) => keys.contains(value) ? [value] : const [],
    formatter: () => [
      ValuePart(type: 'string', locale: ctx.locale, dir: ctx.dir, text: value),
    ],
  );
}

/// `:string` as the interpreter applies it to a bare literal operand.
MessageValue stringValue(FunctionContext ctx, String value) =>
    _string(ctx, const {}, value, true);

/// A bare value the runtime has no function for: rendered with `toString`.
MessageValue unknownValue(Object? value) => MessageValue(
  type: 'unknown',
  value: value,
  dir: 'auto',
  formatter: () => [ValuePart(type: 'unknown', text: '$value')],
);

// ---------------------------------------------------------------------------
// :number and friends
// ---------------------------------------------------------------------------

/// Which numeric function is being applied.
enum _NumberKind { number, integer, percent, currency }

/// Options `package:intl` has no equivalent for. Naming one reports
/// `unsupported-operation` rather than silently producing another locale's
/// answer.
const Set<String> _unsupportedNumberOptions = {
  'roundingIncrement',
  'roundingMode',
  'roundingPriority',
  'trailingZeroDisplay',
  'signDisplay',
  'currencySign',
};

MessageValue _numberFunction(
  _NumberKind kind,
  FunctionContext ctx,
  Map<String, Object?> options,
  Object? operand,
  bool hasOperand,
) {
  if (!hasOperand) throw FunctionError('bad-operand');
  var (value, inherited) = _numeric(operand);

  final resolved = <String, Object?>{...?inherited};
  int? minFraction;
  int? maxFraction;
  int? minInteger;
  int? minSignificant;
  int? maxSignificant;
  bool? grouping;
  String? currency;
  String? currencyDisplay;
  String? select;

  void readInt(String name, void Function(int) set) {
    if (!options.containsKey(name)) return;
    try {
      set(_asDigitSize(options[name]));
    } on FunctionError {
      ctx.onError('bad-option');
    }
  }

  for (final name in options.keys) {
    if (_unsupportedNumberOptions.contains(name)) {
      ctx.onError('unsupported-operation');
    }
  }
  readInt('minimumFractionDigits', (v) => minFraction = v);
  readInt('maximumFractionDigits', (v) => maxFraction = v);
  readInt('minimumIntegerDigits', (v) => minInteger = v);
  readInt('minimumSignificantDigits', (v) => minSignificant = v);
  readInt('maximumSignificantDigits', (v) => maxSignificant = v);
  if (options.containsKey('useGrouping')) {
    final v = '${unwrap(options['useGrouping']).$1}';
    grouping = v != 'never' && v != 'false';
  }
  if (options.containsKey('select')) {
    if (!ctx.literals.contains('select')) {
      ctx.onError('bad-option');
    } else {
      select = '${unwrap(options['select']).$1}';
      if (select == 'ordinal') {
        // package:intl carries cardinal plural rules only.
        ctx.onError('unsupported-operation');
      } else if (select != 'exact' && select != 'plural') {
        ctx.onError('bad-option');
        select = null;
      }
    }
  }

  switch (kind) {
    case _NumberKind.integer:
      value = value.round();
      maxFraction = 0;
      minFraction = null;
      minSignificant = null;
    case _NumberKind.currency:
      try {
        currency = _asString(options['currency'] ?? resolved['currency']);
      } on FunctionError {
        throw FunctionError('bad-operand');
      }
      if (options.containsKey('fractionDigits')) {
        final v = '${unwrap(options['fractionDigits']).$1}';
        if (v != 'auto') {
          try {
            minFraction = maxFraction = _asDigitSize(v);
          } on FunctionError {
            ctx.onError('bad-option');
          }
        }
      }
      if (options.containsKey('currencyDisplay')) {
        currencyDisplay = '${unwrap(options['currencyDisplay']).$1}';
        if (currencyDisplay != 'symbol' && currencyDisplay != 'code') {
          // narrowSymbol, name and never need currency display data
          // package:intl doesn't carry.
          ctx.onError('unsupported-operation');
          currencyDisplay = null;
        }
      }
    case _NumberKind.number:
    case _NumberKind.percent:
      break;
  }

  final format = switch (kind) {
    _NumberKind.percent => NumberFormat.percentPattern(ctx.locale),
    // `simpleCurrency` looks the locale's symbol up ("€", "￥");
    // `currency` without a symbol falls back to the ISO code, which is what
    // `currencyDisplay=code` asks for.
    _NumberKind.currency =>
      currencyDisplay == 'code'
          ? NumberFormat.currency(
              locale: ctx.locale,
              name: currency,
              decimalDigits: maxFraction,
            )
          : NumberFormat.simpleCurrency(
              locale: ctx.locale,
              name: currency,
              decimalDigits: maxFraction,
            ),
    _ => NumberFormat.decimalPattern(ctx.locale),
  };
  if (minFraction != null) format.minimumFractionDigits = minFraction!;
  if (maxFraction != null) format.maximumFractionDigits = maxFraction!;
  if (minInteger != null) format.minimumIntegerDigits = minInteger!;
  if (maxSignificant != null) {
    format
      ..significantDigits = maxSignificant
      ..significantDigitsInUse = true;
    if (minSignificant != null) {
      format.minimumSignificantDigits = minSignificant;
    }
  }
  if (grouping == false) format.turnOffGrouping();

  resolved
    ..['style'] = kind.name
    ..['currency'] = currency ?? resolved['currency'];

  final dir = _dirOf(ctx.locale);
  final text = format.format(value);
  final canSelect = kind == _NumberKind.number || kind == _NumberKind.integer;
  final selectMode = select;

  return MessageValue(
    type: 'number',
    value: value,
    dir: dir,
    options: resolved,
    formatter: () => [
      ValuePart(type: 'number', locale: ctx.locale, dir: dir, text: text),
    ],
    selector: !canSelect || selectMode == 'ordinal'
        ? null
        : (keys) {
            final out = <String>[];
            final exact = value is int || value == value.roundToDouble()
                ? '${value.toInt()}'
                : '$value';
            if (keys.contains(exact)) out.add(exact);
            if (selectMode != 'exact') {
              final category = pluralCategory(
                value,
                ctx.locale,
                precision: visibleFractionDigits(
                  value,
                  format.minimumFractionDigits,
                  format.maximumFractionDigits,
                ),
              );
              if (keys.contains(category)) out.add(category);
            }
            return out;
          },
  );
}

/// How many fraction digits [value] actually shows once formatted with
/// [minFraction] and [maxFraction].
///
/// This is CLDR's `v` operand, which plural rules depend on: `1` is *one* in
/// English but `1.0` is *other*. `package:intl` calls it `precision` and
/// does not derive it from the formatter, so it is computed here.
int visibleFractionDigits(num value, int minFraction, int maxFraction) {
  if (value is int) return minFraction;
  final fixed = value.toStringAsFixed(maxFraction.clamp(0, 20));
  final dot = fixed.indexOf('.');
  final digits = dot < 0
      ? ''
      : fixed.substring(dot + 1).replaceAll(RegExp(r'0+$'), '');
  return digits.length < minFraction ? minFraction : digits.length;
}

/// The CLDR cardinal plural category of [value] in [locale].
///
/// `package:intl` exposes its plural rules only through [Intl.pluralLogic],
/// so the category names are passed in as the branch values and the chosen
/// branch is the answer.
///
/// `useExplicitNumberCases` is off: it is a `package:intl` convenience that
/// returns the `zero`, `one` and `two` branches for those exact numbers
/// whatever the locale's rule says. MF2 selects an exact key separately
/// (`0`, `1`), and CLDR decides the category — Japanese has no *one*, and
/// French counts 0 as *one*.
String pluralCategory(num value, String locale, {int? precision}) =>
    Intl.pluralLogic<String>(
      value,
      zero: 'zero',
      one: 'one',
      two: 'two',
      few: 'few',
      many: 'many',
      other: 'other',
      locale: locale,
      precision: precision,
      useExplicitNumberCases: false,
    );

MessageValue _offset(
  FunctionContext ctx,
  Map<String, Object?> options,
  Object? operand,
  bool hasOperand,
) {
  if (!hasOperand) throw FunctionError('bad-operand');
  final (value, inherited) = _numeric(operand);
  final add = options.containsKey('add');
  if (add == options.containsKey('subtract')) throw FunctionError('bad-option');
  final int delta;
  try {
    delta = _asDigitSize(add ? options['add'] : options['subtract']);
  } on FunctionError {
    throw FunctionError('bad-option');
  }
  final shifted = add ? value + delta : value - delta;
  return _numberFunction(
    _NumberKind.number,
    ctx,
    const {},
    MessageValue(type: 'number', value: shifted, options: inherited),
    true,
  );
}

// ---------------------------------------------------------------------------
// :date, :time, :datetime
// ---------------------------------------------------------------------------

/// Which date/time function is being applied.
enum _DateKind { date, time, datetime }

bool _dateSymbolsReady = false;

/// `package:intl` requires date symbols to be registered before any locale
/// but the default one can be formatted, and otherwise throws.
///
/// `date_symbol_data_local` carries every locale's data, so registration is
/// synchronous despite the `Future` the API returns, and this package is
/// usable without the application having to initialize anything. It is the
/// one place the package's CLDR version is decided: the data ships with
/// `package:intl`, not with Glossa.
void _ensureDateSymbols() {
  if (_dateSymbolsReady) return;
  initializeDateFormatting();
  _dateSymbolsReady = true;
}

const Map<String, String> _monthSkeleton = {
  'long': 'MMMM',
  'medium': 'MMM',
  'short': 'M',
};

const Map<String, String> _weekdaySkeleton = {
  'long': 'EEEE',
  'medium': 'EEE',
  'short': 'EEE',
};

const Set<String> _dateFields = {
  'weekday',
  'day-weekday',
  'month-day',
  'month-day-weekday',
  'year-month-day',
  'year-month-day-weekday',
};

MessageValue _dateTimeFunction(
  _DateKind kind,
  FunctionContext ctx,
  Map<String, Object?> options,
  Object? operand,
  bool hasOperand,
) {
  if (!hasOperand) throw FunctionError('bad-operand');
  var (raw, inherited) = unwrap(operand);
  if (raw is String) raw = DateTime.tryParse(raw);
  if (raw is int) raw = DateTime.fromMillisecondsSinceEpoch(raw, isUtc: true);
  if (raw is! DateTime) throw FunctionError('bad-operand');
  var date = raw;

  String? read(String name, Set<String>? allowed) {
    final value = options[name] ?? inherited?[name];
    if (value == null) return null;
    final s = '${unwrap(value).$1}';
    if (allowed != null && !allowed.contains(s)) {
      ctx.onError('bad-option');
      return null;
    }
    return s;
  }

  // package:intl formats in the DateTime's own zone and carries no tz
  // database, so only UTC (and "input", which means "leave it alone") can be
  // honoured.
  final timeZone = read('timeZone', null);
  if (timeZone != null && timeZone != 'input') {
    if (timeZone == 'UTC') {
      date = date.toUtc();
    } else {
      ctx.onError('unsupported-operation');
    }
  }
  if (read('calendar', null) != null) ctx.onError('unsupported-operation');
  if (kind != _DateKind.date && options.containsKey('hour12')) {
    ctx.onError('unsupported-operation');
  }

  final skeleton = StringBuffer();
  var dateLength = 'medium';
  if (kind != _DateKind.time) {
    final fields =
        read(kind == _DateKind.date ? 'fields' : 'dateFields', _dateFields) ??
        'year-month-day';
    final length =
        read(kind == _DateKind.date ? 'length' : 'dateLength', const {
          'long',
          'medium',
          'short',
        }) ??
        'medium';
    dateLength = length;
    final parts = fields.split('-');
    if (parts.contains('year')) skeleton.write('y');
    if (parts.contains('month')) skeleton.write(_monthSkeleton[length]);
    if (parts.contains('weekday')) skeleton.write(_weekdaySkeleton[length]);
    if (parts.contains('day')) skeleton.write('d');
  }

  final timeSkeleton = StringBuffer();
  if (kind != _DateKind.date) {
    final precision =
        read(kind == _DateKind.time ? 'precision' : 'timePrecision', const {
          'hour',
          'minute',
          'second',
        }) ??
        'minute';
    timeSkeleton.write('j');
    if (precision != 'hour') timeSkeleton.write('m');
    if (precision == 'second') timeSkeleton.write('s');
    if (read('timeZoneStyle', const {'long', 'short'}) != null) {
      ctx.onError('unsupported-operation');
    }
  }

  _ensureDateSymbols();
  final DateFormat format;
  try {
    if (skeleton.isEmpty) {
      format = DateFormat(timeSkeleton.toString(), ctx.locale);
    } else if (timeSkeleton.isEmpty) {
      format = DateFormat(skeleton.toString(), ctx.locale);
    } else {
      // `addPattern` joins with a plain space; CLDR joins a date and a time
      // with the locale's own dateTimeFormat ("{1} 'um' {0}" in German), so
      // the two resolved patterns are substituted into it.
      final datePart = DateFormat(skeleton.toString(), ctx.locale);
      final timePart = DateFormat(timeSkeleton.toString(), ctx.locale);
      final combiner =
          datePart.dateSymbols.DATETIMEFORMATS[const {
            'long': 1,
            'medium': 2,
            'short': 3,
          }[dateLength]!];
      format = DateFormat(
        combiner
            .replaceFirst('{1}', datePart.pattern!)
            .replaceFirst('{0}', timePart.pattern!),
        ctx.locale,
      );
    }
  } on Exception {
    throw FunctionError('unsupported-operation');
  }

  final dir = _dirOf(ctx.locale);
  final text = format.format(date);
  return MessageValue(
    type: 'datetime',
    value: date,
    dir: dir,
    options: {...?inherited, 'timeZone': ?timeZone},
    formatter: () => [
      ValuePart(type: 'datetime', locale: ctx.locale, dir: dir, text: text),
    ],
  );
}

/// The MF2 default function set this runtime implements.
///
/// `:unit` is deliberately absent: `package:intl` carries no unit display
/// data, and inventing one would produce a different string from every other
/// Glossa runtime. An unregistered function is `unknown-function`, which is
/// visible on the error channel instead of silently wrong.
final Map<String, MessageFunction> builtins = {
  'string': _string,
  'number': (c, o, v, h) => _numberFunction(_NumberKind.number, c, o, v, h),
  'integer': (c, o, v, h) => _numberFunction(_NumberKind.integer, c, o, v, h),
  'percent': (c, o, v, h) => _numberFunction(_NumberKind.percent, c, o, v, h),
  'currency': (c, o, v, h) => _numberFunction(_NumberKind.currency, c, o, v, h),
  'offset': _offset,
  'date': (c, o, v, h) => _dateTimeFunction(_DateKind.date, c, o, v, h),
  'time': (c, o, v, h) => _dateTimeFunction(_DateKind.time, c, o, v, h),
  'datetime': (c, o, v, h) => _dateTimeFunction(_DateKind.datetime, c, o, v, h),
};

/// A value the interpreter resolves without a function: numbers format with
/// the locale's rules, everything else renders as a string.
MessageValue implicitValue(FunctionContext ctx, Object? value) {
  if (value is num) {
    return _numberFunction(_NumberKind.number, ctx, const {}, value, true);
  }
  if (value is String) return _string(ctx, const {}, value, true);
  return unknownValue(value);
}
