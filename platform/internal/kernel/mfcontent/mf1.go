package mfcontent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// ErrNoMF1 means a message has no ICU MessageFormat 1 form: it uses
// something only MF2 expresses (markup, literal placeholders, options
// MF1 has no style for, variants that are not a full MF1 nesting).
var ErrNoMF1 = errors.New("mfcontent: MF1 can't express this message")

// RenderMF1 writes a canonical model as ICU MessageFormat 1 — the
// inverse of the MF1 conversion (messageformat/testdata/glossa/README.md
// lists its rules). The result is proven: it must parse back, with the
// MessageFormat kernel, to exactly m for locale; anything else answers
// ErrNoMF1, so callers fall back to MF2.
func RenderMF1(m mf.Message, locale bcp47.Tag) (string, error) {
	r := mf1Renderer{selectors: map[string]string{}}
	text, err := r.render(m)
	if err != nil {
		return "", ErrNoMF1
	}
	back, err := mf.ParseMF1(text, locale.String())
	if err != nil || !sameJSON(back, m) {
		return "", ErrNoMF1
	}
	return text, nil
}

// Render writes m in syntax: MF2 always, MF1 when it can express m
// (ErrNoMF1 otherwise).
func Render(m mf.Message, syntax Syntax, locale bcp47.Tag) (string, error) {
	switch syntax {
	case MF2:
		return mf.Stringify(m)
	case MF1:
		return RenderMF1(m, locale)
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidSyntax, syntax)
}

func sameJSON(a, b mf.Message) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// mf1Renderer knows each selector variable's MF1 kind.
type mf1Renderer struct {
	selectors map[string]string // variable → plural, selectordinal or select
}

func (r mf1Renderer) render(m mf.Message) (string, error) {
	if !m.IsSelect() {
		if len(m.Declarations) > 0 {
			return "", ErrNoMF1
		}
		return r.pattern(m.Pattern, false)
	}
	for _, d := range m.Declarations {
		kind, ok := selectorKind(d)
		if !ok {
			return "", ErrNoMF1
		}
		r.selectors[d.Name] = kind
	}
	for _, s := range m.Selectors {
		if _, ok := r.selectors[s.Name]; !ok {
			return "", ErrNoMF1
		}
	}
	return r.variants(m.Selectors, m.Variants, false)
}

// selectorKind maps a selector's .input declaration to its MF1 kind.
func selectorKind(d mf.Declaration) (string, bool) {
	arg, ok := d.Value.Arg.(mf.VariableRef)
	fn := d.Value.Function
	if d.Type != mf.InputDeclaration || !ok || arg.Name != d.Name || fn == nil || len(d.Value.Attributes) > 0 {
		return "", false
	}
	switch {
	case fn.Name == "string" && len(fn.Options) == 0:
		return "select", true
	case fn.Name == "number" && len(fn.Options) == 0:
		return "plural", true
	case fn.Name == "number" && len(fn.Options) == 1 && literal(fn.Options["select"]) == "ordinal":
		return "selectordinal", true
	}
	return "", false
}

// variants nests the selectors in order: the first selector's keys
// outermost, each key's variants rendered for the remaining selectors.
func (r mf1Renderer) variants(selectors []mf.VariableRef, vs []mf.Variant, inPlural bool) (string, error) {
	if len(selectors) == 0 {
		if len(vs) != 1 {
			return "", ErrNoMF1
		}
		return r.pattern(vs[0].Value, inPlural)
	}
	sel, kind := selectors[0].Name, r.selectors[selectors[0].Name]
	var (
		order  []string
		groups = map[string][]mf.Variant{}
	)
	for _, v := range vs {
		if len(v.Keys) != len(selectors) {
			return "", ErrNoMF1
		}
		key, err := caseKey(v.Keys[0], kind)
		if err != nil {
			return "", err
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], mf.Variant{Keys: v.Keys[1:], Value: v.Value})
	}
	var b strings.Builder
	b.WriteString("{" + sel + ", " + kind + ",")
	for _, key := range order {
		body, err := r.variants(selectors[1:], groups[key], inPlural || kind != "select")
		if err != nil {
			return "", err
		}
		b.WriteString(" " + key + " {" + body + "}")
	}
	b.WriteString("}")
	return b.String(), nil
}

