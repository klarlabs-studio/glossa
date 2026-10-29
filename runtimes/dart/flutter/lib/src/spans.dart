/// Formatted parts as `InlineSpan`s: the Flutter end of the shared
/// safe-tag contract (`runtimes/testdata/markup.json`, SPEC §5).
///
/// The rules are not reimplemented here. `partsToTree` in the core
/// package owns them — which markup becomes an element, what a stray
/// close does, that options are dropped — and this file only decides what
/// an element *looks* like. The same tree the Go runtime turns into HTML
/// becomes spans here, so one fixture covers both.
library;

import 'package:flutter/widgets.dart';
import 'package:glossa/glossa.dart';

import 'tag_styles.dart';

/// A word-break opportunity: what `{#wbr/}` means, and the only thing a
/// text layout engine can do with it.
const String zeroWidthSpace = '​';

/// Build the span tree for already-formatted [parts].
///
/// Markup on [safeTags] takes its style from [tagStyles]; markup named in
/// [builders] is handed to the application, children and all; everything
/// else keeps only its content. `{#br/}` is a line break, `{#wbr/}` a
/// [zeroWidthSpace].
///
/// Nothing here can throw: [parts] are already formatted, and a failing
/// placeholder arrived as its MF2 fallback text.
InlineSpan glossaSpan(
  BuildContext context,
  List<Part> parts, {
  GlossaTagStyles tagStyles = const GlossaTagStyles.standard(),
  Map<String, GlossaTagBuilder> builders = const {},
  TextStyle? style,
}) {
  final elements = builders.isEmpty
      ? safeTags
      : <String>{...safeTags, ...builders.keys};
  return TextSpan(
    style: style,
    children: _spans(
      context,
      partsToTree(parts, elements: elements),
      tagStyles,
      builders,
    ),
  );
}

List<InlineSpan> _spans(
  BuildContext context,
  List<MarkupNode> nodes,
  GlossaTagStyles tagStyles,
  Map<String, GlossaTagBuilder> builders,
) {
  final spans = <InlineSpan>[];
  for (final node in nodes) {
    switch (node) {
      case MarkupText():
        spans.add(TextSpan(text: node.text));
      case MarkupElement():
        final children = _spans(context, node.children, tagStyles, builders);
        final builder = builders[node.tag];
        if (builder != null) {
          spans.add(builder(context, children));
        } else if (node.tag == 'br') {
          spans.add(const TextSpan(text: '\n'));
        } else if (node.tag == 'wbr') {
          spans.add(const TextSpan(text: zeroWidthSpace));
        } else {
          spans.add(TextSpan(style: tagStyles[node.tag], children: children));
        }
    }
  }
  return spans;
}
