/// A MessageFormat 2 interpreter over the precompiled data model
/// (`runtimes/SPEC.md` §5).
///
/// There is no parser: release artifacts carry the canonical data model.
/// Formatting never throws — errors go to [FormatOptions.onError] and the
/// failing placeholder renders as its MF2 fallback (`{$name}`, `{|lit|}`,
/// `{:fn}`).
library;

import 'functions.dart';
import 'locale.dart';
import 'model.dart';
import 'parts.dart';

/// An error reported while formatting. [source] identifies the placeholder.
class MessageFormatError {
  /// Creates a format error.
  const MessageFormatError(this.type, this.source);

  /// The MF2 error type, e.g. `unresolved-variable`.
  final String type;

  /// The MF2 source of the placeholder it happened in.
  final String source;

  @override
  String toString() => '$type at $source';
}

/// How a message is formatted.
class FormatOptions {
  /// Creates format options.
  const FormatOptions({
    this.bidiIsolation = true,
    this.dir,
    this.functions = const {},
    this.onError,
  });

  /// Isolate placeholders with Unicode bidi isolates, per the MF2 spec.
  /// On by default (SPEC §5).
  final bool bidiIsolation;

  /// The message's base direction; derived from the locale when omitted.
  final String? dir;

  /// Extra functions, keyed by name without the colon. They win over the
  /// built-ins.
  final Map<String, MessageFunction> functions;

  /// Called once per error. Formatting continues with a fallback.
  final void Function(MessageFormatError error)? onError;
}

const String _lri = '\u2066';
const String _rli = '\u2067';
const String _fsi = '\u2068';
const String _pdi = '\u2069';

/// Format [message] to parts with [locale]'s rules and [values].
List<Part> formatToParts(
  Message message,
  String locale, {
  Map<String, Object?> values = const {},
  FormatOptions options = const FormatOptions(),
}) => _Interpreter(message, locale, values, options).run();

/// Format [message] to a string.
String formatMessage(
  Message message,
  String locale, {
  Map<String, Object?> values = const {},
  FormatOptions options = const FormatOptions(),
}) => partsToString(
  formatToParts(message, locale, values: values, options: options),
);

/// A resolved expression: a value plus the MF2 source it came from.
class _Resolved {
  _Resolved(this.value, this.source, {this.isolate = false, this.id});

  final MessageValue value;
  final String source;

  /// True when `u:dir` was given: the placeholder is isolated even in an
  /// all-LTR message.
  final bool isolate;
  final String? id;

  bool get isFallback => value.type == 'fallback';
}

class _Interpreter {
  _Interpreter(this.message, this.locale, this.values, this.options)
    : functions = {...builtins, ...options.functions};

  final Message message;
  final String locale;
  final Map<String, Object?> values;
  final FormatOptions options;
  final Map<String, MessageFunction> functions;

  late final Map<String, Declaration> declarations = {
    for (final d in message.declarations) d.name: d,
  };
  final Map<String, _Resolved> locals = {};

  void report(String type, String source) =>
      options.onError?.call(MessageFormatError(type, source));

  _Resolved fallback(String source) => _Resolved(
    MessageValue(
      type: 'fallback',
      value: null,
      formatter: () => [FallbackPart(source)],
    ),
    source,
  );

  FunctionContext context(
    String source, {
    String? dir,
    Set<String>? literals,
  }) => FunctionContext(
    locale: locale,
    dir: dir,
    literals: literals ?? const {},
    onError: (type) => report(type, source),
  );

  /// The value of a variable: a resolved declaration, or an external value.
  ///
  /// [external] is set inside an `.input` declaration, whose operand is the
  /// external value the declaration shadows.
  Object? lookup(String name, String source, {bool external = false}) {
    if (!external) {
      var local = locals[name];
      final decl = declarations[name];
      if (local == null && decl != null) {
        local = resolve(decl.value, external: decl is InputDeclaration);
        locals[name] = local;
      }
      if (local != null) return local;
    }
    if (!values.containsKey(name)) {
      report('unresolved-variable', source);
      return null;
    }
    final v = values[name];
    if (v == null) report('unresolved-variable', source);
    return v;
  }

  Object? operandValue(Operand operand, {bool external = false}) =>
      switch (operand) {
        Literal(:final value) => value,
        VariableRef(:final name) => lookup(
          name,
          operand.source,
          external: external,
        ),
      };

  /// Resolve an expression to a value. Never throws: failures become
  /// fallbacks.
  _Resolved resolve(Expression expr, {bool external = false}) {
    final arg = expr.arg;
    final fn = expr.function;
    final source = expr.source;

    if (fn == null) {
      final operand = arg!;
      if (operand is Literal) {
        return _Resolved(stringValue(context(source), operand.value), source);
      }
      final name = (operand as VariableRef).name;
      final v = lookup(name, source, external: external);
      if (v == null) return fallback(source);
      if (!external && locals.containsKey(name)) {
        final local = v as _Resolved;
        return local.isFallback
            ? fallback(source)
            : _Resolved(local.value, source, id: local.id);
      }
      return _Resolved(implicitValue(context(source), v), source);
    }

    try {
      Object? operand;
      final hasOperand = arg != null;
      if (hasOperand) {
        operand = operandValue(arg, external: external);
        if (operand is _Resolved) {
          if (operand.isFallback) throw FunctionError('bad-operand');
          operand = operand.value;
        }
      }
      final handler = functions[fn.name];
      if (handler == null) throw FunctionError('unknown-function');

      final resolvedOptions = <String, Object?>{};
      final literals = <String>{};
      String? dir;
      String? id;
      for (final entry in fn.options.entries) {
        final name = entry.key;
        var v = operandValue(entry.value);
        if (v is _Resolved) v = v.value;
        if (name == 'u:dir') {
          final d = '${unwrap(v).$1}';
          if (d == 'ltr' || d == 'rtl' || d == 'auto') {
            dir = d;
          } else if (d != 'inherit') {
            report('bad-option', source);
          }
        } else if (name == 'u:id') {
          id = '${unwrap(v).$1}';
        } else if (!name.startsWith('u:')) {
          resolvedOptions[name] = v;
          if (entry.value is Literal) literals.add(name);
        }
      }

      final ctx = context(source, dir: dir, literals: literals);
      final value = handler(ctx, resolvedOptions, operand, hasOperand);
      return _Resolved(
        value.copyWith(dir: dir ?? value.dir),
        source,
        isolate: dir != null,
        id: id,
      );
    } on FunctionError catch (e) {
      report(e.type, source);
      return fallback(source);
    } on Object {
      report('bad-function-result', source);
      return fallback(source);
    }
  }

