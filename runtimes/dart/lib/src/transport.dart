/// The runtime's side of the delivery endpoints (`runtimes/SPEC.md` §2).
///
/// The core package makes no HTTP calls of its own: it asks for a
/// [Transport] and calls it. That keeps `lib/glossa.dart` free of
/// `dart:io`, lets a Flutter app reuse the client it already has, and lets
/// the conformance drivers put a fake edge behind the same seam the real
/// one uses. `package:glossa/io.dart` ships a `dart:io` transport for the
/// VM and Flutter's mobile and desktop targets.
///
/// Credentials are never sent. A delivery key is publishable by design
/// (SPEC §2) and the artifacts are public, so a transport must not attach
/// cookies or authorization headers.
library;

/// What the runtime reads from an edge response.
class EdgeResponse {
  /// Creates a response.
  const EdgeResponse(this.status, {this.body, this.etag});

  /// The HTTP status. Only `200` and `304` mean anything to the runtime;
  /// every other status is a `network` error and falls through (SPEC §3).
  final int status;

  /// The response body, for a `200`.
  final String? body;

  /// The strong `ETag`, for a `200`. It is echoed as `If-None-Match` on
  /// the next manifest request.
  final String? etag;
}

/// Fetches one URL. A thrown error is a network failure like any other
/// status: it is reported and the loader falls through to the next source.
typedef Transport = Future<EdgeResponse> Function(
  String url,
  Map<String, String> headers,
);
