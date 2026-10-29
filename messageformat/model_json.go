package messageformat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// JSON encoding of the canonical model, exactly in the shape of
// testdata/unicode/data-model/message.schema.json. Decoding validates the
// shape (types, required members) and returns errors, never panics.

var errNotObject = errors.New("expected a JSON object")

// --- Message ---------------------------------------------------------------

type jsonPatternMessage struct {
	Type         MessageType   `json:"type"`
	Declarations []Declaration `json:"declarations"`
	Pattern      Pattern       `json:"pattern"`
}

type jsonSelectMessage struct {
	Type         MessageType   `json:"type"`
	Declarations []Declaration `json:"declarations"`
	Selectors    []VariableRef `json:"selectors"`
	Variants     []Variant     `json:"variants"`
}

// MarshalJSON encodes m in the MF2 data model JSON shape.
func (m Message) MarshalJSON() ([]byte, error) {
	decls := nonNil(m.Declarations)
	switch m.Type {
	case PatternMessageType:
		return json.Marshal(jsonPatternMessage{Type: m.Type, Declarations: decls, Pattern: m.Pattern})
	case SelectMessageType:
		return json.Marshal(jsonSelectMessage{
			Type:         m.Type,
			Declarations: decls,
			Selectors:    nonNil(m.Selectors),
			Variants:     nonNil(m.Variants),
		})
	default:
		return nil, fmt.Errorf("messageformat: cannot encode message type %q", m.Type)
	}
}

// UnmarshalJSON decodes m from the MF2 data model JSON shape.
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type         MessageType   `json:"type"`
		Declarations []Declaration `json:"declarations"`
		Pattern      Pattern       `json:"pattern"`
		Selectors    []VariableRef `json:"selectors"`
		Variants     []Variant     `json:"variants"`
	}
	if err := requireObject(data); err != nil {
		return fmt.Errorf("message: %w", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("message: %w", err)
	}
	switch raw.Type {
	case PatternMessageType:
		*m = Message{Type: raw.Type, Declarations: raw.Declarations, Pattern: raw.Pattern}
	case SelectMessageType:
		*m = Message{Type: raw.Type, Declarations: raw.Declarations, Selectors: raw.Selectors, Variants: raw.Variants}
	default:
		return fmt.Errorf("message: unknown message type %q", raw.Type)
	}
	return nil
}

// --- Declaration -----------------------------------------------------------

type jsonDeclaration struct {
	Type  DeclarationType `json:"type"`
	Name  string          `json:"name"`
	Value Expression      `json:"value"`
}

// MarshalJSON encodes d as an input or local declaration.
func (d Declaration) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonDeclaration(d))
}

// UnmarshalJSON decodes an input or local declaration.
func (d *Declaration) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type  DeclarationType  `json:"type"`
		Name  string           `json:"name"`
		Value *json.RawMessage `json:"value"`
	}
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("declaration: %w", err)
	}
	if raw.Type != InputDeclaration && raw.Type != LocalDeclaration {
		return fmt.Errorf("declaration: unknown declaration type %q", raw.Type)
	}
	if raw.Value == nil {
		return fmt.Errorf("declaration %q: missing value", raw.Name)
	}
	var expr Expression
	if err := json.Unmarshal(*raw.Value, &expr); err != nil {
		return fmt.Errorf("declaration %q: %w", raw.Name, err)
	}
	if raw.Type == InputDeclaration {
		if _, ok := expr.Arg.(VariableRef); !ok {
			return fmt.Errorf("input declaration %q: operand must be a variable", raw.Name)
		}
	}
	*d = Declaration{Type: raw.Type, Name: raw.Name, Value: expr}
	return nil
}

// --- Variants and keys -----------------------------------------------------

type jsonVariant struct {
	Keys  []VariantKey `json:"keys"`
	Value Pattern      `json:"value"`
}

// MarshalJSON encodes v with its keys and pattern.
func (v Variant) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonVariant{Keys: nonNil(v.Keys), Value: v.Value})
}

// UnmarshalJSON decodes a variant.
func (v *Variant) UnmarshalJSON(data []byte) error {
	var raw jsonVariant
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("variant: %w", err)
	}
	*v = Variant(raw)
	return nil
}

