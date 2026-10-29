package domain

import (
	"maps"
	"slices"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// DefaultChain is the fallback key for every locale without its own
// entry (runtimes/SPEC.md §1.1).
const DefaultChain = "*"

// FallbackGraph maps a locale to the locales it falls back to, in order
// (intent §39: fallback is a graph, not a default language). Entries may
// chain (de-AT → de-CH → de); "*" is the default chain. The graph is
// published as-is in release manifests.
//
// A valid graph names only the project's locales, never a locale in its
// own list twice or itself, and has no cycles through explicit edges —
// runtimes must survive cycles, but the platform never ships one.
type FallbackGraph struct {
	edges map[string][]bcp47.Tag
}

// NewFallbackGraph canonicalizes and validates raw against the
// project's locales. An empty list removes a key.
func NewFallbackGraph(raw map[string][]string, locales []bcp47.Tag) (FallbackGraph, error) {
	known := map[bcp47.Tag]bool{}
	for _, l := range locales {
		known[l] = true
	}
	g := FallbackGraph{edges: map[string][]bcp47.Tag{}}
	for _, rawKey := range slices.Sorted(maps.Keys(raw)) {
		key, err := fallbackKey(rawKey, known)
		if err != nil {
			return FallbackGraph{}, err
		}
		if _, dup := g.edges[key]; dup {
			return FallbackGraph{}, &FallbackError{Code: "fallback_duplicate", Locale: key, Detail: "two keys name the same locale"}
		}
		chain, err := fallbackChain(key, raw[rawKey], known)
		if err != nil {
			return FallbackGraph{}, err
		}
		if len(chain) > 0 {
			g.edges[key] = chain
		}
	}
	if err := g.checkAcyclic(); err != nil {
		return FallbackGraph{}, err
	}
	return g, nil
}

func fallbackKey(raw string, known map[bcp47.Tag]bool) (string, error) {
	if raw == DefaultChain {
		return DefaultChain, nil
	}
	tag, err := bcp47.Parse(raw)
	if err != nil {
		return "", &FallbackError{Code: "fallback_invalid_locale", Locale: raw, Detail: err.Error()}
	}
	if !known[tag] {
		return "", &FallbackError{Code: "fallback_unknown_locale", Locale: tag.String(), Detail: "not a locale of this project"}
	}
	return tag.String(), nil
}

func fallbackChain(key string, raw []string, known map[bcp47.Tag]bool) ([]bcp47.Tag, error) {
	var chain []bcp47.Tag
	for _, r := range raw {
		tag, err := bcp47.Parse(r)
		if err != nil {
			return nil, &FallbackError{Code: "fallback_invalid_locale", Locale: r, Detail: err.Error()}
		}
		switch {
		case !known[tag]:
			return nil, &FallbackError{Code: "fallback_unknown_locale", Locale: tag.String(), Detail: "not a locale of this project"}
		case tag.String() == key:
			return nil, &FallbackError{Code: "fallback_self_reference", Locale: key, Detail: "a locale can't fall back to itself"}
		case slices.Contains(chain, tag):
			return nil, &FallbackError{Code: "fallback_duplicate", Locale: tag.String(), Detail: "listed twice in the chain of " + key}
		}
		chain = append(chain, tag)
	}
	return chain, nil
}

// checkAcyclic walks the explicit edges depth-first with the usual
// three colors, iteratively over a bounded graph, so validation itself
// can't loop.
func (g FallbackGraph) checkAcyclic() error {
	const (
		unvisited = iota
		onPath
		done
	)
	color := map[string]int{}
	var visit func(node string) error
	visit = func(node string) error {
		color[node] = onPath
		for _, next := range g.edges[node] {
			switch color[next.String()] {
			case onPath:
				return &FallbackError{Code: "fallback_cycle", Locale: next.String(), Detail: "falls back to itself through " + node}
			case unvisited:
				if err := visit(next.String()); err != nil {
					return err
				}
			}
		}
		color[node] = done
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(g.edges)) {
		if key == DefaultChain || color[key] != unvisited {
			continue
		}
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

// Edges returns the graph as the manifest's `fallback` object.
func (g FallbackGraph) Edges() map[string][]string {
	out := make(map[string][]string, len(g.edges))
	for k, chain := range g.edges {
		s := make([]string, len(chain))
		for i, t := range chain {
			s[i] = t.String()
		}
		out[k] = s
	}
	return out
}

// Mentions reports whether code appears anywhere in the graph, as a key
// or in a chain.
func (g FallbackGraph) Mentions(code bcp47.Tag) bool {
	if _, ok := g.edges[code.String()]; ok {
		return true
	}
	for _, chain := range g.edges {
		if slices.Contains(chain, code) {
			return true
		}
	}
	return false
}

// Chain is the fallback chain a runtime builds for the active locale l
// (runtimes/SPEC.md §4.2): l; its explicit fallbacks, depth-first; if l
// has none, its truncations that are available; the "*" chain, expanded
// the same way; the source locale. Duplicates are dropped, and a repeat
// stops recursion, so even a cyclic graph terminates.
func (g FallbackGraph) Chain(l bcp47.Tag, available []bcp47.Tag, source bcp47.Tag) []bcp47.Tag {
	var out []bcp47.Tag
	seen := map[bcp47.Tag]bool{}
	add := func(t bcp47.Tag) bool {
		if seen[t] {
			return false
		}
		seen[t] = true
		out = append(out, t)
		return true
	}
	var expand func(key string)
	expand = func(key string) {
		for _, next := range g.edges[key] {
			if add(next) {
				expand(next.String())
			}
		}
	}
	add(l)
	if _, explicit := g.edges[l.String()]; explicit {
		expand(l.String())
	} else {
		for _, t := range l.Truncations() {
			if slices.Contains(available, t) {
				add(t)
			}
		}
	}
	expand(DefaultChain)
	if !source.IsZero() {
		add(source)
	}
	return out
}
