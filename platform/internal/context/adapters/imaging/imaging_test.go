package imaging_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/context/adapters/imaging"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
)

// picture is a small image with some structure.
func picture(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 13), B: uint8(x ^ y), A: 255}) //nolint:gosec // test pattern
		}
	}
	return img
}

func encode(t *testing.T, img image.Image, level png.CompressionLevel) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// chunk is one PNG chunk with its CRC.
func chunk(typ string, data []byte) []byte {
	var out bytes.Buffer
	_ = binary.Write(&out, binary.BigEndian, uint32(len(data))) //nolint:gosec // small test data
	out.WriteString(typ)
	out.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	_ = binary.Write(&out, binary.BigEndian, crc.Sum32())
	return out.Bytes()
}

// withText inserts a tEXt chunk (metadata) after the IHDR.
func withText(t *testing.T, b []byte, text string) []byte {
	t.Helper()
	const ihdrEnd = 8 + 8 + 13 + 4
	out := append([]byte{}, b[:ihdrEnd]...)
	out = append(out, chunk("tEXt", []byte("Comment\x00"+text))...)
	return append(out, b[ihdrEnd:]...)
}

// header is a PNG that declares w×h pixels and holds no image data.
func header(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	return append(out, chunk("IEND", nil)...)
}

func digestOf(b []byte) domain.Digest {
	sum := sha256.Sum256(b)
	return domain.Digest(hex.EncodeToString(sum[:]))
}

func normalize(t *testing.T, n *imaging.Normalizer, b []byte) ([]byte, domain.Image, error) {
	t.Helper()
	got, err := n.Normalize(context.Background(), bytes.NewReader(b), digestOf(b))
	if err != nil {
		return nil, domain.Image{}, err
	}
	defer func() {
		if err := got.Close(); err != nil {
			t.Error(err)
		}
	}()
	r, err := got.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size() != int64(len(out)) {
		t.Errorf("Size = %d, read %d", got.Size(), len(out))
	}
	return out, got.Image(), nil
}

func TestNormalizeReencodesWithoutMetadata(t *testing.T) {
	n := imaging.New(t.TempDir(), 0)
	src := picture(64, 40)
	upload := withText(t, encode(t, src, png.BestSpeed), "customer: Jane Doe")
	if _, err := png.Decode(bytes.NewReader(upload)); err != nil {
		t.Fatalf("the test upload isn't a PNG: %v", err)
	}
	out, img, err := normalize(t, n, upload)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("tEXt")) || bytes.Contains(out, []byte("Jane Doe")) {
		t.Error("the stored image kept the upload's metadata")
	}
	if img.Digest != digestOf(out) || img.Width != 64 || img.Height != 40 {
		t.Errorf("image = %+v, want the re-encoded bytes' digest and 64×40", img)
	}
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []image.Point{{0, 0}, {63, 39}, {17, 23}} {
		r1, g1, b1, a1 := decoded.At(p.X, p.Y).RGBA()
		r2, g2, b2, a2 := src.At(p.X, p.Y).RGBA()
		if [4]uint32{r1, g1, b1, a1} != [4]uint32{r2, g2, b2, a2} {
			t.Errorf("pixel %v = %v, want %v", p, decoded.At(p.X, p.Y), src.At(p.X, p.Y))
		}
	}
}

func TestNormalizeGivesTheSamePixelsTheSameDigest(t *testing.T) {
	n := imaging.New(t.TempDir(), 0)
	src := picture(50, 30)
	_, fast, err := normalize(t, n, encode(t, src, png.BestSpeed))
	if err != nil {
		t.Fatal(err)
	}
	_, best, err := normalize(t, n, withText(t, encode(t, src, png.BestCompression), "x"))
	if err != nil {
		t.Fatal(err)
	}
	if fast.Digest != best.Digest {
		t.Errorf("two encodings of the same pixels stored as %s and %s", fast.Digest, best.Digest)
	}
}

func TestNormalizeRefusals(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, picture(8, 8), nil); err != nil {
		t.Fatal(err)
	}
	valid := encode(t, picture(8, 8), png.DefaultCompression)
	truncated := valid[:len(valid)-20]
	cases := []struct {
		why  string
		body []byte
		want error
	}{
		{"a JPEG", jpg.Bytes(), domain.ErrInvalidImage},
		{"not an image", []byte("hello"), domain.ErrInvalidImage},
		{"a truncated PNG", truncated, domain.ErrInvalidImage},
		{"over 40 megapixels", header(8000, 5001), domain.ErrImageTooLarge},
		{"over 10 MB", append(valid, bytes.Repeat([]byte{0}, domain.MaxImageBytes)...), domain.ErrImageTooLarge},
	}
	n := imaging.New(t.TempDir(), 0)
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			if _, _, err := normalize(t, n, tc.body); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	t.Run("a part that isn't its name", func(t *testing.T) {
		_, err := n.Normalize(context.Background(), bytes.NewReader(valid), domain.Digest(strings.Repeat("0", 64)))
		if !errors.Is(err, domain.ErrInvalidImage) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a broken upload", func(t *testing.T) {
		boom := errors.New("connection reset")
		_, err := n.Normalize(context.Background(), io.MultiReader(bytes.NewReader(valid[:10]), errReader{boom}), digestOf(valid))
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want the reader's", err)
		}
	})
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestNormalizeLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	n := imaging.New(dir, 0)
	valid := encode(t, picture(8, 8), png.DefaultCompression)
	if _, _, err := normalize(t, n, valid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := normalize(t, n, []byte("junk")); err == nil {
		t.Fatal("junk was accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("spool left %d files", len(entries))
	}
}

func TestNormalizeStopsWhenCancelled(t *testing.T) {
	n := imaging.New(t.TempDir(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	valid := encode(t, picture(8, 8), png.DefaultCompression)
	if _, err := n.Normalize(ctx, bytes.NewReader(valid), digestOf(valid)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}
