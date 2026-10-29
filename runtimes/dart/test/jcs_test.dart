/// RFC 8785 (JCS) in Dart (`lib/src/jcs.dart`).
///
/// JCS has no Dart prior art, so it is the one piece of the delivery
/// contract that needs vectors of its own (RFC 0005 §14, decision 9). Where
/// they come from:
///
/// * **`runtimes/testdata/loading/*.json`** — the end-to-end check. Those
///   manifests are signed by `gen/generate.py` over its own canonical form,
///   and `loading_test.dart` will not pass unless this canonicalizer
///   reproduces it byte for byte. That is the vector that actually gates
///   the runtime, but it exercises only objects, arrays, strings and small
///   integers, because a manifest holds nothing else.
/// * **RFC 8785 §3.2.2, §3.2.3 and Appendix B** — the document's own
///   examples, carried here in the same form
///   [`runtimes/go/jcs_test.go`](../../go/jcs_test.go) carries them, so the
///   two implementations are checked against one list.
/// * **Node 22 (V8) `String(x)`** — the number cases the fixtures cannot
///   reach. RFC 8785 §3.2.2.3 defines number serialization by reference to
///   ECMAScript, so the expectations below were produced by running the bit
///   patterns through V8 rather than derived by hand. Each one is
///   reproducible: `new DataView(b).setBigUint64(0, 0x<bits>n)`, then
///   `String(new DataView(b).getFloat64(0))`.
///
/// Numbers are the trap: Dart's own `toString` writes
/// `2.9514790517935283e+20` where ECMAScript writes
/// `295147905179352830000`, so every branch of ECMA-262 §6.1.6.1.20 —
/// integers up to 10²¹, the fixed-point window down to 10⁻⁶, and the
/// exponential form on either side of it — has a case here.
library;

import 'dart:typed_data';

import 'package:glossa/src/jcs.dart';
import 'package:test/test.dart';

/// The double with this IEEE 754 bit pattern, written as two words so a
/// pattern with the sign bit set doesn't overflow a signed Dart `int`.
double _bits(String hex) {
  final data = ByteData(8)
    ..setUint32(0, int.parse(hex.substring(0, 8), radix: 16))
    ..setUint32(4, int.parse(hex.substring(8), radix: 16));
  return data.getFloat64(0);
}

