/// How a markup name from a translation becomes a style, and how a host
/// renders markup of its own (`runtimes/SPEC.md` §5, RFC 0005 §6.2).
library;

import 'package:flutter/widgets.dart';

/// Builds the span for one markup name.
///
/// The builder gets the markup's already-rendered [children] and the
/// [context] it is built in — and deliberately *nothing else*. The
/// markup's options never reach it, because they come from the
/// translation: SPEC §5 says a translation can't add a link. A tappable
/// link is therefore the application's decision, at the call site, with
/// the application's own target:
///
/// ```dart
/// GlossaText(
///   'legal.accept',
///   builders: {
///     'link': (context, children) => TextSpan(
///       children: children,
///       style: const TextStyle(decoration: TextDecoration.underline),
///       recognizer: TapGestureRecognizer()..onTap = () => openTerms(),
///     ),
///   },
/// )
/// ```
typedef GlossaTagBuilder = InlineSpan Function(
  BuildContext context,
  List<InlineSpan> children,
);

/// The text style each safe markup name contributes.
///
/// A style is *merged* into the surrounding one, so nesting composes:
/// `{#b}bold {#i}and italic{/i}{/b}` is bold, then bold italic.
@immutable
class GlossaTagStyles {
  /// Creates a set of styles. A tag with no entry renders its content in
  /// the surrounding style.
  const GlossaTagStyles(this.styles);

  /// The styles this package applies when the application names none.
  ///
  /// Only the tags whose meaning is unambiguous and theme-free are here:
  /// weight, slant and decoration, plus a monospace family for `code`,
  /// `kbd` and `samp`. `small`, `sub`, `sup` and `mark` need a size or a
  /// colour that belongs to the application's theme, and `q`, `abbr`,
  /// `bdi` and `span` need a decision no default can make, so they carry
  /// no style and render their content plainly. Supply your own for them
  /// with [merge].
  ///
  /// `br` and `wbr` are not styles: they are a line break and a
  /// word-break opportunity, and the renderer handles them.
  const GlossaTagStyles.standard() : styles = _standard;

  /// Tag name → the style it adds.
  final Map<String, TextStyle> styles;

  /// The style for [tag], or null when it adds none.
  TextStyle? operator [](String tag) => styles[tag];

  /// A copy with [overrides] applied on top, entry by entry: a tag named
  /// in [overrides] takes that style, the others keep this one's.
  GlossaTagStyles merge(Map<String, TextStyle> overrides) =>
      GlossaTagStyles({...styles, ...overrides});
}

const Map<String, TextStyle> _standard = {
  'b': TextStyle(fontWeight: FontWeight.bold),
  'strong': TextStyle(fontWeight: FontWeight.bold),
  'i': TextStyle(fontStyle: FontStyle.italic),
  'em': TextStyle(fontStyle: FontStyle.italic),
  'var': TextStyle(fontStyle: FontStyle.italic),
  'cite': TextStyle(fontStyle: FontStyle.italic),
  'dfn': TextStyle(fontStyle: FontStyle.italic),
  'u': TextStyle(decoration: TextDecoration.underline),
  'ins': TextStyle(decoration: TextDecoration.underline),
  's': TextStyle(decoration: TextDecoration.lineThrough),
  'del': TextStyle(decoration: TextDecoration.lineThrough),
  'code': _monospace,
  'kbd': _monospace,
  'samp': _monospace,
};

/// A family that exists on each platform Flutter targets, tried in turn.
const TextStyle _monospace = TextStyle(
  fontFamily: 'monospace',
  fontFamilyFallback: <String>['Menlo', 'Courier New', 'Roboto Mono'],
);
