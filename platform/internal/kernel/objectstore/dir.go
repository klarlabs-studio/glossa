package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Dir is a Store on a local directory, for single-node development: a
// glossa-server and a glossa-edge on one machine share it. Writes go to
// a temporary file that is renamed into place, so readers never see a
// partly written object.
type Dir struct{ root string }

var _ Store = (*Dir)(nil)

// NewDir returns a Store rooted at root, creating it if needed.
func NewDir(root string) (*Dir, error) {
	if root == "" {
		return nil, errors.New("objectstore: directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("objectstore: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("objectstore: create %s: %w", abs, err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("objectstore: %s is not a directory", abs)
	}
	return &Dir{root: abs}, nil
}

func (d *Dir) path(key string) (string, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	return filepath.Join(d.root, filepath.FromSlash(key)), nil
}

// Get implements Reader.
func (d *Dir) Get(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(p) //nolint:gosec // p is a validated key under root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("objectstore: open %s: %w", key, err)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("objectstore: read %s: %w", key, err)
	}
	if int64(len(b)) > maxBytes {
		return nil, ErrTooLarge
	}
	return b, nil
}

// Exists implements Reader.
func (d *Dir) Exists(_ context.Context, key string) (bool, error) {
	p, err := d.path(key)
	if err != nil {
		return false, err
	}
	fi, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("objectstore: stat %s: %w", key, err)
	}
	return fi.Mode().IsRegular(), nil
}

// Put implements Writer. Published objects are public by design; a
// colocated edge running as another user must be able to read them.
func (d *Dir) Put(ctx context.Context, key string, body []byte, _ string) error {
	_, err := d.write(ctx, key, 0o644, func(w io.Writer) (int64, error) { //nolint:gosec // public release data
		n, err := w.Write(body)
		return int64(n), err
	})
	return err
}

var _ StreamStore = (*Dir)(nil)

// PutStream implements Streamer. Interchange files are private to this
// process's user.
func (d *Dir) PutStream(ctx context.Context, key string, r io.Reader, _ string) (int64, error) {
	return d.write(ctx, key, 0o600, func(w io.Writer) (int64, error) { return io.Copy(w, r) })
}

// Open implements Streamer.
func (d *Dir) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(p) //nolint:gosec // p is a validated key under root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("objectstore: open %s: %w", key, err)
	}
	return f, nil
}

// write fills a temporary file next to the object and renames it into
// place, so readers never see a partly written object. A failed fill
// leaves nothing behind and returns fill's error unwrapped.
func (d *Dir) write(ctx context.Context, key string, mode os.FileMode, fill func(io.Writer) (int64, error)) (int64, error) {
	p, err := d.path(key)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, fmt.Errorf("objectstore: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("objectstore: put %s: %w", key, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename
	n, err := fill(tmp)
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("objectstore: put %s: %w", key, err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return 0, fmt.Errorf("objectstore: put %s: %w", key, err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return 0, fmt.Errorf("objectstore: put %s: %w", key, err)
	}
	return n, nil
}

// Delete implements Writer.
func (d *Dir) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("objectstore: delete %s: %w", key, err)
	}
	return nil
}
