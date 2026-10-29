//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	auditpg "go.klarlabs.de/glossa/platform/internal/audit/adapters/postgres"
	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	auditdomain "go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The composition root wires the Audit context end to end: a sign-in
// over HTTP is recorded directly, the events it causes (the individual
// tenant, its owner) reach the trail through the outbox, and the chain
// the server wrote verifies.
func TestTheServerKeepsAnAuditTrail(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	r := s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	var me struct {
		Person struct {
			ID                 string `json:"id"`
			IndividualTenantID string `json:"individual_tenant_id"`
		} `json:"person"`
	}
	r.decode(t, &me)
	tenant, err := tenancy.ParseID(me.Person.IndividualTenantID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.ContextWithTenant(context.Background(), tenant)
	store := auditpg.NewStore(db.NewUnitOfWork(s.db.App))

	want := []string{"identity.person.signed_in", "identity.tenant.created", "identity.member.added"}
	deadline := time.Now().Add(10 * time.Second)
	var actions []string
	for time.Now().Before(deadline) {
		es, err := store.Entries(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		actions = actions[:0]
		for _, e := range es {
			actions = append(actions, e.Action)
			if e.Actor != "person:"+me.Person.ID {
				t.Errorf("%s by %s, want ada", e.Action, e.Actor)
			}
			if e.Action == "identity.person.signed_in" {
				if _, err := uuid.Parse(e.RequestID); err != nil {
					t.Errorf("the sign-in entry has no request id: %q", e.RequestID)
				}
			}
		}
		if len(actions) >= len(want) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, w := range want {
		if !slices.Contains(actions, w) {
			t.Errorf("the trail lacks %s: %v", w, actions)
		}
	}
	report, err := auditapp.New(store).Verify(ctx)
	if err != nil || report.Entries != int64(len(actions)) {
		t.Errorf("verify: %v (%d entries)", err, report.Entries)
	}
	if strings.Contains(s.logs.String(), "was not audited") {
		t.Errorf("the server logged an audit failure:\n%s", s.logs)
	}
}

// auditKeyEnv generates an active audit key and a retired one for a
// test server; nothing is committed.
func auditKeyEnv(t *testing.T) (env map[string]string, active, retired ed25519.PublicKey) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	old, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"GLOSSA_AUDIT_EXPORTS_ENABLED": "true",
		"GLOSSA_AUDIT_SIGNING_KEY":     "audit-2026b=" + base64.StdEncoding.EncodeToString(seed),
		"GLOSSA_AUDIT_RETIRED_KEYS":    "audit-2026a=" + base64.StdEncoding.EncodeToString(old),
	}, ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey), old
}

type auditEntryJSON struct {
	Sequence  int64  `json:"sequence"`
	Action    string `json:"action"`
	ProjectID string `json:"project_id"`
}

