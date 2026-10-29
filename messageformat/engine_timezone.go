package messageformat

import (
	"maps"
	"time"

	"github.com/kaptinlin/messageformat-go/pkg/functions"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
)

// A default time zone for date and time placeholders. The engine formats a
// time.Time in its own location unless a placeholder names a timeZone, so
// without this a server's output would depend on where its values came
// from (the database, time.Now, a parsed string). The engine derives the
// zone from the value's location: an IANA name as such, any other
// location (time.Local, a fixed zone) as its UTC offset at that instant.
// The wrapper therefore converts the operand into loc and lets the engine
// do the rest.

// WithTimeZone formats the :date, :time and :datetime placeholders that
// don't set a timeZone option in loc. A placeholder's own timeZone wins.
// A nil loc keeps the engine's behaviour: each value in its own location.
func WithTimeZone(loc *time.Location) FormatOption {
	return func(c *formatConfig) { c.timeZone = loc }
}

var dateTimeFunctions = []string{"date", "time", "datetime"}

// withTimeZone wraps the date and time functions of fns in place.
func withTimeZone(fns map[string]functions.MessageFunction, loc *time.Location) {
	if loc == nil {
		return
	}
	for _, name := range dateTimeFunctions {
		if fn, ok := fns[name]; ok {
			fns[name] = inTimeZone(fn, loc)
		}
	}
}

func inTimeZone(fn functions.MessageFunction, loc *time.Location) functions.MessageFunction {
	return func(ctx functions.MessageFunctionContext, opts functions.Options, operand any) messagevalue.MessageValue {
		if tz, ok := opts.Value("timeZone"); ok && tz != nil {
			return fn(ctx, opts, operand)
		}
		instant, ok := operandInstant(operand)
		if !ok {
			// A string or number: let the engine parse it without
			// reporting, then format the instant it found. If it finds
			// none, the call below reports why.
			silent := functions.NewMessageFunctionContext(ctx.Locales(), ctx.Source(), ctx.LocaleMatcher(),
				nil, ctx.LiteralOptionKeys(), ctx.Dir(), ctx.ID())
			parsed, isDateTime := fn(silent, opts, operand).(*messagevalue.DateTimeValue)
			if !isDateTime {
				return fn(ctx, opts, operand)
			}
			instant = parsed.Time()
		}
		return fn(ctx, opts, withInstant(operand, instant.In(loc)))
	}
}

// operandInstant returns the time.Time an operand holds directly: a
// time.Time, a resolved date/time value, or an operand object's valueOf.
func operandInstant(operand any) (time.Time, bool) {
	switch v := operand.(type) {
	case time.Time:
		return v, true
	case messagevalue.MessageValue:
		inner, err := v.ValueOf()
		t, ok := inner.(time.Time)
		return t, ok && err == nil && v.Type() != "fallback"
	case map[string]any:
		t, ok := v["valueOf"].(time.Time)
		return t, ok
	}
	return time.Time{}, false
}

// withInstant replaces the instant of operand, keeping an operand
// object's options.
func withInstant(operand any, t time.Time) any {
	if m, ok := operand.(map[string]any); ok {
		if _, has := m["valueOf"]; has {
			m = maps.Clone(m)
			m["valueOf"] = t
			return m
		}
	}
	return t
}
