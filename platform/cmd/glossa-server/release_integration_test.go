//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	glossa "go.klarlabs.de/glossa/runtimes/go"

	"go.klarlabs.de/glossa/platform/internal/edge"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
	"go.klarlabs.de/glossa/platform/internal/release/releasetest"
)

const bucket = "glossa-e2e"

// testSigningSeed is the Ed25519 seed the e2e deployment signs with.
var testSigningSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

func s3Vars(minio *s3test.Env) map[string]string {
	return map[string]string{
		"GLOSSA_STORAGE_DRIVER":       "s3",
		"GLOSSA_S3_ENDPOINT":          minio.Endpoint,
		"GLOSSA_S3_BUCKET":            bucket,
		"GLOSSA_S3_ACCESS_KEY_ID":     minio.AccessKeyID,
		"GLOSSA_S3_SECRET_ACCESS_KEY": minio.SecretAccessKey,
		"GLOSSA_S3_INSECURE":          "true",
		"GLOSSA_S3_PATH_STYLE":        "true",
	}
}

// startEdge runs a real glossa-edge (the same edge.Run the binary runs)
// on the bucket, and returns its base URL.
func startEdge(t *testing.T, minio *s3test.Env) string {
	t.Helper()
	vars := s3Vars(minio)
	vars["GLOSSA_HTTP_ADDR"] = "127.0.0.1:0"
	vars["GLOSSA_EDGE_KEY_TTL"] = "200ms"
	vars["GLOSSA_EDGE_MANIFEST_TTL"] = "100ms"
	vars["GLOSSA_SHUTDOWN_TIMEOUT"] = "5s"
	cfg, err := config.LoadEdge(lookupFrom(vars))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- edge.Run(ctx, cfg, slog.New(slog.DiscardHandler), "test", func(a net.Addr) { addrc <- a })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("edge did not stop")
		}
	})
	select {
	case a := <-addrc:
		return "http://" + a.String()
	case err := <-done:
		t.Fatalf("edge: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("edge never listened")
	}
	return ""
}

