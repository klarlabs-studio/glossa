package messageformat

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Exact decimal values. The engine reads numeric strings as float64, so a
// tax amount such as 12345678901234.56 would lose its last digit on the way
// to the formatter. A Decimal keeps the literal text instead; the numeric
// functions (engine_exact.go) hand it to the CLDR formatter and plural rules
// as a decimal string, so no binary floating point ever touches it.

// ErrInvalidDecimal reports text that is not a decimal literal.
var ErrInvalidDecimal = errors.New("messageformat: invalid decimal")

// maxDecimalLength and maxDecimalExponent bound a literal, so formatting a
// value from untrusted input stays cheap: its plain notation has at most
// about maxDecimalLength+maxDecimalExponent digits.
const (
	maxDecimalLength   = 1000
	maxDecimalExponent = 1000
)

// decimalLiteral is the MF2 number-literal grammar (a JSON number).
var decimalLiteral = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][-+]?[0-9]+)?$`)

// Decimal is an exact decimal number, such as a money amount. It keeps the
// literal it was parsed from, and formats to exactly those digits (after
// the rounding the formatting options ask for). The zero value is 0.
//
// A Decimal is accepted wherever a message value may be a number: as the
// operand of :number, :integer, :percent, :currency, :unit and :offset,
// and as an unannotated placeholder, which formats like :number.
type Decimal struct {
	lit string
}

// ParseDecimal parses a decimal literal in the MessageFormat 2 number
// grammar (a JSON number): an optional minus sign, digits without leading
// zeros, an optional fraction and an optional exponent, e.g. "-1234.50" or
// "1.5e3". Literals longer than 1000 characters or with an exponent beyond
// ±1000 are rejected.
func ParseDecimal(s string) (Decimal, error) {
	if len(s) > maxDecimalLength || !decimalLiteral.MatchString(s) {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalidDecimal, truncate(s))
	}
	if _, e, ok := strings.Cut(strings.ToLower(s), "e"); ok {
		if n, err := strconv.Atoi(e); err != nil || n > maxDecimalExponent || n < -maxDecimalExponent {
			return Decimal{}, fmt.Errorf("%w: exponent of %q out of range", ErrInvalidDecimal, truncate(s))
		}
	}
	return Decimal{lit: s}, nil
}

// MustParseDecimal is ParseDecimal for literals known to be valid, such as
// constants. It panics on an invalid literal.
func MustParseDecimal(s string) Decimal {
	d, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

// NewDecimal returns unscaled × 10^-scale, e.g. NewDecimal(1999, 2) is
// 19.99: amounts kept in minor units (cents) become exact decimals without
// any floating-point arithmetic. The scale's trailing zeros are kept
// (NewDecimal(0, 2) is 0.00).
func NewDecimal(unscaled int64, scale int) Decimal {
	return decimalOf(big.NewInt(unscaled), -scale)
}

// String returns the decimal literal.
func (d Decimal) String() string {
	if d.lit == "" {
		return "0"
	}
	return d.lit
}

// MarshalText encodes the decimal as its literal (a JSON string).
func (d Decimal) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText parses a decimal literal.
func (d *Decimal) UnmarshalText(text []byte) error {
	parsed, err := ParseDecimal(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Money is an exact amount in a currency, the value a :currency
// placeholder formats. Currency is an ISO 4217 code ("EUR", "JPY"); the
// CLDR fraction digits of the currency apply unless the placeholder sets
// its own. As an unannotated placeholder, Money formats like :currency;
// under :number it formats its amount.
type Money struct {
	Amount   Decimal
	Currency string
}

// String returns the amount and currency, e.g. "19.99 EUR", for logs; it
// is not a localized format.
func (m Money) String() string {
	return m.Amount.String() + " " + m.Currency
}

func truncate(s string) string {
	const limit = 40
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

// --- exact arithmetic for selection and :offset -----------------------------

// parts splits d into coefficient and exponent: d = coeff × 10^exp.
func (d Decimal) parts() (*big.Int, int) {
	s := strings.ToLower(d.String())
	mant, e, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(e) // validated; "" is 0
	intPart, frac, _ := strings.Cut(mant, ".")
	coeff, _ := new(big.Int).SetString(intPart+frac, 10)
	return coeff, exp - len(frac)
}

// decimalOf writes coeff × 10^exp in plain notation.
func decimalOf(coeff *big.Int, exp int) Decimal {
	if coeff.Sign() == 0 && exp >= 0 {
		return Decimal{lit: "0"}
	}
	digits := new(big.Int).Abs(coeff).String()
	var s string
	if exp >= 0 {
		s = digits + strings.Repeat("0", exp)
	} else {
		n := -exp
		if len(digits) <= n {
			digits = strings.Repeat("0", n-len(digits)+1) + digits
		}
		s = digits[:len(digits)-n] + "." + digits[len(digits)-n:]
	}
	if coeff.Sign() < 0 {
		s = "-" + s
	}
	return Decimal{lit: s}
}

var bigTen = big.NewInt(10)

func pow10(n int) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

// shift returns d × 10^n.
func (d Decimal) shift(n int) Decimal {
	coeff, exp := d.parts()
	return decimalOf(coeff, exp+n)
}

// add returns d + delta.
func (d Decimal) add(delta int64) Decimal {
	coeff, exp := d.parts()
	if exp > 0 {
		coeff.Mul(coeff, pow10(exp))
		exp = 0
	}
	coeff.Add(coeff, new(big.Int).Mul(big.NewInt(delta), pow10(-exp)))
	return decimalOf(coeff, exp)
}

// roundInteger rounds d to an integer, halves away from zero, as the
// engine's :integer rounds its operand.
func (d Decimal) roundInteger() Decimal {
	coeff, exp := d.parts()
	if exp >= 0 {
		return decimalOf(coeff, exp)
	}
	unit := pow10(-exp)
	q, r := new(big.Int).QuoRem(new(big.Int).Abs(coeff), unit, new(big.Int))
	if r.Lsh(r, 1).Cmp(unit) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if coeff.Sign() < 0 {
		q.Neg(q)
	}
	return decimalOf(q, 0)
}

// normalized returns d in plain notation without trailing fraction zeros,
// the form a selection key is compared with.
func (d Decimal) normalized() Decimal {
	coeff, exp := d.parts()
	if coeff.Sign() == 0 {
		return Decimal{lit: "0"}
	}
	r := new(big.Int)
	for exp < 0 {
		q, m := new(big.Int).QuoRem(coeff, bigTen, r)
		if m.Sign() != 0 {
			break
		}
		coeff, exp = q, exp+1
	}
	return decimalOf(coeff, exp)
}

// equal reports whether d and o are the same number.
func (d Decimal) equal(o Decimal) bool {
	a, ea := d.parts()
	b, eb := o.parts()
	if ea > eb {
		a.Mul(a, pow10(ea-eb))
	} else {
		b.Mul(b, pow10(eb-ea))
	}
	return a.Cmp(b) == 0
}
