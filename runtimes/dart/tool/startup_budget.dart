/// The startup half of the RFC 0005 §6.4 budgets, measured.
///
/// §6.4 asks for three numbers:
///
/// * verifying a 200 kB manifest and its signature **≤ 30 ms**,
/// * the first `t()` after a warm persisted cache **≤ 5 ms**,
/// * no jank frame on activation.
///
/// All three are *device* budgets: the RFC names a mid-range Android
/// phone. This program is not one, and neither is a CI runner, which is
/// why it **records** its wall-clock numbers and does not fail on them.
/// What it does fail on are two machine-independent properties that the
/// wall-clock budgets rest on, and that a regression would break on every
/// machine at once:
///
/// 1. canonicalization stays **linear** in manifest size, so a ten-times
///    bigger manifest costs about ten times as much and not a hundred;
/// 2. the first `t()` after a warm cache does **no verification work** —
///    it costs a small fraction of a signature check, because the release
///    was verified when it was activated and not again per message.
///
/// A number that only a phone can answer is left to the M4 exit report
/// (§12.7), which runs on a device. Saying "30 ms, enforced" because a
/// laptop managed it would be the kind of claim §6.4 exists to prevent.
///
/// Run it from `runtimes/dart`, AOT-compiled, because AOT is what ships:
///
/// ```sh
/// dart compile exe tool/startup_budget.dart -o .dart_tool/startup_budget
/// .dart_tool/startup_budget
/// ```
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:glossa/glossa.dart';
import 'package:glossa/io.dart';

/// One frame at 60 Hz: the line between "activation" and "a dropped
/// frame", and the reason artifact decode runs off the isolate.
const double frameBudgetMs = 1000 / 60;

/// §6.4's device budgets, for the report. Nothing here fails on them.
const double verifyBudgetMs = 30;

/// §6.4's warm-start budget, likewise recorded and not enforced.
const double firstCallBudgetMs = 5;

/// Canonicalizing the big manifest may cost at most this much more *per
/// byte* than canonicalizing a tenth of it. Linear work lands at 1.0;
/// quadratic work in [canonicalizeJson] — a string concatenation in a
/// loop, say — lands near 10, and that is what this catches. The ceiling
/// sits well above 1.0 because a shared runner is noisy; it sits well
/// below 10 because that is the number worth catching.
const double jcsScalingCeiling = 3.0;

/// The first `t()` after a warm cache may cost at most this share of one
/// 200 kB verification. It renders one message from a catalog that is
/// already decoded and already verified; anything near 1.0 means the
/// runtime is verifying, parsing or decoding per call.
const double firstCallShareOfVerify = 0.25;

Future<void> main(List<String> args) async {
  final report = StringBuffer();
  void say(String line) {
    stdout.writeln(line);
    report.writeln(line);
  }

  say('Glossa Dart runtime — RFC 0005 §6.4 startup budgets');
  say('host: ${Platform.operatingSystem} ${Platform.version.split(' ').first}');
  say(
    'mode: ${bool.fromEnvironment('dart.vm.product') ? 'AOT (release)' : 'JIT — compile with `dart compile exe` for a number that means something'}',
  );
  say('');

  final fixture = _SignedFixture.load();
  final verify = _measureVerification(fixture, say);
  final warm = await _measureWarmStart(say);

  say('');
  say('— §6.4 device budgets, recorded ————————————————————————————');
  _line(
    say,
    'verify 200 kB manifest + signature',
    verify.big.cold,
    verifyBudgetMs,
  );
  _line(say, 'first t() after a warm cache', warm.firstCall, firstCallBudgetMs);
  _line(
    say,
    'longest event-loop stall on activation',
    warm.stall,
    frameBudgetMs,
  );
  say(
    '  (steady state after warm-up: verification '
    '${verify.big.median.toStringAsFixed(2)} ms, canonicalization '
    '${verify.jcsBig.median.toStringAsFixed(2)} ms of it.)',
  );
  say(
    '  These are wall clock on this machine. §6.4 names a mid-range '
    'Android device; neither a laptop nor a CI runner is one, so none of '
    'the three fails this program. The device numbers belong to the M4 '
    'exit report (§12).',
  );

  say('');
  say('— enforced, because they hold on every machine ——————————————');
  final failures = <String>[];
  final scaling = verify.jcsCostRatio;
  say(
    '  canonicalization cost per byte, ${verify.bigBytes} B over '
    '${verify.smallBytes} B  ×${scaling.toStringAsFixed(2)} '
    '(linear is ×1.00, ceiling ×${jcsScalingCeiling.toStringAsFixed(2)})',
  );
  if (scaling > jcsScalingCeiling) {
    failures.add(
      'canonicalizing the big manifest costs ${scaling.toStringAsFixed(2)}× '
      'as much per byte as the small one; canonicalizeJson has stopped '
      'being linear in the size of the manifest.',
    );
  }
  final share = warm.firstCall / verify.big.median;
  say(
    '  first t() ÷ one verification  ${(share * 100).toStringAsFixed(1)} % '
    '(ceiling ${(firstCallShareOfVerify * 100).toStringAsFixed(0)} %)',
  );
  if (share > firstCallShareOfVerify) {
    failures.add(
      'the first t() after a warm cache costs '
      '${(share * 100).toStringAsFixed(1)} % of a 200 kB verification. A '
      'render from a decoded, already verified catalog should be a small '
      'fraction of one: something is verifying, parsing or decoding per '
      'call.',
    );
  }

  say('');
  if (failures.isEmpty) {
    say('OK — both enforced properties hold.');
  } else {
    for (final failure in failures) {
      say('FAIL — $failure');
    }
  }

  final out = args.length == 2 && args[0] == '--report' ? args[1] : null;
  if (out != null) {
    File(out).writeAsStringSync(report.toString());
  }
  exit(failures.isEmpty ? 0 : 1);
}

