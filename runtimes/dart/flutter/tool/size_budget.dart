/// The size half of the RFC 0005 §6.4 budgets, measured and enforced.
///
/// §6.4 names its method: *the package's contribution to a release build,
/// measured with `flutter build --analyze-size` against a fixture app
/// with and without it.* That is a **delta**, and the delta is what this
/// gates — the section exists to stop a hidden megabyte, so a gate that
/// reads a number with most of the megabyte left out would work against
/// it however honestly the rest is printed.
///
/// So this builds a throwaway Flutter app three times from one pubspec:
///
/// * `plain.dart` — bare Flutter, one hard-coded string;
/// * `host.dart` — the same app with the things a Glossa client leans on
///   that an app of this kind already has: `package:intl` formatting
///   numbers, currency, percentages, dates and plurals, an `HttpClient`
///   call, and a file read and write;
/// * `glossa.dart` — a realistic client: an edge, a delivery key, a
///   persisted store, an HTTP transport, signing keys, a bundled release,
///   `GlossaScope`, `GlossaText`, `explain()` and the error channel.
///
/// **The gated number is `glossa` minus `host`**, per architecture: what
/// adopting Glossa costs an app that was already making network calls and
/// already formatting numbers and dates. `glossa` minus `plain` is
/// printed beside it and is much larger — that is what an app with no
/// networking and no `intl` would pay, and it is the number intent §33 is
/// really about.
///
/// The realism is not decoration. Dart's AOT tree shaker is a global
/// fixpoint: a client with no transport, no bundle and an empty in-memory
/// store can *never* activate a release, so the compiler proves the whole
/// catalog, formatter and `package:intl` unreachable and drops them. Such
/// a fixture measures 17 kB and means nothing. The two baselines keep
/// that honest: neither may show a single byte of ours, and the `glossa`
/// build must show a lot.
///
/// Run it from `runtimes/dart/flutter`:
///
/// ```sh
/// dart run tool/size_budget.dart              # host desktop target
/// dart run tool/size_budget.dart --platform linux --report size.txt
/// ```
library;

import 'dart:convert';
import 'dart:io';

/// §6.4's budget for adopting Glossa, in bytes, per architecture.
///
/// 150 kB was the original figure. Nothing meets it by the method §6.4
/// names — the delta over a baseline with `intl` and an HTTP client is
/// about 343 kB, and over a bare Flutter app about 1049 kB — so wave 4
/// replaced it with 400 kB, set above today's number with room and not at
/// a comfortable distance. Raising it again is an RFC change, not a
/// constant change.
const int budgetBytes = 400 * 1000;

/// The packages the delta is attributed to when reporting.
const List<String> ourPackages = ['package:glossa', 'package:glossa_flutter'];

