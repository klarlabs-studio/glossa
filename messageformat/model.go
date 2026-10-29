package messageformat

// This file defines Glossa's canonical message model: the Unicode
// MessageFormat 2 data model, mirrored one to one from
// testdata/unicode/data-model/message.schema.json (the wire contract for
// precompiled messages). JSON encoding lives in model_json.go.

// MessageType distinguishes a single-pattern message from a select message.
type MessageType string

// Message types defined by the MF2 data model.
const (
	PatternMessageType MessageType = "message"
	SelectMessageType  MessageType = "select"
)

// Message is a MessageFormat 2 message in its canonical data model form.
//
// A pattern message (Type == PatternMessageType) uses Pattern; a select
// message (Type == SelectMessageType) uses Selectors and Variants.
// Declarations apply to both.
type Message struct {
	Type         MessageType
	Declarations []Declaration
	Pattern      Pattern
	Selectors    []VariableRef
	Variants     []Variant
}

// DeclarationType distinguishes `.input` from `.local` declarations.
type DeclarationType string

// Declaration types defined by the MF2 data model.
const (
	InputDeclaration DeclarationType = "input"
	LocalDeclaration DeclarationType = "local"
)

// Declaration binds a variable name to an expression. For an input
// declaration the expression's operand is the variable itself.
type Declaration struct {
	Type  DeclarationType
	Name  string
	Value Expression
}

// Variant is one case of a select message: a key per selector and the
// pattern to use when the keys match.
type Variant struct {
	Keys  []VariantKey
	Value Pattern
}

// VariantKey is either a literal key or the catch-all key `*`.
type VariantKey struct {
	// Catchall marks the `*` key.
	Catchall bool
	// Value is the literal key. For a catch-all key it optionally carries
	// the key's original source text, as the data model allows.
	Value string
}

// Pattern is a sequence of text, expression and markup elements.
type Pattern []PatternElement

// PatternElement is one of Text, Expression or Markup.
type PatternElement interface {
	patternElement()
}

// Text is literal text in a pattern.
type Text string

// Expression is a placeholder: an operand, a function annotation, or both.
type Expression struct {
	// Arg is the operand: a Literal, a VariableRef, or nil for a
	// function-only expression.
	Arg Operand
	// Function is the optional function annotation.
	Function *FunctionRef
	// Attributes are the expression's attributes (`@name=value`).
	Attributes Attributes
}

// MarkupKind is the kind of a markup placeholder.
type MarkupKind string

// Markup kinds defined by the MF2 data model.
const (
	MarkupOpen       MarkupKind = "open"
	MarkupStandalone MarkupKind = "standalone"
	MarkupClose      MarkupKind = "close"
)

// Markup is a markup placeholder such as {#b}, {/b} or {#br/}.
type Markup struct {
	Kind       MarkupKind
	Name       string
	Options    Options
	Attributes Attributes
}

func (Text) patternElement()       {}
func (Expression) patternElement() {}
func (Markup) patternElement()     {}

// Operand is a Literal or a VariableRef.
type Operand interface {
	operand()
}

// Literal is an immediately defined string value.
type Literal struct {
	Value string
}

// VariableRef refers to a declared or external variable.
type VariableRef struct {
	Name string
}

func (Literal) operand()     {}
func (VariableRef) operand() {}

// FunctionRef is a function annotation with its options.
type FunctionRef struct {
	Name    string
	Options Options
}

// Options maps option names to literal or variable values.
type Options map[string]Operand

// Attributes maps attribute names to literal values. A nil value is a
// value-less attribute (`@name`), encoded as `true` in JSON.
type Attributes map[string]*Literal

// IsSelect reports whether m is a select message.
func (m Message) IsSelect() bool {
	return m.Type == SelectMessageType
}

// clonePatternElement returns a deep copy of el, so converted messages
// never share maps or pointers between variants.
func clonePatternElement(el PatternElement) PatternElement {
	switch el := el.(type) {
	case Expression:
		return el.clone()
	case Markup:
		el.Options = cloneOptions(el.Options)
		el.Attributes = cloneAttributes(el.Attributes)
		return el
	default:
		return el
	}
}

func (e Expression) clone() Expression {
	if e.Function != nil {
		fn := FunctionRef{Name: e.Function.Name, Options: cloneOptions(e.Function.Options)}
		e.Function = &fn
	}
	e.Attributes = cloneAttributes(e.Attributes)
	return e
}

func cloneOptions(o Options) Options {
	if o == nil {
		return nil
	}
	out := make(Options, len(o))
	for k, v := range o {
		out[k] = v
	}
	return out
}

func cloneAttributes(a Attributes) Attributes {
	if a == nil {
		return nil
	}
	out := make(Attributes, len(a))
	for k, v := range a {
		if v != nil {
			lit := *v
			v = &lit
		}
		out[k] = v
	}
	return out
}

// Patterns returns every pattern of m: the single pattern of a pattern
// message, or each variant's pattern in order.
func (m Message) Patterns() []Pattern {
	if !m.IsSelect() {
		return []Pattern{m.Pattern}
	}
	patterns := make([]Pattern, len(m.Variants))
	for i, v := range m.Variants {
		patterns[i] = v.Value
	}
	return patterns
}
