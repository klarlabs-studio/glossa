/// Manifest and artifact decoding (`runtimes/SPEC.md` §1).
///
/// Unknown top-level fields are ignored; a different `schema` major version
/// is rejected.
library;

import 'dart:convert';

import 'model.dart';

/// The manifest schema this runtime reads.
const String manifestSchema = 'glossa.manifest/v1';

/// The artifact schema this runtime reads.
const String artifactSchema = 'glossa.artifact/v1';

/// Thrown when a manifest or artifact can't be read: malformed JSON, a
/// missing required field, or a `schema` major this runtime doesn't speak.
/// The catalog reports it as a `schema` error (SPEC §6).
class SchemaException implements Exception {
  /// Creates a schema exception.
  SchemaException(this.detail);

  /// What was wrong.
  final String detail;

  @override
  String toString() => 'SchemaException: $detail';
}

Never _bad(String detail) => throw SchemaException(detail);

/// The release a manifest names.
class ReleaseRef {
  /// Creates a release reference.
  const ReleaseRef(this.id, this.version, this.createdAt);

  /// The release id.
  final String id;

  /// The monotonic release version; the higher one wins at startup
  /// (SPEC §3).
  final int version;

  /// When the release was created, as the manifest spells it.
  final String? createdAt;
}

/// One locale a release carries.
class LocaleEntry {
  /// Creates a locale entry.
  const LocaleEntry(this.code, this.direction);

  /// The canonical BCP 47 code.
  final String code;

  /// The base text direction the platform records for it.
  final String? direction;
}

/// One signature over a manifest (SPEC §1.3).
class ManifestSignature {
  /// Creates a signature.
  const ManifestSignature(this.keyId, this.alg, this.sig);

  /// The id of the key that signed.
  final String keyId;

  /// The algorithm; only `Ed25519` is defined.
  final String alg;

  /// The signature, base64url without padding, over the RFC 8785 (JCS)
  /// form of the manifest with `signatures` removed.
  final String sig;
}

/// A release manifest (SPEC §1.1).
class Manifest {
  /// Creates a manifest.
  const Manifest({
    required this.project,
    required this.environment,
    required this.release,
    required this.sourceLocale,
    required this.locales,
    required this.fallback,
    required this.artifacts,
    this.signatures = const [],
    this.rolloutJson,
  });

  /// The project the release belongs to.
  final String project;

  /// The environment the manifest was published for.
  final String environment;

  /// The release currently served.
  final ReleaseRef release;

  /// The locale messages are authored in; the last step of every chain.
  final String sourceLocale;

  /// Every locale the release carries.
  final List<LocaleEntry> locales;

  /// The fallback graph, `"*"` holding the default chain.
  final Map<String, List<String>> fallback;

  /// `artifacts[locale][namespace]` → the SHA-256 of the artifact's bytes.
  final Map<String, Map<String, String>> artifacts;

  /// The signatures the release was published with, in manifest order.
  /// Empty when the manifest is unsigned.
  final List<ManifestSignature> signatures;

  /// The `rollout` member as served (SPEC §1.4), unread: `Rollout.of`
  /// reads it, so an invalid one is ignored without failing the manifest.
  /// Null when there is none.
  final Object? rolloutJson;

  /// The codes of [locales], in manifest order.
  List<String> get localeCodes => [for (final l in locales) l.code];

  /// The direction the manifest records for [code], or null.
  String? directionOfLocale(String code) {
    for (final l in locales) {
      if (l.code == code) return l.direction;
    }
    return null;
  }

  /// Reads a manifest from decoded JSON.
  factory Manifest.fromJson(Object? json) {
    final m = _object(json, 'manifest');
    _checkSchema(m['schema'], manifestSchema);
    return Manifest._release(
      m,
      project: _string(m['project'], 'project'),
      environment: _string(m['environment'], 'environment'),
      sourceLocale: _string(m['sourceLocale'], 'sourceLocale'),
      signatures: [
        // A malformed entry is skipped rather than failing the manifest:
        // it simply isn't a signature anyone can verify against.
        for (final s
            in m['signatures'] is List<Object?>
                ? m['signatures']! as List<Object?>
                : const <Object?>[])
          if (s is Map<String, Object?> &&
              s['keyId'] is String &&
              s['alg'] is String &&
              s['sig'] is String)
            ManifestSignature(
              s['keyId']! as String,
              s['alg']! as String,
              s['sig']! as String,
            ),
      ],
      rolloutJson: m['rollout'],
    );
  }

