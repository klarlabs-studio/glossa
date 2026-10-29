package formats

import "io"

// Limits bound what a reader accepts. A zero field takes the reader's
// default.
type Limits struct {
	// MaxBytes bounds the input size.
	MaxBytes int64
	// MaxItems bounds the number of entries, units or concepts.
	MaxItems int
}

// Default limits. Catalog formats are read whole; TMX streams, so its
// byte limit is higher (see tmx.DefaultLimits).
const (
	DefaultMaxBytes = 64 << 20
	DefaultMaxItems = 200_000
	// MaxDepth bounds XML element and JSON object nesting.
	MaxDepth = 64
)

// WithDefaults fills zero fields from def.
func (l Limits) WithDefaults(def Limits) Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = def.MaxBytes
	}
	if l.MaxItems <= 0 {
		l.MaxItems = def.MaxItems
	}
	return l
}

// DefaultLimits are the catalog readers' defaults.
var DefaultLimits = Limits{MaxBytes: DefaultMaxBytes, MaxItems: DefaultMaxItems}

// LimitReader returns a reader that fails with ErrTooLarge once more than
// n bytes have been read from r.
func LimitReader(r io.Reader, n int64) io.Reader {
	return &limitedReader{r: r, left: n}
}

type limitedReader struct {
	r    io.Reader
	left int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n - int(-l.left), ErrTooLarge
	}
	return n, err
}

// ReadAll reads r whole, failing with ErrTooLarge beyond max bytes.
func ReadAll(r io.Reader, max int64) ([]byte, error) {
	return io.ReadAll(LimitReader(r, max))
}