void main() {
  group('formatEs6Number', () {
    // RFC 8785 Appendix B, cross-checked against Node 22 (V8).
    const appendixB = {
      '0000000000000000': '0',
      '8000000000000000': '0',
      '0000000000000001': '5e-324',
      '8000000000000001': '-5e-324',
      '7fefffffffffffff': '1.7976931348623157e+308',
      'ffefffffffffffff': '-1.7976931348623157e+308',
      '4340000000000000': '9007199254740992',
      'c340000000000000': '-9007199254740992',
      '4430000000000000': '295147905179352830000',
      '44b52d02c7e14af5': '9.999999999999997e+22',
      '44b52d02c7e14af6': '1e+23',
      '44b52d02c7e14af7': '1.0000000000000001e+23',
      '444b1ae4d6e2ef4e': '999999999999999700000',
      '444b1ae4d6e2ef4f': '999999999999999900000',
      '444b1ae4d6e2ef50': '1e+21',
      '3eb0c6f7a0b5ed8c': '9.999999999999997e-7',
      '3eb0c6f7a0b5ed8d': '0.000001',
      '41b3de4355555553': '333333333.3333332',
      '41b3de4355555554': '333333333.33333325',
      '41b3de4355555555': '333333333.3333333',
      '41b3de4355555556': '333333333.3333334',
      '41b3de4355555557': '333333333.33333343',
      'becbf647612f3696': '-0.0000033333333333333333',
      '43143ff3c1cb0959': '1424953923781206.2',
    };
    appendixB.forEach((hex, want) {
      test('0x$hex is $want', () {
        expect(formatEs6Number(_bits(hex)), want);
      });
    });

    // The branch boundaries of ECMA-262 §6.1.6.1.20, from Node 22 (V8).
    final boundaries = <double, String>{
      1.0: '1',
      -1.0: '-1',
      100.0: '100',
      1.5: '1.5',
      4.5: '4.5',
      0.1: '0.1',
      0.3: '0.3',
      0.002: '0.002',
      // The last fixed-point value before the exponential form takes over,
      // at either end of the window.
      1e20: '100000000000000000000',
      1e21: '1e+21',
      1e22: '1e+22',
      1e-6: '0.000001',
      1e-7: '1e-7',
      1e-27: '1e-27',
      1e30: '1e+30',
      1e-323: '1e-323',
      1.2345678901234568e20: '123456789012345680000',
    };
    boundaries.forEach((value, want) {
      test('$value is $want', () => expect(formatEs6Number(value), want));
    });

    test('an int is widened to a double first, as JSON.parse would', () {
      // 2⁵³ + 1 is not representable; ECMAScript reads it as 2⁵³.
      expect(formatEs6Number(9007199254740993), '9007199254740992');
      expect(formatEs6Number(42), '42');
    });

    test('NaN and the infinities have no canonical form', () {
      for (final bad in [double.nan, double.infinity, -double.infinity]) {
        expect(() => formatEs6Number(bad), throwsA(isA<JcsException>()));
      }
    });
  });

  group('canonicalizeJson', () {
    test('RFC 8785 §3.2.2 example', () {
      const input = '''
{"numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
 "string": "\\u20ac\$\\u000F\\u000aA'\\u0042\\u0022\\u005c\\\\\\"\\/",
 "literals": [null, true, false]}''';
      expect(
        canonicalizeJson(input),
        r'{"literals":[null,true,false],'
        r'"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],'
        '"string":"\u20ac\$\\u000f\\nA\'B\\"\\\\\\\\\\"/"}',
      );
    });

    test('RFC 8785 §3.2.3 sorts members by UTF-16 code units', () {
      const input =
          r'{"\u20ac":"Euro Sign","\r":"Carriage Return",'
          r'"\ufb33":"Hebrew Letter Dalet With Dagesh","1":"One",'
          r'"\ud83d\ude00":"Emoji: Grinning Face","\u0080":"Control",'
          r'"\u00f6":"Latin Small Letter O With Diaeresis"}';
      expect(
        canonicalizeJson(input),
        '{"\\r":"Carriage Return","1":"One","\u0080":"Control",'
        '"\u00f6":"Latin Small Letter O With Diaeresis",'
        '"\u20ac":"Euro Sign","\u{1f600}":"Emoji: Grinning Face",'
        '"\ufb33":"Hebrew Letter Dalet With Dagesh"}',
      );
    });

    test('only JSON-required escapes, so no HTML escaping', () {
      expect(canonicalizeJson(r'{"a":"<&>\u2028"}'), '{"a":"<&>\u2028"}');
    });

    test('control characters use the short forms where they exist', () {
      expect(
        canonicalizeJson(r'"\u0001\b\t\f\u001f"'),
        r'"\u0001\b\t\f\u001f"',
      );
    });

    test('nesting is canonicalized at every level', () {
      expect(
        canonicalizeJson('{"b":[{"d":1,"c":{}}],"a":[]}'),
        '{"a":[],"b":[{"c":{},"d":1}]}',
      );
    });

    test('a number outside the double range is rejected', () {
      expect(
        () => canonicalizeJson('{"a":1e400}'),
        throwsA(isA<JcsException>()),
      );
    });

    test('malformed JSON is rejected', () {
      for (final bad in ['[1,]', '{"a":1} {"b":2}', '']) {
        expect(() => canonicalizeJson(bad), throwsA(isA<FormatException>()));
      }
    });

    test('`without` drops the named top-level member only', () {
      expect(
        canonicalizeJson(
          '{"z":1,"signatures":[{"sig":"x"}],"a":{"signatures":2}}',
          without: 'signatures',
        ),
        '{"a":{"signatures":2},"z":1}',
      );
    });

    test('`without` leaves the caller\'s document alone', () {
      const source = '{"a":1,"signatures":[]}';
      canonicalizeJson(source, without: 'signatures');
      expect(canonicalizeJson(source), '{"a":1,"signatures":[]}');
    });
  });
}
