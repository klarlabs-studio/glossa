package messageformat

import (
	"fmt"
	"reflect"

	"github.com/kaptinlin/messageformat-go/pkg/bidi"
	"github.com/kaptinlin/messageformat-go/pkg/messagevalue"
)

// Conversion of the engine's formatted parts to Part. Every part gets the
// text the engine's Format writes for it, so PartsText(parts) equals the
// Format output.

func fromEngineParts(in []messagevalue.MessagePart) []Part {
	out := make([]Part, 0, len(in))
	for _, p := range in {
		if p == nil {
			continue
		}
		out = append(out, fromEnginePart(p))
	}
	return out
}

func fromEnginePart(p messagevalue.MessagePart) Part {
	part := Part{
		Type:   PartType(p.Type()),
		Source: p.Source(),
		Locale: p.Locale(),
		Dir:    partDir(p.Dir()),
	}
	if withID, ok := p.(interface{ ID() string }); ok {
		part.ID = withID.ID()
	}
	switch p := p.(type) {
	case *messagevalue.MarkupPart:
		part.Kind, part.Name, part.Source = MarkupKind(p.Kind()), p.Name(), ""
		if opts := p.Options(); len(opts) > 0 {
			part.Options = opts
		}
	case *messagevalue.BidiIsolationPart, *messagevalue.TextPart:
		part.Value, part.Source, part.Locale, part.Dir = p.(interface{ Text() string }).Text(), "", "", ""
	case *messagevalue.NumberPart:
		part.Value, part.Parts = p.Text(), subParts(p.Parts())
	case *messagevalue.DateTimePart:
		part.Value, part.Parts = p.Text(), subParts(p.Parts())
	case *exactNumberPart:
		part.Value, part.Parts = p.Text(), p.parts
	case *messagevalue.UnknownPart:
		part.Value = unknownText(p.Value())
	case interface{ Text() string }:
		part.Value = p.Text()
	default:
		part.Value = fmt.Sprint(p.Value())
	}
	return part
}

func subParts(in []messagevalue.MessagePart) []SubPart {
	out := make([]SubPart, 0, len(in))
	for _, p := range in {
		if p == nil {
			continue
		}
		out = append(out, SubPart{Type: p.Type(), Value: fmt.Sprint(p.Value())})
	}
	return out
}

// partDir keeps the directions a renderer can act on.
func partDir(d bidi.Direction) string {
	if d == bidi.DirLTR || d == bidi.DirRTL {
		return string(d)
	}
	return ""
}

// unknownText renders a value the way the engine's UnknownValue.ToString
// does (JavaScript String(input)): nil and nil references are "null".
func unknownText(v any) string {
	if v == nil {
		return "null"
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if rv.IsNil() {
			return "null"
		}
	default:
	}
	return fmt.Sprintf("%v", v)
}
