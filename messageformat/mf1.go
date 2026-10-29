package messageformat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kaptinlin/messageformat-go/mf1"
	"go.klarlabs.de/glossa/messageformat/internal/cldr"
	"golang.org/x/text/language"
)

// ICU MessageFormat 1 -> canonical MF2 conversion.
//
// The approach follows mf1ToMessageData of @messageformat/icu-messageformat-1
// (nested selects are lifted into one top-level .match), with one Glossa
// decision: MF1 constructs map onto standard MF2 functions wherever those
// reproduce the MF1 output, so runtimes only need the standard function
// set. testdata/glossa/README.md lists the rules; testdata/glossa/
// mf1-to-mf2.json proves them against the reference implementations.

// maxVariants bounds the cartesian product of lifted selectors, so a
// pathological message cannot exhaust memory.
const maxVariants = 4096

// ParseMF1 parses ICU MessageFormat 1 source and converts it to the
// canonical MF2 model. locale (a BCP 47 tag) decides which plural keys are
// valid, as in the MF1 reference implementation.
//
// Errors: CodeInvalidLocale, CodeMF1SyntaxError for source that does not
// parse (including plural keys the locale doesn't have), and
// CodeMF1Unsupported for valid MF1 that has no MF2 representation.
func ParseMF1(src, locale string) (msg Message, err error) {
	defer containEngineFailure(&err)
	return parseMF1(src, locale)
}

func parseMF1(src, locale string) (Message, error) {
	opts, err := mf1ParseOptions(locale)
	if err != nil {
		return Message{}, err
	}
	tokens, err := mf1.Parse(src, opts)
	if err != nil {
		return Message{}, &Error{Code: CodeMF1SyntaxError, Message: err.Error()}
	}
	msg, err := newMF1Converter(tokens).convert()
	if err != nil {
		return Message{}, &Error{Code: CodeMF1Unsupported, Message: err.Error()}
	}
	if err := Validate(msg); err != nil {
		return Message{}, &Error{Code: CodeMF1Unsupported, Message: fmt.Sprintf("converted message is not valid MF2: %v", err)}
	}
	return msg, nil
}

func mf1ParseOptions(locale string) (*mf1.ParseOptions, error) {
	if _, err := language.Parse(locale); err != nil {
		return nil, &Error{Code: CodeInvalidLocale, Message: fmt.Sprintf("%q: %v", locale, err)}
	}
	cardinal, err := cldr.PluralCategories(locale, false)
	if err != nil {
		return nil, &Error{Code: CodeInvalidLocale, Message: err.Error()}
	}
	ordinal, err := cldr.PluralCategories(locale, true)
	if err != nil {
		return nil, &Error{Code: CodeInvalidLocale, Message: err.Error()}
	}
	return &mf1.ParseOptions{Cardinal: asMF1Categories(cardinal), Ordinal: asMF1Categories(ordinal)}, nil
}

func asMF1Categories(cats []string) []mf1.PluralCategory {
	out := make([]mf1.PluralCategory, len(cats))
	for i, c := range cats {
		out[i] = mf1.PluralCategory(c)
	}
	return out
}

// --- selector analysis --------------------------------------------------------

type mf1SelectKind string

const (
	mf1Select        mf1SelectKind = "select"
	mf1Plural        mf1SelectKind = "plural"
	mf1SelectOrdinal mf1SelectKind = "selectordinal"
)

// mf1Selector is one MF1 argument used as a selector, with every key it is
// matched against anywhere in the message.
type mf1Selector struct {
	name   string
	kind   mf1SelectKind
	offset int
	exact  []string // plural `=n` keys as MF2 numeric literals
	keys   []string // select keys or plural categories, without `other`

	displayVar string // what `#` shows: the variable, or its :offset local
	exactCol   int    // column of exact keys (the variable itself), or -1
	keyCol     int    // column of the other keys, or -1
}

func (s *mf1Selector) isPlural() bool { return s.kind != mf1Select }

type mf1Converter struct {
	tokens    []mf1.Token
	selectors []*mf1Selector
	byName    map[string]*mf1Selector
	names     map[string]bool // every variable name in the message
	columns   []column
	variants  []Variant
}

// column is one MF2 selector of the lifted .match.
type column struct {
	variable string
	keys     []string // literal keys in order; the catch-all is implied last
}

func newMF1Converter(tokens []mf1.Token) *mf1Converter {
	return &mf1Converter{tokens: tokens, byName: map[string]*mf1Selector{}, names: map[string]bool{}}
}

