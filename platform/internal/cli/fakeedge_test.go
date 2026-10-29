package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"
)

// fakeEdge is glossa-edge serving one release to one delivery key in
// one environment: a manifest (unsigned; the runtime verifies a
// signature only when given keys) and one artifact per locale, each
// message the MF2 model the platform's own MF1 converter makes of its
// text — what an import and a publish would put there.
type fakeEdge struct {
	srv *httptest.Server

	mu       sync.Mutex
	manifest []byte
	byHash   map[string][]byte
}

func newFakeEdge(t *testing.T, key, env, source string, texts map[string]map[string]string) *fakeEdge {
	t.Helper()
	e := &fakeEdge{}
	e.publish(t, env, source, texts)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		prefix := "/v1/" + key + "/"
		switch {
		case r.URL.Path == prefix+env+"/manifest.json":
			w.Header().Set("ETag", `"`+hashOf(e.manifest)[:16]+`"`)
			_, _ = w.Write(e.manifest)
		case strings.HasPrefix(r.URL.Path, prefix+"a/"):
			b, ok := e.byHash[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"a/"), ".json")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(b)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// publish replaces the release the edge serves.
func (e *fakeEdge) publish(t *testing.T, env, source string, texts map[string]map[string]string) {
	t.Helper()
	byHash := map[string][]byte{}
	artifacts := map[string]any{}
	var locales []map[string]string
	for locale, msgs := range texts {
		models := map[string]json.RawMessage{}
		for key, text := range msgs {
			msg, err := mf.ParseMF1(text, locale)
			if err != nil {
				t.Fatalf("%s %s: %v", locale, key, err)
			}
			raw, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			models[key] = raw
		}
		doc, err := json.Marshal(map[string]any{"schema": "glossa.artifact/v1", "locale": locale, "namespace": "default", "messages": models})
		if err != nil {
			t.Fatal(err)
		}
		h := hashOf(doc)
		byHash[h] = doc
		artifacts[locale] = map[string]any{"default": map[string]any{"sha256": h, "size": len(doc)}}
		locales = append(locales, map[string]string{"code": locale, "direction": "ltr"})
	}
	manifest, err := json.Marshal(map[string]any{
		"schema": "glossa.manifest/v1", "project": "prj_1", "environment": env,
		"release": map[string]any{"id": "rel_" + hashOf([]byte(strings.Join(sortedHashes(byHash), ",")))[:8], "version": 1,
			"createdAt": "2026-10-01T08:00:00Z"},
		"sourceLocale": source, "locales": locales, "fallback": map[string]any{}, "artifacts": artifacts,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.manifest, e.byHash = manifest, byHash
	e.mu.Unlock()
}

func sortedHashes(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for h := range m {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// jsModules finds the two built packages --verify renders with — the
// environment's GLOSSA_V0_FORMAT_MODULE and GLOSSA_RUNTIME_MODULE, or
// this repository's packages/format and runtimes/js/runtime when they
// are built — and node; it skips the test when any is missing.
func jsModules(t *testing.T) (format, runtime string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	format = orDefault(os.Getenv("GLOSSA_V0_FORMAT_MODULE"), filepath.Join(root, "packages", "format"))
	runtime = orDefault(os.Getenv("GLOSSA_RUNTIME_MODULE"), filepath.Join(root, "runtimes", "js", "glossa"))
	for d, entry := range map[string]string{format: "dist/index.js", runtime: "dist/runtime/index.js"} {
		if _, err := os.Stat(filepath.Join(d, filepath.FromSlash(entry))); err != nil {
			t.Skipf("%s is not built (set GLOSSA_V0_FORMAT_MODULE and GLOSSA_RUNTIME_MODULE, or build it)", d)
		}
	}
	return format, runtime
}

// fakeV03Texts is a v0.3 API serving texts (locale → key → ICU).
func fakeV03Texts(t *testing.T, texts map[string]map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer glossa_v03key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/projects/site/locales":
			var ls []map[string]string
			for l := range texts {
				ls = append(ls, map[string]string{"code": l})
			}
			_ = json.NewEncoder(w).Encode(ls)
		case strings.HasPrefix(r.URL.Path, "/api/v1/projects/site/locales/"):
			l := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/site/locales/"), "/messages")
			_ = json.NewEncoder(w).Encode(map[string]any{"project": "site", "locale": l, "messages": texts[l]})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "{}")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
