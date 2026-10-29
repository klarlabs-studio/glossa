package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// BundleSource serves one release's manifest and artifacts as exact
// bytes.
type BundleSource interface {
	Manifest(ctx context.Context) ([]byte, error)
	Artifact(ctx context.Context, sha256 string) ([]byte, error)
}

// BundleRef is the release and environment a bundle must be of. Runtimes
// reject a manifest naming another environment than theirs, so the
// manifest is checked before anything is written. Empty fields aren't
// checked.
type BundleRef struct {
	ReleaseID   string
	Environment string
}

// manifest is the part of glossa.manifest/v1 a bundle needs.
type manifest struct {
	Schema      string `json:"schema"`
	Environment string `json:"environment"`
	Release     struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	} `json:"release"`
	Artifacts map[string]map[string]struct {
		SHA256 string `json:"sha256"`
		Size   int    `json:"size"`
	} `json:"artifacts"`
}

// Bundle reports a written bundle.
type Bundle struct {
	Dir         string   `json:"dir"`
	ReleaseID   string   `json:"release_id"`
	Version     int      `json:"version"`
	Environment string   `json:"environment"`
	Locales     []string `json:"locales"`
	Artifacts   int      `json:"artifacts"`
	Bytes       int      `json:"bytes"`
	// Removed counts artifacts of an earlier bundle in the directory that
	// this release no longer names.
	Removed int `json:"removed"`
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// WriteBundle writes the release to dir in the layout runtimes load
// (SPEC §3.4): manifest.json exactly as served, and a/<sha256>.json for
// every artifact, each verified against its manifest hash. The manifest
// is written last, so an interrupted run never leaves a manifest naming
// missing artifacts; artifacts of an earlier bundle the new manifest
// doesn't name are removed after it.
func WriteBundle(ctx context.Context, src BundleSource, dir string, want BundleRef) (Bundle, error) {
	raw, err := src.Manifest(ctx)
	if err != nil {
		return Bundle{}, err
	}
	m, err := parseManifest(raw, want)
	if err != nil {
		return Bundle{}, err
	}
	b := Bundle{Dir: dir, ReleaseID: m.Release.ID, Version: m.Release.Version, Environment: m.Environment, Bytes: len(raw)}
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		return Bundle{}, err
	}
	written := map[string]bool{}
	for locale, namespaces := range m.Artifacts {
		b.Locales = append(b.Locales, locale)
		for ns, a := range namespaces {
			if written[a.SHA256] {
				continue
			}
			n, err := writeArtifact(ctx, src, dir, a.SHA256)
			if err != nil {
				return Bundle{}, fmt.Errorf("release: artifact %s/%s: %w", locale, ns, err)
			}
			written[a.SHA256] = true
			b.Artifacts++
			b.Bytes += n
		}
	}
	sort.Strings(b.Locales)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		return Bundle{}, err
	}
	b.Removed, err = pruneArtifacts(dir, written)
	return b, err
}

func parseManifest(raw []byte, want BundleRef) (manifest, error) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("release: manifest is not JSON: %w", err)
	}
	if m.Schema != "glossa.manifest/v1" {
		return m, fmt.Errorf("release: manifest schema %q is not glossa.manifest/v1", m.Schema)
	}
	if want.ReleaseID != "" && m.Release.ID != want.ReleaseID {
		return m, fmt.Errorf("release: the manifest names release %q, not %q", m.Release.ID, want.ReleaseID)
	}
	if want.Environment != "" && m.Environment != want.Environment {
		return m, fmt.Errorf("release: the manifest names environment %q, not %q", m.Environment, want.Environment)
	}
	for locale, namespaces := range m.Artifacts {
		for ns, a := range namespaces {
			if !shaPattern.MatchString(a.SHA256) {
				return m, fmt.Errorf("release: artifact %s/%s has an invalid sha256 %q", locale, ns, a.SHA256)
			}
		}
	}
	return m, nil
}

func writeArtifact(ctx context.Context, src BundleSource, dir, sha string) (int, error) {
	data, err := src.Artifact(ctx, sha)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != sha {
		return 0, fmt.Errorf("integrity: bytes hash to %s, the manifest says %s", got, sha)
	}
	return len(data), os.WriteFile(filepath.Join(dir, "a", sha+".json"), data, 0o644)
}

// pruneArtifacts removes a/<sha256>.json files keep doesn't name. Other
// files are left alone.
func pruneArtifacts(dir string, keep map[string]bool) (int, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "a"))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		sha, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || !shaPattern.MatchString(sha) || keep[sha] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, "a", e.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
