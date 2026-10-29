package messageformat

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// jsonEqual reports whether two JSON documents are semantically equal
// (object key order is irrelevant).
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("decode a: %v", err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("decode b: %v", err)
	}
	return reflect.DeepEqual(va, vb)
}

func TestMessageJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "empty pattern message",
			json: `{"type":"message","declarations":[],"pattern":[]}`,
		},
		{
			name: "text and variable",
			json: `{"type":"message","declarations":[],"pattern":["Hallo ",{"type":"expression","arg":{"type":"variable","name":"name"}},"!"]}`,
		},
		{
			name: "function with literal and variable options and attributes",
			json: `{"type":"message","declarations":[],"pattern":[{"type":"expression","arg":{"type":"variable","name":"total"},"function":{"type":"function","name":"currency","options":{"currency":{"type":"literal","value":"EUR"},"currencyDisplay":{"type":"variable","name":"display"}}},"attributes":{"mf1:argType":{"type":"literal","value":"number"},"translate":true}}]}`,
		},
		{
			name: "function-only expression",
			json: `{"type":"message","declarations":[],"pattern":[{"type":"expression","function":{"type":"function","name":"u:now"}}]}`,
		},
		{
			name: "literal operand",
			json: `{"type":"message","declarations":[],"pattern":[{"type":"expression","arg":{"type":"literal","value":"|x|"}}]}`,
		},
		{
			name: "markup open close standalone",
			json: `{"type":"message","declarations":[],"pattern":[{"type":"markup","kind":"open","name":"b"},"fett",{"type":"markup","kind":"close","name":"b"},{"type":"markup","kind":"standalone","name":"br","options":{"x":{"type":"literal","value":"1"}},"attributes":{"id":{"type":"literal","value":"a"}}}]}`,
		},
		{
			name: "select with declarations",
			json: `{"type":"select","declarations":[{"type":"input","name":"n","value":{"type":"expression","arg":{"type":"variable","name":"n"},"function":{"type":"function","name":"number"}}},{"type":"local","name":"m","value":{"type":"expression","arg":{"type":"variable","name":"n"},"function":{"type":"function","name":"offset","options":{"subtract":{"type":"literal","value":"1"}}}}}],"selectors":[{"type":"variable","name":"n"},{"type":"variable","name":"m"}],"variants":[{"keys":[{"type":"literal","value":"0"},{"type":"*"}],"value":["keine"]},{"keys":[{"type":"*"},{"type":"literal","value":"one"}],"value":[{"type":"expression","arg":{"type":"variable","name":"m"}}," Datei"]},{"keys":[{"type":"*","value":"other"},{"type":"*"}],"value":[]}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg Message
			if err := json.Unmarshal([]byte(tt.json), &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonEqual(t, []byte(tt.json), out) {
				t.Errorf("round trip mismatch\n got: %s\nwant: %s", out, tt.json)
			}
			assertMatchesSchema(t, out)
		})
	}
}

func TestMessageJSONZeroValuesEncodeAsArrays(t *testing.T) {
	out, err := json.Marshal(Message{Type: PatternMessageType})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"message","declarations":[],"pattern":[]}`
	if !jsonEqual(t, out, []byte(want)) {
		t.Errorf("got %s, want %s", out, want)
	}
	out, err = json.Marshal(Message{Type: SelectMessageType})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want = `{"type":"select","declarations":[],"selectors":[],"variants":[]}`
	if !jsonEqual(t, out, []byte(want)) {
		t.Errorf("got %s, want %s", out, want)
	}
}

func TestMessageJSONDecodedShape(t *testing.T) {
	src := `{"type":"message","declarations":[{"type":"input","name":"d","value":{"type":"expression","arg":{"type":"variable","name":"d"},"function":{"type":"function","name":"date","options":{"length":{"type":"literal","value":"long"}}}}}],"pattern":["Am ",{"type":"expression","arg":{"type":"variable","name":"d"},"attributes":{"u:id":{"type":"literal","value":"x"},"flag":true}},{"type":"markup","kind":"standalone","name":"br"}]}`
	var got Message
	if err := json.Unmarshal([]byte(src), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := Message{
		Type: PatternMessageType,
		Declarations: []Declaration{{
			Type: InputDeclaration,
			Name: "d",
			Value: Expression{
				Arg:      VariableRef{Name: "d"},
				Function: &FunctionRef{Name: "date", Options: Options{"length": Literal{Value: "long"}}},
			},
		}},
		Pattern: Pattern{
			Text("Am "),
			Expression{
				Arg:        VariableRef{Name: "d"},
				Attributes: Attributes{"u:id": &Literal{Value: "x"}, "flag": nil},
			},
			Markup{Kind: MarkupStandalone, Name: "br"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded shape mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestMessageJSONRejectsInvalidShapes(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"unknown message type", `{"type":"nope","declarations":[],"pattern":[]}`, "message type"},
		{"expression without arg or function", `{"type":"message","declarations":[],"pattern":[{"type":"expression"}]}`, "arg or a function"},
		{"unknown pattern element", `{"type":"message","declarations":[],"pattern":[{"type":"bogus"}]}`, "pattern element"},
		{"pattern element number", `{"type":"message","declarations":[],"pattern":[42]}`, "pattern element"},
		{"unknown operand", `{"type":"message","declarations":[],"pattern":[{"type":"expression","arg":{"type":"function","name":"x"}}]}`, "operand"},
		{"input declaration with literal", `{"type":"message","declarations":[{"type":"input","name":"x","value":{"type":"expression","arg":{"type":"literal","value":"1"}}}],"pattern":[]}`, "input declaration"},
		{"unknown declaration type", `{"type":"message","declarations":[{"type":"x","name":"x","value":{"type":"expression","arg":{"type":"variable","name":"x"}}}],"pattern":[]}`, "declaration type"},
		{"bad markup kind", `{"type":"message","declarations":[],"pattern":[{"type":"markup","kind":"half","name":"b"}]}`, "markup kind"},
		{"bad variant key", `{"type":"select","declarations":[],"selectors":[{"type":"variable","name":"x"}],"variants":[{"keys":[{"type":"variable","name":"x"}],"value":[]}]}`, "variant key"},
		{"bad attribute", `{"type":"message","declarations":[],"pattern":[{"type":"expression","arg":{"type":"variable","name":"x"},"attributes":{"a":false}}]}`, "attribute"},
		{"selector is literal", `{"type":"select","declarations":[],"selectors":[{"type":"literal","value":"x"}],"variants":[]}`, "selector"},
		{"not an object", `[]`, "message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg Message
			err := json.Unmarshal([]byte(tt.json), &msg)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}
