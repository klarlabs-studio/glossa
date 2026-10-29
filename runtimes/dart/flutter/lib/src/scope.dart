/// Binding a [GlossaClient] to a widget subtree, and the SPEC §6 surfaces
/// an application reaches through it: `explain()` and the error channel.
library;

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:glossa/glossa.dart';

import 'spans.dart';
import 'tag_styles.dart';

/// The Glossa runtime as a widget subtree sees it.
///
/// Obtained with [GlossaScope.of]. Reading it makes the widget depend on
/// the scope, so it rebuilds when a release activates, when the locale
/// changes and when a refresh brings new text.
@immutable
class Glossa {
  const Glossa._(this.client, this.tagStyles, this._scope);

  /// The runtime itself, for everything this class doesn't forward.
  final GlossaClient client;

  /// The styles [GlossaText] gives safe markup unless a call site
  /// overrides them.
  final GlossaTagStyles tagStyles;

  final _GlossaScopeState _scope;

  /// Render message [id] (SPEC §4.3). It never throws and never renders
  /// the empty string: with no locale in the chain carrying the message,
  /// [defaultText] renders, or the id itself.
  String t(
    String id, {
    Map<String, Object?> values = const {},
    String? defaultText,
    bool bidiIsolation = true,
  }) => client.t(
    id,
    values: values,
    defaultText: defaultText,
    bidiIsolation: bidiIsolation,
  );

  /// Render message [id] to its formatted parts, markup included.
  ///
  /// This is the seam the safe-tag contract needs: markup arrives as
  /// [MarkupPart]s rather than as text, so [span] (and [GlossaText]) can
  /// render `{#b}…{/b}` as a style instead of escaping it into the
  /// string. `partsToTree` in the core package turns parts into the
  /// shared tree.
  List<Part> parts(
    String id, {
    Map<String, Object?> values = const {},
    String? defaultText,
    bool bidiIsolation = true,
  }) => client.parts(
    id,
    values: values,
    defaultText: defaultText,
    bidiIsolation: bidiIsolation,
  );

  /// Message [id] as one [InlineSpan], for a `Text.rich`, a `RichText` or
  /// a `TextSpan` an application is composing itself. [GlossaText] is
  /// this plus a `Text.rich` around it.
  InlineSpan span(
    BuildContext context,
    String id, {
    Map<String, Object?> values = const {},
    String? defaultText,
    bool bidiIsolation = true,
    GlossaTagStyles? tagStyles,
    Map<String, GlossaTagBuilder> builders = const {},
    TextStyle? style,
  }) => glossaSpan(
    context,
    parts(
      id,
      values: values,
      defaultText: defaultText,
      bidiIsolation: bidiIsolation,
    ),
    tagStyles: tagStyles ?? this.tagStyles,
    builders: builders,
    style: style,
  );

  /// Why [id] rendered the way it did (SPEC §6), without side effects:
  /// nothing is loaded and no error is reported.
  ///
  /// [Explanation.toJson] is the SPEC §6 document, field for field.
  /// [requested] explains a different set of requested locales without
  /// switching to them.
  Explanation explain(String id, [List<String>? requested]) =>
      client.explain(id, requested);

  /// Load, verification, format and missing-message errors (SPEC §6),
  /// with the repeat suppression the channel was built with.
  ///
  /// [GlossaScope.onError] is the same stream, already subscribed for the
  /// scope's lifetime; use this one for a second listener, such as a
  /// debug overlay.
  Stream<GlossaError> get errors => client.errors;

  /// The active release, or null when nothing has loaded.
  ReleaseRef? get release => client.release;

  /// Where the active release came from (SPEC §6).
  Source get source => client.source;

  /// The active locale, or null when no release is active.
  String? get locale => client.locale;

  /// The active locale's base direction, as Flutter spells it. Wrap a
  /// subtree in `Directionality(textDirection: …)` to honour it.
  TextDirection get textDirection =>
      client.direction == Direction.rtl ? TextDirection.rtl : TextDirection.ltr;

  /// The locales the active release carries, e.g. for a locale picker.
  List<LocaleEntry> get availableLocales => client.availableLocales;

  /// Switch the requested locales and rebuild the subtree once the new
  /// chain has loaded and verified. The switch is atomic: until it
  /// completes, the previous chain keeps rendering.
  Future<void> setLocales(List<String> locales) => _scope.setLocales(locales);

  /// Revalidate the manifest now and rebuild if it brought a new release.
  Future<void> refresh() => _scope.refresh();
}

