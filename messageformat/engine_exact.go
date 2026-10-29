package messageformat

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/numberformat"
	"github.com/agentable/go-intl/pluralrules"
	"github.com/kaptinlin/messageformat-go/pkg/bidi"
	mferrors "github.com/kaptinlin/messageformat-go/pkg/errors"
	"github.com/kaptinlin/messageformat-go/pkg/functions"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
)

// Exact numeric formatting for Decimal and Money operands.
//
// Engine gap (README.md, "Engine gaps"): messageformat-go reads a numeric
// string operand as float64 and has no decimal operand type, although its
// CLDR layer (go-intl) formats and selects decimal strings exactly. The
// kernel closes the gap at its boundary instead of converting through a
// float: each numeric function is wrapped, and for a Decimal operand the
// engine's own function runs on a zero stand-in, so option validation,
// merging and errors stay the engine's. The kernel then formats and
// selects the real value with go-intl, with the options the engine
// resolved. TestDecimalMatchesEngine proves both paths agree wherever the
// engine is exact.

// exactNumericFunctions are the functions whose operand may be a Decimal.
var exactNumericFunctions = []string{"number", "integer", "percent", "currency", "unit", "offset"}

// withExactNumbers wraps the numeric functions of fns in place.
func withExactNumbers(fns map[string]functions.MessageFunction) {
	for _, name := range exactNumericFunctions {
		if fn, ok := fns[name]; ok {
			fns[name] = exactNumeric(name, fn)
		}
	}
}

func exactNumeric(name string, fn functions.MessageFunction) functions.MessageFunction {
	return func(ctx functions.MessageFunctionContext, opts functions.Options, operand any) messagevalue.MessageValue {
		value, standIn, ok := exactOperand(operand)
		if !ok {
			return fn(ctx, opts, operand)
		}
		res := fn(ctx, opts, standIn)
		resolved, ok := res.(*messagevalue.NumberValue)
		if !ok {
			return res // a fallback; the engine reported why
		}
		switch name {
		case "integer":
			value = value.roundInteger()
		case "offset":
			delta, ok := resolved.Number().(int64) // the stand-in 0 plus the offset
			if !ok {
				ctx.OnError(mferrors.NewBadOperandError(fmt.Sprintf("unexpected :offset result %T", resolved.Number()), ctx.Source()))
				return messagevalue.NewFallbackValue(ctx.Source(), functions.GetFirstLocale(ctx.Locales()))
			}
			value = value.add(delta)
		}
		x, err := newExactNumber(ctx, value, resolved)
		if err != nil {
			ctx.OnError(mferrors.NewMessageResolutionError(mferrors.ErrorTypeBadOption, "Invalid number formatting options", ctx.Source(), err))
			return messagevalue.NewFallbackValue(ctx.Source(), functions.GetFirstLocale(ctx.Locales()))
		}
		return x
	}
}

// exactOperand recognizes a Decimal operand and returns it with the
// stand-in the engine's function runs on: 0, with the operand's options.
func exactOperand(operand any) (Decimal, any, bool) {
	switch v := operand.(type) {
	case Decimal:
		return v, int64(0), true
	case Money:
		opts := map[string]any{}
		if v.Currency != "" {
			opts["currency"] = v.Currency
		}
		return v.Amount, map[string]any{"valueOf": int64(0), "options": opts}, true
	case messagevalue.MessageValue:
		// A resolved Decimal, such as a .local bound to {$amount :number},
		// possibly wrapped by the engine for u:id or u:dir.
		inner, err := v.ValueOf()
		d, ok := inner.(Decimal)
		if err != nil || !ok || v.Type() == "fallback" {
			return Decimal{}, nil, false
		}
		var opts map[string]any
		if optioned, ok := v.(messagevalue.OptionedValue); ok {
			opts = optioned.Options()
		}
		standIn, err := messagevalue.NewNumberValue(int64(0), v.Locale(), v.Source(), opts)
		if err != nil {
			return d, int64(0), true
		}
		return d, standIn, true
	}
	return Decimal{}, nil, false
}

// exactNumber is a resolved Decimal: the engine's NumberValue with an
// exact value.
type exactNumber struct {
	value      Decimal
	formatted  numberformat.Value
	source     string
	locale     string
	dir        bidi.Direction
	id         string
	options    map[string]any
	selectable bool
	formatter  *numberformat.NumberFormat
	plural     *pluralrules.PluralRules
}

func newExactNumber(ctx functions.MessageFunctionContext, value Decimal, resolved *messagevalue.NumberValue) (*exactNumber, error) {
	formatted, err := numberformat.Decimal(value.String())
	if err != nil {
		return nil, err
	}
	opts := resolved.Options()
	formatter, err := numberformat.New(intlLocale(functions.GetFirstLocale(ctx.Locales())), numberFormatOptions(opts))
	if err != nil {
		return nil, err
	}
	plural, err := exactPluralRules(formatter.ResolvedOptions(), opts, resolved.CanSelect())
	if err != nil {
		return nil, err
	}
	return &exactNumber{
		value:      value,
		formatted:  formatted,
		source:     resolved.Source(),
		locale:     resolved.Locale(),
		dir:        resolved.Dir(),
		id:         ctx.ID(),
		options:    opts,
		selectable: resolved.CanSelect(),
		formatter:  formatter,
		plural:     plural,
	}, nil
}