Future<void> main(List<String> args) async {
  final platform = _argument(args, '--platform') ?? _hostPlatform();
  final reportPath = _argument(args, '--report');

  final report = StringBuffer();
  void say(String line) {
    stdout.writeln(line);
    report.writeln(line);
  }

  final package = Directory.current;
  if (!File('${package.path}/lib/glossa_flutter.dart').existsSync()) {
    _die('run this from runtimes/dart/flutter');
  }

  say('Glossa Flutter runtime — RFC 0005 §6.4 size budget');
  say('target: $platform');

  final workspace = Directory.systemTemp.createTempSync('glossa-size-');
  final int code;
  try {
    await _scaffold(workspace, package, platform, say);
    final plain = await _build(workspace, platform, 'plain', say);
    final host = await _build(workspace, platform, 'host', say);
    final withGlossa = await _build(workspace, platform, 'glossa', say);

    say('');
    say('— the fixture is honest ————————————————————————————————————');
    for (final baseline in {'plain': plain, 'host': host}.entries) {
      final leaked = _sum(baseline.value, ourPackages);
      say('  bytes of ours in ${baseline.key}, which does not use us: $leaked');
      if (leaked != 0) {
        _die(
          'the ${baseline.key} build carries $leaked bytes of Glossa; it '
          'should carry none, so every delta below would be wrong.',
        );
      }
    }
    final ours = _sum(withGlossa, ourPackages);
    say('  bytes of ours in glossa, which does: $ours');
    if (ours < 20000) {
      _die(
        'the Glossa build carries only $ours bytes of Glossa. The tree '
        'shaker has proved most of the runtime unreachable, which means '
        'the fixture app is not exercising it — not that the package is '
        'small. Fix lib/glossa.dart in the fixture, not the budget.',
      );
    }

    final intl = _sum(withGlossa, const ['package:intl']);
    final overHost = withGlossa.total - host.total;
    final overPlain = withGlossa.total - plain.total;
    final overPlainWithoutIntl = overPlain - intl;

    say('');
    say('— enforced: §6.4\'s own method, the delta ————————————————————');
    _line(say, 'adopting Glossa, over a realistic baseline', overHost);
    say('    budget ${_kb(budgetBytes)} (§6.4, set in wave 4)');
    say('    The baseline already has package:intl formatting numbers and');
    say('    dates and an HttpClient making a call — what most apps that');
    say('    would adopt Glossa already carry.');

    say('');
    say('— reported, because a hidden megabyte is a lie ————————————————');
    _line(say, 'over a bare Flutter app (no intl, no networking)', overPlain);
    _line(say, 'the same, excluding package:intl', overPlainWithoutIntl);
    _line(say, 'package:intl (its own CLDR data)', intl);
    _line(say, 'package:glossa + package:glossa_flutter, attributed', ours);
    say('    The first of these is what an app with neither would pay, and');
    say('    it is the number intent §33 is about. The gap between it and');
    say('    the gated one is the dart:io HTTP client a transport needs,');
    say('    package:intl, the dart:core BigInt arithmetic behind the');
    say('    pure-Dart Ed25519 verifier, and package:crypto — carried by');
    say('    the baseline above because an app of this kind already has');
    say('    them. Per architecture: one slice, whatever the bundle packs.');
    say('');
    say('  where the growth over a bare app went, by library:');
    for (final entry in _delta(plain, withGlossa)) {
      say('    ${_kb(entry.value).padLeft(10)}  ${entry.key}');
    }

    say('');
    if (overHost <= budgetBytes) {
      say(
        'OK — ${_kb(overHost)} of ${_kb(budgetBytes)}, the delta over a '
        'baseline that already has package:intl and an HTTP client. That '
        'verdict does not stand alone:',
      );
    } else {
      say(
        'FAIL — ${_kb(overHost)} of ${_kb(budgetBytes)}: over by '
        '${_kb(overHost - budgetBytes)}, on the delta over a baseline that '
        'already has package:intl and an HTTP client.',
      );
    }
    // 400 kB is a replacement, not the figure §6.4 was written with, and
    // the gated delta assumes a baseline. Whoever reads this log later
    // must not be able to take the line above as "Glossa costs 343 kB"
    // without meeting these two.
    say(
      '  What it assumes: an app that already makes HTTP calls and already '
      'formats numbers and dates. An app with neither pays '
      '${_kb(overPlain)} — ${_kb(overPlainWithoutIntl)} of it outside '
      'package:intl.',
    );
    say(
      '  What the budget is: 150 kB was §6.4\'s original figure and nothing '
      'meets it by this method. Wave 4 replaced it with '
      '${_kb(budgetBytes)} deliberately, after measuring. See RFC 0005 '
      '§6.4 and §15, question 6, which records the decision and its '
      'reasoning.',
    );
    if (reportPath != null) {
      File(reportPath).writeAsStringSync(report.toString());
    }
    code = overHost <= budgetBytes ? 0 : 1;
  } finally {
    // Three release builds' worth of intermediates. `exit` skips
    // `finally`, which is why the verdict travels out of the block.
    workspace.deleteSync(recursive: true);
  }
  exit(code);
}

// ── The fixture app ─────────────────────────────────────────────────────

Future<void> _scaffold(
  Directory workspace,
  Directory package,
  String platform,
  void Function(String) say,
) async {
  final app = '${workspace.path}/app';
  // The runner (Xcode project, CMake files, Gradle) is generated rather
  // than committed: it is platform-specific boilerplate that would rot,
  // and `flutter create` is the thing that keeps it current.
  await _run(
    'flutter',
    [
      'create',
      '--platforms=${platform == 'apk' ? 'android' : platform}',
      '--project-name=glossa_size_fixture',
      '--no-pub',
      app,
    ],
    workspace.path,
    say,
  );

  File('$app/pubspec.yaml').writeAsStringSync('''
name: glossa_size_fixture
description: The RFC 0005 §6.4 size fixture. Generated; never committed.
publish_to: none
version: 1.0.0

environment:
  sdk: ^3.13.0

dependencies:
  flutter:
    sdk: flutter
  glossa_flutter:
    path: ${package.path}
  # Direct, so host.dart can import it without going through us. It is
  # the same resolved version the runtime uses, because one pubspec
  # resolves once — the baseline and the Glossa build never differ in
  # which CLDR data they could reach.
  intl: any

flutter:
  uses-material-design: true
''');
  File('$app/lib/main.dart').deleteSync();
  File('$app/lib/plain.dart').writeAsStringSync(_plainApp);
  File('$app/lib/host.dart').writeAsStringSync(_hostApp);
  File('$app/lib/glossa.dart').writeAsStringSync(_glossaApp);
  await _run('flutter', ['pub', 'get'], app, say);
}

