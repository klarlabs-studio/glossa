/// The loader (`runtimes/SPEC.md` §3): where the text a runtime renders
/// comes from, and how a new release replaces the old one.
///
/// ## The order, exactly as SPEC §3 writes it
///
/// A [GlossaClient] resolves the content it renders from the first
/// available source:
///
/// 1. **Memory** — the release already loaded in this process.
/// 2. **Persisted last-good** — the most recent release that loaded and
///    verified completely ([ReleaseStore]).
/// 3. **Network** — the edge, revalidating the manifest with
///    `If-None-Match`.
/// 4. **Bundled** — artifacts shipped with the build, for offline-first
///    apps and cold starts.
/// 5. **Inline default** — the text at the call site, or the message id, so
///    a missing string is visible and never blank.
///
/// Artifacts are content-addressed, so the *bytes* of one artifact are
/// looked for in a different order — memory → persisted → bundled →
/// network — and the network is used only for a hash held nowhere else.
/// Both orders are SPEC §3; they answer different questions.
///
/// At startup, when a bundled release and a persisted one both exist, the
/// higher `release.version` wins: an app update may ship a newer catalog
/// than the one on disk. Bundled bytes are trusted like application code
/// and are neither re-hashed nor signature-checked; persisted ones are
/// verified again every time they are read.
///
/// ## Activation is atomic
///
/// A release becomes active only after its manifest has verified *and*
/// every artifact its active fallback chain needs has loaded and verified.
/// The switch itself is a single field assignment ([_active]), so a reader
/// on the same isolate sees either the whole old release or the whole new
/// one — never a half-updated mixture. Any failure along the way leaves
/// the previous release serving and goes to the error channel; nothing
/// throws into application code and nothing ever renders `""`.
library;

import 'dart:async';

import 'catalog.dart';
import 'decode.dart';
import 'errors.dart';
import 'locale.dart';
import 'manifest.dart';
import 'parts.dart';
import 'rollout.dart';
import 'store.dart';
import 'transport.dart';
import 'verify.dart';
import 'worker.dart';

/// A release shipped inside the build (`glossa pull --release`).
///
/// The layout is the one SPEC §3 fixes for every runtime and the CLI: the
/// manifest exactly as the edge serves it, and each artifact's exact bytes
/// by SHA-256 — i.e. the edge's URL space below `/v1/{deliveryKey}/`. A
/// Flutter app ships that directory as an asset bundle and reads it into
/// this class.
class BundledRelease {
  /// Creates a bundled release.
  const BundledRelease({required this.manifest, required this.artifacts});

  /// `manifest.json`, the exact bytes.
  final String manifest;

  /// `a/<sha256>.json` → the artifact's exact bytes.
  final Map<String, String> artifacts;
}

/// A Glossa runtime: it loads releases and renders messages.
///
/// Nothing here needs Flutter, and nothing here needs `dart:io`. Give it a
/// [Transport] and a [ReleaseStore] (`package:glossa/io.dart` has both for
/// the VM) and it works; give it neither and it renders inline defaults,
/// which is exactly what SPEC §3 asks of a cold start with no network.
class GlossaClient {
  /// Creates a runtime and starts its first load.
  ///
  /// [edge] and [deliveryKey] together enable network loading; without
  /// them nothing is fetched. [publicKeys], when given, make a manifest
  /// without a valid signature from one of them unusable (SPEC §1.3);
  /// without them signature verification is off and TLS is the only
  /// protection, which SPEC §1.3 permits but mobile OTA should not rely on.
  ///
  /// [rollout] turns staged rollout support (SPEC §1.4) on, the default,
  /// or off: off, a manifest's `rollout` is ignored, the stable release is
  /// served and no candidate artifact is ever fetched. [installationId]
  /// replaces this installation's cohort key; without it the runtime
  /// creates a random one the first time a rollout is read and keeps it in
  /// [store] when that is an [InstallationIdStore], in memory otherwise.
  GlossaClient({
    String? edge,
    String? deliveryKey,
    this.environment = 'production',
    List<String> locales = const [],
    this.bundled,
    List<GlossaPublicKey> publicKeys = const [],
    this.store,
    this.transport,
    Duration refreshInterval = const Duration(minutes: 5),
    Duration errorInterval = const Duration(seconds: 60),
    this._rollout = true,
    this._installationId,
  }) : _base = edge == null || deliveryKey == null
           ? null
           : '${edge.replaceAll(RegExp(r'/+$'), '')}/v1/$deliveryKey',
       _publicKeys = List.unmodifiable(publicKeys),
       _requested = canonicalizeLocales(locales),
       _errors = ErrorChannel(errorInterval) {
    ready = _run(() => _cycle(initial: true));
    if (_base != null && refreshInterval > Duration.zero) {
      _timer = Timer.periodic(refreshInterval, (_) => unawaited(refresh()));
    }
  }

