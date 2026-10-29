/// Integrity and authenticity (`runtimes/SPEC.md` §1.3).
///
/// Two independent checks, in the order the SPEC puts them:
///
/// * an **artifact** is accepted only when the SHA-256 of its exact bytes
///   is the hash the manifest names ([sha256Hex]);
/// * a **manifest** is accepted only when it carries an Ed25519 signature
///   from one of the configured keys over the RFC 8785 (JCS) form of
///   itself with `signatures` removed ([verifyManifestSignature]).
///
/// A runtime with no configured keys skips the second check, which SPEC
/// §1.3 allows — TLS still protects the transport — and which is what the
/// browser-side default is. Mobile OTA should configure keys.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';

import 'ed25519.dart';
import 'jcs.dart';
import 'manifest.dart';

/// The only signature algorithm the contract defines.
const String signatureAlgorithm = 'Ed25519';

/// The lowercase hex SHA-256 of the UTF-8 bytes of [text], the form the
/// manifest addresses artifacts by.
String sha256Hex(String text) => sha256.convert(utf8.encode(text)).toString();

/// Decodes base64url with the padding optional, which is how the platform
/// writes keys and signatures. Throws [FormatException] on anything else.
Uint8List decodeBase64Url(String encoded) {
  final padded = encoded.padRight((encoded.length + 3) ~/ 4 * 4, '=');
  return base64.decode(padded.replaceAll('-', '+').replaceAll('_', '/'));
}

/// A release-signing key the runtime trusts.
class GlossaPublicKey {
  /// Creates a key from raw Ed25519 public key bytes.
  GlossaPublicKey(this.keyId, this.key);

  /// Matches a manifest's `signatures[].keyId`.
  final String keyId;

  /// The 32 raw Ed25519 public key bytes.
  final Uint8List key;

  /// Reads a raw Ed25519 public key in base64url (padding optional), the
  /// form the platform publishes signing keys in and the form
  /// `runtimes/testdata/loading/*.json` carries them in.
  ///
  /// Throws [FormatException] when [encoded] is not 32 base64url bytes.
  factory GlossaPublicKey.parse(String keyId, String encoded) {
    final raw = decodeBase64Url(encoded);
    if (raw.length != 32) {
      throw FormatException(
        'public key $keyId is ${raw.length} bytes, expected 32',
      );
    }
    return GlossaPublicKey(keyId, raw);
  }
}

/// Why [bytes] don't carry a valid signature from one of [keys], or null
/// when one of them verifies.
///
/// [bytes] must be the manifest's **exact bytes**, not a re-serialization:
/// the signature covers the canonical form of what was published, and a
/// round trip through this package's typed [Manifest] would drop the
/// unknown fields SPEC §1.1 says to ignore but the signer still signed.
///
/// With no [keys], verification is off and this returns null.
String? verifyManifestSignature(
  String bytes,
  Manifest manifest,
  List<GlossaPublicKey> keys,
) {
  if (keys.isEmpty) return null;
  final String signed;
  try {
    signed = canonicalizeJson(bytes, without: 'signatures');
  } on JcsException catch (e) {
    return 'manifest cannot be canonicalized: ${e.detail}';
  } on FormatException catch (e) {
    return 'manifest cannot be canonicalized: ${e.message}';
  }
  final message = utf8.encode(signed);

  var candidates = 0;
  for (final signature in manifest.signatures) {
    if (signature.alg != signatureAlgorithm) continue;
    for (final key in keys) {
      if (key.keyId != signature.keyId) continue;
      candidates++;
      final Uint8List raw;
      try {
        raw = decodeBase64Url(signature.sig);
      } on FormatException {
        continue; // Not base64url: not a signature anyone can check.
      }
      if (ed25519Verify(publicKey: key.key, signature: raw, message: message)) {
        return null;
      }
    }
  }
  return candidates == 0
      ? 'release ${manifest.release.id} has no signature from a configured key'
      : 'release ${manifest.release.id} has an invalid signature';
}
