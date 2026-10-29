/// Glossa's Dart runtime: the delivery contract in `runtimes/SPEC.md`,
/// implemented in pure Dart so it works on the VM, in AOT and on the web.
///
/// It covers locale identity and negotiation (SPEC §4), message
/// resolution over the fallback graph (§4.3), the MessageFormat 2
/// interpreter over the precompiled data model (§5) with the shared
/// safe-tag contract for its markup ([partsToTree]), `explain()` and the
/// error channel (§6), and the loader: the §3 load order, SHA-256 artifact
/// integrity and Ed25519 manifest signatures over the RFC 8785 (JCS) form
/// (§1.3), and staged rollout (§1.4): the installation's cohort, the
/// candidate view, and the stable view when the candidate can't load.
///
/// Nothing here imports Flutter or `dart:io`. A host supplies a
/// [Transport] and a [ReleaseStore]; `package:glossa/io.dart` has both for
/// the Dart VM, Flutter mobile and Flutter desktop, and `glossa_flutter`
/// (`flutter/` next to this package) has the widgets.
///
/// ```dart
/// final glossa = GlossaClient(
///   edge: 'https://edge.example.com',
///   deliveryKey: 'pk_live_…',
///   locales: ['de-AT'],
///   publicKeys: [GlossaPublicKey.parse('k_2026a', '…')],
/// );
/// await glossa.ready;
/// print(glossa.t('cart.checkout')); // Zur Kassa
/// print(glossa.explain('cart.checkout').chain); // [de-AT, de, en]
/// ```
library;

export 'src/catalog.dart'
    show Catalog, Explanation, Localizer, Outcome, Source, Step;
export 'src/errors.dart' show ErrorChannel, ErrorType, GlossaError;
export 'src/format.dart'
    show FormatOptions, MessageFormatError, formatMessage, formatToParts;
export 'src/functions.dart'
    show
        FunctionContext,
        FunctionError,
        MessageFunction,
        MessageValue,
        builtins,
        pluralCategory;
export 'src/jcs.dart'
    show JcsException, canonicalizeJson, canonicalizeJsonValue;
export 'src/loader.dart' show BundledRelease, GlossaClient;
export 'src/locale.dart'
    show
        Direction,
        acceptLanguage,
        canonicalizeLocale,
        canonicalizeLocales,
        directionOf,
        fallbackChain,
        lookupLocale,
        resolveLocales,
        rtlScripts,
        truncations;
export 'src/manifest.dart'
    show
        Artifact,
        LocaleEntry,
        Manifest,
        ManifestSignature,
        ReleaseRef,
        SchemaException,
        artifactSchema,
        manifestSchema;
export 'src/markup.dart'
    show
        MarkupElement,
        MarkupNode,
        MarkupText,
        escapeHtml,
        partsToHtml,
        partsToTree,
        safeTags,
        voidTags;
export 'src/model.dart'
    show
        CatchallKey,
        Declaration,
        Expression,
        FunctionRef,
        InputDeclaration,
        Literal,
        LocalDeclaration,
        Markup,
        MarkupKind,
        Message,
        MessageModelException,
        Operand,
        Pattern,
        PatternElement,
        PatternMessage,
        SelectMessage,
        TextElement,
        VariableRef,
        Variant,
        VariantKey;
export 'src/parts.dart'
    show
        BidiIsolationPart,
        FallbackPart,
        MarkupPart,
        Part,
        TextPart,
        ValuePart,
        partsToString;
export 'src/rollout.dart' show RolloutInfo, RolloutSide, cohortOf;
export 'src/store.dart'
    show InstallationIdStore, MemoryReleaseStore, ReleaseStore, StoredManifest;
export 'src/subtags.g.dart' show subtagRegistryDate;
export 'src/transport.dart' show EdgeResponse, Transport;
export 'src/verify.dart'
    show
        GlossaPublicKey,
        sha256Hex,
        signatureAlgorithm,
        verifyManifestSignature;
