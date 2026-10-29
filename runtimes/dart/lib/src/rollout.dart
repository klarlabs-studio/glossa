/// Staged rollout (`runtimes/SPEC.md` §1.4): a signed manifest may carry a
/// candidate release for a share of installations, and the runtime decides
/// from its installation id which side it is on.
///
/// The cohort function is integer arithmetic over SHA-256, so it agrees
/// with the JS and Go runtimes and with the generator in
/// `runtimes/testdata/gen/generate.py` id for id — including on the web,
/// where every intermediate value here stays below 2⁵³.
library;

import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';

import 'manifest.dart';

/// How many cohorts there are: `percent × 100` of them are in the
/// candidate.
const int _buckets = 10000;

final RegExp _salt = RegExp(r'^[A-Za-z0-9_-]{22}$');

/// The SPEC §1.4 cohort, 0–9999, of cohort [key] under a rollout's [salt]:
/// the first four bytes of SHA-256(UTF-8(salt) ‖ UTF-8(key)) as a
/// big-endian unsigned integer, mod 10000.
///
/// Both strings are hashed as given — the salt is never decoded, and the
/// key is neither case-folded nor Unicode-normalized.
int cohortOf(String salt, String key) {
  final d = sha256.convert(utf8.encode(salt + key)).bytes;
  // Multiplication, not shifts: on the web `<<` truncates to a signed
  // 32-bit integer, and the result has to be the unsigned one.
  return (((d[0] * 256 + d[1]) * 256 + d[2]) * 256 + d[3]) % _buckets;
}

/// A new installation id (SPEC §1.4): 128 bits from a cryptographically
/// secure source, as 32 lowercase hexadecimal digits.
String newInstallationId() {
  final random = Random.secure();
  return [
    for (var i = 0; i < 16; i++)
      random.nextInt(256).toRadixString(16).padLeft(2, '0'),
  ].join();
}

/// Whether [id] is an installation id in its persisted form.
bool isInstallationId(String id) => RegExp(r'^[0-9a-f]{32}$').hasMatch(id);

/// Which view of a manifest is active under a rollout.
enum RolloutSide {
  /// The manifest's top-level release.
  stable,

  /// The rollout's candidate release.
  candidate;

  /// The wire spelling.
  @override
  String toString() => name;
}

/// A staged rollout as `explain()` reports it (SPEC §6).
class RolloutInfo {
  /// Creates rollout info.
  const RolloutInfo({
    required this.id,
    required this.percent,
    required this.cohort,
    required this.side,
  });

  /// The rollout's id.
  final String id;

  /// The share of cohorts in the candidate, 0–100.
  final int percent;

  /// This installation's cohort, 0–9999.
  final int cohort;

  /// The view actually active. [RolloutSide.stable] with
  /// `cohort < percent × 100` is a candidate that failed to activate.
  final RolloutSide side;

  /// The SPEC §6 JSON shape.
  Map<String, Object?> toJson() => {
    'id': id,
    'percent': percent,
    'cohort': cohort,
    'side': side.toString(),
  };

  @override
  String toString() => jsonEncode(toJson());
}

/// A manifest's valid `rollout` member.
class Rollout {
  Rollout._(this.id, this.percent, this.salt, this._candidate);

  /// Reads [manifest]'s `rollout`: null when there is none, and a
  /// [SchemaException] when it doesn't match the schema — the runtime then
  /// ignores it (SPEC §1.4).
  ///
  /// The candidate's members must be present here; whether their contents
  /// are valid is decided by [candidateView], on the candidate side only,
  /// so a candidate that fails the schema falls back to the stable view of
  /// the same manifest.
  static Rollout? of(Manifest manifest) {
    final raw = manifest.rolloutJson;
    if (raw == null) return null;
    if (raw is! Map<String, Object?>) {
      throw SchemaException('rollout is not an object');
    }
    final id = raw['id'];
    final percent = raw['percent'];
    final salt = raw['salt'];
    final candidate = raw['candidate'];
    if (id is! String || id.isEmpty) {
      throw SchemaException('rollout: id is required');
    }
    if (percent is! int || percent < 0 || percent > 100) {
      throw SchemaException(
        'rollout $id: percent $percent is not an integer from 0 to 100',
      );
    }
    if (salt is! String || !_salt.hasMatch(salt)) {
      throw SchemaException('rollout $id: salt is not 22 base64url characters');
    }
    if (candidate is! Map<String, Object?>) {
      throw SchemaException('rollout $id: candidate is not an object');
    }
    for (final member in const [
      'release',
      'locales',
      'fallback',
      'artifacts',
    ]) {
      if (!candidate.containsKey(member)) {
        throw SchemaException('rollout $id: candidate has no $member');
      }
    }
    return Rollout._(id, percent, salt, candidate);
  }

  /// Names the rollout, for `explain()` and the audit log.
  final String id;

  /// The share of cohorts in the candidate, 0–100.
  final int percent;

  /// The salt cohorts are computed under, used as text.
  final String salt;

  final Map<String, Object?> _candidate;

  /// The candidate release's id as the manifest spells it, for errors.
  String? get candidateReleaseId {
    final release = _candidate['release'];
    return release is Map<String, Object?> && release['id'] is String
        ? release['id']! as String
        : null;
  }

  /// Whether [cohort] is in the candidate.
  bool includes(int cohort) => cohort < percent * 100;

  /// The candidate view of [stable] (SPEC §1.4): the same manifest with
  /// the candidate's release, locales, fallback and artifacts, and without
  /// `rollout`. Throws a [SchemaException] when the candidate's members
  /// don't match the schema.
  Manifest candidateView(Manifest stable) {
    try {
      return Manifest.view(stable, _candidate);
    } on SchemaException catch (e) {
      throw SchemaException('rollout $id: candidate: ${e.detail}');
    }
  }
}