func (c *mf1Converter) convert() (Message, error) {
	if err := c.collect(c.tokens); err != nil {
		return Message{}, err
	}
	if len(c.selectors) == 0 {
		pattern := Pattern{}
		for _, tok := range c.tokens {
			el, err := c.leaf(tok, nil)
			if err != nil {
				return Message{}, err
			}
			pattern = appendElement(pattern, el)
		}
		return Message{Type: PatternMessageType, Pattern: pattern}, nil
	}
	decls := c.planColumns()
	if err := c.buildVariants(); err != nil {
		return Message{}, err
	}
	if err := c.addParts(c.tokens, nil, nil); err != nil {
		return Message{}, err
	}
	selectors := make([]VariableRef, len(c.columns))
	for i, col := range c.columns {
		selectors[i] = VariableRef{Name: col.variable}
	}
	return Message{Type: SelectMessageType, Declarations: decls, Selectors: selectors, Variants: c.variants}, nil
}

// collect records every selector (depth first, in source order) and every
// variable name.
func (c *mf1Converter) collect(tokens []mf1.Token) error {
	for _, tok := range tokens {
		switch t := tok.(type) {
		case *mf1.PlainArg:
			c.names[t.Arg] = true
		case *mf1.FunctionArg:
			c.names[t.Arg] = true
		case *mf1.Select:
			c.names[t.Arg] = true
			if err := c.addSelector(t); err != nil {
				return err
			}
			for _, sc := range t.Cases {
				if err := c.collect(sc.Tokens); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *mf1Converter) addSelector(t *mf1.Select) error {
	kind := mf1SelectKind(t.Type)
	offset := 0
	if t.PluralOffset != nil {
		offset = *t.PluralOffset
	}
	sel, seen := c.byName[t.Arg]
	if !seen {
		sel = &mf1Selector{name: t.Arg, kind: kind, offset: offset, exactCol: -1, keyCol: -1}
		c.byName[t.Arg] = sel
		c.selectors = append(c.selectors, sel)
	} else if sel.kind != kind || sel.offset != offset {
		return fmt.Errorf("argument %q is used both as %s and as %s (offset %d vs %d); MF2 needs one declaration per variable",
			t.Arg, sel.kind, kind, sel.offset, offset)
	}
	for _, sc := range t.Cases {
		switch {
		case sc.Key == "other":
		case sel.isPlural() && strings.HasPrefix(sc.Key, "="):
			sel.exact = appendUnique(sel.exact, normalizeExactKey(sc.Key[1:]))
		default:
			sel.keys = appendUnique(sel.keys, sc.Key)
		}
	}
	return nil
}

// normalizeExactKey turns the digits of an `=n` key into the MF2 numeric
// literal that :number matches exactly (`=01` -> `1`).
func normalizeExactKey(digits string) string {
	if n, err := strconv.Atoi(digits); err == nil {
		return strconv.Itoa(n)
	}
	return digits
}

// planColumns assigns MF2 selector columns and returns the declarations.
func (c *mf1Converter) planColumns() []Declaration {
	var decls []Declaration
	for _, s := range c.selectors {
		decls = append(decls, inputDeclaration(s))
		s.displayVar = s.name
		if !s.isPlural() || s.offset == 0 {
			s.keyCol = c.addColumn(s.name, append(append([]string{}, s.exact...), s.keys...))
			s.exactCol = s.keyCol
			continue
		}
		local := c.uniqueName(fmt.Sprintf("%s_minus_%d", s.name, s.offset))
		s.displayVar = local
		decls = append(decls, Declaration{
			Type: LocalDeclaration,
			Name: local,
			Value: Expression{
				Arg:      VariableRef{Name: s.name},
				Function: &FunctionRef{Name: "offset", Options: Options{"subtract": Literal{Value: strconv.Itoa(s.offset)}}},
			},
		})
		if len(s.exact) > 0 {
			s.exactCol = c.addColumn(s.name, s.exact)
		}
		if len(s.keys) > 0 || len(s.exact) == 0 {
			s.keyCol = c.addColumn(local, s.keys)
		}
	}
	return decls
}

func inputDeclaration(s *mf1Selector) Declaration {
	fn := &FunctionRef{Name: "number"}
	switch s.kind {
	case mf1Select:
		fn = &FunctionRef{Name: "string"}
	case mf1SelectOrdinal:
		fn.Options = Options{"select": Literal{Value: "ordinal"}}
	}
	return Declaration{Type: InputDeclaration, Name: s.name, Value: Expression{Arg: VariableRef{Name: s.name}, Function: fn}}
}

func (c *mf1Converter) addColumn(variable string, keys []string) int {
	c.columns = append(c.columns, column{variable: variable, keys: keys})
	return len(c.columns) - 1
}

func (c *mf1Converter) uniqueName(base string) string {
	name := base
	for i := 2; c.names[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	c.names[name] = true
	return name
}

// buildVariants creates the cartesian product of all column keys, first
// column outermost, each column's catch-all last.
func (c *mf1Converter) buildVariants() error {
	total := 1
	for _, col := range c.columns {
		total *= len(col.keys) + 1
		if total > maxVariants {
			return fmt.Errorf("the lifted message would need more than %d variants", maxVariants)
		}
	}
	keys := [][]VariantKey{{}}
	for _, col := range c.columns {
		options := make([]VariantKey, 0, len(col.keys)+1)
		for _, k := range col.keys {
			options = append(options, VariantKey{Value: k})
		}
		options = append(options, VariantKey{Catchall: true})
		next := make([][]VariantKey, 0, len(keys)*len(options))
		for _, prefix := range keys {
			for _, k := range options {
				next = append(next, append(append([]VariantKey{}, prefix...), k))
			}
		}
		keys = next
	}
	c.variants = make([]Variant, len(keys))
	for i, k := range keys {
		c.variants[i] = Variant{Keys: k, Value: Pattern{}}
	}
	return nil
}

// --- pattern construction -----------------------------------------------------

// constraint restricts the variants a token belongs to: column col must
// have key (the empty string for the catch-all).
type constraint struct {
	col int
	key string
}

// addParts appends every leaf token to each variant that satisfies filter.
// plural is the innermost enclosing plural, which `#` refers to.
func (c *mf1Converter) addParts(tokens []mf1.Token, plural *mf1Selector, filter []constraint) error {
	for _, tok := range tokens {
		sel, ok := tok.(*mf1.Select)
		if !ok {
			el, err := c.leaf(tok, plural)
			if err != nil {
				return err
			}
			c.appendMatching(el, filter)
			continue
		}
		s := c.byName[sel.Arg]
		inner := plural
		if s.isPlural() {
			inner = s
		}
		for _, sc := range sel.Cases {
			cons := append(append([]constraint{}, filter...), caseConstraints(s, sc.Key)...)
			if err := c.addParts(sc.Tokens, inner, cons); err != nil {
				return err
			}
		}
	}
	return nil
}

// caseConstraints maps one MF1 case key to column constraints.
func caseConstraints(s *mf1Selector, key string) []constraint {
	switch {
	case s.isPlural() && strings.HasPrefix(key, "="):
		return []constraint{{s.exactCol, normalizeExactKey(key[1:])}}
	case key == "other":
		var out []constraint
		if s.exactCol >= 0 {
			out = append(out, constraint{s.exactCol, ""})
		}
		if s.keyCol >= 0 && s.keyCol != s.exactCol {
			out = append(out, constraint{s.keyCol, ""})
		}
		return out
	default:
		out := []constraint{{s.keyCol, key}}
		if s.exactCol >= 0 && s.exactCol != s.keyCol {
			out = append(out, constraint{s.exactCol, ""})
		}
		return out
	}
}

func (c *mf1Converter) appendMatching(el PatternElement, filter []constraint) {
	for i := range c.variants {
		v := &c.variants[i]
		if variantMatches(v.Keys, filter) {
			v.Value = appendElement(v.Value, clonePatternElement(el))
		}
	}
}

func variantMatches(keys []VariantKey, filter []constraint) bool {
	for _, f := range filter {
		k := keys[f.col]
		if f.key == "" {
			if !k.Catchall {
				return false
			}
		} else if k.Catchall || k.Value != f.key {
			return false
		}
	}
	return true
}

// leaf converts a non-select token to a pattern element.
func (c *mf1Converter) leaf(tok mf1.Token, plural *mf1Selector) (PatternElement, error) {
	switch t := tok.(type) {
	case *mf1.Content:
		return Text(t.Value), nil
	case *mf1.PlainArg:
		return Expression{Arg: VariableRef{Name: t.Arg}}, nil
	case *mf1.FunctionArg:
		return convertFunctionArg(t)
	case *mf1.Octothorpe:
		if plural == nil {
			return Text("#"), nil
		}
		return Expression{Arg: VariableRef{Name: plural.displayVar}}, nil
	default:
		return nil, fmt.Errorf("unsupported MF1 token %q", tok.GetType())
	}
}

// appendElement appends el to p, merging adjacent text.
func appendElement(p Pattern, el PatternElement) Pattern {
	if text, ok := el.(Text); ok && len(p) > 0 {
		if last, ok := p[len(p)-1].(Text); ok {
			p[len(p)-1] = last + text
			return p
		}
	}
	return append(p, el)
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
