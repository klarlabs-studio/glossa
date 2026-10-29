package glossa

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Persisted last-good release (runtimes/SPEC.md §3, step 2): a cache
// directory holding the manifest of the release last activated, and its
// artifacts by SHA-256. Every write is a rename of a fully written
// temporary file, and the manifest is written last, so a crash never
// leaves a manifest pointing at missing artifacts.

const (
	stateFile          = "release.json"
	installationIDFile = "installation-id"
	artifactsDir       = "a"
	dirPerm            = 0o700
	filePerm           = 0o600
)

// store is a cache directory. A nil *store is a disabled cache.
type store struct {
	dir string
}

// persistedState is the commit record of the last-good release.
type persistedState struct {
	ETag string `json:"etag"`
	// Manifest holds the manifest's exact bytes.
	Manifest string `json:"manifest"`
}

func newStore(dir string) *store {
	return &store{dir: dir}
}

// cacheScope names the subdirectory for one edge, delivery key and
// environment, so clients for different projects never share state.
func cacheScope(edgeURL, deliveryKey, environment string) string {
	sum := sha256.Sum256([]byte(edgeURL + "\n" + deliveryKey + "\n" + environment))
	return hex.EncodeToString(sum[:8])
}

func (s *store) loadState() (persistedState, error) {
	var st persistedState
	if s == nil {
		return st, fs.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(s.dir, stateFile))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("%w: persisted state: %v", errSchema, err)
	}
	return st, nil
}

func (s *store) saveState(st persistedState) error {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.dir, stateFile, b)
}

// installationID reads the persisted installation id (SPEC §1.4).
func (s *store) installationID() (string, error) {
	if s == nil {
		return "", fs.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(s.dir, installationIDFile))
	if err != nil {
		return "", err
	}
	if id := strings.TrimSpace(string(b)); installationIDPattern.MatchString(id) {
		return id, nil
	}
	return "", fmt.Errorf("glossa: %s is not 32 lowercase hex digits", installationIDFile)
}

func (s *store) saveInstallationID(id string) error {
	if s == nil {
		return nil
	}
	return writeFileAtomic(s.dir, installationIDFile, []byte(id))
}

func (s *store) artifact(digest string) ([]byte, error) {
	if s == nil {
		return nil, fs.ErrNotExist
	}
	if !sha256Pattern.MatchString(digest) {
		return nil, fmt.Errorf("glossa: invalid artifact digest %q", digest)
	}
	return os.ReadFile(filepath.Join(s.dir, artifactsDir, digest+".json"))
}

// saveArtifact persists verified artifact bytes. Artifacts are content
// addressed, so writing one before its release activates is harmless.
func (s *store) saveArtifact(digest string, body []byte) error {
	if s == nil {
		return nil
	}
	if !sha256Pattern.MatchString(digest) {
		return fmt.Errorf("glossa: invalid artifact digest %q", digest)
	}
	return writeFileAtomic(filepath.Join(s.dir, artifactsDir), digest+".json", body)
}

// prune removes artifacts the last-good release no longer references.
// It's best effort: a leftover file only costs disk space.
func (s *store) prune(keep map[string]bool) {
	if s == nil {
		return
	}
	dir := filepath.Join(s.dir, artifactsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		digest, ok := strings.CutSuffix(e.Name(), ".json")
		if ok && sha256Pattern.MatchString(digest) && !keep[digest] {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// writeFileAtomic writes name in dir through a synced temporary file and a
// rename, so readers see either the old or the new content.
func writeFileAtomic(dir, name string, data []byte) (err error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), filePerm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
