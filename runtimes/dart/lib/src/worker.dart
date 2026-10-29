/// Running one pure function off the calling thread.
///
/// RFC 0005 §6.2 asks for verification and decode in an isolate, so a
/// Flutter app never drops a frame while a release loads. `Isolate.run` is
/// that, without importing Flutter for `compute`. The web has no isolates,
/// so there the same call runs inline — correct everywhere, off-thread
/// where the platform has threads.
library;

export 'worker_inline.dart'
    if (dart.library.io) 'worker_isolate.dart'
    show runOffThread;