  /// The environment this runtime reads. A manifest published for another
  /// one is rejected as a `schema` error (SPEC §3).
  final String environment;

  /// The release shipped inside the build, if any (SPEC §3, step 4).
  final BundledRelease? bundled;

  /// Where the last-good release persists, if anywhere (SPEC §3, step 2).
  final ReleaseStore? store;

  /// How the edge is reached, if it is (SPEC §3, step 3).
  final Transport? transport;

  final String? _base;
  final bool _rollout;
  final List<GlossaPublicKey> _publicKeys;

  /// The cohort key (SPEC §1.4): the option, the persisted id, or a new
  /// one — settled the first time a rollout needs it.
  String? _installationId;
  final ErrorChannel _errors;

  /// The active release. Assigning this field *is* the activation.
  Catalog? _active;
  Localizer? _localizer;
  List<String> _requested;
  String? _etag;

  /// Decoded artifacts by SHA-256, and their exact bytes so a release can
  /// be persisted without fetching anything twice.
  final Map<String, Artifact> _artifacts = {};
  final Map<String, String> _bytes = {};

  Future<void> _queue = Future<void>.value();
  Future<void>? _inflight;
  Timer? _timer;
  var _disposed = false;

  /// Settles when the first load is done. It never fails: a cold start
  /// without network or storage simply leaves nothing active.
  late final Future<void> ready;

  /// Load, verification, format and missing-message errors (SPEC §6).
  /// Repeats of the same error within the configured interval are dropped.
  Stream<GlossaError> get errors => _errors.stream;

  /// The active release, or null when nothing has loaded.
  ReleaseRef? get release => _active?.release;

  /// Where the active release came from (SPEC §6). [Source.inline] when
  /// there is none.
  Source get source => _active?.source ?? Source.inline;

  /// The active locale, or null when no release is active.
  String? get locale => _localizer?.locale;

  /// The active locale's base text direction, `ltr` until a release is.
  Direction get direction => _localizer?.direction ?? Direction.ltr;

  /// The locales the active release carries, e.g. for a locale picker.
  List<LocaleEntry> get availableLocales =>
      _active?.manifest.locales ?? const [];

  /// Render message [id]. See [Localizer.t].
  String t(
    String id, {
    Map<String, Object?> values = const {},
    String? defaultText,
    bool bidiIsolation = true,
  }) =>
      _localizer?.t(
        id,
        values: values,
        defaultText: defaultText,
        bidiIsolation: bidiIsolation,
      ) ??
      // SPEC §6: a cold start without any release reports `network` once,
      // not one `missing-message` per message.
      (defaultText ?? id);

  /// Render message [id] to parts. See [Localizer.parts].
  List<Part> parts(
    String id, {
    Map<String, Object?> values = const {},
    String? defaultText,
    bool bidiIsolation = true,
  }) =>
      _localizer?.parts(
        id,
        values: values,
        defaultText: defaultText,
        bidiIsolation: bidiIsolation,
      ) ??
      [TextPart(defaultText ?? id)];

  /// How [id] resolves, without side effects (SPEC §6).
  Explanation explain(String id, [List<String>? requested]) {
    final localizer = _localizer;
    if (localizer != null) return localizer.explain(id, requested);
    return Explanation(
      id: id,
      requested: requested == null
          ? _requested
          : canonicalizeLocales(requested),
      locale: null,
      chain: const [],
      resolvedFrom: null,
      release: null,
      source: Source.inline,
      steps: const [],
    );
  }

  /// Switch the requested locales. The switch is atomic too: the new chain
  /// becomes visible only once its artifacts have loaded and verified.
  Future<void> setLocales(List<String> locales) {
    _requested = canonicalizeLocales(locales);
    return _run(() async {
      final active = _active;
      if (active != null) {
        // Re-activate the release that is already serving, for the new
        // chain. Its own bytes are in memory, so this usually fetches
        // nothing; when it does and the fetch fails, the old chain stays.
        final stored = _bytes[_manifestKey];
        if (stored != null) {
          await _activate(stored, active.source, etag: _etag);
        } else {
          _localizer = active.forLocales(_requested);
        }
      }
    });
  }