/// The app without us: one string, laid out the same way.
const String _plainApp = '''
import 'package:flutter/material.dart';

void main() => runApp(const App());

class App extends StatelessWidget {
  const App({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    home: Scaffold(
      body: Center(
        child: Text.rich(
          const TextSpan(text: 'Zur Kasse'),
          style: Theme.of(context).textTheme.bodyMedium,
        ),
      ),
    ),
  );
}
''';

/// The baseline the budget is measured against: the same app, plus the
/// things a Glossa client leans on that an app of this kind already has.
///
/// Every line here is chosen to reach a corner of the SDK or of
/// `package:intl` that the runtime also reaches — the CLDR number, date
/// and plural data its MF2 functions use, the `HttpClient` its transport
/// is, the file I/O its persisted store is. What this app pulls in, the
/// Glossa build is not charged for, because an app that was going to
/// adopt Glossa was already paying it. What it does *not* pull in — the
/// BigInt arithmetic behind Ed25519, `package:crypto`, the catalog and
/// the formatter — is ours, and is the gated number.
const String _hostApp = '''
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:intl/date_symbol_data_local.dart';
import 'package:intl/intl.dart';

/// The backend call, and the cache file beside it.
Future<String> fetch() async {
  final client = HttpClient();
  final request = await client.getUrl(Uri.parse('https://api.example.com/v1'));
  request.headers.set('If-None-Match', '"m1"');
  final response = await request.close();
  final body = await response.transform(utf8.decoder).join();
  final file = File('\${Directory.systemTemp.path}/cache.json');
  await file.writeAsString(body, flush: true);
  return file.readAsString();
}

/// The CLDR surface: the same corners package:glossa's MF2 functions use.
String render() {
  initializeDateFormatting();
  final decimal = NumberFormat.decimalPattern('de').format(1234.5);
  final currency = NumberFormat.currency(locale: 'de', name: 'EUR').format(9.99);
  final percent = NumberFormat.percentPattern('de').format(0.25);
  final date = DateFormat.yMMMd('de').add_Hms().format(DateTime.now());
  final plural = Intl.pluralLogic(
    3,
    locale: 'de',
    zero: 'zero',
    one: 'one',
    two: 'two',
    few: 'few',
    many: 'many',
    other: 'other',
  );
  return '\$decimal \$currency \$percent \$date \$plural';
}

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  unawaited(fetch().then(debugPrint).catchError((Object _) {}));
  runApp(const App());
}

class App extends StatelessWidget {
  const App({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    home: Scaffold(
      body: Center(
        child: Text.rich(
          TextSpan(text: render()),
          style: Theme.of(context).textTheme.bodyMedium,
        ),
      ),
    ),
  );
}
''';

/// The app with us, wired the way `flutter/README.md` says to wire it:
/// every source of the SPEC §3 load order, signatures on, the error
/// channel drained, `explain()` reachable. Anything left out here is
/// something the tree shaker will drop and the budget will not see.
const String _glossaApp = '''
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:glossa/io.dart';
import 'package:glossa_flutter/glossa_flutter.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final client = GlossaClient(
    edge: 'https://edge.example.com',
    deliveryKey: 'pk_live_fixture',
    locales: const ['de-AT'],
    publicKeys: [GlossaPublicKey.parse('k_2026a', 'A' * 43)],
    transport: ioTransport(),
    store: FileReleaseStore.scoped(
      Directory.systemTemp,
      deliveryKey: 'pk_live_fixture',
      environment: 'production',
    ),
    bundled: await loadBundledRelease(),
  );
  client.errors.listen((e) => debugPrint('\${e.type} \${e.detail}'));
  await client.ready;
  debugPrint(client.explain('cart.checkout').chain.join(','));
  runApp(GlossaScope(client: client, child: const App()));
}

class App extends StatelessWidget {
  const App({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    home: Scaffold(
      body: Center(
        child: GlossaText(
          'cart.checkout',
          values: const {'count': 3, 'amount': 9.99},
          style: Theme.of(context).textTheme.bodyMedium,
        ),
      ),
    ),
  );
}
''';

// ── Building and reading the size report ────────────────────────────────

/// One build's AOT snapshot, by library.
class _Snapshot {
  _Snapshot(this.byLibrary);

  /// Bytes attributed to each top-level library, e.g. `package:intl`.
  ///
  /// `flutter build --analyze-size` writes one architecture's snapshot
  /// profile, and these entries account for all of it — so [total] is the
  /// snapshot, not a sample of it.
  final Map<String, int> byLibrary;

