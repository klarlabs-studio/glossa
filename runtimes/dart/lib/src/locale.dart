/// Locale identity, negotiation and the fallback graph (`runtimes/SPEC.md`
/// §4).
///
/// Everything here is deterministic and side-effect free, so `explain()` can
/// reuse it without rendering anything.
library;

import 'manifest.dart';
import 'subtags.g.dart';

/// The base text direction of a locale, matching the HTML `dir` attribute.
enum Direction {
  /// Left to right.
  ltr,

  /// Right to left.
  rtl;

  /// The wire spelling used by the manifest and by `explain()`.
  @override
  String toString() => name;

  /// The direction named by [value], or null when it names neither.
  static Direction? parse(String? value) => switch (value) {
    'ltr' => Direction.ltr,
    'rtl' => Direction.rtl,
    _ => null,
  };
}

/// ISO 15924 scripts written right to left (Unicode bidi class R or AL).
///
/// Kept in sync with `runtimes/go/locale.go` (`rtlScripts`) and
/// `studio/src/lib/bcp47.ts` (`RTL_SCRIPTS`).
const Set<String> rtlScripts = {
  'Adlm',
  'Arab',
  'Aran',
  'Armi',
  'Avst',
  'Chrs',
  'Cprt',
  'Elym',
  'Hatr',
  'Hebr',
  'Hung',
  'Khar',
  'Lydi',
  'Mand',
  'Mani',
  'Mend',
  'Merc',
  'Mero',
  'Narb',
  'Nbat',
  'Nkoo',
  'Orkh',
  'Ougr',
  'Palm',
  'Phli',
  'Phlp',
  'Phnx',
  'Prti',
  'Rohg',
  'Samr',
  'Sarb',
  'Sogd',
  'Sogo',
  'Syrc',
  'Thaa',
  'Yezi',
};

/// Languages whose implied script is right to left, for tags that name no
/// script of their own (`ar` → rtl). Derived from the registry's
/// `Suppress-Script` entries at build time is not possible — suppression is
/// about Latin — so this is the CLDR likely-script answer for the RTL
/// languages a catalog realistically carries.
const Map<String, String> _likelyScript = {
  'ar': 'Arab',
  'arc': 'Armi',
  'az': 'Latn',
  'ckb': 'Arab',
  'dv': 'Thaa',
  'fa': 'Arab',
  'ff': 'Latn',
  'he': 'Hebr',
  'iw': 'Hebr',
  'ks': 'Arab',
  'ku': 'Latn',
  'nqo': 'Nkoo',
  'pa': 'Guru',
  'ps': 'Arab',
  'sd': 'Arab',
  'ug': 'Arab',
  'ur': 'Arab',
  'yi': 'Hebr',
};

final RegExp _alpha = RegExp(r'^[a-z]+$');
final RegExp _alphanum = RegExp(r'^[a-z0-9]+$');
final RegExp _digits3 = RegExp(r'^[0-9]{3}$');

/// A parsed, canonical BCP 47 tag.
class _Parsed {
  _Parsed(this.language, this.script, this.region, this.variants);

  final String language;
  final String? script;
  final String? region;
  final List<String> variants;

  @override
  String toString() => [language, ?script, ?region, ...variants].join('-');
}

/// Canonicalize a requested tag into the form the platform stores
/// (RFC 5646 §4.5: deprecated subtags replaced by their preferred values,
/// the `language-extlang` form collapsed, an implied script left out,
/// conventional casing; `_` accepted as a separator).
///
/// Extensions and private use are dropped: they carry formatting
/// preferences, not a distinct body of translations. Returns null for tags
/// that are malformed or name no language (`und`, `x-foo`, `*`).
String? canonicalizeLocale(String tag) {
  var raw = tag.trim().replaceAll('_', '-').toLowerCase();
  if (raw.isEmpty) return null;

  // Grandfathered and redundant tags are matched whole, before parsing.
  if (grandfathered.containsKey(raw)) {
    final preferred = grandfathered[raw];
    // An irregular tag the registry gives no replacement for (`i-default`,
    // `i-enochian`) can't name a body of translations.
    if (preferred == null) {
      if (raw.startsWith('i-')) return null;
    } else {
      raw = preferred;
    }
  }
  raw = redundantPreferred[raw] ?? raw;

  final parsed = _parse(raw);
  if (parsed == null) return null;
  return parsed.toString();
}

