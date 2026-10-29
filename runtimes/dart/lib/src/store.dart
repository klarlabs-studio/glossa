/// The persisted last-good release (`runtimes/SPEC.md` §3, step 2).
///
/// The store is deliberately dumb: content-addressed artifact bytes, plus
/// the exact manifest bytes of the last release that loaded and verified
/// **completely**. It holds no decoded model, so a release persisted by one
/// version of this package is readable by the next.
///
/// The write order is the whole contract, and it is the one
/// [`runtimes/go/store.go`](../../../go/store.go) established:
///
/// 1. every artifact the release needs, each written whole;
/// 2. the manifest, **last**.
///
/// Artifacts are addressed by the SHA-256 of their bytes, so writing one
/// early can never be wrong — at worst it is unreferenced. The manifest is
/// the only thing that says "this release is good", so a process killed
/// halfway leaves the *previous* manifest in place, pointing at artifacts
/// that are all still there. A half-downloaded release can therefore never
/// be activated on the next start: there is no manifest naming it.
///
/// Every implementation must also make a single write all-or-nothing, so a
/// reader never sees half a file. [FileReleaseStore] in
/// `package:glossa/io.dart` does it with a temporary file and a rename.
library;

/// The persisted manifest and the ETag it was served with.
class StoredManifest {
  /// Creates a stored manifest.
  const StoredManifest(this.bytes, this.etag);

  /// The manifest's exact bytes, as the edge served them.
  final String bytes;

  /// The ETag to revalidate with, or null when it isn't known to belong to
  /// [bytes].
  final String? etag;
}

/// Where a runtime keeps its last-good release between runs.
///
/// Every method may fail. The runtime treats any failure as "nothing
/// persisted" and carries on from the next source, so an implementation
/// may throw rather than swallow.
abstract class ReleaseStore {
  /// The bytes of the artifact with this hash, or null.
  Future<String?> artifact(String sha256);

  /// Persist verified artifact [bytes] under their hash.
  Future<void> putArtifact(String sha256, String bytes);

  /// The last-good manifest, or null when nothing has been persisted.
  Future<StoredManifest?> manifest();

  /// Commit [bytes] as the last-good manifest. Called **only** after every
  /// artifact of the release has been persisted.
  Future<void> putManifest(String bytes, {String? etag});

  /// Drop artifacts the last-good release no longer names. Best effort: a
  /// leftover only costs space.
  Future<void> prune(Set<String> keep);
}

/// A [ReleaseStore] that also keeps this installation's id (SPEC §1.4).
///
/// SPEC §1.4 asks for the installation id to persist in the same store as
/// the last-good release, so it survives exactly as long as the release
/// does. It is a separate interface so a [ReleaseStore] written before
/// staged rollout keeps compiling: a store that doesn't implement it gets
/// an id that lives as long as the runtime does.
///
/// Every method may fail; the runtime then uses an id in memory.
abstract interface class InstallationIdStore {
  /// The persisted installation id, or null when there is none.
  Future<String?> installationId();

  /// Persist [id], 32 lowercase hexadecimal digits.
  Future<void> putInstallationId(String id);
}

/// A [ReleaseStore] in memory. It survives nothing, which makes it the
/// right store for tests, for a server that has a cache directory of its
/// own, and for any host without durable storage.
class MemoryReleaseStore implements ReleaseStore, InstallationIdStore {
  /// Creates an empty store.
  MemoryReleaseStore();

  final Map<String, String> _artifacts = {};
  StoredManifest? _manifest;
  String? _installationId;

  @override
  Future<String?> installationId() async => _installationId;

  @override
  Future<void> putInstallationId(String id) async {
    _installationId = id;
  }

  @override
  Future<String?> artifact(String sha256) async => _artifacts[sha256];

  @override
  Future<void> putArtifact(String sha256, String bytes) async {
    _artifacts[sha256] = bytes;
  }

  @override
  Future<StoredManifest?> manifest() async => _manifest;

  @override
  Future<void> putManifest(String bytes, {String? etag}) async {
    _manifest = StoredManifest(bytes, etag);
  }

  @override
  Future<void> prune(Set<String> keep) async {
    _artifacts.removeWhere((sha, _) => !keep.contains(sha));
  }
}
