//go:build integration

package s3store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/objectstoretest"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
)

func TestS3Store(t *testing.T) {
	ctx := context.Background()
	env, err := s3test.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()

	s, err := env.Store(ctx, "glossa-conformance")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Run("conformance", func(t *testing.T) { objectstoretest.Run(t, s) })
	t.Run("streams", func(t *testing.T) { objectstoretest.RunStreams(t, s) })

	t.Run("prefix isolates deployments", func(t *testing.T) {
		cfg := env.Config("glossa-conformance")
		cfg.Prefix = "tenant-a/"
		prefixed, err := s3store.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := prefixed.Put(ctx, "x.json", []byte("{}"), "application/json"); err != nil {
			t.Fatal(err)
		}
		if ok, _ := s.Exists(ctx, "x.json"); ok {
			t.Error("prefixed object visible at the bucket root")
		}
		if ok, _ := s.Exists(ctx, "tenant-a/x.json"); !ok {
			t.Error("prefixed object not under its prefix")
		}
	})

	t.Run("missing bucket", func(t *testing.T) {
		missing, err := s3store.New(env.Config("no-such-bucket"))
		if err != nil {
			t.Fatal(err)
		}
		if err := missing.Ping(ctx); err == nil {
			t.Error("ping of a missing bucket succeeded")
		}
	})

	t.Run("unreachable storage fails fast", func(t *testing.T) {
		cfg := env.Config("glossa-conformance")
		cfg.Endpoint = "127.0.0.1:1"
		cfg.Timeout = 500 * time.Millisecond
		cfg.MaxRetries = 1
		down, err := s3store.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for range 6 {
			if _, err := down.Get(ctx, "x.json", 10); err == nil || errors.Is(err, objectstore.ErrNotFound) {
				t.Fatalf("Get from unreachable storage = %v", err)
			}
		}
		start := time.Now()
		if _, err := down.Get(ctx, "x.json", 10); err == nil || time.Since(start) > 100*time.Millisecond {
			t.Errorf("open circuit didn't fail fast: %v after %s", err, time.Since(start))
		}
	})
}
