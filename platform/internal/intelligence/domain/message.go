package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/pluralrules"
	mf "go.klarlabs.de/glossa/messageformat"
)

// PluralCategories returns locale's CLDR plural categories, cardinal or
// ordinal, in CLDR order. It reads the same CLDR tables (go-intl) as the
// MessageFormat kernel's CheckCompat, so the categories the prompt lists
// are exactly the ones validation accepts. (The kernel keeps its copy in
// an internal package; see the follow-up to export it.)
func PluralCategories(tag string, ordinal bool) ([]string, error) {
	locales, err := locale.ParseList(tag)
	if err != nil {
		return nil, fmt.Errorf("plural categories of %q: %w", tag, err)
	}
	typ := string(pluralrules.Cardinal)
	if ordinal {
		typ = string(pluralrules.Ordinal)
	}
	rules, err := pluralrules.New(locales, pluralrules.Options{Type: &typ})
	if err != nil {
		return nil, fmt.Errorf("plural categories of %q: %w", tag, err)
	}
	cats := rules.ResolvedOptions().PluralCategories
	out := make([]string, len(cats))
	for i, c := range cats {
		out[i] = c.String()
	}
	return out, nil
}

// PlainText is the literal text of every pattern of msg, placeholders and
// markup removed, patterns separated by newlines. Term recognition and
// terminology QA read it.
func PlainText(msg mf.Message) string {
	var parts []string
	for _, p := range msg.Patterns() {
		var b strings.Builder
		for _, el := range p {
			if t, ok := el.(mf.Text); ok {
				b.WriteString(string(t))
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" && !slices.Contains(parts, s) {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

// TextLength is the longest pattern's literal text in characters, the
// lower bound a max_length check can apply without formatting (same rule
// as Localization's max-length-exceeded finding).
func TextLength(msg mf.Message) int {
	longest := 0
	for _, p := range msg.Patterns() {
		n := 0
		for _, el := range p {
			if t, ok := el.(mf.Text); ok {
				n += len([]rune(string(t)))
			}
		}
		longest = max(longest, n)
	}
	return longest
}

// MarkupCount is the number of markup elements across msg's patterns.
func MarkupCount(msg mf.Message) int {
	n := 0
	for _, p := range msg.Patterns() {
		for _, el := range p {
			if _, ok := el.(mf.Markup); ok {
				n++
			}
		}
	}
	return n
}

// ErrUnparsable means a message is not valid MF2 syntax.
var ErrUnparsable = errors.New("intelligence: message is not valid MF2")

// ParseMessage parses and validates MF2 syntax and returns the model with
// its canonical syntax (the kernel's Stringify), so equal messages
// compare equal. Errors wrap ErrUnparsable and the kernel's *mf.Error.
func ParseMessage(src string) (mf.Message, string, error) {
	msg, err := mf.ParseMF2(src)
	if err != nil {
		return mf.Message{}, "", fmt.Errorf("%w: %w", ErrUnparsable, err)
	}
	canonical, err := mf.Stringify(msg)
	if err != nil {
		return mf.Message{}, "", fmt.Errorf("%w: %w", ErrUnparsable, err)
	}
	return msg, canonical, nil
}
