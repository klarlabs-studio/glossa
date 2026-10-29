/// The canonical MessageFormat 2 data model, exactly as the spec's JSON
/// Schema (`messageformat/testdata/unicode/data-model/message.schema.json`)
/// describes it.
///
/// This is the wire contract: release artifacts carry messages in this shape
/// and every runtime reads this shape. There is no parser — messages are
/// precompiled by the platform before they reach an artifact.
library;

/// Thrown when JSON is not a valid data-model message. The catalog turns it
/// into a `schema` error for that message id (SPEC §3).
class MessageModelException implements Exception {
  /// Creates an exception describing why the JSON is not a message.
  MessageModelException(this.detail);

  /// What was wrong.
  final String detail;

  @override
  String toString() => 'MessageModelException: $detail';
}

Never _bad(String detail) => throw MessageModelException(detail);

Map<String, Object?> _obj(Object? v, String what) =>
    v is Map<String, Object?> ? v : _bad('$what is not an object');

List<Object?> _list(Object? v, String what) =>
    v is List<Object?> ? v : _bad('$what is not an array');

/// A data-model message: a pattern, or a selection over variants.
sealed class Message {
  const Message(this.declarations);

  /// `.input` and `.local` declarations, in source order.
  final List<Declaration> declarations;

  /// Reads a message from decoded JSON, throwing
  /// [MessageModelException] when it is not one.
  factory Message.fromJson(Object? json) {
    final m = _obj(json, 'message');
    final declarations = [
      for (final d in _list(m['declarations'] ?? const [], 'declarations'))
        Declaration.fromJson(d),
    ];
    return switch (m['type']) {
      'message' => PatternMessage(declarations, _pattern(m['pattern'])),
      'select' => SelectMessage(
        declarations,
        [
          for (final s in _list(m['selectors'], 'selectors'))
            VariableRef.fromJson(s),
        ],
        [for (final v in _list(m['variants'], 'variants')) Variant.fromJson(v)],
      ),
      final other => _bad('unknown message type ${other ?? 'null'}'),
    };
  }
}

/// A message that always renders the same pattern.
class PatternMessage extends Message {
  /// Creates a pattern message.
  const PatternMessage(super.declarations, this.pattern);

  /// The pattern to render.
  final Pattern pattern;
}

/// A message that selects one variant's pattern.
class SelectMessage extends Message {
  /// Creates a select message.
  const SelectMessage(super.declarations, this.selectors, this.variants);

  /// The variables selection is performed on, in order.
  final List<VariableRef> selectors;

  /// The variants, each keyed once per selector.
  final List<Variant> variants;
}

/// A pattern: text, placeholders and markup, in order.
typedef Pattern = List<PatternElement>;

Pattern _pattern(Object? json) => [
  for (final e in _list(json, 'pattern')) PatternElement.fromJson(e),
];

/// One element of a pattern.
sealed class PatternElement {
  const PatternElement();

  /// Reads a pattern element from decoded JSON.
  factory PatternElement.fromJson(Object? json) {
    if (json is String) return TextElement(json);
    final m = _obj(json, 'pattern element');
    return switch (m['type']) {
      'markup' => Markup.fromJson(m),
      'expression' => Expression.fromJson(m),
      final other => _bad('unknown pattern element type ${other ?? 'null'}'),
    };
  }
}

/// Literal text in a pattern.
class TextElement extends PatternElement {
  /// Creates literal text.
  const TextElement(this.value);

  /// The text, exactly as it renders.
  final String value;
}

/// A `.input` or `.local` declaration.
sealed class Declaration {
  const Declaration(this.name, this.value);

  /// The variable name the declaration binds, without the `$`.
  final String name;

  /// The expression the name is bound to.
  final Expression value;

  /// Reads a declaration from decoded JSON.
  factory Declaration.fromJson(Object? json) {
    final m = _obj(json, 'declaration');
    final name = m['name'];
    if (name is! String) _bad('declaration has no name');
    final value = Expression.fromJson(m['value']);
    return switch (m['type']) {
      'input' => InputDeclaration(name, value),
      'local' => LocalDeclaration(name, value),
      final other => _bad('unknown declaration type ${other ?? 'null'}'),
    };
  }
}

/// `.input {$name …}`: annotates the external value of the same name.
class InputDeclaration extends Declaration {
  /// Creates an input declaration.
  const InputDeclaration(super.name, super.value);
}

/// `.local $name = {…}`: a new name bound to an expression.
class LocalDeclaration extends Declaration {
  /// Creates a local declaration.
  const LocalDeclaration(super.name, super.value);
}

/// One variant of a select message.
class Variant {
  /// Creates a variant.
  const Variant(this.keys, this.value);

  /// One key per selector: a literal, or the catch-all `*`.
  final List<VariantKey> keys;

  /// The pattern this variant renders.
  final Pattern value;

