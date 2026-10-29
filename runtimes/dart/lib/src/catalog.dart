/// Message resolution over a loaded release (`runtimes/SPEC.md` §4.3, §6):
/// the fallback chain, `explain()` and the error channel.
///
/// Wave 1 has no loader. A [Catalog] is built from a manifest and the
/// artifact bytes the caller already holds; wave 2 adds the SPEC §3 load
/// order that produces those bytes.
library;

import 'dart:async';

import 'errors.dart';
import 'format.dart';
import 'locale.dart';
import 'manifest.dart';
import 'model.dart';
import 'parts.dart';
import 'rollout.dart';

/// Where the active release was loaded from (SPEC §6).
enum Source {
  /// A later refresh brought nothing new.
  memory,

  /// Restored from the persisted last-good store.
  persisted,

  /// Loaded from the edge.
  network,

  /// Restored from artifacts shipped with the build.
  bundled,

  /// No loaded locale had the message: the inline default, or the id.
  inline;

  /// The wire spelling.
  @override
  String toString() => name;
}

/// What one fallback step found.
enum Outcome {
  /// The locale's artifact has the message.
  found,

  /// The locale's artifact is loaded and doesn't have the message.
  missing,

  /// The locale is in the chain but its artifacts weren't loaded.
  notLoaded;

  /// The wire spelling.
  @override
  String toString() => this == Outcome.notLoaded ? 'not-loaded' : name;
}

/// One locale of the fallback chain and what resolution found there.
class Step {
  /// Creates a step.
  const Step(this.locale, this.outcome);

  /// The locale.
  final String locale;

  /// What was found.
  final Outcome outcome;

  /// The SPEC §6 JSON shape.
  Map<String, Object?> toJson() => {
    'locale': locale,
    'outcome': outcome.toString(),
  };

  @override
  String toString() => '$locale:$outcome';
}

/// Why a message rendered the way it did (SPEC §6). Computing one has no
/// side effects: nothing is loaded and no error is reported.
class Explanation {
  /// Creates an explanation.
  const Explanation({
    required this.id,
    required this.requested,
    required this.locale,
    required this.chain,
    required this.resolvedFrom,
    required this.release,
    required this.source,
    required this.steps,
    this.rollout,
  });

  /// The message id.
  final String id;

  /// The canonicalized requested locales, in priority order.
  final List<String> requested;

  /// The active locale negotiated from [requested], or null when no
  /// release is active at all and there was nothing to negotiate against.
  final String? locale;

  /// The active locale's fallback chain.
  final List<String> chain;

  /// The locale the message was found in, or null when the inline default
  /// (or the id) was used.
  final String? resolvedFrom;

  /// The active release, or null when nothing is loaded.
  final ReleaseRef? release;

  /// Where the active release came from.
  final Source source;

  /// The chain walk, up to and including the locale that answered.
  final List<Step> steps;

  /// The active manifest's staged rollout (SPEC §1.4), or null without a
  /// valid one or with rollout support off.
  final RolloutInfo? rollout;

  /// The SPEC §6 JSON document, verbatim.
  Map<String, Object?> toJson() => {
    'id': id,
    'requested': requested,
    'locale': locale,
    'chain': chain,
    'resolvedFrom': resolvedFrom,
    'release': release == null
        ? null
        : {'id': release!.id, 'version': release!.version},
    'source': source.toString(),
    'steps': [for (final s in steps) s.toJson()],
    'rollout': rollout?.toJson(),
  };
}

/// A loaded release: its manifest and the messages of every locale it
/// carries, merged across namespaces (SPEC §1.1).
class Catalog {
  Catalog._(
    this.manifest,
    this._messages,
    this._loaded,
    this.source,
    this._errors,
    this._ownsErrors, [
    this.rollout,
  ]);

  /// The release's manifest.
  final Manifest manifest;

  /// Where this release was loaded from.
  ///
  /// The loader lowers it to [Source.memory] when a later refresh brings
  /// nothing new, which is what SPEC §6 asks `explain().source` to report.
  Source source;

