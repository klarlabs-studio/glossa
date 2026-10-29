// Package codegen writes typed message accessors (product intent §10,
// RFC 0002 §8) from the catalog's argument metadata
// (messageformat.Arguments): a TypeScript module
// (messages.checkout.pay({ amount })), its registration for @klarlabs-studio/glossa/vue,
// and Go functions (msg.For(l).CheckoutPay(amount)). A missing or
// mistyped argument then fails at compile time.
//
// Output is deterministic (sorted by message ID) so it can be committed
// and checked for staleness in CI.
package codegen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	mf "go.klarlabs.de/glossa/messageformat"
)

// Entry is one message to generate an accessor for.
type Entry struct {
	Key         string
	Text        string
	Description string
	Arguments   []mf.Argument
}

// Warning is a message that got no accessor (it stays reachable by ID).
type Warning struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

func sortEntries(entries []Entry) []Entry {
	out := append([]Entry(nil), entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// words splits a key segment or argument name into words.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func title(w string) string {
	if w == "" {
		return w
	}
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// camel turns "payment_failed" into "paymentFailed".
func camel(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return "_"
	}
	var b strings.Builder
	b.WriteString(ws[0])
	for _, w := range ws[1:] {
		b.WriteString(title(w))
	}
	out := b.String()
	if unicode.IsDigit([]rune(out)[0]) {
		out = "_" + out
	}
	return out
}

// pascal turns "checkout.payment_failed" into "CheckoutPaymentFailed".
func pascal(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		b.WriteString(title(w))
	}
	out := b.String()
	if out == "" || unicode.IsDigit([]rune(out)[0]) {
		out = "M" + out
	}
	return out
}

var jsIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// tsKey quotes an object key when it isn't an identifier.
func tsKey(name string) string {
	if jsIdent.MatchString(name) {
		return name
	}
	return quote(name)
}

func quote(s string) string {
	return fmt.Sprintf("%q", s)
}

// comment makes text safe inside /* */ and // comments, on one line.
func comment(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "*/", "*\\/")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return s
}

// TSNames returns the accessor path of every key (checkout.pay →
// checkout.pay; payment_failed → paymentFailed), as `extract` needs to
// map typed calls back to IDs.
func TSNames(keys []string) map[string]string {
	names, _ := tsPaths(keys)
	out := map[string]string{}
	for k, segs := range names {
		out[strings.Join(segs, ".")] = k
	}
	return out
}

// tsPaths assigns each key its accessor path; keys whose path collides
// with another key's, or that are both a message and a group, get none.
func tsPaths(keys []string) (map[string][]string, []Warning) {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	paths := map[string][]string{}
	taken := map[string]string{} // path → key
	var warnings []Warning
	for _, k := range sorted {
		var segs []string
		for _, s := range strings.Split(k, ".") {
			segs = append(segs, camel(s))
		}
		p := strings.Join(segs, ".")
		if other, ok := taken[p]; ok {
			warnings = append(warnings, Warning{Key: k, Reason: fmt.Sprintf("its accessor messages.%s would collide with %s", p, other)})
			continue
		}
		taken[p] = k
		paths[k] = segs
	}
	// A path can't be a function and an object at once.
	for k, segs := range paths {
		for i := 1; i < len(segs); i++ {
			prefix := strings.Join(segs[:i], ".")
			if other, ok := taken[prefix]; ok && paths[other] != nil {
				warnings = append(warnings, Warning{Key: other, Reason: fmt.Sprintf("messages.%s is also the group of %s", prefix, k)})
				delete(paths, other)
			}
		}
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Key < warnings[j].Key })
	return paths, dedupe(warnings)
}

func dedupe(ws []Warning) []Warning {
	var out []Warning
	for i, w := range ws {
		if i > 0 && ws[i-1].Key == w.Key {
			continue
		}
		out = append(out, w)
	}
	return out
}

// GoNames maps each Go accessor name to its key.
func GoNames(keys []string) map[string]string {
	names, _ := goNames(keys)
	out := map[string]string{}
	for k, n := range names {
		out[n] = k
	}
	return out
}

func goNames(keys []string) (map[string]string, []Warning) {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	names := map[string]string{}
	taken := map[string]string{}
	var warnings []Warning
	for _, k := range sorted {
		n := pascal(k)
		if other, ok := taken[n]; ok {
			warnings = append(warnings, Warning{Key: k, Reason: fmt.Sprintf("its Go accessor %s would collide with %s", n, other)})
			continue
		}
		if reservedGo[n] {
			n += "Message"
		}
		taken[n] = k
		names[k] = n
	}
	return names, warnings
}

// reservedGo are names the generated Go file declares itself.
var reservedGo = map[string]bool{"For": true, "FromContext": true, "Messages": true, "Localizer": true, "Contextual": true}