// MarshalJSON encodes k as a literal key or the catch-all key.
func (k VariantKey) MarshalJSON() ([]byte, error) {
	if !k.Catchall {
		return Literal{Value: k.Value}.MarshalJSON()
	}
	if k.Value == "" {
		return []byte(`{"type":"*"}`), nil
	}
	return json.Marshal(struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}{"*", k.Value})
}

// UnmarshalJSON decodes a literal or catch-all variant key.
func (k *VariantKey) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("variant key: %w", err)
	}
	switch raw.Type {
	case "*":
		*k = VariantKey{Catchall: true, Value: raw.Value}
	case "literal":
		*k = VariantKey{Value: raw.Value}
	default:
		return fmt.Errorf("variant key: unknown type %q", raw.Type)
	}
	return nil
}

// --- Pattern ---------------------------------------------------------------

// MarshalJSON encodes p as an array of strings, expressions and markup.
// A nil pattern encodes as an empty array.
func (p Pattern) MarshalJSON() ([]byte, error) {
	elems := make([]any, len(p))
	for i, el := range p {
		switch el := el.(type) {
		case Text:
			elems[i] = string(el)
		case Expression, Markup:
			elems[i] = el
		default:
			return nil, fmt.Errorf("pattern: unsupported element %T", el)
		}
	}
	return json.Marshal(elems)
}

// UnmarshalJSON decodes a pattern array.
func (p *Pattern) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("pattern: %w", err)
	}
	out := make(Pattern, 0, len(raw))
	for i, item := range raw {
		el, err := decodePatternElement(item)
		if err != nil {
			return fmt.Errorf("pattern[%d]: %w", i, err)
		}
		out = append(out, el)
	}
	*p = out
	return nil
}

func decodePatternElement(data json.RawMessage) (PatternElement, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, err
		}
		return Text(s), nil
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := decodeObject(trimmed, &probe); err != nil {
		return nil, fmt.Errorf("invalid pattern element: %w", err)
	}
	switch probe.Type {
	case "expression":
		var e Expression
		err := json.Unmarshal(trimmed, &e)
		return e, err
	case "markup":
		var mk Markup
		err := json.Unmarshal(trimmed, &mk)
		return mk, err
	default:
		return nil, fmt.Errorf("invalid pattern element type %q", probe.Type)
	}
}

// --- Expression and markup -------------------------------------------------

type jsonExpression struct {
	Type       string       `json:"type"`
	Arg        Operand      `json:"arg,omitempty"`
	Function   *FunctionRef `json:"function,omitempty"`
	Attributes Attributes   `json:"attributes,omitempty"`
}

// MarshalJSON encodes e as an expression node.
func (e Expression) MarshalJSON() ([]byte, error) {
	if e.Arg == nil && e.Function == nil {
		return nil, errors.New("messageformat: expression needs an arg or a function")
	}
	return json.Marshal(jsonExpression{Type: "expression", Arg: e.Arg, Function: e.Function, Attributes: e.Attributes})
}

// UnmarshalJSON decodes an expression node.
func (e *Expression) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type       string           `json:"type"`
		Arg        *json.RawMessage `json:"arg"`
		Function   *FunctionRef     `json:"function"`
		Attributes Attributes       `json:"attributes"`
	}
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("expression: %w", err)
	}
	if raw.Type != "expression" {
		return fmt.Errorf("expression: unexpected type %q", raw.Type)
	}
	out := Expression{Function: raw.Function, Attributes: raw.Attributes}
	if raw.Arg != nil {
		arg, err := decodeOperand(*raw.Arg)
		if err != nil {
			return fmt.Errorf("expression arg: %w", err)
		}
		out.Arg = arg
	}
	if out.Arg == nil && out.Function == nil {
		return errors.New("expression: needs an arg or a function")
	}
	*e = out
	return nil
}

type jsonMarkup struct {
	Type       string     `json:"type"`
	Kind       MarkupKind `json:"kind"`
	Name       string     `json:"name"`
	Options    Options    `json:"options,omitempty"`
	Attributes Attributes `json:"attributes,omitempty"`
}

