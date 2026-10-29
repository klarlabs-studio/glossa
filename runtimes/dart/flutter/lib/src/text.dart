/// [GlossaText]: a message, rendered.
library;

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:glossa/glossa.dart';

import 'scope.dart';
import 'tag_styles.dart';

/// A `Text` that renders a Glossa message by id.
///
/// It resolves through the [GlossaScope] above it, so it rebuilds when a
/// release activates or the locale changes, and it renders the message's
/// markup as styles rather than as escaped text:
///
/// ```dart
/// GlossaText('cart.items', values: {'n': cart.length})
/// ```
///
/// The widget never throws and never renders the empty string. A message
/// no locale of the chain has renders [defaultText] — the text the
/// developer wrote at the call site (SPEC §3.5) — or the id itself, so a
/// missing string is visible and never blank, and the miss goes to the
/// error channel.
class GlossaText extends StatelessWidget {
  /// Creates a message.
  const GlossaText(
    this.id, {
    this.values = const {},
    this.defaultText,
    this.bidiIsolation = true,
    this.tagStyles,
    this.builders = const {},
    this.style,
    this.strutStyle,
    this.textAlign,
    this.textDirection,
    this.softWrap,
    this.overflow,
    this.textScaler,
    this.maxLines,
    this.semanticsLabel,
    this.textWidthBasis,
    super.key,
  });

  /// The message id, e.g. `checkout.pay`.
  final String id;

  /// The message's arguments, by name.
  final Map<String, Object?> values;

  /// The text at the call site, rendered when no locale in the chain has
  /// [id] (SPEC §3.5).
  final String? defaultText;

  /// Whether placeholders are bidi-isolated, as MessageFormat 2 requires
  /// by default. Turn it off only for a renderer that can't handle the
  /// isolate characters.
  final bool bidiIsolation;

  /// The styles safe markup takes here. Defaults to the scope's.
  final GlossaTagStyles? tagStyles;

  /// Markup this call site renders itself, such as a `link` with a tap
  /// handler. See [GlossaTagBuilder]: the markup's options never reach
  /// the builder, so a translation can't choose the target.
  final Map<String, GlossaTagBuilder> builders;

  /// The base style, merged with the ambient `DefaultTextStyle`.
  final TextStyle? style;

  /// See [Text.strutStyle].
  final StrutStyle? strutStyle;

  /// See [Text.textAlign].
  final TextAlign? textAlign;

  /// See [Text.textDirection]. Null inherits the ambient
  /// [Directionality]; `GlossaScope.of(context).textDirection` is the
  /// active locale's.
  final TextDirection? textDirection;

  /// See [Text.softWrap].
  final bool? softWrap;

  /// See [Text.overflow].
  final TextOverflow? overflow;

  /// See [Text.textScaler].
  final TextScaler? textScaler;

  /// See [Text.maxLines].
  final int? maxLines;

  /// See [Text.semanticsLabel]. Null lets the rendered text speak for
  /// itself, which is what a translated string should do.
  final String? semanticsLabel;

  /// See [Text.textWidthBasis].
  final TextWidthBasis? textWidthBasis;

  @override
  Widget build(BuildContext context) {
    final glossa = GlossaScope.of(context);
    return Text.rich(
      glossa.span(
        context,
        id,
        values: values,
        defaultText: defaultText,
        bidiIsolation: bidiIsolation,
        tagStyles: tagStyles,
        builders: builders,
      ),
      style: style,
      strutStyle: strutStyle,
      textAlign: textAlign,
      textDirection: textDirection,
      softWrap: softWrap,
      overflow: overflow,
      textScaler: textScaler,
      maxLines: maxLines,
      semanticsLabel: semanticsLabel,
      textWidthBasis: textWidthBasis,
    );
  }

  @override
  void debugFillProperties(DiagnosticPropertiesBuilder properties) {
    super.debugFillProperties(properties);
    properties.add(StringProperty('id', id));
    properties.add(
      DiagnosticsProperty<Map<String, Object?>>(
        'values',
        values,
        defaultValue: const <String, Object?>{},
      ),
    );
  }
}

/// The parts of a message, for a renderer [GlossaText] doesn't cover — a
/// canvas, a PDF, a `SelectableText.rich`.
///
/// It is `GlossaScope.of(context).parts(...)`, named so that the seam is
/// easy to find: markup arrives as [MarkupPart]s, and `partsToTree` turns
/// them into the shared tree.
List<Part> glossaParts(
  BuildContext context,
  String id, {
  Map<String, Object?> values = const {},
  String? defaultText,
  bool bidiIsolation = true,
}) => GlossaScope.of(context).parts(
  id,
  values: values,
  defaultText: defaultText,
  bidiIsolation: bidiIsolation,
);
