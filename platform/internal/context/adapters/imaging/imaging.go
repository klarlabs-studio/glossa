// Package imaging implements Context's ImageNormalizer (RFC 0004 §3.3):
// an uploaded capture image is spooled to disk while its SHA-256 is
// checked, its PNG header is read to refuse anything over 40 megapixels
// before a pixel is decoded, and its pixels are decoded with image/png
// and encoded again. Go's PNG encoder writes only the chunks the pixels
// need (IHDR, PLTE, tRNS, IDAT, IEND), so text, EXIF, ICC and every
// other chunk the upload carried is gone, and the same pixels always
// encode to the same bytes: the stored image's digest dedupes across
// builds.
//
// A decoded 40-megapixel image takes up to 320 MB (16-bit RGBA), so
// decoding is a bulkhead: the decoded bytes of the images being
// re-encoded at once stay within a budget per process (by default one
// image of the largest kind, or many small ones); the rest wait. Nothing
// is held in memory beyond the images being re-encoded.
package imaging

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"image/color"
	"image/png"
	"io"
	"os"

	"golang.org/x/sync/semaphore"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
)

// maxBytesPerPixel is what the widest decoded pixel takes (16-bit RGBA).
const maxBytesPerPixel = 8

// DefaultDecodeBudget bounds the decoded bytes a process holds at once:
// one 40-megapixel image of 16-bit RGBA.
const DefaultDecodeBudget = domain.MaxImagePixels * maxBytesPerPixel

// Normalizer implements app.ImageNormalizer.
type Normalizer struct {
	dir    string
	budget *semaphore.Weighted
}

var _ app.ImageNormalizer = (*Normalizer)(nil)

// New returns a Normalizer that spools to dir (the system's temporary
// directory when empty) and decodes images while their decoded bytes
// stay within budget (DefaultDecodeBudget when smaller than it: the
// largest image must always fit).
func New(dir string, budget int64) *Normalizer {
	return &Normalizer{dir: dir, budget: semaphore.NewWeighted(max(budget, DefaultDecodeBudget))}
}

// Normalize implements app.ImageNormalizer.
func (n *Normalizer) Normalize(ctx context.Context, part io.Reader, want domain.Digest) (app.NormalizedImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	upload, err := n.spool(part, want)
	if err != nil {
		return nil, err
	}
	defer remove(upload)
	weight, err := checkHeader(upload)
	if err != nil {
		return nil, err
	}
	return n.reencode(ctx, upload, weight)
}

// spool copies the part to a temporary file, at most MaxImageBytes of
// it, and checks its digest.
func (n *Normalizer) spool(part io.Reader, want domain.Digest) (*os.File, error) {
	f, err := os.CreateTemp(n.dir, "glossa-capture-upload-*")
	if err != nil {
		return nil, fmt.Errorf("imaging: spool: %w", err)
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(part, domain.MaxImageBytes+1))
	switch {
	case err != nil:
		remove(f)
		return nil, err
	case size > domain.MaxImageBytes:
		remove(f)
		return nil, fmt.Errorf("%w: image %s is larger than %d bytes", domain.ErrImageTooLarge, want, domain.MaxImageBytes)
	case sum(h) != want:
		remove(f)
		return nil, fmt.Errorf("%w: the part named %s has the SHA-256 %s", domain.ErrInvalidImage, want, sum(h))
	}
	return f, nil
}

// checkHeader reads the PNG header: a PNG of at most MaxImagePixels.
// It returns the bytes the decoded image will take.
func checkHeader(f *os.File) (int64, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	cfg, err := png.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return 0, fmt.Errorf("%w: not a PNG: %v", domain.ErrInvalidImage, err)
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > domain.MaxImagePixels {
		return 0, fmt.Errorf("%w: %d×%d pixels is more than %d", domain.ErrImageTooLarge, cfg.Width, cfg.Height, domain.MaxImagePixels)
	}
	return pixels * bytesPerPixel(cfg.ColorModel), nil
}

// bytesPerPixel is what image/png decodes a pixel of model into.
func bytesPerPixel(model color.Model) int64 {
	switch model {
	case color.GrayModel:
		return 1
	case color.Gray16Model:
		return 2
	case color.RGBAModel, color.NRGBAModel:
		return 4
	}
	if _, paletted := model.(color.Palette); paletted {
		return 1
	}
	return maxBytesPerPixel
}

// reencode decodes the spooled upload and encodes its pixels again into
// a new temporary file.
func (n *Normalizer) reencode(ctx context.Context, upload *os.File, weight int64) (app.NormalizedImage, error) {
	weight = max(weight, 1)
	if err := n.budget.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	defer n.budget.Release(weight)
	if _, err := upload.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, err := png.Decode(bufio.NewReader(upload))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidImage, err)
	}
	out, err := os.CreateTemp(n.dir, "glossa-capture-image-*")
	if err != nil {
		return nil, fmt.Errorf("imaging: spool: %w", err)
	}
	h := sha256.New()
	w := &counter{w: io.MultiWriter(out, h)}
	buf := bufio.NewWriter(w)
	err = (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(buf, img)
	if err == nil {
		err = buf.Flush()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out.Name())
		return nil, fmt.Errorf("imaging: re-encode: %w", err)
	}
	b := img.Bounds()
	return &spooled{path: out.Name(), size: w.n,
		image: domain.Image{Digest: sum(h), Width: b.Dx(), Height: b.Dy(), Bytes: w.n}}, nil
}

func sum(h hash.Hash) domain.Digest { return domain.Digest(hex.EncodeToString(h.Sum(nil))) }

func remove(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// spooled is a re-encoded image in a temporary file.
type spooled struct {
	path  string
	size  int64
	image domain.Image
}

func (s *spooled) Image() domain.Image { return s.image }
func (s *spooled) Size() int64         { return s.size }

func (s *spooled) Open() (io.ReadCloser, error) { return os.Open(s.path) }

func (s *spooled) Close() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