  /// [base] with the release-specific members — `release`, `locales`,
  /// `fallback` and `artifacts` — read from [members] instead, and without
  /// `rollout`: a rollout's candidate view (SPEC §1.4).
  factory Manifest.view(Manifest base, Map<String, Object?> members) =>
      Manifest._release(
        members,
        project: base.project,
        environment: base.environment,
        sourceLocale: base.sourceLocale,
        signatures: base.signatures,
      );

  /// Reads the members a release owns from [m]; the rest is given.
  factory Manifest._release(
    Map<String, Object?> m, {
    required String project,
    required String environment,
    required String sourceLocale,
    required List<ManifestSignature> signatures,
    Object? rolloutJson,
  }) {
    final release = _object(m['release'], 'release');
    final version = release['version'];
    return Manifest(
      project: project,
      environment: environment,
      release: ReleaseRef(
        _string(release['id'], 'release.id'),
        version is int ? version : _bad('release.version is not an integer'),
        release['createdAt'] is String ? release['createdAt']! as String : null,
      ),
      sourceLocale: sourceLocale,
      locales: [
        for (final l in _array(m['locales'], 'locales'))
          LocaleEntry(
            _string(_object(l, 'locale')['code'], 'locale.code'),
            switch (_object(l, 'locale')['direction']) {
              final String? d => d,
              _ => _bad('locale.direction is not a string'),
            },
          ),
      ],
      fallback: {
        for (final e in _object(
          m['fallback'] ?? const <String, Object?>{},
          'fallback',
        ).entries)
          e.key: [
            for (final t in _array(e.value, 'fallback.${e.key}'))
              _string(t, 'fallback.${e.key} entry'),
          ],
      },
      artifacts: {
        for (final e in _object(m['artifacts'], 'artifacts').entries)
          e.key: {
            for (final n in _object(e.value, 'artifacts.${e.key}').entries)
              n.key: _string(
                _object(n.value, 'artifacts.${e.key}.${n.key}')['sha256'],
                'artifacts.${e.key}.${n.key}.sha256',
              ),
          },
      },
      signatures: signatures,
      rolloutJson: rolloutJson,
    );
  }

  /// Reads a manifest from its exact bytes as the edge serves them.
  factory Manifest.decode(String bytes) =>
      Manifest.fromJson(_json(bytes, 'manifest'));
}

/// One namespace of one locale (SPEC §1.2).
class Artifact {
  /// Creates an artifact.
  const Artifact(this.locale, this.namespace, this.messages, this.unreadable);

  /// The locale the messages are in.
  final String locale;

  /// The namespace.
  final String namespace;

  /// The messages, keyed by id.
  final Map<String, Message> messages;

  /// Ids whose value is not a valid data-model message, with the reason.
  ///
  /// SPEC §3: one unreadable message is a `schema` error and resolves as
  /// missing; it doesn't block the release.
  final Map<String, String> unreadable;

  /// Reads an artifact from decoded JSON.
  factory Artifact.fromJson(Object? json) {
    final a = _object(json, 'artifact');
    _checkSchema(a['schema'], artifactSchema);
    final messages = <String, Message>{};
    final unreadable = <String, String>{};
    for (final e in _object(a['messages'], 'messages').entries) {
      try {
        messages[e.key] = Message.fromJson(e.value);
      } on MessageModelException catch (err) {
        unreadable[e.key] = err.detail;
      }
    }
    return Artifact(
      _string(a['locale'], 'locale'),
      _string(a['namespace'], 'namespace'),
      messages,
      unreadable,
    );
  }

  /// Reads an artifact from its exact bytes.
  factory Artifact.decode(String bytes) =>
      Artifact.fromJson(_json(bytes, 'artifact'));
}

Object? _json(String bytes, String what) {
  try {
    return jsonDecode(bytes);
  } on FormatException catch (e) {
    return _bad('$what is not JSON: ${e.message}');
  }
}

void _checkSchema(Object? value, String expected) {
  if (value is! String) _bad('schema is missing');
  // Only the major matters: additive minors stay readable (SPEC §8).
  final major = expected.split('/').last;
  if (!value.endsWith('/$major')) {
    _bad('unsupported schema $value, expected $expected');
  }
}

Map<String, Object?> _object(Object? v, String what) =>
    v is Map<String, Object?> ? v : _bad('$what is not an object');

List<Object?> _array(Object? v, String what) =>
    v is List<Object?> ? v : _bad('$what is not an array');

String _string(Object? v, String what) =>
    v is String ? v : _bad('$what is not a string');