  /// Revalidate the manifest now. Concurrent calls share one request.
  Future<void> refresh() {
    final inflight = _inflight;
    if (inflight != null) return inflight;
    final next = _run(() => _cycle(initial: false));
    _inflight = next;
    return next.whenComplete(() {
      if (identical(_inflight, next)) _inflight = null;
    });
  }

  /// Stop the background refresh and close the error channel.
  Future<void> dispose() async {
    _disposed = true;
    _timer?.cancel();
    _timer = null;
    await _errors.close();
  }

  // ── Loading ───────────────────────────────────────────────────────────

  /// Activations run one at a time, so the last one to commit is the last
  /// one asked for.
  Future<void> _run(Future<void> Function() task) {
    final next = _queue.then((_) => task()).catchError((Object _) {});
    _queue = next;
    return next;
  }

  /// One pass of the load order. [initial] is the startup pass, which is
  /// the only one that reads the bundle and the persisted store.
  Future<void> _cycle({required bool initial}) async {
    var activated = false;
    if (initial) {
      activated = await _startFromDisk();
    }
    if (_base != null && !_disposed) {
      final response = await _get(
        '$_base/$environment/manifest.json',
        _etag == null ? const {} : {'If-None-Match': _etag!},
      );
      final body = response?.body;
      if (body != null) {
        activated =
            await _activate(body, Source.network, etag: response!.etag) ||
            activated;
      }
    }
    // Nothing new this time: what is active was already in memory.
    if (_active != null && !initial && !activated) {
      _active!.source = Source.memory;
    }
  }

  /// Steps 2 and 4 of SPEC §3 at startup: the persisted last-good release
  /// and the bundled one, the higher `release.version` winning.
  Future<bool> _startFromDisk() async {
    StoredManifest? persisted;
    try {
      persisted = await store?.manifest();
    } on Object catch (e) {
      // Unreadable storage is "nothing persisted", never a failed start.
      _report(ErrorType.schema, 'persisted release unreadable: $e');
    }
    final ours = bundled == null ? null : await _versionOf(bundled!.manifest);
    final theirs = await _versionOf(persisted?.bytes);
    // An app update may ship a newer catalog than the one on disk.
    final bundledWins = ours != null && (theirs == null || ours > theirs);

    if (bundledWins && await _activate(bundled!.manifest, Source.bundled)) {
      return true;
    }
    if (persisted != null &&
        await _activate(
          persisted.bytes,
          Source.persisted,
          etag: persisted.etag,
        )) {
      return true;
    }
    // The bundle is also the last resort when the persisted release turns
    // out to be unusable.
    return !bundledWins &&
        bundled != null &&
        await _activate(bundled!.manifest, Source.bundled);
  }

  /// The version of the view [manifestBytes] would activate (SPEC §1.4):
  /// the candidate's for an installation in it, the stable one otherwise.
  Future<int?> _versionOf(String? manifestBytes) async {
    if (manifestBytes == null) return null;
    try {
      final manifest = Manifest.decode(manifestBytes);
      final rollout = _rollout ? Rollout.of(manifest) : null;
      if (rollout != null &&
          rollout.includes(cohortOf(rollout.salt, await _installation()))) {
        return rollout.candidateView(manifest).release.version;
      }
      return manifest.release.version;
    } on SchemaException {
      // An invalid rollout or candidate: activation falls back to stable.
      try {
        return Manifest.decode(manifestBytes).release.version;
      } on SchemaException {
        return null;
      }
    }
  }

  /// This installation's cohort key (SPEC §1.4): the `installationId`
  /// option, or the id persisted beside the last-good release, or a new
  /// random one, persisted there when the store can keep it.
  Future<String> _installation() async {
    final known = _installationId;
    if (known != null) return known;
    final keeper = store is InstallationIdStore
        ? store! as InstallationIdStore
        : null;
    String? id;
    try {
      id = await keeper?.installationId();
    } on Object {
      id = null; // Unreadable: a new id, as for a first start.
    }
    // Another activation may have settled it while this one awaited.
    if (_installationId != null) return _installationId!;
    if (id == null || !isInstallationId(id)) {
      id = newInstallationId();
      try {
        await keeper?.putInstallationId(id);
      } on Object {
        // Not persisted: this process keeps it, the next draws a new one.
      }
    }
    return _installationId ??= id;
  }

