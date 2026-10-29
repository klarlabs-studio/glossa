// Package jcs implements the RFC 8785 JSON Canonicalization Scheme over
// the full I-JSON value space. Release artifacts and manifests are
// written in canonical form, and manifest signatures are Ed25519 over the
// canonical manifest without `signatures` (runtimes/SPEC.md §1.3).
//
// The algorithm is the one the Go runtime verifies with
// (runtimes/go/jcs.go); both run the RFC's test vectors, and the
// end-to-end test proves a published manifest verifies in the runtime.
package jcs

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

// ErrNotIJSON means the input is not I-JSON (RFC 7493): invalid UTF-8,
// duplicate member names, numbers outside the IEEE 754 double range, or
// trailing data.
var ErrNotIJSON = errors.New("jcs: input is not I-JSON")

// Canonicalize returns the canonical form of one JSON value.
func Canonicalize(data []byte) ([]byte, error) { return Without(data, "") }

// Without canonicalizes data with the top-level object member named drop
// removed ("" drops nothing).
func Without(data []byte, drop string) ([]byte, error) {
	v, err := parse(data)
	if err != nil {
		return nil, err
	}
	if obj, ok := v.(map[string]any); ok && drop != "" {
		delete(obj, drop)
	}
	var buf bytes.Buffer
	if err := write(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Marshal encodes v with encoding/json and canonicalizes the result.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("jcs: %w", err)
	}
	return Canonicalize(raw)
}

// parse decodes exactly one JSON value, keeping numbers as text.
func parse(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrNotIJSON)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", ErrNotIJSON)
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotIJSON, err)
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
			return nil, fmt.Errorf("%w: %v", ErrNotIJSON, err)
		}
		name, _ := tok.(string)
		if _, dup := obj[name]; dup {
			return nil, fmt.Errorf("%w: duplicate member %q", ErrNotIJSON, name)
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

func write(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	case string:
		writeString(buf, t)
	case json.Number:
		return writeNumber(buf, t)
	case []any:
		return writeArray(buf, t)
	case map[string]any:
		return writeObject(buf, t)
	default:
		return fmt.Errorf("%w: unexpected %T", ErrNotIJSON, v)
	}
	return nil
}

func writeNumber(buf *bytes.Buffer, n json.Number) error {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return fmt.Errorf("%w: number %s: %v", ErrNotIJSON, n, err)
	}
	s, err := formatNumber(f)
	if err != nil {
		return err
	}
	buf.WriteString(s)
	return nil
}

func writeArray(buf *bytes.Buffer, arr []any) error {
	buf.WriteByte('[')
	for i, el := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := write(buf, el); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

func writeObject(buf *bytes.Buffer, obj map[string]any) error {
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
		writeString(buf, k)
		buf.WriteByte(':')
		if err := write(buf, obj[k]); err != nil {
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

// writeString escapes only what JSON requires (RFC 8785 §3.2.2.2): `"`,
// `\` and control characters, with the short forms where they exist.
func writeString(buf *bytes.Buffer, s string) {
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

// formatNumber serializes f like ECMAScript's Number.prototype.toString
// (ECMA-262 §6.1.6.1.20), as RFC 8785 §3.2.2.3 requires.
func formatNumber(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("%w: %v is not a JSON number", ErrNotIJSON, f)
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
