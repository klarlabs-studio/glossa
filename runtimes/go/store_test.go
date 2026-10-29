package glossa

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	s := newStore(t.TempDir())
	if _, err := s.loadState(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty store: err = %v, want fs.ErrNotExist", err)
	}
	body := []byte(deArtifact)
	ref := refFor(deArtifact)
	if err := s.saveArtifact(ref.SHA256, body); err != nil {
		t.Fatal(err)
	}
	got, err := s.artifact(ref.SHA256)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("artifact = %q, %v", got, err)
	}
	st := persistedState{ETag: `"m1"`, Manifest: `{"a":1}`}
	if err := s.saveState(st); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.loadState()
	if err != nil || loaded != st {
		t.Fatalf("loadState = %+v, %v", loaded, err)
	}
}

func TestStoreWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir)
	if err := s.saveState(persistedState{ETag: "x", Manifest: "{}"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || e.Name()[0] == '.' {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestStorePrune(t *testing.T) {
	s := newStore(t.TempDir())
	keep, drop := refFor("keep"), refFor("drop")
	for _, r := range []artifactRef{keep, drop} {
		if err := s.saveArtifact(r.SHA256, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	s.prune(map[string]bool{keep.SHA256: true})
	if _, err := s.artifact(keep.SHA256); err != nil {
		t.Fatalf("kept artifact is gone: %v", err)
	}
	if _, err := s.artifact(drop.SHA256); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("pruned artifact: err = %v", err)
	}
}

func TestStoreRejectsBadDigest(t *testing.T) {
	s := newStore(t.TempDir())
	if err := s.saveArtifact("../escape", []byte("x")); err == nil {
		t.Fatal("a non-digest name must be rejected")
	}
	if _, err := s.artifact("../escape"); err == nil {
		t.Fatal("a non-digest name must be rejected")
	}
}

func TestNilStoreIsDisabled(t *testing.T) {
	var s *store
	if err := s.saveState(persistedState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.loadState(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.artifact(testSHA); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
	if err := s.saveArtifact(testSHA, nil); err != nil {
		t.Fatal(err)
	}
	s.prune(nil)
}

func TestCacheScopeDir(t *testing.T) {
	a := cacheScope("https://edge.example", "pk_1", "production")
	b := cacheScope("https://edge.example", "pk_1", "staging")
	if a == b || len(a) != 16 {
		t.Fatalf("scopes %q and %q", a, b)
	}
}