func (x *exactNumber) Type() string              { return "number" }
func (x *exactNumber) Source() string            { return x.source }
func (x *exactNumber) Dir() bidi.Direction       { return x.dir }
func (x *exactNumber) Locale() string            { return x.locale }
func (x *exactNumber) Options() map[string]any   { return maps.Clone(x.options) }
func (x *exactNumber) CanSelect() bool           { return x.selectable }
func (x *exactNumber) ValueOf() (any, error)     { return x.value, nil }
func (x *exactNumber) ToString() (string, error) { return x.formatter.Format(x.formatted), nil }

// ToParts returns one number part with the formatter's sub-parts.
func (x *exactNumber) ToParts() ([]messagevalue.MessagePart, error) {
	intl := x.formatter.FormatToParts(x.formatted)
	sub := make([]SubPart, len(intl))
	for i, p := range intl {
		sub[i] = SubPart{Type: string(p.Type), Value: p.Value}
	}
	return []messagevalue.MessagePart{&exactNumberPart{number: x, text: x.formatter.Format(x.formatted), parts: sub}}, nil
}

// SelectKeys selects like the engine's NumberValue: an exact numeric key
// (=N or N) first, then the CLDR plural category, computed on the exact
// value with the formatter's digit options.
func (x *exactNumber) SelectKeys(keys []string) ([]string, error) {
	if !x.selectable {
		return nil, messagevalue.ErrNumberNotSelectable
	}
	v := x.value
	if x.options["style"] == "percent" {
		v = v.shift(2)
	}
	for _, key := range keys {
		if n, ok := strings.CutPrefix(key, "="); ok {
			if k, err := ParseDecimal(n); err == nil && k.equal(v) {
				return []string{key}, nil
			}
		}
	}
	exact := v.normalized().String()
	for _, key := range keys {
		if key == exact {
			return []string{key}, nil
		}
	}
	if x.options["select"] == "exact" || x.plural == nil {
		return []string{}, nil
	}
	pv, err := pluralrules.Decimal(v.String())
	if err != nil {
		return nil, err
	}
	category := x.plural.Select(pv).String()
	for _, key := range keys {
		if key == category {
			return []string{key}, nil
		}
	}
	return []string{}, nil
}

// exactNumberPart is the formatted part of an exactNumber.
type exactNumberPart struct {
	number *exactNumber
	text   string
	parts  []SubPart
}

func (p *exactNumberPart) Type() string        { return "number" }
func (p *exactNumberPart) Value() any          { return p.text }
func (p *exactNumberPart) Text() string        { return p.text }
func (p *exactNumberPart) Source() string      { return p.number.source }
func (p *exactNumberPart) Locale() string      { return p.number.locale }
func (p *exactNumberPart) Dir() bidi.Direction { return p.number.dir }
func (p *exactNumberPart) ID() string          { return p.number.id }

// exactPluralRules mirrors the engine's newPluralRules: plural rules with
// the formatter's resolved digit options, so selection follows the digits
// shown.
func exactPluralRules(resolved numberformat.ResolvedOptions, opts map[string]any, selectable bool) (*pluralrules.PluralRules, error) {
	if !selectable || opts["select"] == "exact" {
		return nil, nil
	}
	ruleType := string(pluralrules.Cardinal)
	if opts["select"] == "ordinal" {
		ruleType = string(pluralrules.Ordinal)
	}
	roundingMode := string(resolved.RoundingMode)
	roundingPriority := string(resolved.RoundingPriority)
	trailingZeroDisplay := string(resolved.TrailingZeroDisplay)
	notation := string(resolved.Notation)
	minInt, increment := resolved.MinimumIntegerDigits, resolved.RoundingIncrement
	po := pluralrules.Options{
		Type:                     &ruleType,
		MinimumIntegerDigits:     &minInt,
		MinimumFractionDigits:    resolved.MinimumFractionDigits,
		MaximumFractionDigits:    resolved.MaximumFractionDigits,
		MinimumSignificantDigits: resolved.MinimumSignificantDigits,
		MaximumSignificantDigits: resolved.MaximumSignificantDigits,
		RoundingIncrement:        &increment,
		RoundingMode:             &roundingMode,
		RoundingPriority:         &roundingPriority,
		TrailingZeroDisplay:      &trailingZeroDisplay,
		Notation:                 &notation,
	}
	if resolved.CompactDisplay != nil {
		compact := string(*resolved.CompactDisplay)
		po.CompactDisplay = &compact
	}
	return pluralrules.New(intlLocale(resolved.Locale.String()), po)
}

