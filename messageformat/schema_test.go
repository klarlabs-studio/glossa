package messageformat

import (
	"bytes"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const dataModelSchemaPath = "testdata/unicode/data-model/message.schema.json"

var (
	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
)

// dataModelSchema compiles the vendored MF2 data model JSON Schema, the wire
// contract every serialized message must satisfy.
func dataModelSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaOnce.Do(func() {
		f, err := os.Open(dataModelSchemaPath)
		if err != nil {
			schemaErr = err
			return
		}
		defer func() { _ = f.Close() }()
		doc, err := jsonschema.UnmarshalJSON(f)
		if err != nil {
			schemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("message.schema.json", doc); err != nil {
			schemaErr = err
			return
		}
		schema, schemaErr = c.Compile("message.schema.json")
	})
	if schemaErr != nil {
		t.Fatalf("compile data model schema: %v", schemaErr)
	}
	return schema
}

// assertMatchesSchema validates a JSON document against the data model schema.
func assertMatchesSchema(t *testing.T, doc []byte) {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("decode instance: %v", err)
	}
	if err := dataModelSchema(t).Validate(inst); err != nil {
		t.Errorf("document does not match the MF2 data model schema: %v\n%s", err, doc)
	}
}

// assertMessageMatchesSchema encodes msg and validates it against the schema.
func assertMessageMatchesSchema(t *testing.T, msg Message) {
	t.Helper()
	doc, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertMatchesSchema(t, doc)
}

func TestSchemaRejectsInvalidDocument(t *testing.T) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(`{"type":"message","pattern":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	if dataModelSchema(t).Validate(inst) == nil {
		t.Fatal("schema accepted a message without declarations; the validator is not wired up")
	}
}

func TestEncodedMessagesMatchSchema(t *testing.T) {
	msgs := []Message{
		{Type: PatternMessageType},
		{Type: SelectMessageType},
		{
			Type: SelectMessageType,
			Declarations: []Declaration{{
				Type:  InputDeclaration,
				Name:  "n",
				Value: Expression{Arg: VariableRef{Name: "n"}, Function: &FunctionRef{Name: "number"}},
			}},
			Selectors: []VariableRef{{Name: "n"}},
			Variants: []Variant{
				{Keys: []VariantKey{{Value: "one"}}, Value: Pattern{Expression{Arg: VariableRef{Name: "n"}}, Text(" Datei")}},
				{Keys: []VariantKey{{Catchall: true}}, Value: Pattern{Markup{Kind: MarkupStandalone, Name: "br", Attributes: Attributes{"x": nil}}}},
			},
		},
	}
	for _, m := range msgs {
		assertMessageMatchesSchema(t, m)
	}
}
