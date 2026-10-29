package glossa

// Locale negotiation, exposed for the control plane.
//
// Glossa's MCP `explain_delivery` tool answers "why does this locale
// resolve this way for this release" (RFC 0005 §7.3), and a second
// implementation of SPEC §4.1 and §4.2 on the server would be a second
// answer. So the server calls the runtime's own negotiation, on the
// release content it already holds, rather than reimplementing lookup
// and the fallback chain.

// LocaleSet is the part of a release manifest that decides which locale
// a request resolves to: the release's source locale, the locales it
// ships and its fallback graph (`*` is the default entry).
type LocaleSet struct {
	SourceLocale string
	Locales      []string
	Fallback     map[string][]string
}

// ResolveLocale negotiates the active locale for requested tags over a
// release's locales and returns its fallback chain.
//
// canonical is requested canonicalized (SPEC §4.1: malformed tags and
// duplicates dropped), active the locale RFC 4647 Lookup picks — the
// source locale when nothing matches — and chain the order a message is
// looked for in (SPEC §4.2), always ending at the source locale.
func ResolveLocale(s LocaleSet, requested []string) (canonical []string, active string, chain []string) {
	m := &manifest{SourceLocale: s.SourceLocale, Fallback: s.Fallback}
	for _, code := range s.Locales {
		m.Locales = append(m.Locales, localeEntry{Code: code})
	}
	canonical = canonicalizeAll(requested)
	active = m.negotiate(canonical)
	return canonical, active, m.chain(active)
}
