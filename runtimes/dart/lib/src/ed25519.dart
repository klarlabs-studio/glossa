/// Ed25519 signature **verification** (RFC 8032 §5.1.7), in pure Dart.
///
/// `runtimes/SPEC.md` §1.3 signs a release manifest with Ed25519, and RFC
/// 0005 §14 (decision 9) asks for an implementation with no FFI so the
/// package works on the web and in AOT alike. Dart's platform libraries
/// offer no Ed25519, so this is it: `BigInt` arithmetic on the twisted
/// Edwards curve and SHA-512 from `package:crypto`.
///
/// Only verification is here. Nothing in this package ever holds a private
/// key, so there is no secret to leak through a timing side channel and no
/// signing code to get wrong; a runtime that could sign a manifest would be
/// a bug in the contract, not a missing feature.
///
/// The checks follow RFC 8032 §5.1.7 with the two strictness rules that
/// keep a signature from being malleable: `S` must be canonical (below the
/// group order `L`), and both the public key `A` and the commitment `R`
/// must decode to real curve points.
library;

import 'dart:typed_data';

import 'package:crypto/crypto.dart';

/// 2²⁵⁵ − 19, the field prime.
final BigInt _p = BigInt.two.pow(255) - BigInt.from(19);

/// The prime order of the base point's subgroup (RFC 8032 §5.1).
final BigInt _l =
    BigInt.two.pow(252) +
    BigInt.parse('27742317777372353535851937790883648493');

/// The curve constant `d = -121665/121666`.
final BigInt _d =
    (_p - BigInt.from(121665)) * BigInt.from(121666).modInverse(_p) % _p;

/// A square root of −1, used when the candidate `x` comes out with the
/// wrong sign of the square (RFC 8032 §5.1.3).
final BigInt _sqrtMinusOne = BigInt.two.modPow(
  (_p - BigInt.one) ~/ BigInt.from(4),
  _p,
);

/// A point in extended homogeneous coordinates: `x = X/Z`, `y = Y/Z` and
/// `T = XY/Z` (RFC 8032 §5.1.4).
class _Point {
  const _Point(this.x, this.y, this.z, this.t);

  final BigInt x;
  final BigInt y;
  final BigInt z;
  final BigInt t;
}

final _Point _identity = _Point(
  BigInt.zero,
  BigInt.one,
  BigInt.one,
  BigInt.zero,
);

/// The base point `B`, recovered from `y = 4/5`.
final _Point _base = () {
  final y = BigInt.from(4) * BigInt.from(5).modInverse(_p) % _p;
  final x = _recoverX(y, 0)!;
  return _Point(x, y, BigInt.one, x * y % _p);
}();

/// Whether [signature] is a valid Ed25519 signature of [message] under
/// [publicKey]. Malformed input is a failed verification, never a throw:
/// the caller is checking a manifest that may be hostile.
bool ed25519Verify({
  required List<int> publicKey,
  required List<int> signature,
  required List<int> message,
}) {
  if (publicKey.length != 32 || signature.length != 64) return false;
  final r = _decodePoint(signature.sublist(0, 32));
  if (r == null) return false;
  final s = _littleEndian(signature.sublist(32));
  // A non-canonical S would make the signature malleable (RFC 8032 §5.1.7).
  if (s >= _l) return false;
  final a = _decodePoint(publicKey);
  if (a == null) return false;

  final k =
      _littleEndian(
        sha512.convert([
          ...signature.sublist(0, 32),
          ...publicKey,
          ...message,
        ]).bytes,
      ) %
      _l;
  // [S]B = R + [k]A
  return _samePoint(_scalarMultiply(s, _base), _add(r, _scalarMultiply(k, a)));
}

/// The twisted Edwards addition law for `a = -1` (RFC 8032 §5.1.4). It is
/// unified, so doubling is `_add(p, p)`.
_Point _add(_Point p1, _Point p2) {
  final a = (p1.y - p1.x) * (p2.y - p2.x) % _p;
  final b = (p1.y + p1.x) * (p2.y + p2.x) % _p;
  final c = p1.t * BigInt.two * _d % _p * p2.t % _p;
  final d = p1.z * BigInt.two * p2.z % _p;
  final e = b - a;
  final f = d - c;
  final g = d + c;
  final h = b + a;
  return _Point(e * f % _p, g * h % _p, f * g % _p, e * h % _p);
}

_Point _scalarMultiply(BigInt scalar, _Point point) {
  var result = _identity;
  var addend = point;
  var n = scalar;
  while (n > BigInt.zero) {
    if (n.isOdd) result = _add(result, addend);
    addend = _add(addend, addend);
    n >>= 1;
  }
  return result;
}

/// Projective equality, so no modular inverse is needed to compare.
bool _samePoint(_Point p1, _Point p2) =>
    (p1.x * p2.z - p2.x * p1.z) % _p == BigInt.zero &&
    (p1.y * p2.z - p2.y * p1.z) % _p == BigInt.zero;

/// Decodes a 32-byte point: little-endian `y` with the sign of `x` in the
/// top bit (RFC 8032 §5.1.3). Null when the bytes are not a curve point.
_Point? _decodePoint(List<int> encoded) {
  final bytes = Uint8List.fromList(encoded);
  final sign = bytes[31] >> 7;
  bytes[31] &= 0x7f;
  final y = _littleEndian(bytes);
  // A non-canonical y (≥ p) is rejected, so one point has one encoding.
  if (y >= _p) return null;
  final x = _recoverX(y, sign);
  if (x == null) return null;
  return _Point(x, y, BigInt.one, x * y % _p);
}

/// The `x` with the given low bit that satisfies the curve equation for
/// `y`, or null when `(y² − 1)/(d y² + 1)` is not a square.
BigInt? _recoverX(BigInt y, int sign) {
  final y2 = y * y % _p;
  final u = (y2 - BigInt.one) % _p;
  final v = (_d * y2 + BigInt.one) % _p;
  final v3 = v * v % _p * v % _p;
  final v7 = v3 * v3 % _p * v % _p;
  var x =
      u *
      v3 %
      _p *
      (u * v7 % _p).modPow((_p - BigInt.from(5)) ~/ BigInt.from(8), _p) %
      _p;
  final check = v * x % _p * x % _p;
  if (check != u) {
    if (check != (_p - u) % _p) return null;
    x = x * _sqrtMinusOne % _p;
  }
  if (x == BigInt.zero && sign == 1) return null;
  if ((x.isOdd ? 1 : 0) != sign) x = _p - x;
  return x;
}

BigInt _littleEndian(List<int> bytes) {
  var n = BigInt.zero;
  for (var i = bytes.length - 1; i >= 0; i--) {
    n = (n << 8) | BigInt.from(bytes[i]);
  }
  return n;
}