void _line(void Function(String) say, String what, double ms, double budget) {
  final verdict = ms <= budget ? 'within' : 'OVER';
  say(
    '  ${what.padRight(38)} ${ms.toStringAsFixed(2).padLeft(8)} ms   '
    '($verdict the ${budget.toStringAsFixed(budget == frameBudgetMs ? 1 : 0)} ms budget)',
  );
}

// ── Verification ────────────────────────────────────────────────────────

class _Verification {
  _Verification({
    required this.big,
    required this.jcsBig,
    required this.jcsSmall,
    required this.bigBytes,
    required this.smallBytes,
  });

  /// Verifying the ~200 kB manifest.
  final _Timing big;

  /// Canonicalizing the big manifest, and a tenth of it.
  final _Timing jcsBig;
  final _Timing jcsSmall;

  /// The sizes the two canonicalizations ran over.
  final int bigBytes;
  final int smallBytes;

  /// Canonicalization cost per byte, big over small. Linear work is 1.0.
  double get jcsCostRatio =>
      (jcsBig.median / bigBytes) / (jcsSmall.median / smallBytes);
}

_Verification _measureVerification(
  _SignedFixture fixture,
  void Function(String) say,
) {
  // The control. The fixture's manifest carries a real signature from a
  // real key over its own bytes, so this must come back null. It proves
  // the public key parses, the signature's R decompresses to a curve
  // point and its s is a valid scalar — which is what makes the timing
  // below the timing of a *complete* verification and not of an early
  // bail-out.
  final control = verifyManifestSignature(
    fixture.bytes,
    Manifest.decode(fixture.bytes),
    fixture.keys,
  );
  if (control != null) {
    stderr.writeln(
      'the shared fixture runtimes/testdata/loading/signatures.json no '
      'longer verifies against its own public key: $control',
    );
    exit(2);
  }

  final big = _syntheticManifest(fixture, locales: 21, namespaces: 100);
  final small = _syntheticManifest(fixture, locales: 2, namespaces: 100);
  final bigManifest = Manifest.decode(big);

  // The synthetic manifest carries the fixture's signature, which does not
  // cover *these* bytes, so verification ends in a mismatch. It costs what
  // a match costs: canonicalization, SHA-512 and the same double scalar
  // multiplication run either way, and only the final 32-byte comparison
  // differs. Signing here would mean shipping a signer in a package whose
  // size is the other half of §6.4.
  final outcome = verifyManifestSignature(big, bigManifest, fixture.keys);
  if (outcome == null || !outcome.contains('invalid signature')) {
    stderr.writeln('expected a signature mismatch, got: $outcome');
    exit(2);
  }

  final bigBytes = utf8.encode(big).length;
  final smallBytes = utf8.encode(small).length;
  say('manifest under test: $bigBytes bytes (control: $smallBytes bytes)');
  say('the shared fixture verifies against its own key: yes');
  say('');

  // The two canonicalizations are timed together, round by round: the
  // gate below is the ratio between them, and a ratio is only as honest
  // as the weather both sides ran in.
  final jcs = _timeAll([
    () => canonicalizeJson(big, without: 'signatures'),
    () => canonicalizeJson(small, without: 'signatures'),
  ]);
  return _Verification(
    big: _time(() => verifyManifestSignature(big, bigManifest, fixture.keys)),
    jcsBig: jcs[0],
    jcsSmall: jcs[1],
    bigBytes: bigBytes,
    smallBytes: smallBytes,
  );
}

