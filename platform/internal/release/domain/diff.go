package domain

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
)

// LocaleMessages is what a release ships for one locale: message ID
// (key) → canonical MF2 model, all namespaces merged.
type LocaleMessages map[string]json.RawMessage

// LocaleDiff is how one locale changed between two releases.
type LocaleDiff struct {
	Locale  string
	Added   []string
	Changed []string
	Removed []string
}

// Empty reports whether nothing changed.
func (d LocaleDiff) Empty() bool { return len(d.Added)+len(d.Changed)+len(d.Removed) == 0 }

// Diff compares what base and head ship, locale by locale. A locale only
// one of them lists counts as all added or all removed. Message IDs are
// sorted; locales come in head's order, then locales only base has.
func Diff(baseLocales, headLocales []string, base, head map[string]LocaleMessages) []LocaleDiff {
	var order []string
	seen := map[string]bool{}
	for _, l := range append(slices.Clone(headLocales), baseLocales...) {
		if !seen[l] {
			seen[l] = true
			order = append(order, l)
		}
	}
	out := make([]LocaleDiff, 0, len(order))
	for _, l := range order {
		b, h := base[l], head[l]
		d := LocaleDiff{Locale: l, Added: []string{}, Changed: []string{}, Removed: []string{}}
		for _, id := range slices.Sorted(maps.Keys(h)) {
			old, ok := b[id]
			switch {
			case !ok:
				d.Added = append(d.Added, id)
			case !bytes.Equal(old, h[id]):
				d.Changed = append(d.Changed, id)
			}
		}
		for _, id := range slices.Sorted(maps.Keys(b)) {
			if _, ok := h[id]; !ok {
				d.Removed = append(d.Removed, id)
			}
		}
		out = append(out, d)
	}
	return out
}
