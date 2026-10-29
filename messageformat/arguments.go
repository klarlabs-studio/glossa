package messageformat

import (
	"maps"
	"slices"
)

// Derived metadata (RFC 0002 §5): the arguments a message needs, their
// types and selector cases, and its markup. Typed accessors, structural QA
// and AI prompts read these. testdata/glossa/arguments.json is the shared
// specification.

// ArgumentType is the inferred type of a message argument.
type ArgumentType string

// Argument types.
const (
	ArgString   ArgumentType = "string"
	ArgNumber   ArgumentType = "number"
	ArgInteger  ArgumentType = "integer"
	ArgPercent  ArgumentType = "percent"
	ArgCurrency ArgumentType = "currency"
	ArgDate     ArgumentType = "date"
	ArgTime     ArgumentType = "time"
	ArgDatetime ArgumentType = "datetime"
	ArgUnit     ArgumentType = "unit"
	// ArgSelect is a string argument that selects between enumerated cases.
	ArgSelect ArgumentType = "select"
)

// SelectorKind is how a selector matches variant keys.
type SelectorKind string

// Selector kinds.
const (
	SelectPlural  SelectorKind = "plural"  // CLDR cardinal categories and exact numbers
	SelectOrdinal SelectorKind = "ordinal" // CLDR ordinal categories and exact numbers
	SelectExact   SelectorKind = "exact"   // exact numeric values only
	SelectString  SelectorKind = "string"  // exact string values
)

// Argument is an external variable of a message.
type Argument struct {
	Name string       `json:"name"`
	Type ArgumentType `json:"type"`
	// Function names the annotating function, if any.
	Function string `json:"function,omitempty"`
	// Selector is set when the argument selects a variant, directly or
	// through a .local bound to it.
	Selector *Selector `json:"selector,omitempty"`
}

// Selector describes how an argument selects variants.
type Selector struct {
	Kind SelectorKind `json:"kind"`
	// Keys are the literal variant keys in order of appearance. The
	// catch-all key `*` always exists and is not listed.
	Keys []string `json:"keys"`
}

// MarkupElement is a distinct markup element used by a message.
type MarkupElement struct {
	Name string     `json:"name"`
	Kind MarkupKind `json:"kind"`
}

// functionTypes maps annotating functions to argument types; unlisted
// functions leave an argument a string.
var functionTypes = map[string]ArgumentType{
	"string":       ArgString,
	"number":       ArgNumber,
	"integer":      ArgInteger,
	"offset":       ArgNumber,
	"percent":      ArgPercent,
	"currency":     ArgCurrency,
	"date":         ArgDate,
	"time":         ArgTime,
	"datetime":     ArgDatetime,
	"unit":         ArgUnit,
	"mf1:number":   ArgNumber,
	"mf1:plural":   ArgNumber,
	"mf1:duration": ArgNumber,
	"mf1:currency": ArgCurrency,
	"mf1:date":     ArgDate,
	"mf1:time":     ArgTime,
	"mf1:unit":     ArgUnit,
}

// Arguments returns the external variables of msg in order of first
// appearance (declarations, selectors, then patterns) with their inferred
// types. A variable's .input annotation wins; otherwise its first annotated
// use does, including through .local declarations bound to it.
func Arguments(msg Message) []Argument {
	a := newArgumentAnalysis(msg)
	a.walkDeclarations()
	for _, s := range msg.Selectors {
		a.observe(s.Name, nil)
	}
	for _, p := range msg.Patterns() {
		a.walkPattern(p)
	}
	a.addSelectors()
	return a.result()
}