/// A manifest of [locales] × [namespaces] artifact entries, carrying the
/// fixture's signature block so the whole verification path runs.
String _syntheticManifest(
  _SignedFixture fixture, {
  required int locales,
  required int namespaces,
}) {
  final entries = <String, Object?>{};
  final list = <Object?>[];
  for (var l = 0; l < locales; l++) {
    final code = l == 0 ? 'en' : 'l$l';
    list.add({'code': code, 'direction': 'ltr'});
    entries[code] = {
      for (var n = 0; n < namespaces; n++)
        'ns$n': {'sha256': sha256Hex('$code/$n'), 'size': 148},
    };
  }
  return jsonEncode({
    'schema': 'glossa.manifest/v1',
    'project': 'prj_budget',
    'environment': 'production',
    'release': {
      'id': 'rel_budget',
      'version': 1,
      'createdAt': '2026-09-01T08:00:00Z',
    },
    'sourceLocale': 'en',
    'locales': list,
    'fallback': <String, Object?>{},
    'artifacts': entries,
    'signatures': fixture.signatures,
  });
}

// ── Warm start ──────────────────────────────────────────────────────────

class _WarmStart {
  _WarmStart(this.activation, this.firstCall, this.stall, this.messages);

  /// `await client.ready` from a populated persisted store, milliseconds.
  final double activation;

  /// The first `t()` after it.
  final double firstCall;

  /// The longest the event loop went unserved while that happened — a
  /// stall longer than one frame is a dropped frame in a Flutter app.
  final double stall;

  /// How many messages the catalog carries.
  final int messages;
}

Future<_WarmStart> _measureWarmStart(void Function(String) say) async {
  final dir = Directory.systemTemp.createTempSync('glossa-budget-');
  try {
    const messages = 500;
    final artifact = _artifact('de', messages);
    final sha = sha256Hex(artifact);
    final manifest = jsonEncode({
      'schema': 'glossa.manifest/v1',
      'project': 'prj_budget',
      'environment': 'production',
      'release': {
        'id': 'rel_warm',
        'version': 1,
        'createdAt': '2026-09-01T08:00:00Z',
      },
      'sourceLocale': 'de',
      'locales': [
        {'code': 'de', 'direction': 'ltr'},
      ],
      'fallback': <String, Object?>{},
      'artifacts': {
        'de': {
          'default': {'sha256': sha, 'size': utf8.encode(artifact).length},
        },
      },
    });

    // Persist it the way the runtime does: artifacts first, manifest last.
    final store = FileReleaseStore(dir);
    await store.putArtifact(sha, artifact);
    await store.putManifest(manifest, etag: '"warm"');

    // Verification is off here on purpose: it is measured on its own
    // above, against §6.4's own 200 kB shape, and folding it in would
    // hide which of the two numbers moved. SPEC §1.3 allows a runtime
    // with no configured keys; a mobile app should configure them, and
    // then its warm start costs this plus one verification.
    final watchdog = _Watchdog()..start();
    final clock = Stopwatch()..start();
    final client = GlossaClient(
      locales: const ['de'],
      store: FileReleaseStore(dir),
      refreshInterval: Duration.zero,
    );
    await client.ready;
    clock.stop();
    watchdog.stop();

    if (client.source != Source.persisted) {
      stderr.writeln(
        'the warm client did not load from the persisted store '
        '(source: ${client.source}); the measurement would be of nothing.',
      );
      exit(2);
    }

    final first = Stopwatch()..start();
    final rendered = client.t('m0', values: {'n': 3});
    first.stop();
    if (!rendered.startsWith('Nachricht')) {
      stderr.writeln('the warm client rendered "$rendered", not the catalog');
      exit(2);
    }
    await client.dispose();

    say(
      'warm cache: $messages messages, '
      '${utf8.encode(artifact).length} artifact bytes on disk',
    );
    say(
      'activation from the persisted store: '
      '${clock.elapsedMicroseconds / 1000} ms',
    );

    return _WarmStart(
      clock.elapsedMicroseconds / 1000,
      first.elapsedMicroseconds / 1000,
      watchdog.longestMs,
      messages,
    );
  } finally {
    dir.deleteSync(recursive: true);
  }
}

/// An artifact of [count] messages, one of them with a `:number`
/// placeholder so the first render exercises the formatter and not only a
/// map lookup.
String _artifact(String locale, int count) => jsonEncode({
  'schema': 'glossa.artifact/v1',
  'locale': locale,
  'namespace': 'default',
  'messages': {
    for (var i = 0; i < count; i++)
      'm$i': {
        'type': 'message',
        'declarations': <Object?>[],
        'pattern': [
          'Nachricht $i mit ',
          {
            'type': 'expression',
            'arg': {'type': 'variable', 'name': 'n'},
            'function': {'name': 'number'},
          },
          ' Posten',
        ],
      },
  },
});

