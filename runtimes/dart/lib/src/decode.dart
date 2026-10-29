/// The work a runtime does per artifact: hash it, parse it, build the
/// data model. It is the only part of loading that costs real CPU on a
/// large catalog, so it is written as one pure function of one sendable
/// argument, which is what lets [runOffThread] push it into an isolate
/// (RFC 0005 §6.2: "verification and decode run in an isolate … so the UI
/// never renders a half-loaded release").
///
/// Nothing here touches the runtime's state, reports an error or throws:
/// it returns a verdict and the caller decides.
library;

import 'manifest.dart';
import 'verify.dart';

/// Why an artifact was rejected.
enum ArtifactRejection {
  /// The bytes don't hash to the SHA-256 the manifest names (SPEC §1.3).
  integrity,

  /// The bytes aren't a readable `glossa.artifact/v1` document.
  schema,
}

/// What [decodeArtifact] found.
class ArtifactDecoding {
  /// A rejection, with the detail for the error channel.
  const ArtifactDecoding.rejected(this.rejection, this.detail)
    : artifact = null;

  /// A decoded artifact.
  const ArtifactDecoding.accepted(Artifact this.artifact)
    : rejection = null,
      detail = '';

  /// The artifact, when it was accepted.
  final Artifact? artifact;

  /// Why it was rejected, or null when it was accepted.
  final ArtifactRejection? rejection;

  /// The detail for the error channel.
  final String detail;
}

/// Verify [bytes] against [sha256] and decode them.
///
/// With [verifyHash] false the hash check is skipped, which is how bundled
/// artifacts are read: SPEC §3 trusts them like application code, because
/// they ship inside the build and never crossed a network.
ArtifactDecoding decodeArtifact(
  ({String sha256, String bytes, bool verifyHash}) input,
) {
  if (input.verifyHash && sha256Hex(input.bytes) != input.sha256) {
    return ArtifactDecoding.rejected(
      ArtifactRejection.integrity,
      'artifact ${input.sha256} does not match its hash',
    );
  }
  try {
    return ArtifactDecoding.accepted(Artifact.decode(input.bytes));
  } on SchemaException catch (e) {
    return ArtifactDecoding.rejected(ArtifactRejection.schema, e.detail);
  }
}
