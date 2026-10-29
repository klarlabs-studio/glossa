/// The core stays pure Dart (RFC 0005 §6.1, §6.4).
///
/// Everything else in this package rests on it: `dart pub get`,
/// `dart analyze` and `dart test` work without the Flutter SDK, and
/// `dart compile js` (`tool/web_compile.dart`) builds the package for the
/// web. A single `import 'package:flutter/…'` or an unguarded `dart:io`
/// below `lib/` breaks all of that, and a lint can't see it — so it is
/// asserted here, where a mistake fails loudly and cheaply.
///
/// The Flutter widgets live in their own package, `flutter/`
/// (`glossa_flutter`), which is the only place `package:flutter` appears.
library;

import 'dart:io';

import 'package:test/test.dart';

/// Every Dart file below `lib/`, by its path relative to the package.
Map<String, String> _libraryFiles() {
  final lib = Directory('lib');
  if (!lib.existsSync()) {
    throw StateError('run the tests from runtimes/dart');
  }
  return {
    for (final file in lib.listSync(recursive: true).whereType<File>())
      if (file.path.endsWith('.dart')) file.path: file.readAsStringSync(),
  };
}

/// The `import`/`export` URIs of one library, ignoring comments — the doc
/// comments talk about `dart:io` and Flutter on purpose.
Iterable<String> _directiveUris(String source) sync* {
  final directive = RegExp(
    '''^\\s*(?:import|export)\\s+['"]([^'"]+)['"]''',
    multiLine: true,
  );
  for (final match in directive.allMatches(source)) {
    yield match[1]!;
  }
  // A conditional import names its other library in an `if (…)` clause.
  final conditional = RegExp('''\\bif\\s*\\([^)]*\\)\\s*['"]([^'"]+)['"]''');
  for (final match in conditional.allMatches(source)) {
    yield match[1]!;
  }
}

void main() {
  final files = _libraryFiles();

  test('lib/ has files to check', () => expect(files, isNotEmpty));

  test('nothing below lib/ imports Flutter', () {
    for (final entry in files.entries) {
      for (final uri in _directiveUris(entry.value)) {
        expect(
          uri.startsWith('package:flutter'),
          isFalse,
          reason:
              '${entry.key} imports $uri. The core must stay pure Dart: '
              'the widgets belong in flutter/ (glossa_flutter).',
        );
      }
    }
  });

  test('only package:glossa/io.dart reaches for dart:io', () {
    for (final entry in files.entries) {
      if (entry.key == 'lib/io.dart') continue;
      for (final uri in _directiveUris(entry.value)) {
        expect(
          uri,
          isNot('dart:io'),
          reason:
              '${entry.key} imports dart:io, so the package no longer '
              'compiles for the web. Put it behind package:glossa/io.dart, '
              'or behind a conditional import like src/worker.dart.',
        );
      }
    }
  });

  // RFC 0005 §6.4, the AOT-and-web half of the budgets. `dart:mirrors`
  // has no AOT implementation at all and `dart:ffi` has none on the web,
  // so either one turns "the package compiles everywhere" into "the
  // package compiles where you happened to try it". The compile tests in
  // CI (`dart compile js`, `dart compile exe`) would catch it too, but
  // only after a build; this names the file and the import.
  test('the core uses no reflection and no FFI', () {
    const banned = {'dart:mirrors', 'dart:ffi', 'package:reflectable'};
    for (final entry in files.entries) {
      for (final uri in _directiveUris(entry.value)) {
        for (final ban in banned) {
          expect(
            uri == ban || uri.startsWith('$ban/'),
            isFalse,
            reason:
                '${entry.key} imports $uri. The runtime must compile to '
                'AOT and to JavaScript: no reflection, no FFI. Message '
                'dispatch goes through the data model, never a mirror.',
          );
        }
      }
    }
  });
}
