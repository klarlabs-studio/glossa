/// [runOffThread] on the platforms that have isolates: the VM, AOT, and
/// therefore Flutter on mobile and desktop. See `worker.dart`.
library;

import 'dart:isolate';

/// Runs [task] on [message] in a short-lived isolate and returns its
/// result, so hashing and decoding a catalog never block the UI thread.
///
/// [task] must be a top-level or static function, and [message] and the
/// result must be sendable — which is why the work is shaped as one pure
/// function over plain data (`decode.dart`).
Future<R> runOffThread<M, R>(R Function(M) task, M message) =>
    Isolate.run(() => task(message));
