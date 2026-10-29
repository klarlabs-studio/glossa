//go:build system

package m5_test

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli"
	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
)

// memStore is an in-memory credentials.Store: the exit test never
// touches the developer's keychain.
type memStore struct{ tokens map[string]string }

func (m *memStore) Get(s string) (string, error) {
	if t, ok := m.tokens[s]; ok {
		return t, nil
	}
	return "", credentials.ErrNotFound
}
func (m *memStore) Set(s, t string) error { m.tokens[s] = t; return nil }
func (m *memStore) Delete(s string) error { delete(m.tokens, s); return nil }
func (m *memStore) Name() string          { return "memory" }

// cliResult is one CLI run.
type cliResult struct {
	code           int
	stdout, stderr string
}

func (r cliResult) String() string {
	out := strings.TrimSpace(r.stderr)
	if out == "" {
		out = strings.TrimSpace(r.stdout)
	}
	return lastLines(out, 4)
}

// glossa runs the CLI in process — the code path the binary's main
// takes — in dir, with env as its whole environment.
func glossa(dir string, env map[string]string, args ...string) cliResult {
	return glossaWith(dir, env, &memStore{tokens: map[string]string{}}, args...)
}

// glossaWith is glossa with the credential store a login filled.
func glossaWith(dir string, env map[string]string, store credentials.Store, args ...string) cliResult {
	var out, errb bytes.Buffer
	code := cli.Main(context.Background(), args, cli.Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv:      func(k string) string { return env[k] },
		Dir:         dir,
		Credentials: store,
		Version:     "0.0.0-m5",
	})
	return cliResult{code: code, stdout: out.String(), stderr: errb.String()}
}

// lockedBuffer is a stderr the test reads while the CLI still writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var userCodePattern = regexp.MustCompile(`\b[A-Z]{4}-[A-Z]{4}\b`)

// glossaDeviceLogin runs `glossa login --device` the way a person does
// (RFC 0006 §7.2): the CLI shows a code and polls, someone signed in
// approves it in a browser. approve plays that someone — here the
// owner's cookie session and CSRF token. The CLI stores the session in
// store, from which later runs authenticate.
func glossaDeviceLogin(dir string, env map[string]string, store credentials.Store, approve func(userCode string) error) cliResult {
	var out bytes.Buffer
	errb := &lockedBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- cli.Main(context.Background(), []string{"login", "--device", "--client-name", "glossa CLI in the M5 exit test"}, cli.Env{
			Stdin: strings.NewReader(""), Stdout: &out, Stderr: errb,
			Getenv:      func(k string) string { return env[k] },
			Dir:         dir,
			Credentials: store,
			Version:     "0.0.0-m5",
			Sleep:       func(context.Context, time.Duration) error { time.Sleep(200 * time.Millisecond); return nil },
		})
	}()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case code := <-done:
			return cliResult{code: code, stdout: out.String(), stderr: errb.String()}
		case <-deadline:
			return cliResult{code: -1, stderr: "timed out waiting for the device sign-in: " + errb.String()}
		case <-time.After(100 * time.Millisecond):
		}
		if code := userCodePattern.FindString(errb.String()); code != "" {
			if err := approve(code); err != nil {
				return cliResult{code: -1, stderr: "approving " + code + ": " + err.Error()}
			}
			r := <-done
			return cliResult{code: r, stdout: out.String(), stderr: errb.String()}
		}
	}
}
