// Package cldr exposes the CLDR data the MessageFormat kernel needs.
//
// Plural categories come from github.com/agentable/go-intl, the same CLDR
// tables the formatting engine selects variants with, so structural checks
// and runtime selection always agree. (golang.org/x/text/feature/plural is
// generated from CLDR 32 and lacks categories added since, such as French
// and Spanish "many".)
package cldr

import (
	"fmt"
	"sync"

	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/pluralrules"
)

var categoryCache sync.Map // cacheKey -> []string

type cacheKey struct {
	locale  string
	ordinal bool
}

// PluralCategories returns the CLDR plural categories of locale, cardinal
// or ordinal, in CLDR order. The result is a fresh slice.
func PluralCategories(tag string, ordinal bool) ([]string, error) {
	key := cacheKey{tag, ordinal}
	if cached, ok := categoryCache.Load(key); ok {
		return append([]string(nil), cached.([]string)...), nil
	}
	cats, err := loadCategories(tag, ordinal)
	if err != nil {
		return nil, err
	}
	categoryCache.Store(key, cats)
	return append([]string(nil), cats...), nil
}

func loadCategories(tag string, ordinal bool) ([]string, error) {
	locales, err := locale.ParseList(tag)
	if err != nil {
		return nil, fmt.Errorf("cldr: locale %q: %w", tag, err)
	}
	typ := string(pluralrules.Cardinal)
	if ordinal {
		typ = string(pluralrules.Ordinal)
	}
	rules, err := pluralrules.New(locales, pluralrules.Options{Type: &typ})
	if err != nil {
		return nil, fmt.Errorf("cldr: plural rules for %q: %w", tag, err)
	}
	resolved := rules.ResolvedOptions().PluralCategories
	out := make([]string, len(resolved))
	for i, c := range resolved {
		out[i] = c.String()
	}
	return out, nil
}

// IsPluralCategory reports whether key is one of the six CLDR plural
// category names.
func IsPluralCategory(key string) bool {
	switch key {
	case "zero", "one", "two", "few", "many", "other":
		return true
	default:
		return false
	}
}
