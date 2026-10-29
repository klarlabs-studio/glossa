// Package objectstoretest is the conformance suite every objectstore
// adapter runs: the port's behaviour, written once.
package objectstoretest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
)

// Run exercises s. It must start empty (or at least without the keys
// below, which are prefixed with a per-run name).
func Run(t *testing.T, s objectstore.Store) {
	t.Helper()
	ctx := context.Background()
	const limit = 1 << 20

	t.Run("missing object", func(t *testing.T) {
		if _, err := s.Get(ctx, "conformance/missing.json", limit); !errors.Is(err, objectstore.ErrNotFound) {
			t.Errorf("Get missing = %v, want ErrNotFound", err)
		}
		if ok, err := s.Exists(ctx, "conformance/missing.json"); ok || err != nil {
			t.Errorf("Exists missing = %v, %v", ok, err)
		}
		if err := s.Delete(ctx, "conformance/missing.json"); err != nil {
			t.Errorf("Delete missing = %v", err)
		}
	})

	t.Run("put, get, replace, delete", func(t *testing.T) {
		key := "conformance/a/b/object.json"
		if err := s.Put(ctx, key, []byte(`{"v":1}`), "application/json"); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, key, limit); err != nil || string(got) != `{"v":1}` {
			t.Fatalf("Get = %q, %v", got, err)
		}
		if ok, err := s.Exists(ctx, key); !ok || err != nil {
			t.Fatalf("Exists = %v, %v", ok, err)
		}
		if err := s.Put(ctx, key, []byte(`{"v":2}`), "application/json"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Get(ctx, key, limit); string(got) != `{"v":2}` {
			t.Fatalf("after replace Get = %q", got)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, key, limit); !errors.Is(err, objectstore.ErrNotFound) {
			t.Fatalf("after delete Get = %v", err)
		}
	})

	t.Run("empty and binary objects", func(t *testing.T) {
		body := []byte{0, 1, 2, 0xff, '\n'}
		if err := s.Put(ctx, "conformance/bin", body, "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, "conformance/bin", limit); err != nil || !bytes.Equal(got, body) {
			t.Fatalf("Get = %v, %v", got, err)
		}
		if err := s.Put(ctx, "conformance/empty", nil, "application/json"); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, "conformance/empty", limit); err != nil || len(got) != 0 {
			t.Fatalf("Get empty = %q, %v", got, err)
		}
	})

	t.Run("size limit", func(t *testing.T) {
		if err := s.Put(ctx, "conformance/big", bytes.Repeat([]byte("x"), 100), "text/plain"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "conformance/big", 99); !errors.Is(err, objectstore.ErrTooLarge) {
			t.Errorf("Get over limit = %v, want ErrTooLarge", err)
		}
		if got, err := s.Get(ctx, "conformance/big", 100); err != nil || len(got) != 100 {
			t.Errorf("Get at limit = %d bytes, %v", len(got), err)
		}
	})

	t.Run("invalid keys", func(t *testing.T) {
		for _, key := range []string{"", "/abs", "a/../b", "a//b", "trailing/", "sp ace", "a/./b"} {
			if err := s.Put(ctx, key, []byte("x"), "text/plain"); !errors.Is(err, objectstore.ErrInvalidKey) {
				t.Errorf("Put(%q) = %v, want ErrInvalidKey", key, err)
			}
			if _, err := s.Get(ctx, key, limit); !errors.Is(err, objectstore.ErrInvalidKey) {
				t.Errorf("Get(%q) = %v, want ErrInvalidKey", key, err)
			}
		}
	})

	t.Run("concurrent writers", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = s.Put(ctx, "conformance/race", []byte(fmt.Sprintf(`{"writer":%d}`, i)), "application/json")
			}()
		}
		wg.Wait()
		got, err := s.Get(ctx, "conformance/race", limit)
		if err != nil || !bytes.HasPrefix(got, []byte(`{"writer":`)) || !bytes.HasSuffix(got, []byte("}")) {
			t.Fatalf("torn or missing object after concurrent writes: %q, %v", got, err)
		}
	})
}

// RunStreams exercises the streaming half of s: large objects written
// from a reader and read back through Open, all or nothing.
func RunStreams(t *testing.T, s objectstore.StreamStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("stream in and out", func(t *testing.T) {
		body := bytes.Repeat([]byte("0123456789abcdef"), 3<<16) // 3 MiB, more than one buffer
		key := "conformance/stream/large.bin"
		n, err := s.PutStream(ctx, key, bytes.NewReader(body), "application/octet-stream")
		if err != nil || n != int64(len(body)) {
			t.Fatalf("PutStream = %d, %v", n, err)
		}
		rc, err := s.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("Open read %d bytes, %v; want %d identical bytes", len(got), err, len(body))
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("open a missing object", func(t *testing.T) {
		if _, err := s.Open(ctx, "conformance/stream/missing"); !errors.Is(err, objectstore.ErrNotFound) {
			t.Errorf("Open missing = %v, want ErrNotFound", err)
		}
		if _, err := s.Open(ctx, "../escape"); !errors.Is(err, objectstore.ErrInvalidKey) {
			t.Errorf("Open(../escape) = %v, want ErrInvalidKey", err)
		}
	})

	t.Run("a failed stream stores nothing", func(t *testing.T) {
		key := "conformance/stream/failed"
		broken := io.MultiReader(bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20)), errReader{})
		if _, err := s.PutStream(ctx, key, broken, "application/octet-stream"); !errors.Is(err, errBroken) {
			t.Fatalf("PutStream from a failing reader = %v, want its error", err)
		}
		if ok, err := s.Exists(ctx, key); ok || err != nil {
			t.Errorf("a failed stream left an object: %v, %v", ok, err)
		}
	})
}

var errBroken = errors.New("objectstoretest: the reader broke")

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBroken }
