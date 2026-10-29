package po

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// cldrOrder is the order of CLDR plural categories.
var cldrOrder = []string{"zero", "one", "two", "few", "many", "other"}

// samples are the counts that decide which gettext form each CLDR
// category takes. The powers of ten reach the "many" of French, Spanish
// and Italian (1000000).
var samples = func() []int64 {
	var out []int64
	for n := int64(0); n <= 1000; n++ {
		out = append(out, n)
	}
	return append(out, 10000, 100000, 1000000)
}()

// smallSamples bound the count-based fallback, which must not see the
// "many" of large numbers that gettext's formulas fold into "other".
const smallSamples = 1001

// categoryCache holds each locale's category for every sample.
var categoryCache sync.Map // string → []string

// categories returns the CLDR plural category of every sample in locale,
// as the MessageFormat kernel selects it.
func categories(locale bcp47.Tag) ([]string, error) {
	if v, ok := categoryCache.Load(locale.String()); ok {
		return v.([]string), nil
	}
	probe, err := mf.ParseMF2(".input {$n :number} .match $n zero {{zero}} one {{one}} two {{two}} few {{few}} many {{many}} * {{other}}")
	if err != nil {
		return nil, err
	}
	out := make([]string, len(samples))
	for i, n := range samples {
		cat, err := mf.Format(probe, locale.String(), map[string]any{"n": n}, mf.WithBidiIsolation(false))
		if err != nil {
			return nil, fmt.Errorf("plural category of %d in %s: %w", n, locale, err)
		}
		out[i] = cat
	}
	if cached.Add(1) <= maxCachedLocales {
		categoryCache.Store(locale.String(), out)
	}
	return out, nil
}

// maxCachedLocales bounds the cache: file headers choose locales.
const maxCachedLocales = 512

var cached atomic.Int64

// formMap maps CLDR categories to gettext form indexes; catchAll is the
// form of "*".
type formMap struct {
	forms    map[string]int
	catchAll int
}

// mapForms decides which gettext form each CLDR category of locale
// takes, from the Plural-Forms header or, without one, from the number
// of forms.
func mapForms(header string, nforms int, locale bcp47.Tag) (formMap, error) {
	cats, err := categories(locale)
	if err != nil {
		return formMap{}, err
	}
	if header == "" {
		return countMap(cats[:smallSamples], nforms, locale)
	}
	n, expr, err := parsePluralForms(header)
	if err != nil {
		return formMap{}, err
	}
	if n != nforms {
		return formMap{}, fmt.Errorf("Plural-Forms says nplurals=%d, the entry has %d forms", n, nforms)
	}
	return exprMap(expr, cats, n)
}

func exprMap(expr node, cats []string, nforms int) (formMap, error) {
	m := formMap{forms: map[string]int{}, catchAll: nforms - 1}
	for i, n := range samples {
		idx, err := expr.eval(n)
		if err != nil {
			return formMap{}, err
		}
		if idx < 0 || idx >= int64(nforms) {
			return formMap{}, fmt.Errorf("Plural-Forms selects form %d for n=%d, beyond nplurals=%d", idx, n, nforms)
		}
		if _, seen := m.forms[cats[i]]; !seen {
			m.forms[cats[i]] = int(idx)
		}
	}
	if other, ok := m.forms["other"]; ok {
		m.catchAll = other
	}
	return m, nil
}

// countMap maps forms in CLDR order when their number matches.
func countMap(cats []string, nforms int, locale bcp47.Tag) (formMap, error) {
	var present []string
	for _, c := range cldrOrder {
		for _, s := range cats {
			if s == c {
				present = append(present, c)
				break
			}
		}
	}
	if len(present) != nforms {
		return formMap{}, fmt.Errorf("%d plural forms don't match the %d CLDR categories of %s; add a Plural-Forms header", nforms, len(present), locale)
	}
	m := formMap{forms: map[string]int{}, catchAll: nforms - 1}
	for i, c := range present {
		m.forms[c] = i
	}
	if other, ok := m.forms["other"]; ok {
		m.catchAll = other
	}
	return m, nil
}

var pluralFormsPattern = regexp.MustCompile(`^\s*nplurals\s*=\s*(\d+)\s*;\s*plural\s*=\s*(.+?);?\s*$`)

