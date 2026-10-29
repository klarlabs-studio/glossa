package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	audit "go.klarlabs.de/glossa/platform/internal/audit/domain"
)

var auditTenant = uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")

// auditExport writes a signed export of n entries into w's directory
// under name, the way an export job will, and returns its key.
func auditExport(t *testing.T, w *workspace, name, keyID string, n int) audit.SigningKey {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	key, err := audit.ParseSigningKey(keyID, base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		t.Fatal(err)
	}
	var head audit.Head
	var entries []audit.Entry
	for i := range n {
		e, err := audit.Append(auditTenant, head, audit.Draft{
			EventID: uuid.New(), Source: audit.SourceOutbox, Action: "catalog.message.created",
			Actor: "person:" + uuid.NewString(), OccurredAt: time.Date(2026, 10, 1, 9, i, 0, 0, time.UTC),
			AggregateType: "message", AggregateID: uuid.NewString(),
			Summary: json.RawMessage(fmt.Sprintf(`{"key":"checkout.step_%d","source":"string(len=14)"}`, i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
		head = e.Head()
	}
	manifest, lines, err := audit.Export(auditTenant, audit.Head{}, entries, key, audit.ExportOptions{CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	w.write(filepath.Join(name, audit.ManifestFile), string(manifest))
	w.write(filepath.Join(name, audit.EntriesFile), string(lines))
	return key
}

func pair(k audit.SigningKey) string {
	p := k.Public()
	return p.ID + "=" + base64.StdEncoding.EncodeToString(p.Key)
}

// The round trip of §12.5 on files, offline: export a few entries,
// verify, alter one byte of one line, and verify fails — naming the line.
func TestAuditVerifyRoundTrip(t *testing.T) {
	w := newWorkspace(t)
	key := auditExport(t, w, "export", "audit-test", 4)

	r := w.run("audit", "verify", "export", "--public-key", pair(key))
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "audit export verified: 4 entries, sequences 1–4") {
		t.Errorf("human output:\n%s", r.stdout)
	}

	var doc auditVerifyJSON
	w.json(&doc, "audit", "verify", filepath.Join("export", "manifest.json"), "--public-key", pair(key)).want(t, ExitOK)
	if doc.Schema != "glossa.cli.audit.verify/v1" || !doc.OK || doc.Verified != 4 || doc.EntryCount != 4 ||
		doc.TenantID != auditTenant.String() || doc.KeyID != "audit-test" || doc.Failure != nil {
		t.Errorf("json = %+v", doc)
	}

	// One byte of the third line: a digit becomes another digit, so the
	// line still parses and only the chain can tell.
	lines := strings.SplitAfter(w.read("export/entries.jsonl"), "\n")
	third := []byte(lines[2])
	i := strings.Index(lines[2], `"occurred_at":"2026-10-01T09:02`) + len(`"occurred_at":"2026-10-01T09:0`)
	third[i] = '3'
	lines[2] = string(third)
	w.write("export/entries.jsonl", strings.Join(lines, ""))

	r = w.run("audit", "verify", "export", "--public-key", pair(key))
	r.want(t, ExitCheckFailed)
	if !strings.Contains(r.stdout, "does not verify: line 3 (sequence 3)") || !strings.Contains(r.stdout, "[hash_mismatch]") ||
		!strings.Contains(r.stdout, "2 entries verified before it") {
		t.Errorf("human output:\n%s", r.stdout)
	}
	doc = auditVerifyJSON{}
	w.json(&doc, "audit", "verify", "export", "--public-key", pair(key)).want(t, ExitCheckFailed)
	if doc.OK || doc.Failure == nil || doc.Failure.Check != "hash_mismatch" || doc.Failure.Line != 3 || doc.Failure.Sequence != 3 {
		t.Errorf("json = %+v, failure %+v", doc, doc.Failure)
	}
}

func TestAuditVerifyKeys(t *testing.T) {
	w := newWorkspace(t)
	key := auditExport(t, w, "export", "audit-test", 2)

	// A glossa.audit.keys/1 document, as glossa-server will serve it.
	other := auditExport(t, w, "other", "audit-other", 1)
	doc, err := audit.KeyDocument([]audit.PublicKey{other.Public(), {ID: "audit-old", Key: key.Public().Key}})
	if err != nil {
		t.Fatal(err)
	}
	w.write("keys.json", string(doc))
	// key's id is audit-test, the document names its key audit-old: an
	// unknown key, whatever the material.
	var out auditVerifyJSON
	w.json(&out, "audit", "verify", "export", "--public-key", "keys.json").want(t, ExitCheckFailed)
	if out.Failure == nil || out.Failure.Check != "unknown_key" {
		t.Errorf("failure = %+v", out.Failure)
	}
	// The right id with another key's material: the signature fails.
	w.json(&out, "audit", "verify", "export", "--public-key", "audit-test="+base64.StdEncoding.EncodeToString(other.Public().Key)).want(t, ExitCheckFailed)
	if out.Failure == nil || out.Failure.Check != "signature_invalid" {
		t.Errorf("failure = %+v", out.Failure)
	}
	// Several keys, one of them right.
	w.run("audit", "verify", "export", "--public-key", "keys.json", "--public-key", pair(key)).want(t, ExitOK)

	var e errorDoc
	w.json(&e, "audit", "verify", "export").want(t, ExitUsage)
	if e.Error.Code != "public_key_required" {
		t.Errorf("no key: %+v", e.Error)
	}
	w.json(&e, "audit", "verify", "export", "--public-key", "nonsense").want(t, ExitUsage)
	if e.Error.Code != "invalid_public_key" {
		t.Errorf("a bad key: %+v", e.Error)
	}
	w.write("bad.json", `{"format":"something"}`)
	w.json(&e, "audit", "verify", "export", "--public-key", "bad.json").want(t, ExitUsage)
	if e.Error.Code != "invalid_public_key" {
		t.Errorf("a bad key document: %+v", e.Error)
	}
}

func TestAuditVerifyUsage(t *testing.T) {
	w := newWorkspace(t)
	key := auditExport(t, w, "export", "audit-test", 1)
	k := pair(key)

	var e errorDoc
	w.json(&e, "audit", "verify", "missing", "--public-key", k).want(t, ExitUsage)
	if e.Error.Code != "export_unreadable" {
		t.Errorf("a missing export: %+v", e.Error)
	}
	w.json(&e, "audit").want(t, ExitUsage)
	w.json(&e, "audit", "verify", "--public-key", k).want(t, ExitUsage)
	w.json(&e, "audit", "frobnicate").want(t, ExitUsage)

	// An empty directory, or one missing a file, is an export that does
	// not verify (exit 1), not a mistake on the command line.
	if err := os.Mkdir(filepath.Join(w.dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.run("audit", "verify", "empty", "--public-key", k).want(t, ExitCheckFailed)
	if err := os.Remove(filepath.Join(w.dir, "export", audit.EntriesFile)); err != nil {
		t.Fatal(err)
	}
	var out auditVerifyJSON
	w.json(&out, "audit", "verify", "export", "--public-key", k).want(t, ExitCheckFailed)
	if out.Failure == nil || out.Failure.Check != "entries_unreadable" {
		t.Errorf("no entries file: %+v", out.Failure)
	}
}
