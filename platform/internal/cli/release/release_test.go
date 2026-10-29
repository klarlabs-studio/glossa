package release_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/release"
)

type fakeSource struct {
	manifest  []byte
	artifacts map[string][]byte
}

func (f fakeSource) Manifest(context.Context) ([]byte, error) { return f.manifest, nil }

func (f fakeSource) Artifact(_ context.Context, sha string) ([]byte, error) {
	b, ok := f.artifacts[sha]
	if !ok {
		return nil, errors.New("not found")
	}
	return b, nil
}

func hash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func source(tamper bool) fakeSource {
	de := []byte(`{"schema":"glossa.artifact/v1","locale":"de","namespace":"default","messages":{}}`)
	en := []byte(`{"schema":"glossa.artifact/v1","locale":"en","namespace":"default","messages":{}}`)
	m := fmt.Sprintf(`{"schema":"glossa.manifest/v1","environment":"production","release":{"id":"rel_1","version":7},"artifacts":{"de":{"default":{"sha256":"%s","size":%d}},"en":{"default":{"sha256":"%s","size":%d}}}}`,
		hash(de), len(de), hash(en), len(en))
	arts := map[string][]byte{hash(de): de, hash(en): en}
	if tamper {
		arts[hash(en)] = []byte("tampered")
	}
	return fakeSource{manifest: []byte(m), artifacts: arts}
}

var want = release.BundleRef{ReleaseID: "rel_1", Environment: "production"}

func TestWriteBundleWritesTheSpecLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	src := source(false)
	b, err := release.WriteBundle(context.Background(), src, dir, want)
	if err != nil {
		t.Fatal(err)
	}
	if b.ReleaseID != "rel_1" || b.Version != 7 || b.Environment != "production" || b.Artifacts != 2 || strings.Join(b.Locales, ",") != "de,en" {
		t.Errorf("bundle = %+v", b)
	}
	got, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || string(got) != string(src.manifest) {
		t.Errorf("manifest = %s, %v", got, err)
	}
	for sha, want := range src.artifacts {
		got, err := os.ReadFile(filepath.Join(dir, "a", sha+".json"))
		if err != nil || string(got) != string(want) {
			t.Errorf("artifact %s = %s, %v", sha, got, err)
		}
	}
}

func TestWriteBundleRemovesArtifactsTheManifestNoLongerNames(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "a", strings.Repeat("0", 64)+".json")
	keep := filepath.Join(dir, "a", "README")
	for _, p := range []string{stale, keep} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := release.WriteBundle(context.Background(), source(false), dir, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale artifact kept: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a file that isn't an artifact was removed: %v", err)
	}
	if b.Removed != 1 {
		t.Errorf("removed = %d", b.Removed)
	}
}

func TestWriteBundleRefusesTamperedArtifactsAndLeavesNoManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := release.WriteBundle(context.Background(), source(true), dir, want)
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("manifest written despite a bad artifact: %v", err)
	}
}

func TestWriteBundleRefusesAManifestForAnotherReleaseOrEnvironment(t *testing.T) {
	for _, ref := range []release.BundleRef{
		{ReleaseID: "rel_2", Environment: "production"},
		{ReleaseID: "rel_1", Environment: "staging"},
	} {
		dir := t.TempDir()
		_, err := release.WriteBundle(context.Background(), source(false), dir, ref)
		if err == nil || !strings.Contains(err.Error(), "manifest names") {
			t.Errorf("%+v: err = %v", ref, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%+v: wrote %v", ref, entries)
		}
	}
}
