/// Formatted parts as a tree of text and safe elements
/// (`runtimes/testdata/markup.json`, `runtimes/SPEC.md` §5).
///
/// Translation text is never parsed as markup of the host's own kind. MF2
/// markup (`{#b}…{/b}`) becomes an element only when its name is on
/// [safeTags], and it never carries the options the translation wrote:
/// SPEC §5 is explicit that "markup options never become attributes (a
/// translation can't add a link)". Markup that isn't an element keeps just
/// its content.
///
/// This is pure Dart, because the rules are the contract, not the
/// renderer: `package:glossa_flutter` maps the tree onto `InlineSpan`s,
/// [partsToHtml] serializes it the way `@klarlabs-studio/glossa/elements` and the Go
/// runtime do, and both are the same tree.
library;

import 'model.dart';
import 'parts.dart';

/// The inline, attribute-free phrasing elements a translation may produce.
///
/// The list is shared with every other runtime and is asserted against
/// `runtimes/testdata/markup.json` in the tests. Comparison is
/// case-sensitive: `{#B}` is not `{#b}`.
const Set<String> safeTags = {
  'b',
  'strong',
  'i',
  'em',
  'u',
  's',
  'small',
  'mark',
  'sub',
  'sup',
  'code',
  'kbd',
  'samp',
  'var',
  'abbr',
  'cite',
  'dfn',
  'q',
  'del',
  'ins',
  'bdi',
  'span',
  'br',
  'wbr',
};

/// The safe tags rendered without children and without a closing tag.
const Set<String> voidTags = {'br', 'wbr'};

/// A node of the tree [partsToTree] builds: a [MarkupText] run or a
/// [MarkupElement] with its children.
sealed class MarkupNode {
  const MarkupNode();
}

/// A run of text. Adjacent runs are merged, so two of them are never
/// siblings.
class MarkupText extends MarkupNode {
  /// Creates a text run.
  const MarkupText(this.text);

  /// The text, exactly as the parts carried it. It is not escaped: a
  /// serializer escapes it for its own syntax ([escapeHtml]).
  final String text;

  @override
  String toString() => 'MarkupText(${_quote(text)})';
}

/// An element a translation's markup produced. It has a name and children
/// and nothing else — never the markup's options.
class MarkupElement extends MarkupNode {
  /// Creates an element.
  const MarkupElement(this.tag, [this.children = const []]);

  /// The markup name, e.g. `b`. It is on the `elements` set
  /// [partsToTree] was given.
  final String tag;

  /// The element's content. Always empty for a tag in [voidTags].
  final List<MarkupNode> children;

  @override
  String toString() => 'MarkupElement($tag, $children)';
}

String _quote(String s) => "'${s.replaceAll("'", r"\'")}'";

/// Build the tree of text and elements for [parts].
///
/// [elements] is the set of markup names that become a [MarkupElement];
/// everything else keeps only its content. It defaults to [safeTags],
/// which is the SPEC §5 contract and the only value a conformance run
/// uses. A host widens it to render markup of its *own* — a `link` a
/// Flutter app gives a tap handler, say — which stays safe because the
/// host, not the translation, decides what the name means: the markup's
/// options are dropped here and never reach the renderer.
///
/// The structure rules are the shared ones, and they are what
/// `markup.json` pins:
///
/// - markup that isn't an element adds no node, but its content stays;
/// - a [MarkupKind.standalone] element renders only when its tag is in
///   [voidTags], and an *opened* void tag renders once, without children;
/// - unclosed markup closes at the end of the message;
/// - a close closes everything opened after its matching open;
/// - a close with no open is ignored.
List<MarkupNode> partsToTree(
  List<Part> parts, {
  Set<String> elements = safeTags,
}) {
  final root = <MarkupNode>[];
  // Every open markup is on the stack, element or not; one that isn't an
  // element has a null target, so its children land further out.
  final stack = <({String name, List<MarkupNode>? children})>[];

  List<MarkupNode> target() {
    for (var i = stack.length - 1; i >= 0; i--) {
      final children = stack[i].children;
      if (children != null) return children;
    }
    return root;
  }

  void pushText(String text) {
    if (text.isEmpty) return;
    final into = target();
    final last = into.isEmpty ? null : into.last;
    if (last is MarkupText) {
      into[into.length - 1] = MarkupText(last.text + text);
      return;
    }
    into.add(MarkupText(text));
  }

  for (final part in parts) {
    if (part is! MarkupPart) {
      pushText(part.text);
      continue;
    }
    final isElement = elements.contains(part.name);
    final isVoid = voidTags.contains(part.name);
    switch (part.kind) {
      case MarkupKind.standalone:
        if (isElement && isVoid) target().add(MarkupElement(part.name));
      case MarkupKind.open when isVoid:
        // A void tag has no content, so it never opens a span; a later
        // close for it finds no open and is ignored.
        if (isElement) target().add(MarkupElement(part.name));
      case MarkupKind.open:
        List<MarkupNode>? children;
        if (isElement) {
          children = <MarkupNode>[];
          target().add(MarkupElement(part.name, children));
        }
        stack.add((name: part.name, children: children));
      case MarkupKind.close:
        for (var i = stack.length - 1; i >= 0; i--) {
          if (stack[i].name == part.name) {
            stack.removeRange(i, stack.length);
            break;
          }
        }
    }
  }
  return root;
}

const Map<String, String> _htmlEscapes = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
};

/// Escape [text] for an HTML text node: `&`, `<` and `>`, and nothing
/// else. Quotes are kept, because nothing here ever writes an attribute.
String escapeHtml(String text) =>
    text.replaceAllMapped(RegExp('[&<>]'), (m) => _htmlEscapes[m[0]]!);

/// Serialize [parts] as safe HTML: escaped text and bare safe elements,
/// exactly as `@klarlabs-studio/glossa/elements` and the Go runtime's `HTML` do.
///
/// Flutter renders [partsToTree] instead; this is here for the hosts that
/// do speak HTML — a Dart web app, an email or a PDF pipeline — and
/// because it is how `markup.json` states the contract.
String partsToHtml(List<Part> parts) {
  final out = StringBuffer();
  _writeHtml(out, partsToTree(parts));
  return out.toString();
}

void _writeHtml(StringBuffer out, List<MarkupNode> nodes) {
  for (final node in nodes) {
    switch (node) {
      case MarkupText():
        out.write(escapeHtml(node.text));
      case MarkupElement() when !safeTags.contains(node.tag):
        _writeHtml(out, node.children);
      case MarkupElement() when voidTags.contains(node.tag):
        out.write('<${node.tag}>');
      case MarkupElement():
        out.write('<${node.tag}>');
        _writeHtml(out, node.children);
        out.write('</${node.tag}>');
    }
  }
}
