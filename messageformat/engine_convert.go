package messageformat

import (
	"errors"
	"fmt"

	"github.com/kaptinlin/messageformat-go/pkg/datamodel"
)

// Conversion between the canonical model and the engine's data model.

// --- canonical -> engine -----------------------------------------------------

func toEngine(msg Message) (datamodel.Message, error) {
	decls, err := toEngineDeclarations(msg.Declarations)
	if err != nil {
		return nil, err
	}
	switch msg.Type {
	case PatternMessageType:
		pattern, err := toEnginePattern(msg.Pattern)
		if err != nil {
			return nil, err
		}
		return datamodel.NewPatternMessage(decls, pattern, "")
	case SelectMessageType:
		selectors := make([]datamodel.VariableRef, len(msg.Selectors))
		for i, s := range msg.Selectors {
			selectors[i] = *datamodel.NewVariableRef(s.Name)
		}
		variants, err := toEngineVariants(msg.Variants)
		if err != nil {
			return nil, err
		}
		return datamodel.NewSelectMessage(decls, selectors, variants, "")
	default:
		return nil, fmt.Errorf("unknown message type %q", msg.Type)
	}
}

func toEngineDeclarations(decls []Declaration) ([]datamodel.Declaration, error) {
	out := make([]datamodel.Declaration, 0, len(decls))
	for _, d := range decls {
		expr, err := toEngineExpression(d.Value)
		if err != nil {
			return nil, fmt.Errorf("declaration %q: %w", d.Name, err)
		}
		switch d.Type {
		case InputDeclaration:
			arg, ok := d.Value.Arg.(VariableRef)
			if !ok || arg.Name != d.Name {
				return nil, fmt.Errorf("input declaration %q must reference $%s", d.Name, d.Name)
			}
			decl, err := datamodel.NewInputDeclaration(expr)
			if err != nil {
				return nil, fmt.Errorf("declaration %q: %w", d.Name, err)
			}
			out = append(out, decl)
		case LocalDeclaration:
			decl, err := datamodel.NewLocalDeclaration(d.Name, expr)
			if err != nil {
				return nil, fmt.Errorf("declaration %q: %w", d.Name, err)
			}
			out = append(out, decl)
		default:
			return nil, fmt.Errorf("unknown declaration type %q", d.Type)
		}
	}
	return out, nil
}

func toEngineVariants(variants []Variant) ([]datamodel.Variant, error) {
	out := make([]datamodel.Variant, 0, len(variants))
	for i, v := range variants {
		keys := make([]datamodel.VariantKey, len(v.Keys))
		for j, k := range v.Keys {
			if k.Catchall {
				keys[j] = datamodel.NewCatchallKey(k.Value)
			} else {
				keys[j] = datamodel.NewLiteral(k.Value)
			}
		}
		pattern, err := toEnginePattern(v.Value)
		if err != nil {
			return nil, fmt.Errorf("variant %d: %w", i, err)
		}
		ev, err := datamodel.NewVariant(keys, pattern)
		if err != nil {
			return nil, fmt.Errorf("variant %d: %w", i, err)
		}
		out = append(out, *ev)
	}
	return out, nil
}

func toEnginePattern(p Pattern) (datamodel.Pattern, error) {
	elems := make([]datamodel.PatternElement, 0, len(p))
	for i, el := range p {
		switch el := el.(type) {
		case Text:
			elems = append(elems, datamodel.NewTextElement(string(el)))
		case Expression:
			expr, err := toEngineExpression(el)
			if err != nil {
				return nil, fmt.Errorf("pattern[%d]: %w", i, err)
			}
			elems = append(elems, expr)
		case Markup:
			mk, err := datamodel.NewMarkup(datamodel.MarkupKind(el.Kind), el.Name, toEngineOptions(el.Options), toEngineAttributes(el.Attributes))
			if err != nil {
				return nil, fmt.Errorf("pattern[%d]: %w", i, err)
			}
			elems = append(elems, mk)
		default:
			return nil, fmt.Errorf("pattern[%d]: unsupported element %T", i, el)
		}
	}
	return datamodel.NewPattern(elems)
}

func toEngineExpression(e Expression) (*datamodel.Expression, error) {
	if e.Arg == nil && e.Function == nil {
		return nil, errors.New("expression needs an arg or a function")
	}
	var arg datamodel.ExpressionArg
	switch a := e.Arg.(type) {
	case nil:
	case Literal:
		arg = datamodel.NewLiteral(a.Value)
	case VariableRef:
		arg = datamodel.NewVariableRef(a.Name)
	default:
		return nil, fmt.Errorf("unsupported operand %T", a)
	}
	var fn *datamodel.FunctionRef
	if e.Function != nil {
		var err error
		fn, err = datamodel.NewFunctionRef(e.Function.Name, toEngineOptions(e.Function.Options))
		if err != nil {
			return nil, err
		}
	}
	return datamodel.NewExpression(arg, fn, toEngineAttributes(e.Attributes))
}

func toEngineOptions(opts Options) datamodel.Options {
	if len(opts) == 0 {
		return nil
	}
	out := make(datamodel.Options, len(opts))
	for name, v := range opts {
		switch v := v.(type) {
		case Literal:
			out[name] = datamodel.NewLiteral(v.Value)
		case VariableRef:
			out[name] = datamodel.NewVariableRef(v.Name)
		}
	}
	return out
}