// intlLocale mirrors the engine's locale bridge: POSIX underscores become
// hyphens, and an unusable tag falls back to English.
func intlLocale(tag string) locale.List {
	if loc, err := locale.Parse(strings.ReplaceAll(tag, "_", "-")); tag != "" && err == nil {
		return locale.List{loc}
	}
	en, _ := locale.Parse("en")
	return locale.List{en}
}

// numberFormatOptions mirrors the engine's option bridge (internal/
// intlbridge.NumberOptions): the engine's functions leave options as
// strings and ints, which map one to one onto go-intl's typed options.
func numberFormatOptions(opts map[string]any) numberformat.Options {
	var out numberformat.Options
	strOpts := map[string]**string{
		"style": &out.Style, "currencyDisplay": &out.CurrencyDisplay, "currencySign": &out.CurrencySign,
		"unit": &out.Unit, "unitDisplay": &out.UnitDisplay, "notation": &out.Notation,
		"compactDisplay": &out.CompactDisplay, "signDisplay": &out.SignDisplay, "roundingMode": &out.RoundingMode,
		"roundingPriority": &out.RoundingPriority, "trailingZeroDisplay": &out.TrailingZeroDisplay,
		"numberingSystem": &out.NumberingSystem, "localeMatcher": &out.LocaleMatcher,
	}
	intOpts := map[string]**int{
		"minimumIntegerDigits": &out.MinimumIntegerDigits, "roundingIncrement": &out.RoundingIncrement,
		"minimumFractionDigits": &out.MinimumFractionDigits, "maximumFractionDigits": &out.MaximumFractionDigits,
		"minimumSignificantDigits": &out.MinimumSignificantDigits, "maximumSignificantDigits": &out.MaximumSignificantDigits,
	}
	for name, raw := range opts {
		switch {
		case name == "currency":
			if s, ok := raw.(string); ok && s != "" {
				s = strings.ToUpper(s)
				out.Currency = &s
			}
		case name == "useGrouping":
			if g := useGrouping(raw); g != "" {
				out.UseGrouping = &g
			}
		case strOpts[name] != nil:
			if s, ok := raw.(string); ok && s != "" {
				*strOpts[name] = &s
			}
		case intOpts[name] != nil:
			if n, ok := optionInt(raw); ok {
				*intOpts[name] = &n
			}
		}
	}
	return out
}

// useGrouping accepts the MF2 forms of useGrouping, as the engine does.
func useGrouping(raw any) string {
	switch v := raw.(type) {
	case bool:
		if v {
			return string(numberformat.UseGroupingAlways)
		}
		return string(numberformat.UseGroupingFalse)
	case string:
		switch v {
		case "never", "false":
			return string(numberformat.UseGroupingFalse)
		case "true", "always":
			return string(numberformat.UseGroupingAlways)
		case "min2":
			return string(numberformat.UseGroupingMin2)
		case "auto":
			return string(numberformat.UseGroupingAuto)
		}
	}
	return ""
}

// optionInt reads a digit option; the engine's functions store ints.
func optionInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	return 0, false
}

// annotateExactValues gives unannotated placeholders of Decimal and Money
// values their default function, :number and :currency. The engine formats
// only Go's numeric kinds implicitly and would render any other value as
// an unknown value. msg is not modified; it is copied when values holds a
// Decimal or Money.
func annotateExactValues(msg Message, values map[string]any) Message {
	implicit := map[string]string{}
	for name, v := range values {
		switch v.(type) {
		case Decimal:
			implicit[name] = "number"
		case Money:
			implicit[name] = "currency"
		}
	}
	if len(implicit) == 0 {
		return msg
	}
	declared := map[string]bool{}
	for _, d := range msg.Declarations {
		declared[d.Name] = true
	}
	// external reports whether the operand is the external value, not a
	// declaration of the same name.
	annotate := func(e Expression, external bool) Expression {
		ref, ok := e.Arg.(VariableRef)
		fn, known := implicit[ref.Name]
		if !ok || !known || e.Function != nil || (declared[ref.Name] && !external) {
			return e
		}
		e = e.clone()
		e.Function = &FunctionRef{Name: fn}
		return e
	}
	pattern := func(p Pattern) Pattern {
		out := make(Pattern, len(p))
		for i, el := range p {
			if e, ok := el.(Expression); ok {
				el = annotate(e, false)
			}
			out[i] = el
		}
		return out
	}
	out := msg
	out.Declarations = make([]Declaration, len(msg.Declarations))
	for i, d := range msg.Declarations {
		// An .input declaration's operand is the external value itself.
		d.Value = annotate(d.Value, d.Type == InputDeclaration)
		out.Declarations[i] = d
	}
	out.Pattern = pattern(msg.Pattern)
	out.Variants = make([]Variant, len(msg.Variants))
	for i, v := range msg.Variants {
		v.Value = pattern(v.Value)
		out.Variants[i] = v
	}
	return out
}
