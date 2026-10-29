package objectstore_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/objectstoretest"
)

func TestMemory(t *testing.T) {
	objectstoretest.Run(t, objectstore.NewMemory())
	objectstoretest.RunStreams(t, objectstore.NewMemory())
}

func TestDir(t *testing.T) {
	s, err := objectstore.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	objectstoretest.Run(t, s)
	objectstoretest.RunStreams(t, s)
}

func TestDirLeavesNoTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	s, err := objectstore.NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), "a/b.json", []byte("{}"), "application/json"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "a"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "b.json" {
		t.Fatalf("entries = %v, %v", entries, err)
	}
}

func TestNewDirNeedsADirectory(t *testing.T) {
	if _, err := objectstore.NewDir(""); err == nil {
		t.Error("empty root accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := objectstore.NewDir(file); err == nil {
		t.Error("a file accepted as root")
	}
}

func TestCheckKey(t *testing.T) {
	for _, ok := range []string{"a", "v1/projects/x/a/0f.json", "k=v", "a.b-c_d"} {
		if err := objectstore.CheckKey(ok); err != nil {
			t.Errorf("CheckKey(%q) = %v", ok, err)
		}
	}
}
