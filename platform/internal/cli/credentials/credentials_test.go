package credentials_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
)

const tok = "glossa_api_0123456789012345678901234567890123456789abc"

func TestFileStoreIsPrivateAndPerServer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "glossa", "credentials.json")
	f := &credentials.File{Path: p}
	if _, err := f.Get("https://a"); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("empty Get = %v", err)
	}
	if err := f.Set("https://a", tok); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("https://b", "other"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Get("https://a"); err != nil || got != tok {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", st.Mode().Perm())
		}
	}
	if err := f.Delete("https://a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("https://a"); !errors.Is(err, credentials.ErrNotFound) {
		t.Errorf("after delete = %v", err)
	}
	if got, _ := f.Get("https://b"); got != "other" {
		t.Errorf("other server lost: %q", got)
	}
}

// fakeRunner records calls and plays a keychain.
type fakeRunner struct {
	calls  []string
	stdins []string
	fail   bool
	out    string
}

func (r *fakeRunner) run(_ context.Context, stdin, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	r.stdins = append(r.stdins, stdin)
	if r.fail {
		return "", errors.New("keychain locked")
	}
	return r.out, nil
}

func TestMacKeychainPassesTheTokenOnStdin(t *testing.T) {
	r := &fakeRunner{}
	k := &credentials.MacKeychain{Run: r.run, Bin: "security"}
	if err := k.Set("https://glossa.example.com", tok); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.calls[0], tok) {
		t.Errorf("token on the command line: %s", r.calls[0])
	}
	if r.calls[0] != "security -i" || !strings.Contains(r.stdins[0], `-w "`+tok+`"`) {
		t.Errorf("call = %q stdin = %q", r.calls[0], r.stdins[0])
	}
	r.out = tok + "\n"
	if got, err := k.Get("https://glossa.example.com"); err != nil || got != tok {
		t.Errorf("Get = %q, %v", got, err)
	}
}

func TestSecretServicePassesTheTokenOnStdin(t *testing.T) {
	r := &fakeRunner{}
	s := &credentials.SecretService{Run: r.run, Bin: "secret-tool"}
	if err := s.Set("https://g", tok); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.calls[0], tok) || r.stdins[0] != tok {
		t.Errorf("call = %q stdin = %q", r.calls[0], r.stdins[0])
	}
}

func TestFallbackUsesTheFileWhenTheKeychainFails(t *testing.T) {
	file := &credentials.File{Path: filepath.Join(t.TempDir(), "c.json")}
	fb := &credentials.Fallback{Primary: &credentials.MacKeychain{Run: (&fakeRunner{fail: true}).run, Bin: "security"}, Secondary: file}
	if err := fb.Set("https://g", tok); err != nil {
		t.Fatal(err)
	}
	if fb.Name() != file.Path {
		t.Errorf("Name = %q, want the file", fb.Name())
	}
	if got, err := fb.Get("https://g"); err != nil || got != tok {
		t.Errorf("Get = %q, %v", got, err)
	}
}
