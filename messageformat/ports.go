package messageformat

// Ports (RFC 0002 §5): the platform depends on these interfaces, not on
// the third-party engine, so an engine can be replaced behind them and the
// conformance suites in testdata/ prove the replacement.

// Parser turns authoring syntax into the canonical model.
type Parser interface {
	ParseMF2(src string) (Message, error)
	ParseMF1(src, locale string) (Message, error)
}

// Formatter formats canonical messages, to a string or to formatted parts.
type Formatter interface {
	Format(msg Message, locale string, values map[string]any, opts ...FormatOption) (string, error)
	FormatToParts(msg Message, locale string, values map[string]any, opts ...FormatOption) ([]Part, error)
}

// Engine is the default Parser and Formatter, backed by
// github.com/kaptinlin/messageformat-go. Its zero value is ready to use.
type Engine struct{}

var (
	_ Parser    = Engine{}
	_ Formatter = Engine{}
)

// ParseMF2 implements Parser; see the package-level ParseMF2.
func (Engine) ParseMF2(src string) (Message, error) { return ParseMF2(src) }

// ParseMF1 implements Parser; see the package-level ParseMF1.
func (Engine) ParseMF1(src, locale string) (Message, error) { return ParseMF1(src, locale) }

// Format implements Formatter; see the package-level Format.
func (Engine) Format(msg Message, locale string, values map[string]any, opts ...FormatOption) (string, error) {
	return Format(msg, locale, values, opts...)
}

// FormatToParts implements Formatter; see the package-level FormatToParts.
func (Engine) FormatToParts(msg Message, locale string, values map[string]any, opts ...FormatOption) ([]Part, error) {
	return FormatToParts(msg, locale, values, opts...)
}
