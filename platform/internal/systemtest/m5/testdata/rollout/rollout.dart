// The Dart side of RFC 0006 §12.4: one GlossaClient per installation
// id, each loading through a transport that serves the edge's real
// answers (fetched once, then from memory), and the release each one
// activated.
//
//   dart --packages=<runtimes/dart/.dart_tool/package_config.json> \
//     rollout.dart <input.json> <output.json>
//
// input:  { edgeURL, deliveryKey, environment, ids: [...], rollout: bool,
//           manifest?: "<manifest JSON served instead of the edge's>" }
// output: { "<installation id>": "<active release id>" | null, ... }
//
// The installation id and the switch that turns rollout support off are
// GlossaClient's `installationId` and `rollout` options (SPEC §1.4,
// RFC 0006 wave 3). No store is given, so the runtime persists no id of
// its own; the one given is used as is.
import 'dart:convert';
import 'dart:io';

import 'package:glossa/glossa.dart';

Future<void> main(List<String> args) async {
  final input = jsonDecode(File(args[0]).readAsStringSync()) as Map<String, dynamic>;
  final ids = (input['ids'] as List).cast<String>();
  final probe = input['manifest'] as String?;
  final http = HttpClient();
  final cache = <String, EdgeResponse>{};

  Future<EdgeResponse> transport(String url, Map<String, String> headers) async {
    if (probe != null && url.endsWith('/manifest.json')) {
      return EdgeResponse(200, body: probe, etag: '"m5-probe"');
    }
    final hit = cache[url];
    if (hit != null) return hit;
    final req = await http.getUrl(Uri.parse(url));
    final res = await req.close();
    final body = await res.transform(utf8.decoder).join();
    final r = EdgeResponse(res.statusCode, body: body, etag: res.headers.value('etag'));
    cache[url] = r;
    return r;
  }

  final out = <String, String?>{};
  for (final id in ids) {
    final client = GlossaClient(
      edge: input['edgeURL'] as String,
      deliveryKey: input['deliveryKey'] as String,
      environment: input['environment'] as String,
      transport: transport,
      refreshInterval: Duration.zero,
      installationId: id,
      rollout: input['rollout'] as bool,
    );
    await client.ready;
    out[id] = client.release?.id;
    await client.dispose();
  }
  http.close();
  File(args[1]).writeAsStringSync(jsonEncode(out));
}
