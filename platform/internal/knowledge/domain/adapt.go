package domain

import (
	mf "go.klarlabs.de/glossa/messageformat"
)

// AdaptVariables returns a copy of target, a TM unit's translation,
// with its variables renamed from the unit's source names to the
// query's by position: from[i] becomes to[i]. That is what makes a
// match reusable as a structure — "Pay {$amount}" → "{$amount} zahlen"
// answers "Pay {$total}" with "{$total} zahlen". Renaming is
// simultaneous, so swapped names swap. complete is false when a
// variable of target has no counterpart; it then keeps its name.
func AdaptVariables(target mf.Message, from, to []string) (adapted mf.Message, complete bool) {
	rename := map[string]string{}
	for i, name := range from {
		if i < len(to) {
			rename[name] = to[i]
		}
	}
	local := map[string]bool{}
	for _, d := range target.Declarations {
		if d.Type == mf.LocalDeclaration {
			local[d.Name] = true
		}
	}
	r := renamer{rename: rename, local: local, complete: true}
	out := mf.Message{Type: target.Type, Pattern: r.pattern(target.Pattern)}
	for _, d := range target.Declarations {
		name := d.Name
		if d.Type == mf.InputDeclaration {
			name = r.name(d.Name)
		}
		out.Declarations = append(out.Declarations, mf.Declaration{Type: d.Type, Name: name, Value: r.expression(d.Value)})
	}
	for _, s := range target.Selectors {
		out.Selectors = append(out.Selectors, mf.VariableRef{Name: r.name(s.Name)})
	}
	for _, v := range target.Variants {
		out.Variants = append(out.Variants, mf.Variant{Keys: append([]mf.VariantKey(nil), v.Keys...), Value: r.pattern(v.Value)})
	}
	return out, r.complete
}

type renamer struct {
	rename   map[string]string
	local    map[string]bool
	complete bool
}

func (r *renamer) name(n string) string {
	if r.local[n] {
		return n
	}
	if to, ok := r.rename[n]; ok {
		return to
	}
	r.complete = false
	return n
}

func (r *renamer) operand(o mf.Operand) mf.Operand {
	if v, ok := o.(mf.VariableRef); ok {
		return mf.VariableRef{Name: r.name(v.Name)}
	}
	return o
}

func (r *renamer) options(o mf.Options) mf.Options {
	if o == nil {
		return nil
	}
	out := make(mf.Options, len(o))
	for k, v := range o {
		out[k] = r.operand(v)
	}
	return out
}

func attributes(a mf.Attributes) mf.Attributes {
	if a == nil {
		return nil
	}
	out := make(mf.Attributes, len(a))
	for k, v := range a {
		if v != nil {
			lit := *v
			v = &lit
		}
		out[k] = v
	}
	return out
}

func (r *renamer) expression(e mf.Expression) mf.Expression {
	out := mf.Expression{Attributes: attributes(e.Attributes)}
	if e.Arg != nil {
		out.Arg = r.operand(e.Arg)
	}
	if e.Function != nil {
		out.Function = &mf.FunctionRef{Name: e.Function.Name, Options: r.options(e.Function.Options)}
	}
	return out
}

func (r *renamer) pattern(p mf.Pattern) mf.Pattern {
	if p == nil {
		return nil
	}
	out := make(mf.Pattern, len(p))
	for i, el := range p {
		switch el := el.(type) {
		case mf.Expression:
			out[i] = r.expression(el)
		case mf.Markup:
			out[i] = mf.Markup{Kind: el.Kind, Name: el.Name, Options: r.options(el.Options), Attributes: attributes(el.Attributes)}
		default:
			out[i] = el
		}
	}
	return out
}
