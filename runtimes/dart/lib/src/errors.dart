/// The runtime error channel (`runtimes/SPEC.md` §6).
///
/// Errors never throw into application code. Repeats of the same error
/// within the channel's interval are dropped: two errors are the same when
/// all five fields are equal.
///
/// No telemetry is sent anywhere. The channel is a local [Stream] the
/// application subscribes to if it wants to (intent §17).
library;

import 'dart:async';

/// The six error types SPEC §6 defines.
enum ErrorType {
  /// A request to the edge failed.
  network,

  /// An artifact's bytes don't match the manifest's SHA-256.
  integrity,

  /// The manifest carries no valid signature from a configured key.
  signature,

  /// A manifest, artifact or message could not be read.
  schema,

  /// A placeholder could not be formatted; it rendered its MF2 fallback.
  format,

  /// No locale in the fallback chain had the message.
  missingMessage;

  /// The wire spelling.
  @override
  String toString() =>
      this == ErrorType.missingMessage ? 'missing-message' : name;
}

/// One error on the channel.
class GlossaError {
  /// Creates an error.
  const GlossaError(
    this.type,
    this.detail, {
    this.messageId,
    this.locale,
    this.releaseId,
  });

  /// Which kind of error this is.
  final ErrorType type;

  /// A human-readable explanation.
  final String detail;

  /// The message it concerns, when it concerns one.
  final String? messageId;

  /// The locale it concerns, when it concerns one.
  final String? locale;

  /// The release it concerns, when it concerns one.
  final String? releaseId;

  /// The SPEC §6 JSON shape.
  Map<String, Object?> toJson() => {
    'type': type.toString(),
    'detail': detail,
    if (messageId != null) 'messageId': messageId,
    if (locale != null) 'locale': locale,
    if (releaseId != null) 'releaseId': releaseId,
  };

  /// The identity used for repeat suppression: all five fields.
  String get _key =>
      '$type\u0000$detail\u0000$messageId\u0000$locale'
      '\u0000$releaseId';

  @override
  String toString() => 'GlossaError($type: $detail)';
}

/// A broadcast channel that drops repeats.
class ErrorChannel {
  /// Creates a channel suppressing repeats within [interval]. A zero or
  /// negative interval suppresses nothing, which is what the conformance
  /// drivers want: fixture cases repeat message ids on purpose.
  ErrorChannel([this.interval = const Duration(seconds: 60)]);

  /// How long a repeat of the same error is dropped for.
  final Duration interval;

  final StreamController<GlossaError> _controller =
      StreamController<GlossaError>.broadcast(sync: true);
  final Map<String, DateTime> _seen = {};

  /// How many distinct errors are tracked before expired entries are swept.
  static const int _maxTracked = 256;

  /// The errors, as they happen.
  Stream<GlossaError> get stream => _controller.stream;

  /// Report [error], unless it repeats one seen within [interval].
  void report(GlossaError error) {
    if (interval > Duration.zero) {
      final now = DateTime.now();
      final last = _seen[error._key];
      if (last != null && now.difference(last) < interval) return;
      // An error's identity includes its message id and locale, so a
      // long-lived app can see unboundedly many of them. Entries older than
      // the interval can never suppress anything again; drop them once the
      // map has grown, so the channel doesn't retain them for the process's
      // lifetime.
      if (_seen.length >= _maxTracked) {
        _seen.removeWhere((_, at) => now.difference(at) >= interval);
      }
      _seen[error._key] = now;
    }
    if (!_controller.isClosed) _controller.add(error);
  }

  /// Close the channel.
  Future<void> close() => _controller.close();
}
