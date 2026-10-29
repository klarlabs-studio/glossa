/// RFC 8785, the JSON Canonicalization Scheme (JCS).
///
/// A manifest's signature is Ed25519 over the JCS form of the manifest with
/// the `signatures` member removed (`runtimes/SPEC.md` §1.3), so a runtime
/// that wants to check a signature has to reproduce that form byte for byte.
///
/// Dart has no JCS package, so this is the implementation the Dart runtime
/// uses, ported from `runtimes/go/jcs.go` and cross-checked against it. The
/// three rules that matter:
///
/// 1. **Numbers** serialize exactly as ECMAScript's `Number.prototype
///    .toString` does (RFC 8785 §3.2.2.3), which is *not* Dart's `toString`:
///    ECMAScript writes `295147905179352830000`, Dart writes
///    `2.9514790517935283e+20`. [formatEs6Number] implements ECMA-262
///    §6.1.6.1.20 directly.
/// 2. **Object members** sort by their UTF-16 code units (§3.2.3). Dart
///    strings *are* UTF-16 and `String.compareTo` compares code units, so
///    sorting the keys is enough.
/// 3. **Strings** escape only what JSON requires (§3.2.2.2): `"`, `\` and
///    the control characters, with the short forms where they exist and
///    `\u00xx` otherwise. Nothing else is escaped, so `<`, `&` and U+2028
///    stay literal.
///
/// Duplicate member names: `dart:convert` keeps the last one, and this
/// canonicalizer runs over the very map `jsonDecode` produced, so the bytes
/// that get verified and the manifest that gets read can never disagree
/// about which value won.
library;

import 'dart:convert';

/// Thrown when a value has no RFC 8785 canonical form: a number that is not
/// a JSON number (NaN or an infinity, which is what a literal outside the
/// IEEE 754 double range decodes to), or a value `dart:convert` would never
/// have produced.
class JcsException implements Exception {
  /// Creates a JCS exception.
  JcsException(this.detail);

  /// What was wrong.
  final String detail;

  @override
  String toString() => 'JcsException: $detail';
}

/// The RFC 8785 canonical form of one already decoded JSON value.
String canonicalizeJsonValue(Object? value) {
  final out = StringBuffer();
  _write(out, value);
  return out.toString();
}

/// The RFC 8785 canonical form of the JSON document [source].
///
/// With [without], that top-level object member is dropped first — which is
/// how a manifest's signed form is produced (`without: 'signatures'`).
///
/// Throws [FormatException] when [source] is not JSON and [JcsException]
/// when it holds a number with no canonical form.
String canonicalizeJson(String source, {String? without}) {
  final decoded = jsonDecode(source);
  if (without != null && decoded is Map<String, Object?>) {
    // A copy: canonicalizing must not mutate the caller's document.
    final copy = Map<String, Object?>.of(decoded)..remove(without);
    return canonicalizeJsonValue(copy);
  }
  return canonicalizeJsonValue(decoded);
}

void _write(StringBuffer out, Object? value) {
  if (value == null) {
    out.write('null');
  } else if (value is bool) {
    out.write(value ? 'true' : 'false');
  } else if (value is num) {
    out.write(formatEs6Number(value));
  } else if (value is String) {
    _writeString(out, value);
  } else if (value is List<Object?>) {
    out.write('[');
    for (var i = 0; i < value.length; i++) {
      if (i > 0) out.write(',');
      _write(out, value[i]);
    }
    out.write(']');
  } else if (value is Map<String, Object?>) {
    // §3.2.3: sort by UTF-16 code units, which is String.compareTo.
    final keys = value.keys.toList()..sort();
    out.write('{');
    for (var i = 0; i < keys.length; i++) {
      if (i > 0) out.write(',');
      _writeString(out, keys[i]);
      out.write(':');
      _write(out, value[keys[i]]);
    }
    out.write('}');
  } else {
    throw JcsException('${value.runtimeType} is not a JSON value');
  }
}

const Map<int, String> _shortEscapes = {
  0x08: r'\b',
  0x09: r'\t',
  0x0a: r'\n',
  0x0c: r'\f',
  0x0d: r'\r',
  0x22: r'\"',
  0x5c: r'\\',
};

void _writeString(StringBuffer out, String value) {
  out.write('"');
  // Code units, not runes: a surrogate pair passes through unchanged and a
  // lone surrogate is left exactly as decoded rather than substituted.
  for (var i = 0; i < value.length; i++) {
    final unit = value.codeUnitAt(i);
    final short = _shortEscapes[unit];
    if (short != null) {
      out.write(short);
    } else if (unit < 0x20) {
      out.write('\\u${unit.toRadixString(16).padLeft(4, '0')}');
    } else {
      out.writeCharCode(unit);
    }
  }
  out.write('"');
}

/// [value] serialized as ECMAScript's `Number.prototype.toString`
/// (ECMA-262 §6.1.6.1.20), which is what RFC 8785 §3.2.2.3 requires.
///
/// Every JSON number is an IEEE 754 double, so an `int` is widened first:
/// `9007199254740993` canonicalizes as `9007199254740992`, exactly as it
/// would after a JavaScript `JSON.parse`.
///
/// Throws [JcsException] for NaN and the infinities. `dart:convert` decodes
/// a literal outside the double range (`1e400`) to an infinity, so this is
/// also how a non-I-JSON document is rejected.
String formatEs6Number(num value) {
  final f = value.toDouble();
  if (f.isNaN || f.isInfinite) {
    throw JcsException('$value is not a JSON number');
  }
  // ECMAScript prints both zeros as "0".
  if (f == 0) return '0';
  final negative = f < 0;
  final (digits, n) = _shortestDigits(negative ? -f : f);
  return (negative ? '-' : '') + _placeDecimalPoint(digits, n);
}

/// The shortest decimal digits that round-trip a positive [f], and `n`, the
/// position of the decimal point: `f == 0.<digits> × 10ⁿ`.
///
/// `toStringAsExponential()` without a digit count is Dart's shortest
/// round-tripping form, the same guarantee Go's `strconv.FormatFloat(f, 'e',
/// -1, 64)` gives, so both runtimes start from identical digits.
(String, int) _shortestDigits(double f) {
  final s = f.toStringAsExponential();
  final e = s.indexOf('e');
  final digits = s.substring(0, e).replaceFirst('.', '');
  return (digits, int.parse(s.substring(e + 1)) + 1);
}

String _placeDecimalPoint(String digits, int n) {
  final k = digits.length;
  if (k <= n && n <= 21) return digits + '0' * (n - k);
  if (0 < n && n <= 21) {
    return '${digits.substring(0, n)}.${digits.substring(n)}';
  }
  if (-6 < n && n <= 0) return '0.${'0' * -n}$digits';
  final exponent = n - 1;
  final mantissa = k > 1
      ? '${digits.substring(0, 1)}.${digits.substring(1)}'
      : digits;
  return exponent < 0 ? '${mantissa}e-${-exponent}' : '${mantissa}e+$exponent';
}
