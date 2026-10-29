package messageformat

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kaptinlin/messageformat-go/mf1"
)

// Mapping of MF1 formatted arguments ({x, type, style}) to MF2 expressions.
// Only mappings proven against the reference implementations (see
// testdata/glossa/mf1-to-mf2.json) use standard functions; everything else
// keeps an mf1: function that preserves the MF1 type and style.

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func convertFunctionArg(t *mf1.FunctionArg) (Expression, error) {
	style, err := argStyle(t)
	if err != nil {
		return Expression{}, err
	}
	arg := VariableRef{Name: t.Arg}
	switch t.Key {
	case "number":
		return numberExpression(arg, style), nil
	case "date":
		return dateExpression(arg, style), nil
	case "time":
		return timeExpression(arg, style), nil
	default:
		return mf1Fallback(arg, t.Key, style), nil
	}
}

// argStyle returns the trimmed style text of a formatted argument. Inside a
// plural the parser reads `#` in a style (a number pattern) as an
// octothorpe; it is text here.
func argStyle(t *mf1.FunctionArg) (string, error) {
	var b strings.Builder
	for _, p := range t.Param {
		switch p := p.(type) {
		case *mf1.Content:
			b.WriteString(p.Value)
		case *mf1.Octothorpe:
			b.WriteString("#")
		default:
			return "", fmt.Errorf("argument %q: unsupported %s in the style of %s", t.Arg, p.GetType(), t.Key)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func numberExpression(arg VariableRef, style string) Expression {
	switch style {
	case "":
		return standardExpression(arg, "number", nil)
	case "integer":
		return standardExpression(arg, "integer", nil)
	case "percent":
		return standardExpression(arg, "percent", nil)
	case "currency":
		// The currency comes from the MF1 formatter's configuration, which
		// MF2 has no equivalent for (the reference maps it the same way).
		return Expression{
			Arg:        arg,
			Function:   &FunctionRef{Name: "mf1:currency"},
			Attributes: mf1Attributes("number", style),
		}
	}
	if skeleton, ok := strings.CutPrefix(style, "::"); ok {
		if expr, ok := numberSkeletonExpression(arg, skeleton); ok {
			return expr
		}
	}
	return mf1Fallback(arg, "number", style)
}

// numberSkeletonExpression maps the ICU number skeletons that have an exact
// standard MF2 equivalent.
func numberSkeletonExpression(arg VariableRef, skeleton string) (Expression, bool) {
	stems := strings.Fields(skeleton)
	switch {
	case len(stems) == 1 && strings.HasPrefix(stems[0], "currency/"):
		code := strings.TrimPrefix(stems[0], "currency/")
		if !currencyCode.MatchString(code) {
			return Expression{}, false
		}
		return standardExpression(arg, "currency", Options{"currency": Literal{Value: code}}), true
	case len(stems) == 2 && hasStems(stems, "percent", "scale/100"):
		return standardExpression(arg, "percent", nil), true
	default:
		return Expression{}, false
	}
}

func hasStems(stems []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, s := range stems {
			found = found || s == w
		}
		if !found {
			return false
		}
	}
	return true
}

func dateExpression(arg VariableRef, style string) Expression {
	switch style {
	case "":
		return standardExpression(arg, "date", nil)
	case "short", "medium", "long":
		return standardExpression(arg, "date", Options{"length": Literal{Value: style}})
	case "full":
		return standardExpression(arg, "date", Options{
			"fields": Literal{Value: "year-month-day-weekday"},
			"length": Literal{Value: "long"},
		})
	default:
		return mf1Fallback(arg, "date", style)
	}
}

func timeExpression(arg VariableRef, style string) Expression {
	switch style {
	case "", "medium":
		return standardExpression(arg, "time", Options{"precision": Literal{Value: "second"}})
	case "short":
		return standardExpression(arg, "time", Options{"precision": Literal{Value: "minute"}})
	case "long", "full":
		// The MF1 reference renders both with the short time zone name.
		return standardExpression(arg, "time", Options{
			"precision":     Literal{Value: "second"},
			"timeZoneStyle": Literal{Value: "short"},
		})
	default:
		return mf1Fallback(arg, "time", style)
	}
}

func standardExpression(arg VariableRef, fn string, opts Options) Expression {
	return Expression{Arg: arg, Function: &FunctionRef{Name: fn, Options: opts}}
}

// mf1Fallback keeps an MF1 argument without a standard MF2 equivalent as
// :mf1:<type>, with its style as an option (so runtimes can see what they
// don't support) and the MF1 type and style as attributes (so it can be
// exported back to MF1 unchanged).
func mf1Fallback(arg VariableRef, argType, style string) Expression {
	fn := &FunctionRef{Name: "mf1:" + argType}
	if style != "" {
		fn.Options = Options{"mf1:argStyle": Literal{Value: style}}
	}
	return Expression{Arg: arg, Function: fn, Attributes: mf1Attributes(argType, style)}
}

func mf1Attributes(argType, style string) Attributes {
	attrs := Attributes{"mf1:argType": &Literal{Value: argType}}
	if style != "" {
		attrs["mf1:argStyle"] = &Literal{Value: style}
	}
	return attrs
}