  /// The staged rollout this release was activated under (SPEC §1.4), or
  /// null. The catalog holds the view the loader activated, so its
  /// [manifest] is the candidate view when [rollout]'s side is candidate.
  final RolloutInfo? rollout;

  final Map<String, Map<String, Message>> _messages;
  final Set<String> _loaded;
  final ErrorChannel _errors;
  final bool _ownsErrors;

  /// The active release.
  ReleaseRef get release => manifest.release;

  /// Load, verification and format errors (SPEC §6). Repeats of the same
  /// error within the channel's interval are dropped.
  Stream<GlossaError> get errors => _errors.stream;

  /// Build a catalog from a manifest and the artifact bytes it names, keyed
  /// by the SHA-256 the manifest uses.
  ///
  /// Integrity and signature verification belong to the loader (wave 2);
  /// this constructor trusts the bytes it is handed, exactly as SPEC §3
  /// says bundled artifacts are trusted. An artifact that is missing or
  /// can't be read is a `schema` error and leaves its locale empty rather
  /// than failing the release.
  factory Catalog.fromRelease({
    required Manifest manifest,
    required Map<String, String> artifacts,
    Source source = Source.bundled,
    Duration errorInterval = const Duration(seconds: 60),
  }) {
    final channel = ErrorChannel(errorInterval);
    final messages = <String, Map<String, Message>>{};
    final loaded = <String>{};

    for (final entry in manifest.artifacts.entries) {
      final locale = entry.key;
      final merged = <String, Message>{};
      var any = false;
      for (final namespace in entry.value.entries) {
        final bytes = artifacts[namespace.value];
        if (bytes == null) {
          channel.report(
            GlossaError(
              ErrorType.network,
              'artifact ${namespace.value} for $locale/${namespace.key} '
              'was not supplied',
              locale: locale,
              releaseId: manifest.release.id,
            ),
          );
          continue;
        }
        try {
          final artifact = Artifact.decode(bytes);
          merged.addAll(artifact.messages);
          for (final bad in artifact.unreadable.entries) {
            channel.report(
              GlossaError(
                ErrorType.schema,
                bad.value,
                messageId: bad.key,
                locale: locale,
                releaseId: manifest.release.id,
              ),
            );
          }
          any = true;
        } on SchemaException catch (e) {
          channel.report(
            GlossaError(
              ErrorType.schema,
              e.detail,
              locale: locale,
              releaseId: manifest.release.id,
            ),
          );
        }
      }
      messages[locale] = merged;
      if (any) loaded.add(locale);
    }

    return Catalog._(manifest, messages, loaded, source, channel, true);
  }

  /// Build a catalog from artifacts that have already been verified and
  /// decoded — the loader's entry point (`loader.dart`).
  ///
  /// [artifacts] is keyed by SHA-256 and holds only what the active
  /// fallback chain needed (SPEC §3). A locale of the manifest with no
  /// artifact here is *not* an error: it is simply not loaded, and
  /// `explain()` reports its step as `not-loaded` (SPEC §6).
  ///
  /// Nothing is reported on [errors] from here. The loader has already
  /// reported whatever went wrong while it fetched and verified, and
  /// re-reporting on every activation would defeat the repeat suppression.
  /// When [errors] is given it belongs to the caller and [dispose] leaves
  /// it open, so one channel spans every release of a runtime's life.
  factory Catalog.fromArtifacts({
    required Manifest manifest,
    required Map<String, Artifact> artifacts,
    required Source source,
    ErrorChannel? errors,
    RolloutInfo? rollout,
  }) {
    final messages = <String, Map<String, Message>>{};
    final loaded = <String>{};
    for (final entry in manifest.artifacts.entries) {
      final merged = <String, Message>{};
      var any = false;
      for (final sha in entry.value.values) {
        final artifact = artifacts[sha];
        if (artifact == null) continue;
        merged.addAll(artifact.messages);
        any = true;
      }
      messages[entry.key] = merged;
      if (any) loaded.add(entry.key);
    }
    return Catalog._(
      manifest,
      messages,
      loaded,
      source,
      errors ?? ErrorChannel(),
      errors == null,
      rollout,
    );
  }

