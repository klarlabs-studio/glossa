package glossa

import (
	"math"
	"testing"
)

// Vectors from RFC 8785 §3.2.2.3 and Appendix B.

func TestFormatES6Number(t *testing.T) {
	cases := map[uint64]string{
		0x0000000000000000: "0",
		0x8000000000000000: "0",
		0x0000000000000001: "5e-324",
		0x8000000000000001: "-5e-324",
		0x7fefffffffffffff: "1.7976931348623157e+308",
		0xffefffffffffffff: "-1.7976931348623157e+308",
		0x4340000000000000: "9007199254740992",
		0xc340000000000000: "-9007199254740992",
		0x4430000000000000: "295147905179352830000",
		0x44b52d02c7e14af5: "9.999999999999997e+22",
		0x44b52d02c7e14af6: "1e+23",
		0x44b52d02c7e14af7: "1.0000000000000001e+23",
		0x444b1ae4d6e2ef4e: "999999999999999700000",
		0x444b1ae4d6e2ef4f: "999999999999999900000",
		0x444b1ae4d6e2ef50: "1e+21",
		0x3eb0c6f7a0b5ed8c: "9.999999999999997e-7",
		0x3eb0c6f7a0b5ed8d: "0.000001",
		0x41b3de4355555553: "333333333.3333332",
		0x41b3de4355555554: "333333333.33333325",
		0x41b3de4355555555: "333333333.3333333",
		0x41b3de4355555556: "333333333.3333334",
		0x41b3de4355555557: "333333333.33333343",
		0xbecbf647612f3696: "-0.0000033333333333333333",
		0x43143ff3c1cb0959: "1424953923781206.2",
	}
	for bits, want := range cases {
		got, err := formatES6Number(math.Float64frombits(bits))
		if err != nil || got != want {
			t.Errorf("formatES6Number(%#x) = %q, %v; want %q", bits, got, err, want)
		}
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := formatES6Number(bad); err == nil {
			t.Errorf("formatES6Number(%v) succeeded; want an error", bad)
		}
	}
}

func TestCanonicalJSON(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"RFC 8785 §3.2.2 example",
			`{"numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
			  "string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
			  "literals": [null, true, false]}`,
			`{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€$\u000f\nA'B\"\\\\\"/"}`,
		},
		{
			"RFC 8785 §3.2.3 sorting by UTF-16 code units",
			`{"\u20ac":"Euro Sign","\r":"Carriage Return","\ufb33":"Hebrew Letter Dalet With Dagesh","1":"One","\ud83d\ude00":"Emoji: Grinning Face","\u0080":"Control","\u00f6":"Latin Small Letter O With Diaeresis"}`,
			"{\"\\r\":\"Carriage Return\",\"1\":\"One\",\"\u0080\":\"Control\",\"\u00f6\":\"Latin Small Letter O With Diaeresis\"," +
				"\"\u20ac\":\"Euro Sign\",\"\U0001F600\":\"Emoji: Grinning Face\",\"\ufb33\":\"Hebrew Letter Dalet With Dagesh\"}",
		},
		{"no HTML escaping", `{"a":"<&>\u2028"}`, "{\"a\":\"<&>\u2028\"}"},
		{"control characters", `"\u0001\b\t\f\u001f"`, `"\u0001\b\t\f\u001f"`},
		{"nesting", `{"b":[{"d":1,"c":{}}],"a":[]}`, `{"a":[],"b":[{"c":{},"d":1}]}`},
	}
	for _, tc := range cases {
		got, err := canonicalJSON([]byte(tc.in))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

func TestCanonicalJSONRejects(t *testing.T) {
	for _, in := range []string{
		`{"a":1,"a":2}`,
		`{"a":1e400}`,
		`[1,]`,
		`{"a":1} {"b":2}`,
		"\"\xff\"",
		``,
	} {
		if got, err := canonicalJSON([]byte(in)); err == nil {
			t.Errorf("canonicalJSON(%q) = %s; want an error", in, got)
		}
	}
}

func TestCanonicalJSONWithout(t *testing.T) {
	got, err := canonicalJSONWithout([]byte(`{"z":1,"signatures":[{"sig":"x"}],"a":{"signatures":2}}`), "signatures")
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":{"signatures":2},"z":1}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