  MarkupPart markup(Markup m) {
    String? id;
    final resolved = <String, Object?>{};
    for (final entry in m.options.entries) {
      if (entry.key == 'u:dir') {
        report('bad-option', entry.value.source);
        continue;
      }
      var v = operandValue(entry.value);
      if (v is _Resolved) v = v.value;
      final (raw, _) = unwrap(v);
      if (entry.key == 'u:id') {
        id = '$raw';
      } else if (!entry.key.startsWith('u:')) {
        resolved[entry.key] = raw;
      }
    }
    return MarkupPart(kind: m.kind, name: m.name, id: id, options: resolved);
  }

  /// Pattern selection per the MF2 spec: resolve each selector's preferred
  /// keys, keep the variants whose keys all match (or are `*`), sort them by
  /// preference — last selector first — and take the best one.
  Pattern selectPattern(SelectMessage msg) {
    final prefs = <List<String>>[];
    for (var i = 0; i < msg.selectors.length; i++) {
      final resolved = resolve(Expression(arg: msg.selectors[i]));
      final keys = <String>[];
      for (final v in msg.variants) {
        final k = v.keys[i];
        if (k is Literal) keys.add(k.value);
      }
      final selector = resolved.value.selector;
      if (selector == null) {
        report('bad-selector', resolved.source);
        prefs.add(const []);
        continue;
      }
      try {
        prefs.add(keys.isEmpty ? const [] : selector(keys));
      } on Object {
        report('bad-selector', resolved.source);
        prefs.add(const []);
      }
    }

    int rank(List<VariantKey> keys, int i) {
      final k = keys[i];
      return k is Literal ? prefs[i].indexOf(k.value) : prefs[i].length;
    }

    final candidates = <(int, Variant)>[];
    for (var v = 0; v < msg.variants.length; v++) {
      final variant = msg.variants[v];
      final matches = List.generate(
        variant.keys.length,
        (i) => rank(variant.keys, i),
      ).every((r) => r >= 0);
      if (matches) candidates.add((v, variant));
    }
    // The spec sorts once per selector, from the last to the first, with a
    // stable sort — so the first selector ends up most significant and each
    // later one is a tie-break. `List.sort` is not stable in Dart, so the
    // whole ordering is expressed as one comparator instead, with the
    // variant's position as the final tie-break.
    candidates.sort((a, b) {
      for (var i = 0; i < prefs.length; i++) {
        final c = rank(a.$2.keys, i).compareTo(rank(b.$2.keys, i));
        if (c != 0) return c;
      }
      return a.$1.compareTo(b.$1);
    });
    if (candidates.isEmpty) {
      // The data model guarantees a `*` variant, so this is unreachable for
      // a valid message; a bad one renders its last variant rather than
      // nothing.
      report('bad-selector', '\ufffd');
      return msg.variants.last.value;
    }
    return candidates.first.$2.value;
  }

  List<Part> run() {
    final Pattern pattern;
    try {
      pattern = switch (message) {
        PatternMessage(:final pattern) => pattern,
        final SelectMessage m => selectPattern(m),
      };
    } on Object {
      report('bad-message', '\ufffd');
      return const [FallbackPart('\ufffd')];
    }

    final messageDir = options.dir ?? directionOf(locale).name;
    final parts = <Part>[];
    for (final element in pattern) {
      switch (element) {
        case TextElement(:final value):
          parts.add(TextPart(value));
        case final Markup m:
          parts.add(markup(m));
        case final Expression e:
          final resolved = resolve(e);
          var dir = resolved.value.dir;
          List<Part> formatted;
          final formatter = resolved.value.formatter;
          if (formatter == null) {
            report('not-formattable', resolved.source);
            formatted = [FallbackPart(resolved.source)];
            dir = null;
          } else {
            try {
              formatted = formatter();
              if (resolved.id != null) {
                formatted = [
                  for (final p in formatted)
                    p is ValuePart ? p.withId(resolved.id) : p,
                ];
              }
            } on FunctionError catch (err) {
              report(err.type, resolved.source);
              formatted = [FallbackPart(resolved.source)];
              dir = null;
            } on Object {
              report('bad-function-result', resolved.source);
              formatted = [FallbackPart(resolved.source)];
              dir = null;
            }
          }
          if (options.bidiIsolation &&
              (messageDir != 'ltr' || dir != 'ltr' || resolved.isolate)) {
            parts
              ..add(
                BidiIsolationPart(switch (dir) {
                  'ltr' => _lri,
                  'rtl' => _rli,
                  _ => _fsi,
                }),
              )
              ..addAll(formatted)
              ..add(const BidiIsolationPart(_pdi));
          } else {
            parts.addAll(formatted);
          }
      }
    }
    return parts;
  }
}