// MarkupElements returns the distinct markup elements of msg, by name and
// kind, in order of first appearance across all patterns.
func MarkupElements(msg Message) []MarkupElement {
	out := []MarkupElement{}
	seen := map[MarkupElement]bool{}
	for _, p := range msg.Patterns() {
		for _, el := range p {
			mk, ok := el.(Markup)
			if !ok {
				continue
			}
			key := MarkupElement{Name: mk.Name, Kind: mk.Kind}
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	return out
}

type argumentAnalysis struct {
	msg    Message
	locals map[string]Declaration  // .local declarations by name
	inputs map[string]*FunctionRef // .input annotations by name
	order  []string
	args   map[string]*Argument
}

func newArgumentAnalysis(msg Message) *argumentAnalysis {
	a := &argumentAnalysis{
		msg:    msg,
		locals: map[string]Declaration{},
		inputs: map[string]*FunctionRef{},
		args:   map[string]*Argument{},
	}
	for _, d := range msg.Declarations {
		if d.Type == LocalDeclaration {
			a.locals[d.Name] = d
		} else {
			a.inputs[d.Name] = d.Value.Function
		}
	}
	return a
}

// root resolves a variable through .local declarations to the external
// variable it is bound to; ok is false for locals bound to literals.
func (a *argumentAnalysis) root(name string) (string, bool) {
	for range len(a.locals) + 1 {
		d, isLocal := a.locals[name]
		if !isLocal {
			return name, true
		}
		ref, ok := d.Value.Arg.(VariableRef)
		if !ok || ref.Name == name {
			return "", false
		}
		name = ref.Name
	}
	return "", false // a cycle; invalid MF2, but never loop forever
}

// observe records a use of variable name, annotated by fn (or nil).
func (a *argumentAnalysis) observe(name string, fn *FunctionRef) {
	r, ok := a.root(name)
	if !ok {
		return
	}
	arg, seen := a.args[r]
	if !seen {
		arg = &Argument{Name: r, Type: ArgString}
		a.args[r] = arg
		a.order = append(a.order, r)
		if input, declared := a.inputs[r]; declared {
			fn = input // the .input annotation wins
		}
	}
	if fn != nil && arg.Function == "" {
		arg.Function = fn.Name
		if t, known := functionTypes[fn.Name]; known {
			arg.Type = t
		}
	}
}

func (a *argumentAnalysis) walkDeclarations() {
	for _, d := range a.msg.Declarations {
		a.walkExpression(d.Value)
	}
}

func (a *argumentAnalysis) walkPattern(p Pattern) {
	for _, el := range p {
		switch el := el.(type) {
		case Expression:
			a.walkExpression(el)
		case Markup:
			a.walkOptions(el.Options)
		}
	}
}

func (a *argumentAnalysis) walkExpression(e Expression) {
	if ref, ok := e.Arg.(VariableRef); ok {
		a.observe(ref.Name, e.Function)
	}
	if e.Function != nil {
		a.walkOptions(e.Function.Options)
	}
}

func (a *argumentAnalysis) walkOptions(opts Options) {
	for _, name := range sortedKeys(opts) {
		if ref, ok := opts[name].(VariableRef); ok {
			a.observe(ref.Name, nil)
		}
	}
}

// addSelectors attaches selector kinds and keys to the arguments behind
// each selector column.
func (a *argumentAnalysis) addSelectors() {
	for col, s := range a.msg.Selectors {
		r, ok := a.root(s.Name)
		if !ok {
			continue
		}
		arg := a.args[r]
		if arg.Selector == nil {
			arg.Selector = &Selector{Kind: a.selectorKind(s.Name), Keys: []string{}}
			if arg.Selector.Kind == SelectString && arg.Type == ArgString {
				arg.Type = ArgSelect
			}
		}
		for _, v := range a.msg.Variants {
			if col < len(v.Keys) && !v.Keys[col].Catchall {
				arg.Selector.Keys = appendUnique(arg.Selector.Keys, v.Keys[col].Value)
			}
		}
	}
}

// selectorKind derives the selection behaviour of a selector variable from
// its annotation, following unannotated and :offset locals to the value
// they are bound to.
func (a *argumentAnalysis) selectorKind(name string) SelectorKind {
	var fn *FunctionRef
	for range len(a.locals) + 1 {
		fn = a.annotation(name)
		d, isLocal := a.locals[name]
		if !isLocal || (fn != nil && fn.Name != "offset") {
			break
		}
		ref, ok := d.Value.Arg.(VariableRef)
		if !ok {
			break
		}
		name = ref.Name
	}
	if fn == nil {
		return SelectString
	}
	switch fn.Name {
	case "number", "integer", "offset", "mf1:plural":
		switch sel, _ := fn.Options["select"].(Literal); sel.Value {
		case "ordinal":
			return SelectOrdinal
		case "exact":
			return SelectExact
		default:
			return SelectPlural
		}
	case "string":
		return SelectString
	default:
		return SelectExact
	}
}

// annotation returns the function of name's declaration, if any.
func (a *argumentAnalysis) annotation(name string) *FunctionRef {
	for _, d := range a.msg.Declarations {
		if d.Name == name {
			return d.Value.Function
		}
	}
	return nil
}

// sortedKeys returns the keys of m in lexicographic order, for
// deterministic traversal of option and attribute maps.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func (a *argumentAnalysis) result() []Argument {
	out := make([]Argument, len(a.order))
	for i, name := range a.order {
		out[i] = *a.args[name]
	}
	return out
}
