package formats

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrTooLarge means the input exceeds a reader's Limits.
	ErrTooLarge = errors.New("input exceeds the size limit")
	// ErrUnsupported means the input uses a feature or version this
	// package doesn't read (e.g. XLIFF 1.2, a DTD internal subset).
	ErrUnsupported = errors.New("unsupported")
	// ErrInvalid means the input or the model violates the format.
	ErrInvalid = errors.New("invalid")
)

// Error reports a problem in a file. Line and Column are 1-based and zero
// when unknown; Item names what was being read (`unit "checkout.title"`).
type Error struct {
	Format string
	Line   int
	Column int
	Item   string
	Err    error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Format)
	if e.Line > 0 {
		fmt.Fprintf(&b, ":%d", e.Line)
		if e.Column > 0 {
			fmt.Fprintf(&b, ":%d", e.Column)
		}
	}
	b.WriteString(": ")
	if e.Item != "" {
		b.WriteString(e.Item)
		b.WriteString(": ")
	}
	b.WriteString(e.Err.Error())
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// Invalidf returns an error wrapping ErrInvalid.
func Invalidf(msg string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(msg, args...))
}

// Unsupportedf returns an error wrapping ErrUnsupported.
func Unsupportedf(msg string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(msg, args...))
}
