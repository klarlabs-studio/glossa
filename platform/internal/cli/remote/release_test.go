package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/release"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

var scope = release.Scope{Tenant: "t1", Project: "p1"}

const releaseJSON = `{"id":"r2","version":2,"environment":"production","parent_id":"r1","note":"hi","author":"token:1",
"created_at":"2026-09-19T10:00:00Z","source_locale":"en","locales":[{"code":"en","direction":"ltr"},{"code":"de","direction":"ltr"}],
"manifest_digest":"abc","policy":{"states":["approved"],"include_outdated":false},
"counts":{"messages":3,"artifacts":2,"new_artifacts":1,"bytes":200,"locales":{"de":{"messages":2,"outdated":1},"en":{"messages":3,"outdated":0}}}}`

func TestPublishSendsTheIdempotencyKeyRetriesAndReportsReplays(t *testing.T) {
	var attempts atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tenants/t1/projects/p1/releases" || r.Header.Get("Idempotency-Key") != "k-1" {
			t.Errorf("request = %s %s %v", r.Method, r.URL, r.Header)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["environment"] != "production" || body["note"] != "hi" {
			t.Errorf("body = %v", body)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, releaseJSON)
	}))
	svc := remote.NewReleaseService(c)
	p, err := svc.Publish(context.Background(), scope, release.PublishRequest{Environment: "production", Note: "hi", IdempotencyKey: "k-1"})
	if err != nil {
		t.Fatal(err)
	}
	r := p.Release
	if !p.Replayed || attempts.Load() != 2 || r.ID != "r2" || r.Version != 2 || r.ParentID != "r1" || r.Note != "hi" ||
		len(r.Locales) != 2 || r.Locales[1] != "de" || r.Counts.Locales["de"].Outdated != 1 || r.Policy.States[0] != "approved" {
		t.Errorf("published = %+v after %d attempts", p, attempts.Load())
	}
}

func TestManifestAndArtifactsAreExactBytes(t *testing.T) {
	const manifest = `{"schema":"glossa.manifest/v1",  "environment":"staging"}`
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/tenants/t1/projects/p1/releases/r2/manifest":
			if r.URL.Query().Get("environment") != "staging" {
				t.Errorf("manifest query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, manifest)
		case "/v1/tenants/t1/projects/p1/releases/r2/artifacts/" + sha64:
			_, _ = io.WriteString(w, `{ "schema" : "glossa.artifact/v1" }`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	src := remote.NewReleaseService(c).BundleSource(scope, "r2", "staging")
	m, err := src.Manifest(context.Background())
	if err != nil || string(m) != manifest {
		t.Errorf("manifest = %s, %v", m, err)
	}
	a, err := src.Artifact(context.Background(), sha64)
	if err != nil || string(a) != `{ "schema" : "glossa.artifact/v1" }` {
		t.Errorf("artifact = %s, %v", a, err)
	}
}

const sha64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestReleasesStopsAtTheLimit(t *testing.T) {
	var pages atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[`+releaseJSON+`,`+releaseJSON+`],"next_page_token":"more"}`)
	}))
	rels, err := remote.NewReleaseService(c).Releases(context.Background(), scope, 3)
	if err != nil || len(rels) != 3 || pages.Load() != 2 {
		t.Errorf("releases = %d, %v after %d pages", len(rels), err, pages.Load())
	}
}

func TestRollbackAndKeysSpeakTheContract(t *testing.T) {
	var seen []string
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(body)+" "+r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/tenants/t1/projects/p1/environments/production/rollbacks":
			_, _ = io.WriteString(w, `{"name":"production","current_release_id":"r1","policy":{"states":["approved"],"include_outdated":false},"created_at":"2026-09-19T10:00:00Z","updated_at":"2026-09-19T10:00:00Z"}`)
		case "POST /v1/tenants/t1/projects/p1/delivery-keys":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"k1","name":"web","key":"glossa_pk_x","scope":{"environments":["preview"],"branches":true},"created_by":"token:1","created_at":"2026-09-19T10:00:00Z"}`)
		case "DELETE /v1/tenants/t1/projects/p1/delivery-keys/k1":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"type":"urn:glossa:problem:key_revoked","code":"key_revoked","title":"Conflict","status":409,"detail":"already revoked"}`)
		}
	}))
	svc := remote.NewReleaseService(c)
	env, err := svc.Rollback(context.Background(), scope, "production", "")
	if err != nil || env.CurrentReleaseID != "r1" {
		t.Errorf("rollback = %+v, %v", env, err)
	}
	k, err := svc.CreateDeliveryKey(context.Background(), scope, "web", &release.KeyScope{Environments: []string{"preview"}, Branches: true}, "idem")
	if err != nil || k.Key != "glossa_pk_x" || k.Name != "web" || k.RevokedAt != nil ||
		!k.Scope.Branches || len(k.Scope.Environments) != 1 {
		t.Errorf("key = %+v, %v", k, err)
	}
	var ae *remote.APIError
	if err := svc.RevokeDeliveryKey(context.Background(), scope, "k1"); !errors.As(err, &ae) || ae.Code != "key_revoked" {
		t.Errorf("revoke = %v", err)
	}
	want := []string{
		"POST /v1/tenants/t1/projects/p1/environments/production/rollbacks {} ",
		`POST /v1/tenants/t1/projects/p1/delivery-keys {"name":"web","scope":{"branches":true,"environments":["preview"]}} idem`,
		"DELETE /v1/tenants/t1/projects/p1/delivery-keys/k1  ",
	}
	for i, w := range want {
		if i >= len(seen) || seen[i] != w {
			t.Errorf("request %d = %q, want %q", i, seen, w)
			break
		}
	}
}
