/// [GlossaText]: resolution through the scope, and MessageFormat 2 markup
/// as styles rather than as escaped text (SPEC §5, RFC 0005 §6.2).
library;

import 'package:flutter/gestures.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:glossa_flutter/glossa_flutter.dart';

import 'release.dart';

/// One flattened run of the rendered span tree.
typedef Run = ({String text, TextStyle? style});

/// The rendered runs of the only [Text] on screen, in order.
List<Run> runs(WidgetTester tester) {
  final out = <Run>[];
  void walk(InlineSpan span, TextStyle? inherited) {
    if (span is! TextSpan) return;
    final style = span.style == null
        ? inherited
        : (inherited ?? const TextStyle()).merge(span.style);
    if (span.text != null && span.text!.isNotEmpty) {
      out.add((text: span.text!, style: style));
    }
    for (final child in span.children ?? const <InlineSpan>[]) {
      walk(child, style);
    }
  }

  walk(tester.widget<Text>(find.byType(Text)).textSpan!, null);
  return out;
}

/// The rendered text, markup flattened away.
String rendered(WidgetTester tester) => runs(tester).map((r) => r.text).join();

/// A client over the test release, with no edge and no store, so nothing
/// ever reaches a network.
///
/// It is built inside [WidgetTester.runAsync] because `testWidgets` runs
/// its body against a fake clock, and the loader's first pass is real
/// asynchrony: without it the `await` would never return.
Future<GlossaClient> offlineClient(
  WidgetTester tester, {
  List<String> locales = const ['de-AT'],
}) async {
  late GlossaClient client;
  await tester.runAsync(() async {
    client = GlossaClient(locales: locales, bundled: bundledRelease());
    await client.ready;
  });
  return client;
}

Future<void> pump(
  WidgetTester tester,
  GlossaClient client,
  Widget child, {
  void Function(GlossaError)? onError,
}) => tester.pumpWidget(
  Directionality(
    textDirection: TextDirection.ltr,
    child: GlossaScope(client: client, onError: onError, child: child),
  ),
);

void main() {
  testWidgets('renders a message by id, through the chain', (tester) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(tester, client, const GlossaText('cart.checkout'));

    expect(rendered(tester), 'Zur Kasse');
  });

  testWidgets('the first step of the chain answers when it can', (
    tester,
  ) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(
      tester,
      client,
      const Column(
        children: [GlossaText('cart.regional'), GlossaText('cart.checkout')],
      ),
    );

    final texts = tester
        .widgetList<Text>(find.byType(Text))
        .map((t) => t.textSpan!.toPlainText());
    expect(texts, ['Servus', 'Zur Kasse']);
  });

  testWidgets('safe markup becomes a style, not escaped text', (tester) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(tester, client, const GlossaText('cart.hint'));

    expect(rendered(tester), 'Tippe hier jetzt.');
    final styled = runs(tester);
    expect(styled.map((r) => r.text), ['Tippe ', 'hier ', 'jetzt', '.']);
    expect(styled[0].style?.fontWeight, isNull);
    expect(styled[1].style?.fontWeight, FontWeight.bold);
    // Nesting composes: bold from {#b}, italic from {#em} inside it.
    expect(styled[2].style?.fontWeight, FontWeight.bold);
    expect(styled[2].style?.fontStyle, FontStyle.italic);
    expect(styled[3].style?.fontWeight, isNull);
  });

  testWidgets('a void tag is a line break', (tester) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(tester, client, const GlossaText('cart.lines'));

    expect(rendered(tester), 'eins\nzwei');
  });

  testWidgets('unsafe markup keeps only its content', (tester) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(tester, client, const GlossaText('legal.accept'));

    expect(
      rendered(tester),
      'Mit dem Fortfahren akzeptierst du die AGB.',
      reason: 'the translation\'s {#link href=…} adds no element',
    );
    expect(runs(tester).every((r) => r.style?.decoration == null), isTrue);
  });

  testWidgets('a builder renders markup the application owns', (tester) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    var target = '';
    final recognizer = TapGestureRecognizer()
      ..onTap = () => target = '/agb-the-app-chose';
    addTearDown(recognizer.dispose);

    await pump(
      tester,
      client,
      GlossaText(
        'legal.accept',
        builders: {
          'link': (context, children) => TextSpan(
            children: children,
            style: const TextStyle(decoration: TextDecoration.underline),
            recognizer: recognizer,
          ),
        },
      ),
    );

    final underlined = runs(tester)
        .where((r) => r.style?.decoration == TextDecoration.underline);
    expect(underlined.map((r) => r.text), ['AGB']);
    // The href the translation wrote never reached the builder: the
    // application supplied the target, and the tap proves which one wins.
    recognizer.onTap!();
    expect(target, '/agb-the-app-chose');
  });

  testWidgets('values are formatted and bidi-isolated', (tester) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(
      tester,
      client,
      const GlossaText('cart.greeting', values: {'name': 'Lina'}),
    );

    expect(rendered(tester), 'Hallo \u2068Lina\u2069!');
  });

  testWidgets('a missing message renders its inline default, never blank', (
    tester,
  ) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(
      tester,
      client,
      const Column(
        children: [
          GlossaText('cart.nothing', defaultText: 'Nichts'),
          GlossaText('cart.nowhere'),
        ],
      ),
    );

    final texts = tester
        .widgetList<Text>(find.byType(Text))
        .map((t) => t.textSpan!.toPlainText());
    expect(texts, ['Nichts', 'cart.nowhere']);
  });

  testWidgets('tag styles are overridable, per scope and per call', (
    tester,
  ) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await tester.pumpWidget(
      Directionality(
        textDirection: TextDirection.ltr,
        child: GlossaScope(
          client: client,
          tagStyles: const GlossaTagStyles.standard().merge({
            'b': const TextStyle(fontWeight: FontWeight.w900),
          }),
          child: const GlossaText('cart.hint'),
        ),
      ),
    );

    expect(
      runs(tester).firstWhere((r) => r.text == 'hier ').style?.fontWeight,
      FontWeight.w900,
    );
  });

  testWidgets('without a scope, the wiring mistake is loud', (tester) async {
    await tester.pumpWidget(
      const Directionality(
        textDirection: TextDirection.ltr,
        child: GlossaText('cart.checkout'),
      ),
    );

    expect(tester.takeException(), isFlutterError);
  });
}