// parsePluralForms reads "nplurals=N; plural=EXPR;".
func parsePluralForms(h string) (int, node, error) {
	m := pluralFormsPattern.FindStringSubmatch(h)
	if m == nil {
		return 0, nil, fmt.Errorf("Plural-Forms %q is not nplurals=N; plural=EXPR", h)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 || n > 6 {
		return 0, nil, fmt.Errorf("nplurals=%s is not 1-6", m[1])
	}
	expr, err := parseExpr(m[2])
	return n, expr, err
}

// --- the plural expression: C's integer expressions over n ---------------

type node interface {
	eval(n int64) (int64, error)
}

type (
	numNode  int64
	varNode  struct{}
	notNode  struct{ x node }
	negNode  struct{ x node }
	condNode struct{ c, a, b node }
	binNode  struct {
		op   string
		l, r node
	}
)

func (v numNode) eval(int64) (int64, error)   { return int64(v), nil }
func (varNode) eval(n int64) (int64, error)   { return n, nil }
func (u notNode) eval(n int64) (int64, error) { x, err := u.x.eval(n); return b2i(x == 0), err }
func (u negNode) eval(n int64) (int64, error) { x, err := u.x.eval(n); return -x, err }

func (c condNode) eval(n int64) (int64, error) {
	v, err := c.c.eval(n)
	if err != nil {
		return 0, err
	}
	if v != 0 {
		return c.a.eval(n)
	}
	return c.b.eval(n)
}

func (b binNode) eval(n int64) (int64, error) {
	l, err := b.l.eval(n)
	if err != nil {
		return 0, err
	}
	if b.op == "&&" && l == 0 || b.op == "||" && l != 0 {
		return b2i(b.op == "||"), nil
	}
	r, err := b.r.eval(n)
	if err != nil {
		return 0, err
	}
	return arith(b.op, l, r)
}

var errDivZero = errors.New("Plural-Forms divides by zero")

func arith(op string, l, r int64) (int64, error) {
	switch op {
	case "+":
		return l + r, nil
	case "-":
		return l - r, nil
	case "*":
		return l * r, nil
	case "/", "%":
		if r == 0 {
			return 0, errDivZero
		}
		if op == "/" {
			return l / r, nil
		}
		return l % r, nil
	}
	return compare(op, l, r), nil
}

func compare(op string, l, r int64) int64 {
	switch op {
	case "==":
		return b2i(l == r)
	case "!=":
		return b2i(l != r)
	case "<":
		return b2i(l < r)
	case "<=":
		return b2i(l <= r)
	case ">":
		return b2i(l > r)
	case ">=":
		return b2i(l >= r)
	case "&&", "||":
		return b2i(r != 0)
	}
	return 0
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// exprParser is a precedence-climbing parser for the expression.
type exprParser struct {
	toks  []string
	pos   int
	depth int
}

var tokenPattern = regexp.MustCompile(`\s*(\d+|n|&&|\|\||==|!=|<=|>=|[-+*/%<>!?:()])`)

// maxExprLen bounds the expression, and with it the parser's recursion.
const maxExprLen = 512

func parseExpr(s string) (node, error) {
	if len(s) > maxExprLen {
		return nil, fmt.Errorf("Plural-Forms expression longer than %d characters", maxExprLen)
	}
	p := &exprParser{}
	rest := s
	for strings.TrimSpace(rest) != "" {
		m := tokenPattern.FindStringSubmatchIndex(rest)
		if m == nil || m[0] != 0 {
			return nil, fmt.Errorf("Plural-Forms expression %q: unexpected %.10q", s, strings.TrimSpace(rest))
		}
		p.toks = append(p.toks, rest[m[2]:m[3]])
		rest = rest[m[1]:]
	}
	x, err := p.cond()
	if err == nil && p.pos != len(p.toks) {
		err = fmt.Errorf("unexpected %q", p.toks[p.pos])
	}
	if err != nil {
		return nil, fmt.Errorf("Plural-Forms expression %q: %w", s, err)
	}
	return x, nil
}

func (p *exprParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *exprParser) cond() (node, error) {
	c, err := p.binary(0)
	if err != nil || p.peek() != "?" {
		return c, err
	}
	p.pos++
	a, err := p.cond()
	if err != nil {
		return nil, err
	}
	if p.peek() != ":" {
		return nil, fmt.Errorf("missing ':'")
	}
	p.pos++
	b, err := p.cond()
	return condNode{c, a, b}, err
}

// levels lists binary operators from loosest to tightest binding.
var levels = [][]string{{"||"}, {"&&"}, {"==", "!="}, {"<", "<=", ">", ">="}, {"+", "-"}, {"*", "/", "%"}}

func (p *exprParser) binary(level int) (node, error) {
	if level == len(levels) {
		return p.unary()
	}
	l, err := p.binary(level + 1)
	for err == nil && contains(levels[level], p.peek()) {
		op := p.toks[p.pos]
		p.pos++
		var r node
		r, err = p.binary(level + 1)
		l = binNode{op, l, r}
	}
	return l, err
}

func contains(ops []string, t string) bool {
	for _, o := range ops {
		if o == t {
			return true
		}
	}
	return false
}

func (p *exprParser) unary() (node, error) {
	if p.depth++; p.depth > maxExprLen {
		return nil, fmt.Errorf("nested too deeply")
	}
	defer func() { p.depth-- }()
	switch t := p.peek(); t {
	case "!", "-":
		p.pos++
		x, err := p.unary()
		if t == "!" {
			return notNode{x}, err
		}
		return negNode{x}, err
	case "(":
		p.pos++
		x, err := p.cond()
		if err == nil && p.peek() != ")" {
			err = fmt.Errorf("missing ')'")
		}
		p.pos++
		return x, err
	case "n":
		p.pos++
		return varNode{}, nil
	case "":
		return nil, fmt.Errorf("unexpected end")
	default:
		v, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unexpected %q", t)
		}
		p.pos++
		return numNode(v), nil
	}
}
