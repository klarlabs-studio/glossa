/// The Flutter layer of the Glossa runtime.
///
/// The runtime itself is `package:glossa`, which is pure Dart and has no
/// Flutter import — that is what lets it be tested with `dart test`,
/// compiled for the web and used off Flutter (RFC 0005 §6.1). This
/// package is the only place `package:flutter` appears, and it adds four
/// things:
///
/// - [GlossaScope], which binds a `GlossaClient` to a widget subtree,
///   subscribes to the SPEC §6 error channel and refreshes on app resume;
/// - [GlossaText], which renders a message by id and turns its
///   MessageFormat 2 markup into styles instead of escaping it;
/// - [glossaSpan] and [GlossaTagStyles], the safe-tag contract
///   (`runtimes/testdata/markup.json`) as `InlineSpan`s, plus
///   [GlossaTagBuilder] for markup the *application* renders, such as a
///   tappable link;
/// - [loadBundledRelease], the asset-bundle layout SPEC §3 fixes.
///
/// `explain()` and the error channel are reached through [Glossa], which
/// [GlossaScope.of] returns; both are the core's, unchanged.
///
/// ```dart
/// final glossa = GlossaClient(
///   edge: 'https://edge.example.com',
///   deliveryKey: 'pk_live_…',
///   locales: ['de-AT'],
///   publicKeys: [GlossaPublicKey.parse('k_2026a', '…')],
///   transport: ioTransport(),
///   store: FileReleaseStore.scoped(
///     await getApplicationSupportDirectory(),
///     deliveryKey: 'pk_live_…',
///     environment: 'production',
///   ),
///   bundled: await loadBundledRelease(),
/// );
///
/// runApp(GlossaScope(client: glossa, child: const App()));
///
/// // anywhere below it
/// GlossaText('cart.items', values: {'n': 3});
/// GlossaScope.of(context).explain('cart.items').chain; // [de-AT, de, en]
/// ```
///
/// The core's own API — `GlossaClient`, `Part`, `Explanation`,
/// `GlossaError`, `partsToTree` — is re-exported, so an application
/// imports this one library.
library;

export 'package:glossa/glossa.dart';

export 'src/assets.dart' show defaultBundlePath, loadBundledRelease;
export 'src/scope.dart' show Glossa, GlossaScope;
export 'src/spans.dart' show glossaSpan, zeroWidthSpace;
export 'src/tag_styles.dart' show GlossaTagBuilder, GlossaTagStyles;
export 'src/text.dart' show GlossaText, glossaParts;
