package glossa

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// RFC 8785 JSON Canonicalization Scheme (JCS), over the full I-JSON value
// space: manifest signatures are Ed25519 over the canonical bytes
// (runtimes/SPEC.md §1.3).

var errNotIJSON = errors.New("jcs: input is not I-JSON")

// canonicalJSON returns the RFC 8785 canonical form of one JSON value.
// Input that is not I-JSON (invalid UTF-8, duplicate member names, numbers
// outside IEEE 754 double range, trailing data) is rejected.
func canonicalJSON(data []byte) ([]byte, error) {
	return canonicalJSONWithout(data, "")
}

// canonicalJSONWithout canonicalizes data with the top-level object member
// named drop removed ("" drops nothing).
func canonicalJSONWithout(data []byte, drop string) ([]byte, error) {
	v, err := parseIJSON(data)
	if err != nil {
		return nil, err
	}
	if obj, ok := v.(map[string]any); ok && drop != "" {
		delete(obj, drop)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseIJSON decodes exactly one JSON value, keeping numbers as text.
func parseIJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: invalid UTF-8", errNotIJSON)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", errNotIJSON)
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNotIJSON, err)
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			return decodeObject(dec)
		}
		return decodeArray(dec)
	default:
		return t, nil
	}
}

func decodeObject(dec *json.Decoder) (map[string]any, error) {
	obj := map[string]any{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errNotIJSON, err)
		}
		name, _ := tok.(string)
		if _, dup := obj[name]; dup {
			return nil, fmt.Errorf("%w: duplicate member %q", errNotIJSON, name)
		}
		if obj[name], err = decodeValue(dec); err != nil {
			return nil, err
		}
	}
	_, err := dec.Token() // '}'
	return obj, err
}

func decodeArray(dec *json.Decoder) ([]any, error) {
	arr := []any{}
	for dec.More() {
		v, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	_, err := dec.Token() // ']'
	return arr, err
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	case string:
		writeCanonicalString(buf, t)
	case json.Number:
		return writeCanonicalNumber(buf, t)
	case []any:
		return writeCanonicalArray(buf, t)
	case map[string]any:
		return writeCanonicalObject(buf, t)
	default:
		return fmt.Errorf("%w: unexpected %T", errNotIJSON, v)
	}
	return nil
}

func writeCanonicalNumber(buf *bytes.Buffer, n json.Number) error {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return fmt.Errorf("%w: number %s: %v", errNotIJSON, n, err)
	}
	s, err := formatES6Number(f)
	if err != nil {
		return err
	}
	buf.WriteString(s)
	return nil
}

func writeCanonicalArray(buf *bytes.Buffer, arr []any) error {
	buf.WriteByte('[')
	for i, el := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeCanonical(buf, el); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func writeCanonicalObject(buf *bytes.Buffer, obj map[string]any) error {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareUTF16)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeCanonicalString(buf, k)
		buf.WriteByte(':')
		if err := writeCanonical(buf, obj[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

// compareUTF16 orders strings by their UTF-16 code units (RFC 8785 §3.2.3).
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

// writeCanonicalString escapes only what JSON requires (RFC 8785 §3.2.2.2):
// `"`, `\` and control characters, with the short forms where they exist.
func writeCanonicalString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// formatES6Number serializes f like ECMAScript's Number.prototype.toString
// (ECMA-262 §6.1.6.1.20), as RFC 8785 §3.2.2.3 requires.
func formatES6Number(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("%w: %v is not a JSON number", errNotIJSON, f)
	}
	if f == 0 {
		return "0", nil
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	digits, n := shortestDigits(f)
	return sign + placeDecimalPoint(digits, n), nil
}

// shortestDigits returns the shortest round-tripping decimal digits of a
// positive f and n, the position of the decimal point: f = 0.digits × 10ⁿ.
func shortestDigits(f float64) (string, int) {
	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	e, _ := strconv.Atoi(exp)
	return strings.Replace(mantissa, ".", "", 1), e + 1
}

func placeDecimalPoint(digits string, n int) string {
	k := len(digits)
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	exp := n - 1
	expSign := "+"
	if exp < 0 {
		expSign, exp = "-", -exp
	}
	mantissa := digits[:1]
	if k > 1 {
		mantissa += "." + digits[1:]
	}
	return mantissa + "e" + expSign + strconv.Itoa(exp)
}