/// Makes a [GlossaClient] available to the widgets below it.
///
/// The scope owns no runtime: the application creates the client, decides
/// its transport, store and bundle, and disposes it. The scope subscribes
/// to its error channel, rebuilds its subtree when a release activates,
/// and — unless [refreshOnResume] is false — revalidates the manifest
/// when the app comes back to the foreground, which is the refresh SPEC §3
/// asks for beyond the periodic one.
///
/// ```dart
/// runApp(
///   GlossaScope(
///     client: glossa,
///     onError: (e) => logger.warning('$e'),
///     child: const MyApp(),
///   ),
/// );
/// ```
class GlossaScope extends StatefulWidget {
  /// Creates a scope around [child].
  const GlossaScope({
    required this.client,
    required this.child,
    this.onError,
    this.tagStyles = const GlossaTagStyles.standard(),
    this.refreshOnResume = true,
    super.key,
  });

  /// The runtime. The scope does not dispose it: a client usually
  /// outlives any one widget tree, and disposing it would close the error
  /// channel for everyone.
  final GlossaClient client;

  /// The subtree that renders from [client].
  final Widget child;

  /// Where the error channel goes (SPEC §6).
  ///
  /// When it is null, errors are printed with `debugPrint` in a debug
  /// build and dropped in a release build. They are never thrown: a
  /// failed load or a failing placeholder must not take the app down.
  /// No telemetry is sent anywhere, here or in the core (intent §17).
  final void Function(GlossaError error)? onError;

  /// The styles safe markup gets unless a [GlossaText] overrides them.
  final GlossaTagStyles tagStyles;

  /// Whether to revalidate the manifest when the app resumes (SPEC §3).
  final bool refreshOnResume;

  /// The runtime for [context], or null when there is no scope above it.
  static Glossa? maybeOf(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<_GlossaBinding>()?.glossa;

  /// The runtime for [context].
  ///
  /// Throws a [FlutterError] when no [GlossaScope] is above [context],
  /// because rendering the message id instead would hide a wiring
  /// mistake behind text that looks almost right.
  static Glossa of(BuildContext context) {
    final glossa = maybeOf(context);
    if (glossa == null) {
      throw FlutterError.fromParts([
        ErrorSummary('No GlossaScope found above this widget.'),
        ErrorDescription(
          '${context.widget.runtimeType} asked for the Glossa runtime, but '
          'no GlossaScope is an ancestor of it.',
        ),
        ErrorHint(
          'Wrap the application in GlossaScope(client: …, child: …), above '
          'every widget that renders a message.',
        ),
      ]);
    }
    return glossa;
  }

  @override
  State<GlossaScope> createState() => _GlossaScopeState();
}

class _GlossaScopeState extends State<GlossaScope> with WidgetsBindingObserver {
  StreamSubscription<GlossaError>? _errors;

  /// Bumped whenever the rendered text may have changed. It is what makes
  /// the inherited widget notify: the client is the same object across a
  /// release swap, so identity can't tell.
  int _generation = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _listen();
  }

  @override
  void didUpdateWidget(GlossaScope old) {
    super.didUpdateWidget(old);
    if (!identical(old.client, widget.client)) {
      unawaited(_errors?.cancel());
      _listen();
      _bump();
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    unawaited(_errors?.cancel());
    // The client belongs to the application, so it is not disposed here.
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (widget.refreshOnResume && state == AppLifecycleState.resumed) {
      unawaited(refresh());
    }
  }

  void _listen() {
    _errors = widget.client.errors.listen(_report);
    // The first load settles after this frame; rebuild when it does, so
    // the subtree stops rendering inline defaults.
    unawaited(widget.client.ready.then((_) => _bump()));
  }

  void _report(GlossaError error) {
    final onError = widget.onError;
    if (onError != null) {
      onError(error);
      return;
    }
    if (kDebugMode) debugPrint('Glossa: $error');
  }

  void _bump() {
    if (mounted) setState(() => _generation++);
  }

  Future<void> refresh() async {
    await widget.client.refresh();
    _bump();
  }

  Future<void> setLocales(List<String> locales) async {
    await widget.client.setLocales(locales);
    _bump();
  }

  @override
  Widget build(BuildContext context) => _GlossaBinding(
    glossa: Glossa._(widget.client, widget.tagStyles, this),
    generation: _generation,
    child: widget.child,
  );
}

class _GlossaBinding extends InheritedWidget {
  const _GlossaBinding({
    required this.glossa,
    required this.generation,
    required super.child,
  });

  final Glossa glossa;
  final int generation;

  @override
  bool updateShouldNotify(_GlossaBinding old) =>
      generation != old.generation ||
      !identical(glossa.client, old.glossa.client) ||
      !identical(glossa.tagStyles, old.glossa.tagStyles);
}
