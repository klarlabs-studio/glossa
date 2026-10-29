/// [runOffThread] where the platform has no isolates — the web.
///
/// The work still happens off the event loop's current turn, so an `await`
/// still yields; it simply has nowhere else to go. See `worker.dart`.
library;

/// Runs [task] on [message] and returns its result.
Future<R> runOffThread<M, R>(R Function(M) task, M message) async =>
    task(message);