/// Watches the event loop and remembers the longest it went unserved.
///
/// A timer that asks to run every millisecond and runs 40 ms late was
/// blocked for 40 ms — on a UI isolate that is two and a half dropped
/// frames. It is the cheapest honest answer to "no jank frame on
/// activation" that does not need a device.
class _Watchdog {
  late Timer _timer;
  late Stopwatch _since;
  int _longestMicros = 0;

  /// The longest gap seen, in milliseconds.
  double get longestMs => _longestMicros / 1000;

  /// Begin watching.
  void start() {
    _since = Stopwatch()..start();
    _timer = Timer.periodic(const Duration(milliseconds: 1), (_) {
      final gap = _since.elapsedMicroseconds;
      if (gap > _longestMicros) _longestMicros = gap;
      _since.reset();
    });
  }

  /// Stop watching.
  void stop() {
    final gap = _since.elapsedMicroseconds;
    if (gap > _longestMicros) _longestMicros = gap;
    _timer.cancel();
  }
}

// ── Plumbing ────────────────────────────────────────────────────────────

/// The signed manifest and public key of the shared loading fixtures.
class _SignedFixture {
  _SignedFixture(this.bytes, this.keys, this.signatures);

  /// The fixture manifest's bytes, signed by [keys].
  final String bytes;

  /// The configured public keys.
  final List<GlossaPublicKey> keys;

  /// The manifest's `signatures` array, as JSON.
  final List<Object?> signatures;

  /// Reads `runtimes/testdata/loading/signatures.json`, relative to the
  /// package root — the same rule `test/fixtures.dart` follows.
  factory _SignedFixture.load() {
    final file = File(
      '${Directory.current.parent.path}/testdata/loading/signatures.json',
    );
    if (!file.existsSync()) {
      stderr.writeln(
        'shared fixture missing: ${file.path}\n'
        'Run this from runtimes/dart inside a checkout of the repository.',
      );
      exit(2);
    }
    final fixture = jsonDecode(file.readAsStringSync()) as Map<String, Object?>;
    final keys = [
      for (final key in fixture['publicKeys']! as List<Object?>)
        GlossaPublicKey.parse(
          (key as Map<String, Object?>)['keyId']! as String,
          key['key']! as String,
        ),
    ];
    final step = (fixture['steps']! as List<Object?>).first;
    final edge =
        (step as Map<String, Object?>)['edge']! as Map<String, Object?>;
    final body =
        (edge['manifest']! as Map<String, Object?>)['body']!
            as Map<String, Object?>;
    return _SignedFixture(
      jsonEncode(body),
      keys,
      body['signatures']! as List<Object?>,
    );
  }
}

/// A first run and a steady-state one, both in milliseconds.
class _Timing {
  _Timing(this.cold, this.median);

  /// The very first run — code paged in, nothing warm. A startup budget
  /// is about this one.
  final double cold;

  /// The median of the runs after it, which is what a regression moves.
  final double median;
}

/// Times [work] once cold, then takes the median of nine more runs.
///
/// The median, not the minimum: a startup budget is about what a user
/// waits for, and the minimum is the number a benchmark reports when it
/// wants to look good.
_Timing _time(void Function() work) => _timeAll([work]).single;

/// Times each of [work] once cold, then runs them **round by round**,
/// taking the median of nine rounds each.
///
/// The interleaving is what makes a ratio of two of these numbers worth
/// gating on. A machine that slows down for half a second — another job
/// on the runner, a thermal step, a garbage collection — slows down
/// whatever is running at that moment; measuring one workload to
/// completion and then the other lets that land entirely on one of them.
/// Alternating puts the same weather over both.
List<_Timing> _timeAll(List<void Function()> work) {
  final cold = <double>[];
  for (final one in work) {
    final clock = Stopwatch()..start();
    one();
    clock.stop();
    cold.add(clock.elapsedMicroseconds / 1000);
  }
  final samples = [for (final _ in work) <int>[]];
  for (var round = 0; round < 9; round++) {
    for (var i = 0; i < work.length; i++) {
      final clock = Stopwatch()..start();
      work[i]();
      clock.stop();
      samples[i].add(clock.elapsedMicroseconds);
    }
  }
  return [
    for (var i = 0; i < work.length; i++)
      _Timing(cold[i], (samples[i]..sort())[samples[i].length ~/ 2] / 1000),
  ];
}
