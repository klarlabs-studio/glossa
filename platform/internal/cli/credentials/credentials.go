// Package credentials stores the CLI's API tokens, one per server: in
// the OS keychain when one is usable (macOS Keychain through
// /usr/bin/security, the freedesktop Secret Service through secret-tool
// on Linux), otherwise in a 0600 JSON file under the user config
// directory. Tokens never appear on a command line: they reach the
// keychain tools on stdin.
//
// GLOSSA_TOKEN in the environment wins over anything stored (CI); that
// rule lives in the CLI, not here.
package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNotFound means no token is stored for the server.
var ErrNotFound = errors.New("credentials: no token stored for this server")

// Store keeps one token per server URL.
type Store interface {
	Get(server string) (string, error)
	Set(server, token string) error
	Delete(server string) error
	// Name says where tokens go, for messages ("macOS Keychain").
	Name() string
}

// Default is the keychain when one is usable on this OS, falling back to
// the file store at path when the keychain fails.
func Default(path string) Store {
	file := &File{Path: path}
	if kc := osKeychain(); kc != nil {
		return &Fallback{Primary: kc, Secondary: file}
	}
	return file
}

// DefaultPath is the token file under the user config directory.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "glossa", "credentials.json"), nil
}

// ── file ────────────────────────────────────────────────────────────

// File stores tokens in a JSON file readable only by the user.
type File struct{ Path string }

type fileData struct {
	Tokens map[string]string `json:"tokens"`
}

// Name implements Store.
func (f *File) Name() string { return f.Path }

func (f *File) read() (fileData, error) {
	d := fileData{Tokens: map[string]string{}}
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("credentials: %s is not valid JSON: %w", f.Path, err)
	}
	if d.Tokens == nil {
		d.Tokens = map[string]string{}
	}
	return d, nil
}

func (f *File) write(d fileData) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

// Get implements Store.
func (f *File) Get(server string) (string, error) {
	d, err := f.read()
	if err != nil {
		return "", err
	}
	tok, ok := d.Tokens[server]
	if !ok {
		return "", ErrNotFound
	}
	return tok, nil
}

// Set implements Store.
func (f *File) Set(server, token string) error {
	d, err := f.read()
	if err != nil {
		return err
	}
	d.Tokens[server] = token
	return f.write(d)
}

// Delete implements Store.
func (f *File) Delete(server string) error {
	d, err := f.read()
	if err != nil {
		return err
	}
	if _, ok := d.Tokens[server]; !ok {
		return ErrNotFound
	}
	delete(d.Tokens, server)
	return f.write(d)
}

// ── fallback ────────────────────────────────────────────────────────

// Fallback reads from and writes to Primary, and uses Secondary when
// Primary fails (a locked or absent keychain, a headless session).
type Fallback struct {
	Primary, Secondary Store
	// Used is the store the last Set wrote to.
	Used Store
}

// Name implements Store.
func (f *Fallback) Name() string {
	if f.Used != nil {
		return f.Used.Name()
	}
	return f.Primary.Name()
}

// Get implements Store.
func (f *Fallback) Get(server string) (string, error) {
	tok, err := f.Primary.Get(server)
	if err == nil {
		return tok, nil
	}
	return f.Secondary.Get(server)
}

// Set implements Store.
func (f *Fallback) Set(server, token string) error {
	if err := f.Primary.Set(server, token); err == nil {
		f.Used = f.Primary
		_ = f.Secondary.Delete(server) // don't leave an older copy behind
		return nil
	}
	f.Used = f.Secondary
	return f.Secondary.Set(server, token)
}

// Delete implements Store.
func (f *Fallback) Delete(server string) error {
	e1 := f.Primary.Delete(server)
	e2 := f.Secondary.Delete(server)
	if e1 == nil || e2 == nil {
		return nil
	}
	if errors.Is(e1, ErrNotFound) && errors.Is(e2, ErrNotFound) {
		return ErrNotFound
	}
	return errors.Join(e1, e2)
}

// ── keychain ────────────────────────────────────────────────────────

// Runner runs a command with stdin and returns its stdout.
type Runner func(ctx context.Context, stdin string, name string, args ...string) (string, error)

// ExecRunner runs real commands.
func ExecRunner(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// service names the CLI's keychain entries.
const service = "glossa-cli"

const keychainTimeout = 10 * time.Second

func osKeychain() Store {
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/usr/bin/security"); err == nil {
			return &MacKeychain{Run: ExecRunner, Bin: "/usr/bin/security"}
		}
	case "linux":
		if p, err := exec.LookPath("secret-tool"); err == nil && os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
			return &SecretService{Run: ExecRunner, Bin: p}
		}
	}
	return nil
}

// MacKeychain stores tokens as generic passwords in the login keychain.
type MacKeychain struct {
	Run Runner
	Bin string
}

// Name implements Store.
func (k *MacKeychain) Name() string { return "macOS Keychain" }

func (k *MacKeychain) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), keychainTimeout)
}

// Get implements Store.
func (k *MacKeychain) Get(server string) (string, error) {
	ctx, cancel := k.ctx()
	defer cancel()
	out, err := k.Run(ctx, "", k.Bin, "find-generic-password", "-s", service, "-a", server, "-w")
	if err != nil {
		return "", fmt.Errorf("%w (%v)", ErrNotFound, err)
	}
	return strings.TrimSpace(out), nil
}

// Set implements Store. The token goes through `security -i` on stdin,
// so it never shows up in the process list.
func (k *MacKeychain) Set(server, token string) error {
	if strings.ContainsAny(server+token, "\"\n\\") {
		return errors.New("credentials: server or token contains characters the keychain tool can't take")
	}
	ctx, cancel := k.ctx()
	defer cancel()
	cmd := fmt.Sprintf("add-generic-password -U -s %q -a %q -l %q -w %q\n", service, server, "Glossa CLI ("+server+")", token)
	_, err := k.Run(ctx, cmd, k.Bin, "-i")
	return err
}

// Delete implements Store.
func (k *MacKeychain) Delete(server string) error {
	ctx, cancel := k.ctx()
	defer cancel()
	if _, err := k.Run(ctx, "", k.Bin, "delete-generic-password", "-s", service, "-a", server); err != nil {
		return fmt.Errorf("%w (%v)", ErrNotFound, err)
	}
	return nil
}

// SecretService stores tokens through secret-tool (GNOME Keyring,
// KWallet, KeePassXC).
type SecretService struct {
	Run Runner
	Bin string
}

// Name implements Store.
func (s *SecretService) Name() string { return "Secret Service keyring" }

// Get implements Store.
func (s *SecretService) Get(server string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	out, err := s.Run(ctx, "", s.Bin, "lookup", "service", service, "server", server)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", ErrNotFound
	}
	return strings.TrimSpace(out), nil
}

// Set implements Store; secret-tool reads the secret from stdin.
func (s *SecretService) Set(server, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	_, err := s.Run(ctx, token, s.Bin, "store", "--label", "Glossa CLI ("+server+")", "service", service, "server", server)
	return err
}

// Delete implements Store.
func (s *SecretService) Delete(server string) error {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	if _, err := s.Run(ctx, "", s.Bin, "clear", "service", service, "server", server); err != nil {
		return fmt.Errorf("%w (%v)", ErrNotFound, err)
	}
	return nil
}