  /// Verify a manifest, load everything its active chain needs, then swap
  /// it in. Returns whether it became the active release.
  Future<bool> _activate(
    String manifestBytes,
    Source source, {
    String? etag,
  }) async {
    final Manifest manifest;
    try {
      manifest = Manifest.decode(manifestBytes);
    } on SchemaException catch (e) {
      _report(ErrorType.schema, e.detail);
      return false;
    }
    if (manifest.environment != environment) {
      _report(
        ErrorType.schema,
        'manifest is for environment ${manifest.environment}, '
        'not $environment',
        releaseId: manifest.release.id,
      );
      return false;
    }
    // SPEC §3: bundled manifests are trusted like application code.
    if (source != Source.bundled) {
      final problem = verifyManifestSignature(
        manifestBytes,
        manifest,
        _publicKeys,
      );
      if (problem != null) {
        _report(ErrorType.signature, problem, releaseId: manifest.release.id);
        return false;
      }
    }

    // SPEC §1.4: the signature covers `rollout`, so it is read only now.
    Rollout? rollout;
    if (_rollout) {
      try {
        rollout = Rollout.of(manifest);
      } on SchemaException catch (e) {
        // Invalid: ignored, and the stable view is activated.
        _report(ErrorType.schema, e.detail, releaseId: manifest.release.id);
      }
    }
    if (rollout == null) {
      return _take(manifest, manifest, manifestBytes, source, etag, null);
    }
    final cohort = cohortOf(rollout.salt, await _installation());
    RolloutInfo info(RolloutSide side) => RolloutInfo(
      id: rollout!.id,
      percent: rollout.percent,
      cohort: cohort,
      side: side,
    );
    if (rollout.includes(cohort)) {
      Manifest? candidate;
      try {
        candidate = rollout.candidateView(manifest);
      } on SchemaException catch (e) {
        _report(
          ErrorType.schema,
          e.detail,
          releaseId: rollout.candidateReleaseId ?? manifest.release.id,
        );
      }
      if (candidate != null &&
          await _take(
            manifest,
            candidate,
            manifestBytes,
            source,
            etag,
            info(RolloutSide.candidate),
          )) {
        return true;
      }
    }
    // The stable side, or a candidate that could not be activated: the
    // stable view of the same manifest, never the release before it.
    return _take(
      manifest,
      manifest,
      manifestBytes,
      source,
      etag,
      info(RolloutSide.stable),
    );
  }

  /// Load everything [view]'s active chain needs, then swap it in. [view]
  /// is [manifest] itself or its rollout's candidate view; [manifestBytes]
  /// are [manifest]'s, as served, which is what persists.
  Future<bool> _take(
    Manifest manifest,
    Manifest view,
    String manifestBytes,
    Source source,
    String? etag,
    RolloutInfo? rollout,
  ) async {
    final active =
        lookupLocale(_requested, view.localeCodes) ?? view.sourceLocale;
    final needed = <String>{
      for (final locale in fallbackChain(active, view))
        ...?view.artifacts[locale]?.values,
    };
    final loaded = <String, Artifact>{};
    for (final sha in needed) {
      final artifact = await _artifactFor(sha, view.release.id);
      // A half-loaded release is never activated: the previous one keeps
      // serving until every artifact of the chain is in hand.
      if (artifact == null) return false;
      loaded[sha] = artifact;
    }

    _commit(view, manifestBytes, loaded, source, etag, rollout);
    if (source != Source.bundled) {
      await _persist(view, manifestBytes, etag);
    }
    return true;
  }

  /// The single assignment that makes a release visible.
  void _commit(
    Manifest manifest,
    String manifestBytes,
    Map<String, Artifact> loaded,
    Source source,
    String? etag,
    RolloutInfo? rollout,
  ) {
    final catalog = Catalog.fromArtifacts(
      manifest: manifest,
      artifacts: {..._artifacts, ...loaded},
      source: source,
      errors: _errors,
      rollout: rollout,
    );
    final localizer = catalog.forLocales(_requested);
    // Everything above is built first and off to the side; these two
    // assignments are the swap, and nothing awaits between them.
    _active = catalog;
    _localizer = localizer;
    _etag = etag;
    _bytes[_manifestKey] = manifestBytes;

    // Drop artifacts no release references any more.
    final keep = {
      for (final namespaces in manifest.artifacts.values) ...namespaces.values,
    };
    _artifacts.removeWhere((sha, _) => !keep.contains(sha));
    _bytes.removeWhere((sha, _) => sha != _manifestKey && !keep.contains(sha));
  }

