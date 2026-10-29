/// Formatted parts, the MF2 shape `runtimes/testdata/markup.json` describes.
///
/// A renderer consumes parts — `markup.dart` turns them into the tree
/// `glossa_flutter` and an HTML serializer share; `format()` joins them
/// into a string.
library;

import 'model.dart';

/// One piece of a formatted message.
sealed class Part {
  const Part();

  /// What this part contributes to `format()`'s string.
  String get text;
}

/// Literal text from the pattern.
class TextPart extends Part {
  /// Creates a text part.
  const TextPart(this.text);

  @override
  final String text;
}

/// A bidi isolate character the formatter inserted.
class BidiIsolationPart extends Part {
  /// Creates a bidi isolation part.
  const BidiIsolationPart(this.text);

  @override
  final String text;
}

/// A markup open, close or standalone element. It contributes nothing to
/// the string.
class MarkupPart extends Part {
  /// Creates a markup part.
  const MarkupPart({
    required this.kind,
    required this.name,
    this.id,
    this.options = const {},
  });

  /// Which end of the span this is.
  final MarkupKind kind;

  /// The markup name.
  final String name;

  /// The `u:id` option, when given.
  final String? id;

  /// The resolved options, `u:` options excluded.
  final Map<String, Object?> options;

  @override
  String get text => '';
}

/// A placeholder that could not be formatted: it renders its MF2 source in
/// braces, and the reason is on the error channel.
class FallbackPart extends Part {
  /// Creates a fallback part.
  const FallbackPart(this.source);

  /// The MF2 source of the failing expression, e.g. `$name`.
  final String source;

  @override
  String get text => '{$source}';
}

/// A formatted value: a number, a date, a string.
class ValuePart extends Part {
  /// Creates a value part.
  const ValuePart({
    required this.type,
    required this.text,
    this.locale,
    this.dir,
    this.id,
  });

  /// The MF2 value type (`string`, `number`, `datetime`, `unknown`).
  final String type;

  @override
  final String text;

  /// The locale the value was formatted with.
  final String? locale;

  /// The value's text direction, when it has one.
  final String? dir;

  /// The `u:id` option of the expression, when given.
  final String? id;

  /// A copy carrying [id].
  ValuePart withId(String? id) =>
      ValuePart(type: type, text: text, locale: locale, dir: dir, id: id);
}

/// Join formatted [parts] into the string `format()` returns.
String partsToString(List<Part> parts) {
  final out = StringBuffer();
  for (final p in parts) {
    out.write(p.text);
  }
  return out.toString();
}
