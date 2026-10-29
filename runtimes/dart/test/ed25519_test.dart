/// Pure-Dart Ed25519 verification (`lib/src/ed25519.dart`) against the
/// RFC 8032 §7.1 vectors.
///
/// Where these came from: the three `Ed25519` test vectors published in RFC
/// 8032 §7.1 (the empty message, one byte, two bytes), each re-verified
/// against an independent implementation — pyca/cryptography's
/// `Ed25519PublicKey.verify` — before being written down here, so a typo in
/// a 64-byte hex string can't quietly become the expectation. The
/// malleability case below was produced the same way: `S + L` re-encoded
/// into TEST 2's signature, confirmed rejected by pyca/cryptography too.
///
/// The end-to-end vector is elsewhere: `runtimes/testdata/loading/
/// signatures.json` carries manifests signed by `gen/generate.py`, and
/// `loading_test.dart` checks a good signature, an absent one and one from
/// the wrong key through the whole loader.
library;

import 'dart:typed_data';

import 'package:glossa/src/ed25519.dart';
import 'package:test/test.dart';

Uint8List _hex(String s) => Uint8List.fromList([
  for (var i = 0; i < s.length; i += 2)
    int.parse(s.substring(i, i + 2), radix: 16),
]);

/// One RFC 8032 §7.1 vector.
typedef _Vector = ({
  String name,
  String publicKey,
  String signature,
  String message,
});

const List<_Vector> _rfc8032 = [
  (
    name: 'TEST 1 (empty message)',
    publicKey:
        'd75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a',
    signature:
        'e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e06522490155'
        '5fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b',
    message: '',
  ),
  (
    name: 'TEST 2 (one byte)',
    publicKey:
        '3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c',
    signature:
        '92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da'
        '085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00',
    message: '72',
  ),
  (
    name: 'TEST 3 (two bytes)',
    publicKey:
        'fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025',
    signature:
        '6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac'
        '18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a',
    message: 'af82',
  ),
];

void main() {
  group('RFC 8032 §7.1', () {
    for (final v in _rfc8032) {
      test(v.name, () {
        expect(
          ed25519Verify(
            publicKey: _hex(v.publicKey),
            signature: _hex(v.signature),
            message: _hex(v.message),
          ),
          isTrue,
        );
      });
    }
  });

  group('rejects', () {
    final v = _rfc8032[1];
    final publicKey = _hex(v.publicKey);
    final signature = _hex(v.signature);
    final message = _hex(v.message);

    test('a different message', () {
      expect(
        ed25519Verify(
          publicKey: publicKey,
          signature: signature,
          message: _hex('73'),
        ),
        isFalse,
      );
    });

    test('a different key', () {
      expect(
        ed25519Verify(
          publicKey: _hex(_rfc8032[2].publicKey),
          signature: signature,
          message: message,
        ),
        isFalse,
      );
    });

    test('a flipped bit in R and in S', () {
      for (final i in [0, 63]) {
        final tampered = Uint8List.fromList(signature)..[i] ^= 0x01;
        expect(
          ed25519Verify(
            publicKey: publicKey,
            signature: tampered,
            message: message,
          ),
          isFalse,
          reason: 'byte $i',
        );
      }
    });

    test('a non-canonical S (S + L), which would be malleable', () {
      expect(
        ed25519Verify(
          publicKey: publicKey,
          signature: _hex(
            '92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da'
            'f52db7415978abc61b2c2eb6aeebfca0387b2eaeb4302aeeb00d291612bb0c10',
          ),
          message: message,
        ),
        isFalse,
      );
    });

    test('a key or signature of the wrong length', () {
      expect(
        ed25519Verify(
          publicKey: publicKey.sublist(0, 31),
          signature: signature,
          message: message,
        ),
        isFalse,
      );
      expect(
        ed25519Verify(
          publicKey: publicKey,
          signature: signature.sublist(0, 63),
          message: message,
        ),
        isFalse,
      );
    });

    test('bytes that are not a curve point', () {
      // y = 2²⁵⁵ − 1 is above the field prime, so it encodes no point.
      expect(
        ed25519Verify(
          publicKey: Uint8List(32)..fillRange(0, 32, 0xff),
          signature: signature,
          message: message,
        ),
        isFalse,
      );
    });
  });
}