  /// One artifact's bytes, content-addressed, so any source holding the
  /// hash will do: memory → persisted → bundled → network (SPEC §3).
  Future<Artifact?> _artifactFor(String sha256, String releaseId) async {
    final cached = _artifacts[sha256];
    if (cached != null) return cached;

    String? stored;
    try {
      stored = await store?.artifact(sha256);
    } on Object {
      stored = null; // Unreadable cache entry: fall through.
    }
    if (stored != null) {
      final artifact = await _accept(sha256, stored, releaseId, verify: true);
      if (artifact != null) return artifact;
    }

    final fromBundle = bundled?.artifacts[sha256];
    if (fromBundle != null) {
      final artifact = await _accept(
        sha256,
        fromBundle,
        releaseId,
        verify: false,
      );
      if (artifact != null) return artifact;
    }

    if (_base == null) return null;
    final response = await _get('$_base/a/$sha256.json', const {}, releaseId);
    final body = response?.body;
    if (body == null) return null;
    return _accept(sha256, body, releaseId, verify: true);
  }

  /// Hash, parse and model one artifact off the calling thread.
  Future<Artifact?> _accept(
    String sha256,
    String bytes,
    String releaseId, {
    required bool verify,
  }) async {
    final decoded = await runOffThread(decodeArtifact, (
      sha256: sha256,
      bytes: bytes,
      verifyHash: verify,
    ));
    final rejection = decoded.rejection;
    if (rejection != null) {
      _report(
        rejection == ArtifactRejection.integrity
            ? ErrorType.integrity
            : ErrorType.schema,
        decoded.detail,
        releaseId: releaseId,
      );
      return null;
    }
    final artifact = decoded.artifact!;
    // SPEC §3: one message that can't be read is a `schema` error and
    // resolves as missing; it doesn't block the release.
    for (final bad in artifact.unreadable.entries) {
      _report(
        ErrorType.schema,
        bad.value,
        messageId: bad.key,
        locale: artifact.locale,
        releaseId: releaseId,
      );
    }
    _artifacts[sha256] = artifact;
    _bytes[sha256] = bytes;
    return artifact;
  }

  /// Write the release to the store, **manifest last** (`store.dart`):
  /// the artifacts of the active view [manifest], and the manifest's bytes
  /// as served, `rollout` included, so a restart decides the side again.
  Future<void> _persist(
    Manifest manifest,
    String manifestBytes,
    String? etag,
  ) async {
    final target = store;
    if (target == null) return;
    final keep = <String>{};
    try {
      for (final namespaces in manifest.artifacts.values) {
        for (final sha in namespaces.values) {
          keep.add(sha);
          final bytes = _bytes[sha];
          if (bytes != null) await target.putArtifact(sha, bytes);
        }
      }
      await target.putManifest(manifestBytes, etag: etag);
      await target.prune(keep);
    } on Object catch (e) {
      // A full disk must not unload a release that is already serving.
      _report(
        ErrorType.schema,
        'persisting release ${manifest.release.id} failed: $e',
        releaseId: manifest.release.id,
      );
    }
  }

  Future<EdgeResponse?> _get(
    String url,
    Map<String, String> headers, [
    String? releaseId,
  ]) async {
    final send = transport;
    if (send == null) {
      _report(
        ErrorType.network,
        '$url: no transport configured',
        releaseId: releaseId,
      );
      return null;
    }
    try {
      final response = await send(url, headers);
      if (response.status == 304) return response;
      if (response.status == 200 && response.body != null) return response;
      _report(
        ErrorType.network,
        '$url: HTTP ${response.status}',
        releaseId: releaseId,
      );
    } on Object catch (e) {
      _report(ErrorType.network, '$url: $e', releaseId: releaseId);
    }
    return null;
  }

  void _report(
    ErrorType type,
    String detail, {
    String? messageId,
    String? locale,
    String? releaseId,
  }) {
    _errors.report(
      GlossaError(
        type,
        detail,
        messageId: messageId,
        locale: locale,
        releaseId: releaseId,
      ),
    );
  }

  /// `_bytes` is keyed by SHA-256; this key can never collide with one.
  static const String _manifestKey = 'manifest';
}
