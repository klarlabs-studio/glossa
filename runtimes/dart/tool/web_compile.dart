/// A web entry point that exists to be compiled, not run.
///
/// RFC 0005 §6.4 asks for a compile test proving the package builds for
/// the web: no `dart:io`, no FFI and no reflection anywhere below
/// `package:glossa/glossa.dart`. Run it from `runtimes/dart`:
///
/// ```sh
/// dart compile js -o .dart_tool/web_compile.js tool/web_compile.dart
/// ```
///
/// A `dart:io` import that creeps into the core makes this fail, which is
/// the whole point. `package:glossa/io.dart` is deliberately not imported
/// here: that entry point is for the VM and for Flutter's mobile and
/// desktop targets.
library;

import 'package:glossa/glossa.dart';

void main() {
  final client = GlossaClient(
    locales: const ['en'],
    store: MemoryReleaseStore(),
    publicKeys: [GlossaPublicKey.parse('k', 'A' * 43)],
  );
  print(client.t('hello'));
  print(canonicalizeJsonValue(const {'a': 1}));
  print(sha256Hex('x'));
}
