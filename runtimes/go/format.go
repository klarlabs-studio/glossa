package glossa

import (
	"fmt"
	"time"

	"go.klarlabs.de/glossa/messageformat"
)

// Numbers, money and dates outside messages (RFC 0004 §7.2): table cells,
// totals and dates in documents. Each formatter builds a one-placeholder
// MF2 message — Number(v) is {$value :number} — and formats it through
// the same engine as T, so a cell and a sentence can never disagree.

// Decimal is an exact decimal number, such as a money amount: it keeps its
// literal and formats to exactly those digits, never passing through a
// float. Use it (or [Money]) for amounts in message arguments and in the
// formatters. The zero value is 0.
type Decimal = messageformat.Decimal

// Money is an exact amount in a currency (an ISO 4217 code such as "EUR").
// The currency's CLDR fraction digits apply (JPY 0, EUR 2) unless the
// placeholder or formatter sets its own. An unannotated {$total} whose
// argument is Money formats as :currency.
type Money = messageformat.Money

// ErrInvalidDecimal reports text that is not a decimal literal.
var ErrInvalidDecimal = messageformat.ErrInvalidDecimal

// ParseDecimal parses a decimal literal: an optional minus sign, digits
// without leading zeros, an optional fraction and an optional exponent
// ("-1234.50", "1.5e3"), the MF2 number grammar. Grouping separators and
// decimal commas are rejected.
func ParseDecimal(s string) (Decimal, error) { return messageformat.ParseDecimal(s) }

// MustParseDecimal is ParseDecimal for constants; it panics on an invalid
// literal.
func MustParseDecimal(s string) Decimal { return messageformat.MustParseDecimal(s) }

// NewDecimal returns unscaled × 10^-scale: NewDecimal(1999, 2) is 19.99,
// for amounts kept in minor units.
func NewDecimal(unscaled int64, scale int) Decimal { return messageformat.NewDecimal(unscaled, scale) }

// TimeZone formats the :date, :time and :datetime placeholders that don't
// set a timeZone option in loc, for one call. It overrides the Localizer's
// default ([Localizer.WithTimeZone]), which is UTC. A nil loc keeps the
// default.
func TimeZone(loc *time.Location) Option {
	return func(o *callOptions) {
		if loc != nil {
			o.timeZone = loc
		}
	}
}

// Opt sets an option of the MF2 function a standalone formatter formats
// with, by its MF2 name and literal value: Opt("minimumFractionDigits",
// "2"), Opt("currencyDisplay", "code"), Opt("length", "long"). The
// options are the function's (see [Localizer.Number] and the others).
// Options of T, Parts, HTML and Runs belong in the message, so Opt is
// ignored there.
func Opt(name, value string) Option {
	return func(o *callOptions) {
		if o.fnOptions == nil {
			o.fnOptions = map[string]string{}
		}
		o.fnOptions[name] = value
	}
}

// WithTimeZone returns a Localizer whose date and time placeholders and
// formatters use loc when they don't set a zone themselves. Without it the
// zone is UTC, whatever the time.Time's location and the server's TZ; a
// [TimeZone] call option overrides it.
func (l *Localizer) WithTimeZone(loc *time.Location) *Localizer {
	c := *l
	c.timeZone = loc
	return &c
}

// Number formats v as {$value :number}: an integer, a float, a [Decimal],
// a [Money] amount or a numeric string, with :number's options
// (minimumFractionDigits, maximumFractionDigits, minimumIntegerDigits,
// minimumSignificantDigits, maximumSignificantDigits, signDisplay,
// useGrouping, roundingMode, …).
//
// The formatters use the active locale ([Localizer.Locale]) and never
// fail: an error renders the MF2 fallback {$value} and is reported like a
// message's. They add no bidi isolation, since the value is all the output.
func (l *Localizer) Number(v any, opts ...Option) string {
	return l.formatValue("number", v, nil, opts)
}

// Percent formats v as {$value :percent}: 0.256 is 26%.
func (l *Localizer) Percent(v any, opts ...Option) string {
	return l.formatValue("percent", v, nil, opts)
}

// Currency formats v as {$value :currency}. v is a [Money], or a number
// with Opt("currency", code). Options: currencyDisplay (symbol,
// narrowSymbol, code, name), currencySign (accounting), fractionDigits,
// and :number's digit and sign options.
func (l *Localizer) Currency(v any, opts ...Option) string {
	return l.formatValue("currency", v, nil, opts)
}

// Unit formats v as {$value :unit unit=…}, with a CLDR unit such as
// "kilometer", "liter" or "kilometer-per-hour". Options: unitDisplay
// (short, long, narrow) and :number's digit options.
func (l *Localizer) Unit(v any, unit string, opts ...Option) string {
	return l.formatValue("unit", v, map[string]string{"unit": unit}, opts)
}

// Date formats t (a time.Time or an ISO 8601 string) as {$value :date}.
// Options: length (short, medium, long, full), fields
// (year-month-day, year-month-day-weekday, month-day, …), calendar and
// timeZone.
func (l *Localizer) Date(t any, opts ...Option) string {
	return l.formatValue("date", t, nil, opts)
}

// Time formats t as {$value :time}. Options: precision (hour, minute,
// second), timeZoneStyle (long, short), hour12 and timeZone.
func (l *Localizer) Time(t any, opts ...Option) string {
	return l.formatValue("time", t, nil, opts)
}

// DateTime formats t as {$value :datetime}. Options: dateLength,
// dateFields, timePrecision, timeZoneStyle, hour12, calendar and timeZone.
func (l *Localizer) DateTime(t any, opts ...Option) string {
	return l.formatValue("datetime", t, nil, opts)
}

// formatValue formats v through the synthetic message {$value :fn …}.
func (l *Localizer) formatValue(fn string, v any, fixed map[string]string, opts []Option) string {
	o := l.options(opts)
	fnOpts := messageformat.Options{}
	for name, value := range o.fnOptions {
		fnOpts[name] = messageformat.Literal{Value: value}
	}
	for name, value := range fixed {
		fnOpts[name] = messageformat.Literal{Value: value}
	}
	msg := messageformat.Message{Type: messageformat.PatternMessageType, Pattern: messageformat.Pattern{
		messageformat.Expression{Arg: messageformat.VariableRef{Name: "value"}, Function: &messageformat.FunctionRef{Name: fn, Options: fnOpts}},
	}}
	locale := l.Locale()
	if locale == "" {
		locale = "und"
	}
	out, err := messageformat.Format(msg, locale, Args{"value": v},
		messageformat.WithBidiIsolation(false), messageformat.WithTimeZone(o.timeZone))
	if err != nil {
		l.reportFormatter(locale, fmt.Sprintf(":%s: %v", fn, err))
	}
	if out == "" {
		return "{$value}"
	}
	return out
}

func (l *Localizer) reportFormatter(locale, detail string) {
	e := Error{Type: ErrorFormat, Detail: detail, Locale: locale}
	if ref, ok := l.c.Release(); ok {
		e.ReleaseID = ref.ID
	}
	l.c.reporter.report(e)
}
