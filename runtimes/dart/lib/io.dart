/// The `dart:io` half of the runtime: a persisted last-good store, a
/// bundle reader and an HTTP transport.
///
/// It is a separate entry point on purpose. `package:glossa/glossa.dart`
/// compiles for the web, so it may not import `dart:io`; a VM, Flutter
/// mobile or Flutter desktop app imports this too and gets the pieces a
/// file system and a socket make possible. On the web, supply an
/// `IndexedDB`-backed [ReleaseStore] and a `fetch` [Transport] instead.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'src/loader.dart';
import 'src/store.dart';
import 'src/verify.dart';
import 'src/transport.dart';

/// The bundle and cache layout SPEC §3 fixes: the manifest, and artifacts
/// addressed by hash.
const String _manifestFile = 'manifest.json';
const String _artifactsDir = 'a';

/// A small sidecar holding the ETag of [_manifestFile] together with the
/// digest of the manifest it belongs to, so a torn write can never pair an
/// ETag with a manifest it didn't come from.
const String _etagFile = 'etag.json';

/// This installation's staged-rollout id (SPEC §1.4), kept beside the
/// release so it lives exactly as long as the cache does.
const String _installationIdFile = 'installation-id';

/// The persisted last-good release (SPEC §3, step 2) in a directory.
///
/// The directory *is* a valid bundle: `manifest.json` plus
/// `a/<sha256>.json`, exact bytes, the same layout `glossa pull --release`
/// writes and [readBundledRelease] reads. A release cached by the app can
/// therefore be shipped as a bundle, and vice versa, with no conversion.
///
/// Durability, in the order the writes happen:
///
/// 1. each artifact, through a temporary file and a rename, so a reader
///    never sees a partial file and a crash leaves no half-written one;
/// 2. `manifest.json`, the same way and **last**.
///
/// Artifacts are content-addressed, so an artifact written for a release
/// that never activated is inert: nothing names it until a manifest does.
/// `manifest.json` is the only thing that says "this release loaded and
/// verified completely", so a process killed at any point leaves the
/// previous manifest in place with all of its artifacts still present. A
/// half-downloaded release can never be activated on the next start.
class FileReleaseStore implements ReleaseStore, InstallationIdStore {
  /// Creates a store in [directory]. The directory is created on first
  /// write; it doesn't have to exist yet.
  FileReleaseStore(this.directory);

  /// Creates a store in a per-project, per-environment subdirectory of
  /// [parent] — an app's support directory, say — so two projects or two
  /// environments in one app never share a cache.
  factory FileReleaseStore.scoped(
    Directory parent, {
    required String deliveryKey,
    required String environment,
  }) {
    final scope = sha256Hex('$deliveryKey\n$environment');
    return FileReleaseStore(Directory('${parent.path}/glossa/$scope'));
  }

  /// Where the release lives.
  final Directory directory;

  File get _manifest => File('${directory.path}/$_manifestFile');
  File get _etag => File('${directory.path}/$_etagFile');
  File _artifactFile(String sha256) =>
      File('${directory.path}/$_artifactsDir/$sha256.json');

  File get _installationId => File('${directory.path}/$_installationIdFile');

  @override
  Future<String?> installationId() async {
    if (!_installationId.existsSync()) return null;
    final id = (await _installationId.readAsString()).trim();
    // Anything else was not written by this store: start a new id.
    return RegExp(r'^[0-9a-f]{32}$').hasMatch(id) ? id : null;
  }

  @override
  Future<void> putInstallationId(String id) =>
      _writeAtomic(_installationId, id);

  @override
  Future<String?> artifact(String sha256) async {
    if (!_isDigest(sha256)) return null;
    final file = _artifactFile(sha256);
    return file.exists().then((e) => e ? file.readAsString() : null);
  }

  @override
  Future<void> putArtifact(String sha256, String bytes) async {
    if (!_isDigest(sha256)) return;
    await _writeAtomic(_artifactFile(sha256), bytes);
  }

  @override
  Future<StoredManifest?> manifest() async {
    if (!_manifest.existsSync()) return null;
    final bytes = await _manifest.readAsString();
    return StoredManifest(bytes, await _readEtag(bytes));
  }

