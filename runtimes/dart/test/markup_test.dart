/// A driver over `runtimes/testdata/markup.json`: the safe-tag contract
/// every runtime shares (`runtimes/SPEC.md` §5).
///
/// The fixture states the rules as parts → HTML, so that is what is
/// asserted, exactly as `runtimes/go/markup_test.go` and
/// `@klarlabs-studio/glossa/elements` assert them. The tree those rules produce is what
/// `package:glossa_flutter` renders, so one fixture covers both.
library;

import 'package:glossa/glossa.dart';
import 'package:test/test.dart';

import 'fixtures.dart';

/// The fixture's MF2-shaped parts as this runtime's [Part]s.
List<Part> _parts(List<Object?> json) => [
  for (final raw in json)
    if (raw case final Map<String, Object?> p)
      switch (p['type']) {
        'text' => TextPart(p['value']! as String),
        'bidiIsolation' => BidiIsolationPart(p['value']! as String),
        'fallback' => FallbackPart(p['source']! as String),
        'markup' => MarkupPart(
          kind: switch (p['kind']) {
            'open' => MarkupKind.open,
            'close' => MarkupKind.close,
            'standalone' => MarkupKind.standalone,
            final other => throw StateError('unknown markup kind $other'),
          },
          name: p['name']! as String,
          options: (p['options'] as Map<String, Object?>?) ?? const {},
        ),
        // A value part's text is its value, or its sub-parts joined.
        final type => ValuePart(
          type: type! as String,
          text: p['value'] as String? ?? _joined(p['parts']),
        ),
      }
    else
      throw StateError('a part is not an object'),
];

String _joined(Object? parts) => [
  for (final p in (parts! as List<Object?>).cast<Map<String, Object?>>())
    p['value']! as String,
].join();

void main() {
  final fixture = loadRuntimeFixture('markup.json');
  final cases = fixture['cases']! as List<Object?>;

  test('the safe and void tag lists are the shared ones', () {
    expect(
      safeTags.toList()..sort(),
      equals(
        (fixture['safeTags']! as List<Object?>).cast<String>().toList()..sort(),
      ),
    );
    expect(
      voidTags.toList()..sort(),
      equals(
        (fixture['voidTags']! as List<Object?>).cast<String>().toList()..sort(),
      ),
    );
  });

  test('the fixture has cases', () => expect(cases, isNotEmpty));

  for (final raw in cases.cast<Map<String, Object?>>()) {
    test('markup.json: ${raw['description']}', () {
      final parts = _parts(raw['parts']! as List<Object?>);
      expect(partsToHtml(parts), equals(raw['html']));
    });
  }

  group('the tree behind the HTML', () {
    test('a safe tag becomes an element with its children', () {
      final tree = partsToTree([
        const TextPart('Tippe '),
        const MarkupPart(kind: MarkupKind.open, name: 'b'),
        const TextPart('hier'),
        const MarkupPart(kind: MarkupKind.close, name: 'b'),
      ]);
      expect(tree, hasLength(2));
      expect((tree[0] as MarkupText).text, 'Tippe ');
      final element = tree[1] as MarkupElement;
      expect(element.tag, 'b');
      expect((element.children.single as MarkupText).text, 'hier');
    });

    test('adjacent text runs merge into one node', () {
      final tree = partsToTree(const [
        TextPart('a'),
        BidiIsolationPart('\u2068'),
        ValuePart(type: 'string', text: 'Lina'),
        BidiIsolationPart('\u2069'),
        TextPart('b'),
      ]);
      expect(tree, hasLength(1));
      expect((tree.single as MarkupText).text, 'a\u2068Lina\u2069b');
    });

    test('options never reach the tree', () {
      final tree = partsToTree(const [
        MarkupPart(
          kind: MarkupKind.open,
          name: 'span',
          options: {'style': 'color:red', 'onclick': 'alert(1)'},
        ),
        TextPart('x'),
        MarkupPart(kind: MarkupKind.close, name: 'span'),
      ]);
      final element = tree.single as MarkupElement;
      expect(element.tag, 'span');
      expect(element.children, hasLength(1));
      // MarkupElement has a tag and children, and nowhere to put an
      // attribute: a translation cannot add one.
      expect(element.toString(), isNot(contains('color:red')));
    });

    test('a host may name its own elements, and still gets no options', () {
      final parts = const [
        TextPart('By continuing you accept the '),
        MarkupPart(
          kind: MarkupKind.open,
          name: 'link',
          options: {'href': 'javascript:alert(1)'},
        ),
        TextPart('terms'),
        MarkupPart(kind: MarkupKind.close, name: 'link'),
      ];
      // The default set drops it, as markup.json requires.
      expect(
        partsToTree(parts).single,
        isA<MarkupText>().having(
          (n) => n.text,
          'text',
          'By continuing you accept the terms',
        ),
      );
      // A host that renders `link` itself gets the element and its
      // content — and nothing the translation wrote about the target.
      final tree = partsToTree(parts, elements: {...safeTags, 'link'});
      final element = tree[1] as MarkupElement;
      expect(element.tag, 'link');
      expect((element.children.single as MarkupText).text, 'terms');
    });
  });
}