// MarshalJSON encodes m as a markup node.
func (m Markup) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonMarkup{Type: "markup", Kind: m.Kind, Name: m.Name, Options: m.Options, Attributes: m.Attributes})
}

// UnmarshalJSON decodes a markup node.
func (m *Markup) UnmarshalJSON(data []byte) error {
	var raw jsonMarkup
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("markup: %w", err)
	}
	switch raw.Kind {
	case MarkupOpen, MarkupStandalone, MarkupClose:
	default:
		return fmt.Errorf("markup: unknown markup kind %q", raw.Kind)
	}
	*m = Markup{Kind: raw.Kind, Name: raw.Name, Options: raw.Options, Attributes: raw.Attributes}
	return nil
}

// --- Functions, options, attributes ----------------------------------------

type jsonFunctionRef struct {
	Type    string  `json:"type"`
	Name    string  `json:"name"`
	Options Options `json:"options,omitempty"`
}

// MarshalJSON encodes f as a function node.
func (f FunctionRef) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonFunctionRef{Type: "function", Name: f.Name, Options: f.Options})
}

// UnmarshalJSON decodes a function node.
func (f *FunctionRef) UnmarshalJSON(data []byte) error {
	var raw jsonFunctionRef
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("function: %w", err)
	}
	if raw.Type != "function" {
		return fmt.Errorf("function: unexpected type %q", raw.Type)
	}
	*f = FunctionRef{Name: raw.Name, Options: raw.Options}
	return nil
}

// UnmarshalJSON decodes an options object of literal or variable values.
func (o *Options) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("options: %w", err)
	}
	out := make(Options, len(raw))
	for name, value := range raw {
		v, err := decodeOperand(value)
		if err != nil {
			return fmt.Errorf("option %q: %w", name, err)
		}
		out[name] = v
	}
	*o = out
	return nil
}

// MarshalJSON encodes attributes; value-less attributes encode as true.
func (a Attributes) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(a))
	for name, lit := range a {
		if lit == nil {
			out[name] = true
		} else {
			out[name] = *lit
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON decodes attributes whose values are literals or true.
func (a *Attributes) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := decodeObject(data, &raw); err != nil {
		return fmt.Errorf("attributes: %w", err)
	}
	out := make(Attributes, len(raw))
	for name, value := range raw {
		if bytes.Equal(bytes.TrimSpace(value), []byte("true")) {
			out[name] = nil
			continue
		}
		op, err := decodeOperand(value)
		lit, ok := op.(Literal)
		if err != nil || !ok {
			return fmt.Errorf("attribute %q: must be a literal or true", name)
		}
		out[name] = &lit
	}
	*a = out
	return nil
}

// --- Operands --------------------------------------------------------------

// MarshalJSON encodes l as a literal node.
func (l Literal) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}{"literal", l.Value})
}

// MarshalJSON encodes v as a variable node.
func (v VariableRef) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}{"variable", v.Name})
}

// UnmarshalJSON decodes a variable node (used for selectors).
func (v *VariableRef) UnmarshalJSON(data []byte) error {
	op, err := decodeOperand(data)
	if err != nil {
		return fmt.Errorf("selector: %w", err)
	}
	ref, ok := op.(VariableRef)
	if !ok {
		return errors.New("selector: must be a variable")
	}
	*v = ref
	return nil
}

func decodeOperand(data []byte) (Operand, error) {
	var raw struct {
		Type  string  `json:"type"`
		Name  *string `json:"name"`
		Value *string `json:"value"`
	}
	if err := decodeObject(data, &raw); err != nil {
		return nil, fmt.Errorf("operand: %w", err)
	}
	switch {
	case raw.Type == "literal" && raw.Value != nil:
		return Literal{Value: *raw.Value}, nil
	case raw.Type == "variable" && raw.Name != nil:
		return VariableRef{Name: *raw.Name}, nil
	default:
		return nil, fmt.Errorf("invalid operand of type %q", raw.Type)
	}
}

// --- helpers ---------------------------------------------------------------

func requireObject(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errNotObject
	}
	return nil
}

func decodeObject(data []byte, v any) error {
	if err := requireObject(data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