  @override
  Future<void> putManifest(String bytes, {String? etag}) async {
    // Last, and only after every artifact is on disk.
    await _writeAtomic(_manifest, bytes);
    if (etag == null) {
      if (_etag.existsSync()) await _etag.delete();
      return;
    }
    await _writeAtomic(
      _etag,
      jsonEncode({'etag': etag, 'manifest': sha256Hex(bytes)}),
    );
  }

  @override
  Future<void> prune(Set<String> keep) async {
    final dir = Directory('${directory.path}/$_artifactsDir');
    if (!dir.existsSync()) return;
    await for (final entry in dir.list()) {
      final name = entry.path.split(Platform.pathSeparator).last;
      final digest = name.endsWith('.json')
          ? name.substring(0, name.length - 5)
          : null;
      if (digest != null && _isDigest(digest) && !keep.contains(digest)) {
        // Best effort: a leftover only costs space.
        try {
          await entry.delete();
        } on FileSystemException {
          // Someone else is reading it; it will go on the next prune.
        }
      }
    }
  }

  /// The ETag, but only when it belongs to exactly these manifest bytes.
  Future<String?> _readEtag(String manifestBytes) async {
    if (!_etag.existsSync()) return null;
    try {
      final record = jsonDecode(await _etag.readAsString());
      if (record is Map<String, Object?> &&
          record['manifest'] == sha256Hex(manifestBytes) &&
          record['etag'] is String) {
        return record['etag']! as String;
      }
    } on Object {
      // Unreadable sidecar: revalidate with a plain GET instead.
    }
    return null;
  }

  static bool _isDigest(String s) =>
      s.length == 64 && RegExp(r'^[0-9a-f]{64}$').hasMatch(s);

  /// Writes through a flushed temporary file and a rename, so a reader
  /// sees either the old content or the new one, never a mixture.
  static Future<void> _writeAtomic(File file, String contents) async {
    await file.parent.create(recursive: true);
    final temp = File('${file.path}.${pid}_${_counter++}.tmp');
    try {
      await temp.writeAsString(contents, flush: true);
      await temp.rename(file.path);
    } on Object {
      if (temp.existsSync()) await temp.delete();
      rethrow;
    }
  }

  static int _counter = 0;
}

/// Reads the bundle at [directory] — `manifest.json` and `a/*.json` — into
/// a [BundledRelease]. Returns null when there is no manifest there.
Future<BundledRelease?> readBundledRelease(Directory directory) async {
  final manifest = File('${directory.path}/$_manifestFile');
  if (!manifest.existsSync()) return null;
  final artifacts = <String, String>{};
  final dir = Directory('${directory.path}/$_artifactsDir');
  if (dir.existsSync()) {
    await for (final entry in dir.list()) {
      final name = entry.path.split(Platform.pathSeparator).last;
      if (entry is File && name.endsWith('.json')) {
        artifacts[name.substring(0, name.length - 5)] = await entry
            .readAsString();
      }
    }
  }
  return BundledRelease(
    manifest: await manifest.readAsString(),
    artifacts: artifacts,
  );
}

/// A [Transport] over `dart:io`'s [HttpClient].
///
/// No credentials, ever: a delivery key is publishable and the artifacts
/// are public (SPEC §2), so nothing here attaches cookies or an
/// `Authorization` header.
Transport ioTransport({
  HttpClient? client,
  Duration timeout = const Duration(seconds: 10),
}) {
  final http = client ?? (HttpClient()..connectionTimeout = timeout);
  return (url, headers) async {
    final request = await http.getUrl(Uri.parse(url)).timeout(timeout);
    headers.forEach(request.headers.set);
    final response = await request.close().timeout(timeout);
    if (response.statusCode != HttpStatus.ok) {
      await response.drain<void>();
      return EdgeResponse(response.statusCode);
    }
    return EdgeResponse(
      response.statusCode,
      body: await response.transform(utf8.decoder).join(),
      etag: response.headers.value(HttpHeaders.etagHeader),
    );
  };
}