  /// Reads a variant from decoded JSON.
  factory Variant.fromJson(Object? json) {
    final m = _obj(json, 'variant');
    return Variant([
      for (final k in _list(m['keys'], 'variant keys')) VariantKey.fromJson(k),
    ], _pattern(m['value']));
  }
}

/// A variant key: a literal, or the catch-all.
sealed class VariantKey {
  const VariantKey();

  /// Reads a variant key from decoded JSON.
  factory VariantKey.fromJson(Object? json) {
    final m = _obj(json, 'variant key');
    return switch (m['type']) {
      '*' => const CatchallKey(),
      'literal' => Literal.fromJson(m),
      final other => _bad('unknown variant key type ${other ?? 'null'}'),
    };
  }
}

/// The catch-all key `*`.
class CatchallKey extends VariantKey {
  /// Creates the catch-all key.
  const CatchallKey();
}

/// An operand: a literal or a variable reference.
sealed class Operand {
  const Operand();

  /// Reads an operand from decoded JSON.
  factory Operand.fromJson(Object? json) {
    final m = _obj(json, 'operand');
    return switch (m['type']) {
      'literal' => Literal.fromJson(m),
      'variable' => VariableRef.fromJson(m),
      final other => _bad('unknown operand type ${other ?? 'null'}'),
    };
  }

  /// The MF2 fallback spelling of this operand (`$name`, `|literal|`).
  String get source;
}

/// A quoted or unquoted literal.
class Literal extends Operand implements VariantKey {
  /// Creates a literal.
  const Literal(this.value);

  /// The literal's text.
  final String value;

  /// Reads a literal from decoded JSON.
  factory Literal.fromJson(Map<String, Object?> m) {
    final v = m['value'];
    if (v is! String) _bad('literal has no value');
    return Literal(v);
  }

  @override
  String get source =>
      '|${value.replaceAllMapped(RegExp(r'[\\|]'), (m) => '\\${m[0]}')}|';
}

/// A `$name` reference.
class VariableRef extends Operand {
  /// Creates a variable reference.
  const VariableRef(this.name);

  /// The name, without the `$`.
  final String name;

  /// Reads a variable reference from decoded JSON.
  factory VariableRef.fromJson(Object? json) {
    final m = _obj(json, 'variable');
    final name = m['name'];
    if (name is! String) _bad('variable has no name');
    return VariableRef(name);
  }

  @override
  String get source => '\$$name';
}

/// A placeholder: an operand, a function, or both.
class Expression extends PatternElement {
  /// Creates an expression.
  const Expression({this.arg, this.function});

  /// The operand, when the expression has one.
  final Operand? arg;

  /// The function, when the expression has one.
  final FunctionRef? function;

  /// Reads an expression from decoded JSON.
  factory Expression.fromJson(Object? json) {
    final m = _obj(json, 'expression');
    final arg = m['arg'] == null ? null : Operand.fromJson(m['arg']);
    final fn = m['function'] == null
        ? null
        : FunctionRef.fromJson(m['function']);
    if (arg == null && fn == null) {
      _bad('expression has neither arg nor function');
    }
    return Expression(arg: arg, function: fn);
  }

  /// The MF2 fallback spelling of this expression.
  String get source => arg?.source ?? ':${function!.name}';
}

/// A `:function` with its options.
class FunctionRef {
  /// Creates a function reference.
  const FunctionRef(this.name, this.options);

  /// The function name, without the `:`.
  final String name;

  /// The options, in source order.
  final Map<String, Operand> options;

  /// Reads a function reference from decoded JSON.
  factory FunctionRef.fromJson(Object? json) {
    final m = _obj(json, 'function');
    final name = m['name'];
    if (name is! String) _bad('function has no name');
    return FunctionRef(name, _options(m['options']));
  }
}

/// Markup: `{#tag}`, `{/tag}` or `{#tag/}`.
class Markup extends PatternElement {
  /// Creates markup.
  const Markup(this.kind, this.name, this.options);

  /// `open`, `standalone` or `close`.
  final MarkupKind kind;

  /// The markup name.
  final String name;

  /// The options, in source order.
  final Map<String, Operand> options;

  /// Reads markup from decoded JSON.
  factory Markup.fromJson(Map<String, Object?> m) {
    final name = m['name'];
    if (name is! String) _bad('markup has no name');
    final kind = switch (m['kind']) {
      'open' => MarkupKind.open,
      'standalone' => MarkupKind.standalone,
      'close' => MarkupKind.close,
      final other => _bad('unknown markup kind ${other ?? 'null'}'),
    };
    return Markup(kind, name, _options(m['options']));
  }
}

/// Which end of a markup span an element is.
enum MarkupKind {
  /// `{#tag}`.
  open,

  /// `{#tag/}`.
  standalone,

  /// `{/tag}`.
  close;

  /// The wire spelling.
  @override
  String toString() => name;
}

Map<String, Operand> _options(Object? json) {
  if (json == null) return const {};
  final m = _obj(json, 'options');
  return {for (final e in m.entries) e.key: Operand.fromJson(e.value)};
}