// The read and export API end to end (RFC 0006 §6.2, §12.5): the
// owner reads the whole trail and a member scoped to one project only
// that project's entries; an export job writes a glossa.audit/v1 export
// that verifies with the published key and fails with one flipped byte;
// the key document publishes the active key and a retired one.
func TestTheAuditTrailIsReadAndExportedOverHTTP(t *testing.T) {
	env, activeKey, retiredKey := auditKeyEnv(t)
	s := startServerWith(t, env)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	project := func(slug string) string {
		var p struct{ ID string }
		r := s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"slug": slug, "name": slug, "source_locale": "en",
				"settings": map[string]any{"default_syntax": "mf1", "review_required": false}}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &p)
		s.do(call{method: "POST", path: base + "/projects/" + p.ID + "/message-upserts", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"items": []map[string]any{{"key": "pay", "text": "Pay"}}}}).want(t, http.StatusOK, "")
		return p.ID
	}
	a, b := project("shop"), project("other")
	s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "sam@acme.example", "roles": []string{"admin"}, "projects": []string{a}}}).
		want(t, http.StatusCreated, "")
	sam := s.signIn("sam@acme.example")

	list := func(who session, query string) []auditEntryJSON {
		t.Helper()
		var page struct{ Items []auditEntryJSON }
		r := s.do(call{method: "GET", path: base + "/audit-entries?page_size=100" + query, cookie: who.cookie})
		r.want(t, http.StatusOK, "")
		r.decode(t, &page)
		return page.Items
	}
	// The outbox delivers the projects' events to the trail.
	var all []auditEntryJSON
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		all = list(ada, "")
		inA, inB := 0, 0
		for _, e := range all {
			inA += map[bool]int{true: 1}[e.ProjectID == a]
			inB += map[bool]int{true: 1}[e.ProjectID == b]
		}
		if inA > 0 && inB > 0 {
			break
		}
	}
	var tenantLevel, ofB int64
	for i, e := range all {
		if i > 0 && e.Sequence >= all[i-1].Sequence {
			t.Errorf("the list is not newest first: %d after %d", e.Sequence, all[i-1].Sequence)
		}
		switch e.ProjectID {
		case "":
			tenantLevel = e.Sequence
		case b:
			ofB = e.Sequence
		}
	}
	if tenantLevel == 0 || ofB == 0 {
		t.Fatalf("the owner's trail lacks tenant-level or project B entries: %+v", all)
	}
	if asc := list(ada, "&order=asc&first_sequence=2&last_sequence=3"); len(asc) != 2 || asc[0].Sequence != 2 {
		t.Errorf("a sequence range in chain order: %+v", asc)
	}
	scoped := list(sam, "")
	if len(scoped) == 0 {
		t.Fatal("a member scoped to project A sees no entries")
	}
	for _, e := range scoped {
		if e.ProjectID != a {
			t.Errorf("a member scoped to A sees %s (project %q)", e.Action, e.ProjectID)
		}
	}
	for _, seq := range []int64{tenantLevel, ofB} {
		s.do(call{method: "GET", path: base + "/audit-entries/" + seqPath(seq), cookie: sam.cookie}).
			want(t, http.StatusNotFound, "not_found")
	}
	s.do(call{method: "GET", path: base + "/audit-entries/" + seqPath(ofB), cookie: ada.cookie}).want(t, http.StatusOK, "")

	// Only an owner limited to no project exports.
	s.do(call{method: "POST", path: base + "/audit-export-jobs", cookie: sam.cookie, csrf: sam.csrf,
		body: map[string]any{"first_sequence": 1}}).want(t, http.StatusForbidden, "forbidden")
	create := call{method: "POST", path: base + "/audit-export-jobs", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"first_sequence": 1, "last_sequence": all[0].Sequence}, headers: map[string]string{"Idempotency-Key": "q4-audit"}}
	r := s.do(create)
	r.want(t, http.StatusCreated, "")
	var job struct {
		ID         string
		State      string
		EntryCount int64 `json:"entry_count"`
		Entries    struct {
			DownloadURL string `json:"download_url"`
		}
		Manifest struct {
			DownloadURL string `json:"download_url"`
		}
		FailureMessage string `json:"failure_message"`
	}
	r.decode(t, &job)
	if replay := s.do(create); replay.status != http.StatusCreated || replay.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("the retry: %d %s", replay.status, replay.body)
	}
	jobPath := base + "/audit-export-jobs/" + job.ID
	s.do(call{method: "GET", path: jobPath + "/file", cookie: sam.cookie}).want(t, http.StatusForbidden, "forbidden")
	for deadline := time.Now().Add(20 * time.Second); job.State != "succeeded" && job.State != "failed"; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the export job is still %s", job.State)
		}
		s.do(call{method: "GET", path: jobPath, cookie: ada.cookie}).decode(t, &job)
	}
	if job.State != "succeeded" || job.EntryCount != all[0].Sequence {
		t.Fatalf("the export job ended %s with %d entries (%s)", job.State, job.EntryCount, job.FailureMessage)
	}
	download := func(path string) []byte {
		r := s.do(call{method: "GET", path: path, cookie: ada.cookie})
		r.want(t, http.StatusOK, "")
		if !strings.HasPrefix(r.header.Get("Content-Disposition"), "attachment") || r.header.Get("ETag") == "" {
			t.Errorf("%s: headers %v", path, r.header)
		}
		return r.body
	}
	lines, manifest := download(job.Entries.DownloadURL), download(job.Manifest.DownloadURL)

	// The key document: unauthenticated, active key first, the retired
	// one kept.
	kr := s.do(call{method: "GET", path: "/.well-known/glossa-audit-keys.json"})
	kr.want(t, http.StatusOK, "")
	keys, err := auditdomain.ParseKeyDocument(kr.body)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ID != "audit-2026b" || !keys[0].Active || !keys[0].Key.Equal(activeKey) ||
		keys[1].ID != "audit-2026a" || keys[1].Active || !keys[1].Key.Equal(retiredKey) {
		t.Fatalf("the key document: %s", kr.body)
	}
	if rep := auditdomain.VerifyExport(manifest, bytes.NewReader(lines), keys); !rep.OK || rep.Verified != job.EntryCount {
		t.Fatalf("the export does not verify: %+v", rep.Failure)
	}
	flipped := bytes.Clone(lines)
	flipped[len(flipped)/3] ^= 0x20
	if rep := auditdomain.VerifyExport(manifest, bytes.NewReader(flipped), keys); rep.OK {
		t.Error("an export with a flipped byte verified")
	}
	if rep := auditdomain.VerifyExport(manifest, bytes.NewReader(lines), keys[1:]); rep.OK {
		t.Error("the export verified with the retired key alone")
	}
	// The export is itself in the trail.
	var actions []string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		actions = actions[:0]
		for _, e := range list(ada, "&action=audit.export.completed") {
			actions = append(actions, e.Action)
		}
		if len(actions) > 0 {
			break
		}
	}
	if len(actions) == 0 {
		t.Error("the export's completion is not in the trail")
	}
}

// Without an audit key a deployment exports nothing and publishes no
// key document; the entries are still readable.
func TestWithoutAnAuditKeyExportsAreUnavailable(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var me struct {
		Person struct {
			IndividualTenantID string `json:"individual_tenant_id"`
		} `json:"person"`
	}
	s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie}).decode(t, &me)
	base := "/v1/tenants/" + me.Person.IndividualTenantID
	s.do(call{method: "POST", path: base + "/audit-export-jobs", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"first_sequence": 1}}).want(t, http.StatusServiceUnavailable, "audit_export_unavailable")
	s.do(call{method: "GET", path: base + "/audit-entries", cookie: ada.cookie}).want(t, http.StatusOK, "")
	if r := s.do(call{method: "GET", path: "/.well-known/glossa-audit-keys.json"}); r.status != http.StatusNotFound {
		t.Errorf("the key document without a key: %d %s", r.status, r.body)
	}
}

func seqPath(n int64) string { return fmt.Sprint(n) }