  /// Every attributed byte.
  int get total => byLibrary.values.fold(0, (a, b) => a + b);
}

Future<_Snapshot> _build(
  Directory workspace,
  String platform,
  String entryPoint,
  void Function(String) say,
) async {
  final app = '${workspace.path}/app';
  final output = await _run(
    'flutter',
    [
      'build',
      platform,
      '--release',
      '--analyze-size',
      if (platform == 'apk') '--target-platform=android-arm64',
      '--target=lib/$entryPoint.dart',
    ],
    app,
    say,
  );

  // `flutter build --analyze-size` prints where it put the summary, and
  // the file name carries a counter, so the path is read rather than
  // guessed.
  final marker = RegExp(r'can be found at: (\S+\.json)').firstMatch(output);
  if (marker == null) {
    _die('flutter build --analyze-size printed no summary path:\n$output');
  }
  final summary =
      jsonDecode(File(marker[1]!).readAsStringSync()) as Map<String, Object?>;
  final aot = _findAot(summary);
  if (aot == null) _die('no "(Dart AOT)" node in ${marker[1]}');
  final children = (aot['children']! as List<Object?>)
      .cast<Map<String, Object?>>();
  final byLibrary = <String, int>{
    for (final child in children) child['n']! as String: _leafSum(child),
  };
  say(
    '  $entryPoint: ${_kb(byLibrary.values.fold(0, (a, b) => a + b))} of Dart code',
  );
  return _Snapshot(byLibrary);
}

/// The AOT snapshot node, whatever the platform calls its binary.
Map<String, Object?>? _findAot(Map<String, Object?> node) {
  final name = node['n'];
  if (name is String && name.contains('(Dart AOT)')) return node;
  for (final child in (node['children'] as List<Object?>? ?? const [])) {
    final found = _findAot(child! as Map<String, Object?>);
    if (found != null) return found;
  }
  return null;
}

/// The bytes below [node]. Only leaves carry a size; an inner node's own
/// `value`, where it has one, is the file on disk and would double-count.
int _leafSum(Map<String, Object?> node) {
  final children = node['children'] as List<Object?>?;
  if (children == null || children.isEmpty) {
    final value = node['value'];
    return value is int ? value : 0;
  }
  var total = 0;
  for (final child in children) {
    total += _leafSum(child! as Map<String, Object?>);
  }
  return total;
}

int _sum(_Snapshot snapshot, List<String> prefixes) {
  var total = 0;
  for (final entry in snapshot.byLibrary.entries) {
    for (final prefix in prefixes) {
      if (entry.key == prefix || entry.key.startsWith('$prefix/')) {
        total += entry.value;
        break;
      }
    }
  }
  return total;
}

/// Every library that grew or shrank by at least 1 kB, biggest first.
List<MapEntry<String, int>> _delta(_Snapshot before, _Snapshot after) {
  final keys = {...before.byLibrary.keys, ...after.byLibrary.keys};
  final deltas = [
    for (final key in keys)
      MapEntry(key, (after.byLibrary[key] ?? 0) - (before.byLibrary[key] ?? 0)),
  ]..sort((a, b) => b.value.compareTo(a.value));
  return [
    for (final d in deltas)
      if (d.value.abs() >= 1000) d,
  ];
}

// ── Plumbing ────────────────────────────────────────────────────────────

Future<String> _run(
  String executable,
  List<String> arguments,
  String workingDirectory,
  void Function(String) say,
) async {
  say('  \$ $executable ${arguments.join(' ')}');
  final result = await Process.run(
    executable,
    arguments,
    workingDirectory: workingDirectory,
  );
  final output = '${result.stdout}${result.stderr}';
  if (result.exitCode != 0) {
    _die(
      '$executable ${arguments.join(' ')} exited ${result.exitCode}\n$output',
    );
  }
  return output;
}

String? _argument(List<String> args, String name) {
  final index = args.indexOf(name);
  return index >= 0 && index + 1 < args.length ? args[index + 1] : null;
}

/// The desktop target this host can build without another SDK.
String _hostPlatform() {
  if (Platform.isMacOS) return 'macos';
  if (Platform.isLinux) return 'linux';
  if (Platform.isWindows) return 'windows';
  _die(
    'no default build target for ${Platform.operatingSystem}; '
    'pass --platform',
  );
}

void _line(void Function(String) say, String what, int bytes) =>
    say('  ${what.padRight(42)} ${_kb(bytes).padLeft(10)}');

String _kb(int bytes) => '${(bytes / 1000).toStringAsFixed(1)} kB';

Never _die(String reason) {
  stderr.writeln(reason);
  exit(2);
}