func toEngineAttributes(attrs Attributes) datamodel.Attributes {
	if len(attrs) == 0 {
		return nil
	}
	out := make(datamodel.Attributes, len(attrs))
	for name, lit := range attrs {
		if lit == nil {
			out[name] = datamodel.NewBooleanAttribute()
		} else {
			out[name] = datamodel.NewLiteral(lit.Value)
		}
	}
	return out
}

// --- engine -> canonical -----------------------------------------------------

func fromEngine(em datamodel.Message) (Message, error) {
	decls, err := fromEngineDeclarations(em.Declarations())
	if err != nil {
		return Message{}, err
	}
	switch m := em.(type) {
	case *datamodel.PatternMessage:
		pattern, err := fromEnginePattern(m.Pattern())
		if err != nil {
			return Message{}, err
		}
		return Message{Type: PatternMessageType, Declarations: decls, Pattern: pattern}, nil
	case *datamodel.SelectMessage:
		selectors := make([]VariableRef, len(m.Selectors()))
		for i, s := range m.Selectors() {
			selectors[i] = VariableRef{Name: s.Name()}
		}
		variants, err := fromEngineVariants(m.Variants())
		if err != nil {
			return Message{}, err
		}
		return Message{Type: SelectMessageType, Declarations: decls, Selectors: selectors, Variants: variants}, nil
	default:
		return Message{}, fmt.Errorf("unsupported engine message %T", em)
	}
}

func fromEngineDeclarations(decls []datamodel.Declaration) ([]Declaration, error) {
	out := make([]Declaration, 0, len(decls))
	for _, d := range decls {
		var (
			typ  DeclarationType
			expr *datamodel.Expression
		)
		switch d := d.(type) {
		case *datamodel.InputDeclaration:
			typ, expr = InputDeclaration, d.Value()
		case *datamodel.LocalDeclaration:
			typ, expr = LocalDeclaration, d.Value()
		default:
			return nil, fmt.Errorf("unsupported declaration %T", d)
		}
		value, err := fromEngineExpression(expr)
		if err != nil {
			return nil, fmt.Errorf("declaration %q: %w", d.Name(), err)
		}
		out = append(out, Declaration{Type: typ, Name: d.Name(), Value: value})
	}
	return out, nil
}

func fromEngineVariants(variants []datamodel.Variant) ([]Variant, error) {
	out := make([]Variant, 0, len(variants))
	for i := range variants {
		v := &variants[i]
		keys := make([]VariantKey, len(v.Keys()))
		for j, k := range v.Keys() {
			switch k := k.(type) {
			case *datamodel.CatchallKey:
				// The engine records the `*` source text as the key's value;
				// the canonical form leaves it empty.
				value := k.Value()
				if value == "*" {
					value = ""
				}
				keys[j] = VariantKey{Catchall: true, Value: value}
			case *datamodel.Literal:
				keys[j] = VariantKey{Value: k.Value()}
			default:
				return nil, fmt.Errorf("variant %d: unsupported key %T", i, k)
			}
		}
		pattern, err := fromEnginePattern(v.Value())
		if err != nil {
			return nil, fmt.Errorf("variant %d: %w", i, err)
		}
		out = append(out, Variant{Keys: keys, Value: pattern})
	}
	return out, nil
}

func fromEnginePattern(p datamodel.Pattern) (Pattern, error) {
	out := make(Pattern, 0, p.Len())
	for i, el := range p.Elements() {
		switch el := el.(type) {
		case *datamodel.TextElement:
			out = append(out, Text(el.Value()))
		case *datamodel.Expression:
			expr, err := fromEngineExpression(el)
			if err != nil {
				return nil, fmt.Errorf("pattern[%d]: %w", i, err)
			}
			out = append(out, expr)
		case *datamodel.Markup:
			out = append(out, Markup{
				Kind:       MarkupKind(el.Kind()),
				Name:       el.Name(),
				Options:    fromEngineOptions(el.Options()),
				Attributes: fromEngineAttributes(el.Attributes()),
			})
		default:
			return nil, fmt.Errorf("pattern[%d]: unsupported element %T", i, el)
		}
	}
	return out, nil
}

func fromEngineExpression(e *datamodel.Expression) (Expression, error) {
	if e == nil {
		return Expression{}, errors.New("missing expression")
	}
	out := Expression{Attributes: fromEngineAttributes(e.Attributes())}
	switch a := e.Arg().(type) {
	case nil:
	case *datamodel.Literal:
		out.Arg = Literal{Value: a.Value()}
	case *datamodel.VariableRef:
		out.Arg = VariableRef{Name: a.Name()}
	default:
		return Expression{}, fmt.Errorf("unsupported operand %T", a)
	}
	if fn := e.FunctionRef(); fn != nil {
		out.Function = &FunctionRef{Name: fn.Name(), Options: fromEngineOptions(fn.Options())}
	}
	return out, nil
}

func fromEngineOptions(opts datamodel.Options) Options {
	if len(opts) == 0 {
		return nil
	}
	out := make(Options, len(opts))
	for name, v := range opts {
		switch v := v.(type) {
		case *datamodel.Literal:
			out[name] = Literal{Value: v.Value()}
		case *datamodel.VariableRef:
			out[name] = VariableRef{Name: v.Name()}
		}
	}
	return out
}

func fromEngineAttributes(attrs datamodel.Attributes) Attributes {
	if len(attrs) == 0 {
		return nil
	}
	out := make(Attributes, len(attrs))
	for name, v := range attrs {
		if lit, ok := v.(*datamodel.Literal); ok {
			out[name] = &Literal{Value: lit.Value()}
		} else {
			out[name] = nil
		}
	}
	return out
}