// caseKey is a variant key as an MF1 case: other for the catch-all, =n
// for a number of a plural.
func caseKey(k mf.VariantKey, kind string) (string, error) {
	switch {
	case k.Catchall:
		return "other", nil
	case k.Value == "other" || k.Value == "":
		return "", ErrNoMF1 // only the catch-all is other in MF1
	case kind != "select":
		if _, err := strconv.Atoi(k.Value); err == nil {
			return "=" + k.Value, nil
		}
	}
	if strings.ContainsAny(k.Value, "{}' \t\n") {
		return "", ErrNoMF1
	}
	return k.Value, nil
}

func (r mf1Renderer) pattern(p mf.Pattern, inPlural bool) (string, error) {
	var b strings.Builder
	for _, el := range p {
		switch el := el.(type) {
		case mf.Text:
			b.WriteString(escapeMF1(string(el), inPlural))
		case mf.Expression:
			s, err := expressionMF1(el)
			if err != nil {
				return "", err
			}
			b.WriteString(s)
		default:
			return "", ErrNoMF1 // markup is MF2 only
		}
	}
	return b.String(), nil
}

// expressionMF1 writes a placeholder as an MF1 argument: plain, or with
// the MF1 type and style the MF2 function stands for.
func expressionMF1(e mf.Expression) (string, error) {
	v, ok := e.Arg.(mf.VariableRef)
	if !ok {
		return "", ErrNoMF1
	}
	if e.Function == nil {
		if len(e.Attributes) > 0 {
			return "", ErrNoMF1
		}
		return "{" + v.Name + "}", nil
	}
	if typ := literalAttr(e.Attributes, "mf1:argType"); typ != "" {
		// A fallback (:mf1:<type>) keeps its MF1 type and style.
		return argument(v.Name, typ, literalAttr(e.Attributes, "mf1:argStyle")), nil
	}
	if len(e.Attributes) > 0 {
		return "", ErrNoMF1
	}
	typ, style, ok := standardStyle(*e.Function)
	if !ok {
		return "", ErrNoMF1
	}
	return argument(v.Name, typ, style), nil
}

func argument(name, typ, style string) string {
	if style == "" {
		return "{" + name + ", " + typ + "}"
	}
	return "{" + name + ", " + typ + ", " + style + "}"
}

// standardStyle maps a standard MF2 function to the MF1 type and style
// that convert to it.
func standardStyle(fn mf.FunctionRef) (typ, style string, ok bool) {
	opts := map[string]string{}
	for k, v := range fn.Options {
		lit, isLit := v.(mf.Literal)
		if !isLit {
			return "", "", false
		}
		opts[k] = lit.Value
	}
	switch fn.Name {
	case "number":
		return "number", "", len(opts) == 0
	case "integer":
		return "number", "integer", len(opts) == 0
	case "percent":
		return "number", "percent", len(opts) == 0
	case "currency":
		return "number", "::currency/" + opts["currency"], len(opts) == 1 && opts["currency"] != ""
	case "date":
		return dateStyle(opts)
	case "time":
		return timeStyle(opts)
	}
	return "", "", false
}

func dateStyle(opts map[string]string) (string, string, bool) {
	switch {
	case len(opts) == 0:
		return "date", "", true
	case len(opts) == 1 && (opts["length"] == "short" || opts["length"] == "medium" || opts["length"] == "long"):
		return "date", opts["length"], true
	case len(opts) == 2 && opts["length"] == "long" && opts["fields"] == "year-month-day-weekday":
		return "date", "full", true
	}
	return "", "", false
}

func timeStyle(opts map[string]string) (string, string, bool) {
	switch {
	case len(opts) == 1 && opts["precision"] == "second":
		return "time", "", true
	case len(opts) == 1 && opts["precision"] == "minute":
		return "time", "short", true
	case len(opts) == 2 && opts["precision"] == "second" && opts["timeZoneStyle"] == "short":
		return "time", "long", true
	}
	return "", "", false
}

func literal(o mf.Operand) string {
	if l, ok := o.(mf.Literal); ok {
		return l.Value
	}
	return ""
}

func literalAttr(a mf.Attributes, name string) string {
	if l := a[name]; l != nil {
		return l.Value
	}
	return ""
}

// escapeMF1 quotes MF1 syntax in text: an apostrophe doubles, a run of
// braces (and, inside a plural, of #) becomes one quoted literal.
func escapeMF1(s string, inPlural bool) string {
	special := func(c byte) bool { return c == '{' || c == '}' || (inPlural && c == '#') }
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] == '\'':
			b.WriteString("''")
			i++
		case special(s[i]):
			j := i
			for j < len(s) && special(s[j]) {
				j++
			}
			b.WriteString("'" + s[i:j] + "'")
			i = j
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}
