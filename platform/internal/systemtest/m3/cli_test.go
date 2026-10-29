//go:build system

package m3_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

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

// runner runs the glossa CLI in a directory, the way the product's CI
// does: the same code path the binary's main takes, with the server, the
// project and the token in the environment.
type runner struct {
	t   *testing.T
	dir string
	env map[string]string
}

// result is one CLI run.
type result struct {
	code           int
	stdout, stderr string
}

func (r *runner) run(args ...string) result {
	r.t.Helper()
	var out, errb bytes.Buffer
	code := cli.Main(context.Background(), args, cli.Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Getenv:      func(k string) string { return r.env[k] },
		Dir:         r.dir,
		Credentials: &memStore{tokens: map[string]string{}},
		Version:     "0.0.0-m3",
	})
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

// ok runs a command with --json, decodes its document and fails the test
// unless it exited cleanly.
func (r *runner) ok(out any, args ...string) result {
	r.t.Helper()
	res := r.run(append(args, "--json")...)
	if res.code != int(cli.ExitOK) {
		r.t.Fatalf("glossa %s: exit %d\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), res.code, res.stdout, res.stderr)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(res.stdout), out); err != nil {
			r.t.Fatalf("glossa %s --json: not one JSON document (%v):\n%s", strings.Join(args, " "), err, res.stdout)
		}
	}
	return res
}