/// Parse and canonicalize a lowercase, `-`-separated tag.
_Parsed? _parse(String raw) {
  final subtags = raw.split('-');
  var i = 0;

  var language = subtags[i];
  // 4-letter and 5-to-8-letter primary language subtags are legal but
  // reserved or rarely used; accept them, reject anything else.
  if (!_alpha.hasMatch(language) ||
      language.length < 2 ||
      language.length > 8) {
    return null;
  }
  if (language == 'und' || language == 'mul' || language == 'zxx') return null;
  i++;

  // Up to three extlang subtags. The canonical form is the first extlang's
  // preferred value on its own (RFC 5646 §4.5).
  var extlangs = 0;
  while (i < subtags.length &&
      extlangs < 3 &&
      subtags[i].length == 3 &&
      _alpha.hasMatch(subtags[i]) &&
      extlangPreferred.containsKey(subtags[i])) {
    if (extlangs == 0) language = extlangPreferred[subtags[i]]!;
    extlangs++;
    i++;
  }
  if (extlangs == 0) language = languagePreferred[language] ?? language;

  String? script;
  if (i < subtags.length &&
      subtags[i].length == 4 &&
      _alpha.hasMatch(subtags[i])) {
    script = subtags[i];
    i++;
  }

  String? region;
  if (i < subtags.length &&
      ((subtags[i].length == 2 && _alpha.hasMatch(subtags[i])) ||
          _digits3.hasMatch(subtags[i]))) {
    region = regionPreferred[subtags[i]] ?? subtags[i];
    i++;
  }

  final variants = <String>[];
  while (i < subtags.length) {
    final s = subtags[i];
    final isVariant =
        (s.length >= 5 && s.length <= 8 && _alphanum.hasMatch(s)) ||
        (s.length == 4 && RegExp(r'^[0-9][a-z0-9]{3}$').hasMatch(s));
    if (!isVariant) break;
    final preferred = variantPreferred[s] ?? s;
    if (!variants.contains(preferred)) variants.add(preferred);
    i++;
  }

  // A singleton subtag starts an extension (`-u-`, `-t-`) or private use
  // (`-x-`): everything from here is dropped. Anything else is malformed.
  if (i < subtags.length && subtags[i].length != 1) return null;

  // A region-only correction can make the language deprecated again
  // (`sgn-br` → `bzs`); the registry's redundant table already covered the
  // whole-tag cases, so one pass is enough here.
  if (script != null && suppressScript[language] == script) script = null;

  return _Parsed(
    language,
    script == null ? null : script[0].toUpperCase() + script.substring(1),
    region?.toUpperCase(),
    variants,
  );
}

/// Canonicalize [tags] in order, dropping malformed tags and repeats.
List<String> canonicalizeLocales(Iterable<String> tags) {
  final out = <String>[];
  for (final tag in tags) {
    final c = canonicalizeLocale(tag);
    if (c != null && !out.contains(c)) out.add(c);
  }
  return out;
}

/// The RFC 4647 §3.4 lookup fallbacks of [tag], most specific first and
/// excluding [tag] itself: `zh-Hant-TW` → `zh-Hant`, `zh`. A trailing
/// single-character subtag is dropped together with the subtag before it.
List<String> truncations(String tag) {
  final subtags = tag.split('-');
  final out = <String>[];
  for (var n = subtags.length - 1; n > 0; n--) {
    if (subtags[n - 1].length == 1) continue;
    out.add(subtags.sublist(0, n).join('-'));
  }
  return out;
}

/// RFC 4647 §3.4 *Lookup* of canonical [requested] tags over [available]:
/// each requested tag in order, then its truncations. Returns null when
/// nothing matches.
String? lookupLocale(List<String> requested, List<String> available) {
  final set = available.toSet();
  for (final tag in requested) {
    if (set.contains(tag)) return tag;
    for (final t in truncations(tag)) {
      if (set.contains(t)) return t;
    }
  }
  return null;
}

/// The fallback chain for the active [locale] (SPEC §4.2): the locale, its
/// explicit edges depth-first (or, without an entry, its available
/// truncations), then the `"*"` chain, then the source locale.
///
/// Repeats are dropped, which is also what stops cycles.
List<String> fallbackChain(String locale, Manifest manifest) {
  final fallback = manifest.fallback;
  final chain = <String>[];

  bool add(String l) {
    if (chain.contains(l)) return false;
    chain.add(l);
    return true;
  }

  void expand(String l) {
    for (final f in fallback[l] ?? const <String>[]) {
      if (add(f)) expand(f);
    }
  }

  add(locale);
  if (fallback.containsKey(locale)) {
    expand(locale);
  } else {
    final available = manifest.localeCodes.toSet();
    for (final t in truncations(locale)) {
      if (available.contains(t)) add(t);
    }
  }
  expand('*');
  add(manifest.sourceLocale);
  return chain;
}

/// The text direction of [tag] from its explicit or likely script. It is the
/// fallback for a locale the manifest names no direction for.
Direction directionOf(String tag) {
  final canonical = canonicalizeLocale(tag);
  if (canonical == null) return Direction.ltr;
  final subtags = canonical.split('-');
  final script = subtags.length > 1 && subtags[1].length == 4
      ? subtags[1]
      : _likelyScript[subtags.first];
  return script != null && rtlScripts.contains(script)
      ? Direction.rtl
      : Direction.ltr;
}

/// Run a resolver chain (intent §13) in priority order — explicit, user
/// preference, organization preference, request metadata,
/// `Accept-Language` — and return the requested locales, canonicalized.
///
/// Each entry is a tag, a list of tags, or null. The manifest's source
/// locale is the implicit last step, applied by the catalog.
List<String> resolveLocales(List<Object?> resolvers) {
  final out = <String>[];
  for (final r in resolvers) {
    switch (r) {
      case null:
        continue;
      case final String tag:
        out.add(tag);
      case final Iterable<String> tags:
        out.addAll(tags);
      default:
        continue;
    }
  }
  return canonicalizeLocales(out);
}

/// The tags of an `Accept-Language` header, by quality, header order
/// breaking ties. `*` and `q=0` are dropped.
List<String> acceptLanguage(String? header) {
  if (header == null || header.trim().isEmpty) return const [];
  final entries = <({String tag, double q, int i})>[];
  final items = header.split(',');
  for (var i = 0; i < items.length; i++) {
    final parts = items[i].split(';').map((s) => s.trim()).toList();
    final tag = parts.first;
    if (tag.isEmpty || tag == '*') continue;
    var q = 1.0;
    for (final p in parts.skip(1)) {
      if (p.startsWith('q=')) q = double.tryParse(p.substring(2)) ?? 1.0;
    }
    if (q <= 0) continue;
    entries.add((tag: tag, q: q, i: i));
  }
  entries.sort((a, b) => a.q == b.q ? a.i.compareTo(b.i) : b.q.compareTo(a.q));
  return [for (final e in entries) e.tag];
}