func get(t *testing.T, url string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// runtime loads a release through the Go runtime from the edge (or only
// from bundled catalogs when edgeURL is empty).
func runtime(t *testing.T, edgeURL, key, environment string, keys []glossa.PublicKey, bundled string) *glossa.Client {
	t.Helper()
	cfg := glossa.Config{
		EdgeURL: edgeURL, DeliveryKey: key, Environment: environment, PublicKeys: keys,
		DisableCache: true, RefreshInterval: -1, DisableBidiIsolation: true,
		Retry:  glossa.RetryPolicy{MaxAttempts: 1},
		Logger: slog.New(slog.DiscardHandler),
	}
	if bundled != "" {
		cfg.Bundled = os.DirFS(bundled)
	}
	c, err := glossa.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if edgeURL != "" {
		if err := c.Refresh(context.Background()); err != nil {
			t.Fatalf("runtime refresh: %v", err)
		}
	}
	return c
}

type releaseBody struct {
	ID             string `json:"id"`
	Version        int    `json:"version"`
	ParentID       string `json:"parent_id"`
	ManifestDigest string `json:"manifest_digest"`
	Counts         struct {
		Messages     int `json:"messages"`
		NewArtifacts int `json:"new_artifacts"`
		Locales      map[string]struct {
			Messages int `json:"messages"`
		} `json:"locales"`
	} `json:"counts"`
}

// TestReleaseToRuntime is the core loop's delivery half end to end:
// messages and translations in, a signed release published to MinIO,
// served by a real glossa-edge, loaded and verified by the Go runtime —
// and still served after glossa-server and its database are gone.
func TestReleaseToRuntime(t *testing.T) {
	ctx := context.Background()
	minio, err := s3test.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(minio.Close)
	if _, err := minio.Store(ctx, bucket); err != nil {
		t.Fatal(err)
	}
	vars := s3Vars(minio)
	vars["GLOSSA_RELEASE_SIGNING_KEYS"] = "e2e-2026=" + testSigningSeed
	s := startServerWith(t, vars)
	edgeURL := startEdge(t, minio)

	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "brotwerk", "name": "Brotwerk"}}).decode(t, &org)
	var project struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "web", "name": "Web", "source_locale": "en"}}).decode(t, &project)
	p := "/v1/tenants/" + org.ID + "/projects/" + project.ID
	write := func(method, path string, body any, headers ...string) reply {
		t.Helper()
		c := call{method: method, path: p + path, cookie: ada.cookie, csrf: ada.csrf, body: body, headers: map[string]string{}}
		for i := 0; i+1 < len(headers); i += 2 {
			c.headers[headers[i]] = headers[i+1]
		}
		return s.do(c)
	}
	write("POST", "/locales", map[string]string{"code": "de"}).want(t, http.StatusCreated, "")
	// Ada writes the text; Bob reviews it (an author never approves their own).
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "bob@example.com", "roles": []string{"reviewer"}, "locales": []string{"de"}}}).
		want(t, http.StatusCreated, "")
	bob := s.signIn("bob@example.com")
	approve := func(key string) {
		t.Helper()
		s.do(call{method: "POST", path: p + "/messages/" + key + "/translations/de/reviews", cookie: bob.cookie, csrf: bob.csrf,
			body: map[string]string{"state": "approved"}, headers: map[string]string{"If-Match": `"1"`}}).want(t, http.StatusOK, "")
	}
	write("PUT", "/fallback-graph", map[string]any{"fallback": map[string][]string{"*": {"en"}}}).want(t, http.StatusOK, "")
	write("POST", "/message-upserts", map[string]any{"items": []map[string]any{
		{"key": "checkout.pay", "text": "Pay {amount, number}"},
		{"key": "cart.items", "text": "{count, plural, one {# item} other {# items}}"},
		{"key": "home.title", "text": "Welcome", "namespace": "marketing"},
	}}).want(t, http.StatusOK, "")
	// checkout.pay is approved; cart.items waits for review.
	write("PUT", "/messages/checkout.pay/translations/de", map[string]string{"text": "{amount, number} bezahlen"}).want(t, http.StatusCreated, "")
	write("POST", "/messages/checkout.pay/translations/de/reviews", map[string]string{"state": "approved"}, "If-Match", `"1"`).
		want(t, http.StatusForbidden, "own_text")
	approve("checkout.pay")
	write("PUT", "/messages/cart.items/translations/de", map[string]string{"text": "{count, plural, one {# Artikel} other {# Artikel}}"}).
		want(t, http.StatusCreated, "")

	var key struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	// A key reads production only unless it says otherwise (RFC 0004
	// §4.3); this one follows the release through every environment.
	r := write("POST", "/delivery-keys", map[string]any{
		"name": "web", "scope": map[string]any{"environments": []string{"development", "preview", "staging", "production"}, "branches": false},
	}, "Idempotency-Key", "web-key")
	r.want(t, http.StatusCreated, "")
	r.decode(t, &key)

	// Publish to production: approved text only.
	publish := func(environment, idem string) releaseBody {
		t.Helper()
		r := write("POST", "/releases", map[string]string{"environment": environment}, "Idempotency-Key", idem)
		r.want(t, http.StatusCreated, "")
		var rel releaseBody
		r.decode(t, &rel)
		return rel
	}
	prod1 := publish("production", "prod-1")
	if prod1.Version != 1 || prod1.Counts.Messages != 3 || prod1.Counts.Locales["de"].Messages != 1 || len(prod1.ManifestDigest) != 64 {
		t.Fatalf("release = %+v", prod1)
	}
	if replay := write("POST", "/releases", map[string]string{"environment": "production"}, "Idempotency-Key", "prod-1"); replay.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay headers %v", replay.header)
	}

	// The keys runtimes trust, as the API publishes them.
	var signing struct {
		Keys []struct {
			KeyID     string `json:"key_id"`
			PublicKey string `json:"public_key"`
			Active    bool   `json:"active"`
		} `json:"keys"`
	}
	s.do(call{method: "GET", path: p + "/release-signing-keys", cookie: ada.cookie}).decode(t, &signing)
	if len(signing.Keys) != 1 || signing.Keys[0].KeyID != "e2e-2026" || !signing.Keys[0].Active {
		t.Fatalf("signing keys %+v", signing)
	}
	pub, err := glossa.ParsePublicKey(signing.Keys[0].KeyID, signing.Keys[0].PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keys := []glossa.PublicKey{pub}

	// The edge serves the manifest per SPEC §2.
	manifestURL := edgeURL + "/v1/" + key.Key + "/production/manifest.json"
	resp, manifest := get(t, manifestURL)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "public, max-age=60, stale-while-revalidate=300, stale-if-error=86400" ||
		resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("ETag") == "" {
		t.Fatalf("manifest %d %v", resp.StatusCode, resp.Header)
	}
	releasetest.Manifest(t, manifest)
	if resp, _ := get(t, manifestURL, "If-None-Match", resp.Header.Get("ETag")); resp.StatusCode != http.StatusNotModified {
		t.Errorf("revalidation: %d", resp.StatusCode)
	}

	// The Go runtime loads and verifies it, and renders.
	prod := runtime(t, edgeURL, key.Key, "production", keys, "")
	if got := prod.For("de").T("checkout.pay", glossa.Args{"amount": 3}); got != "3 bezahlen" {
		t.Errorf("de checkout.pay = %q", got)
	}
	if got := prod.For("de").T("cart.items", glossa.Args{"count": 3}); got != "3 items" {
		t.Errorf("unreviewed de text shipped to production: %q", got)
	}
	if ex := prod.For("de-AT").Explain("checkout.pay"); ex.ResolvedFrom == nil || *ex.ResolvedFrom != "de" || ex.Release.ID != prod1.ID {
		t.Errorf("explain %+v", ex)
	}
	if got := prod.For("en").T("home.title", nil); got != "Welcome" {
		t.Errorf("namespaced message = %q", got)
	}

	// Preview ships the unreviewed text too.
	prev := publish("preview", "prev-1")
	if got := runtime(t, edgeURL, key.Key, "preview", keys, "").For("de").T("cart.items", glossa.Args{"count": 3}); got != "3 Artikel" {
		t.Errorf("preview de cart.items = %q", got)
	}
	write("POST", "/environments/production/promotions", map[string]string{"release_id": prev.ID}).
		want(t, http.StatusConflict, "release_ineligible")

	// Approve and publish again; the diff shows it; the runtime picks it up.
	approve("cart.items")
	prod2 := publish("production", "prod-2")
	if prod2.ParentID != prod1.ID || prod2.Counts.NewArtifacts != 0 {
		// de/default equals preview's artifact: already stored.
		t.Errorf("second release %+v", prod2)
	}
	var diff struct {
		BaseReleaseID string `json:"base_release_id"`
		Locales       []struct {
			Locale string   `json:"locale"`
			Added  []string `json:"added"`
		} `json:"locales"`
	}
	s.do(call{method: "GET", path: p + "/releases/" + prod2.ID + "/diff", cookie: ada.cookie}).decode(t, &diff)
	if diff.BaseReleaseID != prod1.ID || len(diff.Locales) != 2 || diff.Locales[1].Locale != "de" ||
		len(diff.Locales[1].Added) != 1 || diff.Locales[1].Added[0] != "cart.items" {
		t.Errorf("diff %+v", diff)
	}
	time.Sleep(150 * time.Millisecond) // the edge's manifest TTL
	if err := prod.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := prod.For("de").T("cart.items", glossa.Args{"count": 3}); got != "3 Artikel" {
		t.Errorf("after publish: %q", got)
	}

	// Roll production back: only the pointer moves.
	write("POST", "/environments/production/rollbacks", map[string]any{}).want(t, http.StatusOK, "")
	time.Sleep(150 * time.Millisecond)
	if err := prod.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := prod.For("de").T("cart.items", glossa.Args{"count": 3}); got != "3 items" {
		t.Errorf("after rollback: %q", got)
	}

	// A revoked key stops resolving once the edge's key TTL has passed.
	var other struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	write("POST", "/delivery-keys", map[string]string{"name": "old"}).decode(t, &other)
	if resp, _ := get(t, edgeURL+"/v1/"+other.Key+"/production/manifest.json"); resp.StatusCode != 200 {
		t.Errorf("new key: %d", resp.StatusCode)
	}
	write("DELETE", "/delivery-keys/"+other.ID, nil).want(t, http.StatusNoContent, "")
	time.Sleep(250 * time.Millisecond)
	if resp, _ := get(t, edgeURL+"/v1/"+other.Key+"/production/manifest.json"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("revoked key: %d", resp.StatusCode)
	}

	// `glossa pull --release`: the bundle from the API loads offline.
	bundle := t.TempDir()
	r = s.do(call{method: "GET", path: p + "/releases/" + prod2.ID + "/manifest?environment=production", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	writeFile(t, filepath.Join(bundle, "manifest.json"), r.body)
	var m struct {
		Artifacts map[string]map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	r.decode(t, &m)
	for _, namespaces := range m.Artifacts {
		for _, ref := range namespaces {
			a := s.do(call{method: "GET", path: p + "/releases/" + prod2.ID + "/artifacts/" + ref.SHA256, cookie: ada.cookie})
			a.want(t, http.StatusOK, "")
			releasetest.Artifact(t, a.body)
			writeFile(t, filepath.Join(bundle, "a", ref.SHA256+".json"), a.body)
		}
	}
	offline := runtime(t, "", "", "production", keys, bundle)
	if got := offline.For("de").T("cart.items", glossa.Args{"count": 3}); got != "3 Artikel" {
		t.Errorf("bundled release: %q", got)
	}

	// Control-plane outage: stop glossa-server and its database. The
	// edge keeps serving, a fresh edge (empty cache) serves from storage
	// alone, and a fresh runtime loads and verifies the release.
	s.stop()
	s.db.Close()
	if _, err := http.Get(s.base + "/readyz"); err == nil {
		t.Fatal("glossa-server still answers")
	}
	fresh := startEdge(t, minio)
	for _, base := range []string{edgeURL, fresh} {
		resp, body := get(t, base+"/v1/"+key.Key+"/production/manifest.json")
		// Production serves release 1 again: byte for byte the manifest it
		// served first (signing and canonical JSON are deterministic).
		if resp.StatusCode != 200 || !bytes.Equal(body, manifest) {
			t.Fatalf("manifest during the outage from %s: %d", base, resp.StatusCode)
		}
		c := runtime(t, base, key.Key, "production", keys, "")
		if got := c.For("de").T("checkout.pay", glossa.Args{"amount": 3}); got != "3 bezahlen" {
			t.Errorf("runtime during the outage via %s: %q", base, got)
		}
		if ex := c.For("de").Explain("checkout.pay"); ex.Source != glossa.SourceNetwork || ex.Release.ID != prod1.ID {
			t.Errorf("runtime during the outage: %+v", ex)
		}
	}
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestEnvironmentsOverHTTP covers the environment and key operations the
// delivery test doesn't, on the directory store.
func TestEnvironmentsOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	var project struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &project)
	p := "/v1/tenants/" + org.ID + "/projects/" + project.ID

	var list struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
		NextPageToken string `json:"next_page_token"`
	}
	r := s.do(call{method: "GET", path: p + "/environments?page_size=3", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &list)
	if len(list.Items) != 3 || list.Items[0].Name != "development" || list.NextPageToken == "" {
		t.Fatalf("environments %s", r.body)
	}
	r = s.do(call{method: "GET", path: p + "/environments/production", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	if r.header.Get("ETag") != `"1"` || !bytes.Contains(r.body, []byte(`"states":["approved"]`)) {
		t.Errorf("production %s %v", r.body, r.header)
	}
	policy := map[string]any{"policy": map[string]any{"states": []string{"needs_review", "approved"}, "include_outdated": false}}
	s.do(call{method: "PATCH", path: p + "/environments/production", cookie: ada.cookie, csrf: ada.csrf, body: policy}).
		want(t, http.StatusPreconditionRequired, "precondition_required")
	r = s.do(call{method: "PATCH", path: p + "/environments/production", cookie: ada.cookie, csrf: ada.csrf, body: policy,
		headers: map[string]string{"If-Match": `"1"`}})
	r.want(t, http.StatusOK, "")
	if r.header.Get("ETag") != `"2"` {
		t.Errorf("ETag after update %s", r.header.Get("ETag"))
	}
	s.do(call{method: "PATCH", path: p + "/environments/production", cookie: ada.cookie, csrf: ada.csrf, body: policy,
		headers: map[string]string{"If-Match": `"1"`}}).want(t, http.StatusPreconditionFailed, "precondition_failed")

	r = s.do(call{method: "POST", path: p + "/environments", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"name": "qa-7"}})
	r.want(t, http.StatusCreated, "")
	if r.header.Get("Location") != p+"/environments/qa-7" {
		t.Errorf("Location %s", r.header.Get("Location"))
	}
	s.do(call{method: "POST", path: p + "/environments", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"name": "qa-7"}}).
		want(t, http.StatusConflict, "environment_exists")
	s.do(call{method: "GET", path: p + "/environments/nope", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "POST", path: p + "/environments/production/rollbacks", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{}}).
		want(t, http.StatusConflict, "no_rollback_target")
	s.do(call{method: "POST", path: p + "/releases", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"environment": "nope"}}).
		want(t, http.StatusNotFound, "not_found")

	// A read token sees releases but can't publish or create keys.
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"read"}}}).decode(t, &tok)
	s.do(call{method: "GET", path: p + "/releases", bearer: tok.Secret}).want(t, http.StatusOK, "")
	s.do(call{method: "POST", path: p + "/releases", bearer: tok.Secret, body: map[string]string{"environment": "production"}}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "POST", path: p + "/delivery-keys", bearer: tok.Secret, body: map[string]string{"name": "x"}}).
		want(t, http.StatusForbidden, "forbidden")

	// A publish token publishes (to the directory store here).
	s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "deploy", "scopes": []string{"publish"}}}).decode(t, &tok)
	r = s.do(call{method: "POST", path: p + "/releases", bearer: tok.Secret, body: map[string]string{"environment": "production", "note": "from CI"}})
	r.want(t, http.StatusCreated, "")
	var rel releaseBody
	r.decode(t, &rel)
	s.do(call{method: "GET", path: p + "/releases/" + rel.ID, bearer: tok.Secret}).want(t, http.StatusOK, "")
	s.do(call{method: "GET", path: p + "/releases/" + rel.ID + "/artifacts/" + strings.Repeat("0", 64), bearer: tok.Secret}).
		want(t, http.StatusNotFound, "not_found")
	var deps struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: p + "/environments/production/deployments", bearer: tok.Secret}).decode(t, &deps)
	if len(deps.Items) != 1 || deps.Items[0].Action != "publish" {
		t.Errorf("deployments %+v", deps)
	}
}