  /// A localizer for [requested], canonicalized and negotiated by RFC 4647
  /// lookup (SPEC §4.1).
  Localizer forLocales(List<String> requested) {
    final canonical = canonicalizeLocales(requested);
    final active =
        lookupLocale(canonical, manifest.localeCodes) ?? manifest.sourceLocale;
    return Localizer._(this, canonical, active);
  }

  /// Release the error channel, unless it was supplied by the caller.
  Future<void> dispose() async {
    if (_ownsErrors) await _errors.close();
  }
}

/// A catalog bound to one set of requested locales.
class Localizer {
  Localizer._(this._catalog, this.requested, this.locale)
    : chain = fallbackChain(locale, _catalog.manifest);

  final Catalog _catalog;

  /// The canonicalized requested locales, in priority order.
  final List<String> requested;

  /// The active locale (SPEC §4.1).
  final String locale;

  /// The active locale's fallback chain (SPEC §4.2).
  final List<String> chain;

  /// The active locale's base text direction: the manifest's, or the one
  /// its script implies.
  Direction get direction =>
      Direction.parse(_catalog.manifest.directionOfLocale(locale)) ??
      directionOf(locale);

  /// Render message [id].
  ///
  /// Walks the chain and formats with the locale the message was found in
  /// (SPEC §4.3). When nothing in the chain has it, renders [defaultText]
  /// or — failing that — the id, never the empty string, and reports
  /// `missing-message`.
  String t(
    String id, {
    Map<String, Object?> values = const {},
    String? defaultText,
    bool bidiIsolation = true,
  }) => partsToString(
    parts(
      id,
      values: values,
      defaultText: defaultText,
      bidiIsolation: bidiIsolation,
    ),
  );

  /// Render message [id] to parts, for a renderer that needs the markup.
  List<Part> parts(
    String id, {
    Map<String, Object?> values = const {},
    String? defaultText,
    bool bidiIsolation = true,
  }) {
    final hit = _find(id);
    if (hit == null) {
      _catalog._errors.report(
        GlossaError(
          ErrorType.missingMessage,
          'no locale in the chain has the message',
          messageId: id,
          locale: locale,
          releaseId: _catalog.release.id,
        ),
      );
      return [TextPart(defaultText ?? id)];
    }
    final rendered = formatToParts(
      hit.message,
      hit.locale,
      values: values,
      options: FormatOptions(
        bidiIsolation: bidiIsolation,
        onError: (e) => _catalog._errors.report(
          GlossaError(
            ErrorType.format,
            '${e.type} at ${e.source}',
            messageId: id,
            locale: hit.locale,
            releaseId: _catalog.release.id,
          ),
        ),
      ),
    );
    // SPEC §3: a result that is the empty string falls through to the
    // inline default or the id.
    if (partsToString(rendered).isEmpty) {
      return [TextPart(defaultText ?? id)];
    }
    return rendered;
  }

  /// Explain how [id] resolves, without side effects (SPEC §6).
  ///
  /// With [locales], explain those requested locales instead of this
  /// localizer's, without switching.
  Explanation explain(String id, [List<String>? locales]) {
    final target = locales == null ? this : _catalog.forLocales(locales);
    final hit = target._find(id);
    final steps = <Step>[];
    for (final l in target.chain) {
      if (hit != null && l == hit.locale) {
        steps.add(Step(l, Outcome.found));
        break;
      }
      steps.add(
        Step(
          l,
          _catalog._messages.containsKey(l) && !_catalog._loaded.contains(l)
              ? Outcome.notLoaded
              : Outcome.missing,
        ),
      );
    }
    return Explanation(
      id: id,
      requested: target.requested,
      locale: target.locale,
      chain: target.chain,
      resolvedFrom: hit?.locale,
      release: _catalog.release,
      source: hit == null ? Source.inline : _catalog.source,
      steps: steps,
      rollout: _catalog.rollout,
    );
  }

  ({String locale, Message message})? _find(String id) {
    for (final l in chain) {
      final message = _catalog._messages[l]?[id];
      if (message != null) return (locale: l, message: message);
    }
    return null;
  }
}
