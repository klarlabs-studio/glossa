package domain_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
)

// The golden first line of an export: goldenCanonical1 with prev_hash
// and hash in their RFC 8785 places (after "format", after "occurred_at").
// Pinned like the canonical form: changing it is a new export format.
const goldenLine1 = `{"action":"localization.translation.revised","actor":"person:0190a1b2-0000-7000-8000-0000000000aa",` +
	`"aggregate_id":"0190a1b2-0000-7000-8000-0000000000bb","aggregate_type":"translation",` +
	`"event_id":"0190a1b2-0000-7000-8000-00000000000a","format":"glossa.audit.entry/1",` +
	`"hash":"` + goldenHash1 + `","locale":"de-CH",` +
	`"occurred_at":"2026-10-01T09:30:00.123456Z",` +
	`"prev_hash":"0000000000000000000000000000000000000000000000000000000000000000",` +
	`"project_id":"0190a1b2-0000-7000-8000-000000000001",` +
	`"request_id":null,"sequence":1,"source":"outbox",` +
	`"summary":{"revision":3,"state":"needs_review","text":"string(len=12)"},` +
	`"tenant_id":"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`

var exportCreated = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func testKey(t *testing.T, id string) domain.SigningKey {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	k, err := domain.ParseSigningKey(id, base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// longChain is n entries of goldenTenant's chain.
func longChain(t *testing.T, n int) []domain.Entry {
	t.Helper()
	var head domain.Head
	out := make([]domain.Entry, 0, n)
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := range n {
		d := domain.Draft{
			EventID:       uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprint(i))),
			Source:        domain.SourceOutbox,
			Action:        "localization.translation.revised",
			Actor:         "person:0190a1b2-0000-7000-8000-0000000000aa",
			OccurredAt:    base.Add(time.Duration(i) * time.Minute),
			AggregateType: "translation",
			AggregateID:   uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprint(i))).String(),
			Project:       uuid.NullUUID{UUID: goldenProject, Valid: i%2 == 0},
			Locale:        "de",
			Summary:       json.RawMessage(fmt.Sprintf(`{"revision":%d,"text":"string(len=9)"}`, i+1)),
		}
		e, err := domain.Append(goldenTenant, head, d)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
		head = e.Head()
	}
	return out
}

func export(t *testing.T, from domain.Head, entries []domain.Entry, key domain.SigningKey) (manifest, lines []byte) {
	t.Helper()
	m, l, err := domain.Export(goldenTenant, from, entries, key, domain.ExportOptions{CreatedAt: exportCreated})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	return m, l
}

func verify(manifest, lines []byte, keys ...domain.PublicKey) domain.ExportReport {
	return domain.VerifyExport(manifest, bytes.NewReader(lines), keys)
}

func splitLines(b []byte) [][]byte {
	parts := bytes.SplitAfter(b, []byte("\n"))
	if len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func expectFailure(t *testing.T, r domain.ExportReport, check domain.Check, line int64) {
	t.Helper()
	if r.OK || r.Failure == nil {
		t.Fatalf("verified; want %s", check)
	}
	if r.Failure.Check != check || r.Failure.Line != line {
		t.Fatalf("failure = %v (check %s, line %d); want %s at line %d", r.Failure, r.Failure.Check, r.Failure.Line, check, line)
	}
}

func TestExportLineIsPinned(t *testing.T) {
	entries := chain(t)
	got, err := entries[0].Line()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != goldenLine1 {
		t.Fatalf("export line\n got: %s\nwant: %s", got, goldenLine1)
	}
	// A reader without Glossa's code: drop prev_hash and hash, and what
	// is left is the canonical form the hash covers.
	rest, err := jcs.Without(got, "hash")
	if err != nil {
		t.Fatal(err)
	}
	rest, err = jcs.Without(rest, "prev_hash")
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != goldenCanonical1 {
		t.Fatalf("the line without its hashes is not the canonical form:\n%s", rest)
	}
	sum := sha256.Sum256(append(make([]byte, 32), rest...))
	if fmt.Sprintf("%x", sum) != goldenHash1 {
		t.Fatal("sha256(prev_hash ‖ line without hashes) is not the line's hash")
	}
	back, err := domain.ParseLine(got)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := back.Line(); !bytes.Equal(again, got) {
		t.Fatal("a parsed line does not render back to itself")
	}
}

func TestExportVerifies(t *testing.T) {
	key := testKey(t, "audit-2026")
	entries := longChain(t, 12)
	for _, tc := range []struct {
		name  string
		from  domain.Head
		rng   []domain.Entry
		first int64
	}{
		{"from the first entry", domain.Head{}, entries, 1},
		{"a range in the middle", entries[3].Head(), entries[4:9], 5},
		{"an empty range", entries[5].Head(), nil, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, l := export(t, tc.from, tc.rng, key)
			r := verify(m, l, key.Public())
			if !r.OK {
				t.Fatalf("does not verify: %v", r.Failure)
			}
			if r.Verified != int64(len(tc.rng)) || r.Manifest.Range.FirstSequence != tc.first ||
				r.Manifest.EntryCount != int64(len(tc.rng)) || r.Manifest.KeyID != "audit-2026" {
				t.Fatalf("report %+v, manifest %+v", r, r.Manifest)
			}
			if tc.rng != nil && r.Manifest.Range.LastHash != domain.HexHash(tc.rng[len(tc.rng)-1].Hash) {
				t.Fatal("last_hash is not the last entry's hash")
			}
		})
	}
}

// The round trip §12.5 runs against a real export: export, verify,
// alter one byte, and verify fails. Here every byte of a line is
// altered in turn, and every alteration fails.
func TestEveryAlteredByteFails(t *testing.T) {
	key := testKey(t, "audit-2026")
	m, l := export(t, domain.Head{}, longChain(t, 3), key)
	if r := verify(m, l, key.Public()); !r.OK {
		t.Fatalf("the untouched export does not verify: %v", r.Failure)
	}
	for i := range l {
		tampered := bytes.Clone(l)
		tampered[i] ^= 0x01
		if r := verify(m, tampered, key.Public()); r.OK {
			t.Fatalf("flipping byte %d (%q → %q) verifies", i, l[i], tampered[i])
		}
	}
	for i := range m {
		tampered := bytes.Clone(m)
		tampered[i] ^= 0x01
		if r := verify(tampered, l, key.Public()); r.OK {
			t.Fatalf("flipping manifest byte %d (%q → %q) verifies", i, m[i], tampered[i])
		}
	}
}

func TestExportFailures(t *testing.T) {
	key := testKey(t, "audit-2026")
	entries := longChain(t, 5)
	m, l := export(t, domain.Head{}, entries, key)
	lines := splitLines(l)
	join := func(ls ...[]byte) []byte { return bytes.Join(ls, nil) }
	edit := func(line []byte, from, to string) []byte {
		t.Helper()
		if !bytes.Contains(line, []byte(from)) {
			t.Fatalf("%q not in line", from)
		}
		return bytes.Replace(line, []byte(from), []byte(to), 1)
	}
	// resign signs an edited manifest with the right key: what an
	// attacker with the key could do, and the only way to reach the
	// checks behind the signature.
	resign := func(change func(*domain.Manifest)) []byte {
		t.Helper()
		var mm domain.Manifest
		if err := json.Unmarshal(m, &mm); err != nil {
			t.Fatal(err)
		}
		change(&mm)
		mm.Signature = nil
		unsigned, err := jcs.Marshal(mm)
		if err != nil {
			t.Fatal(err)
		}
		mm.Signature = &domain.ManifestSignature{Algorithm: "Ed25519",
			Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key.Key, unsigned))}
		out, err := jcs.Marshal(mm)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	e1, _ := domain.ParseLine(bytes.TrimSuffix(lines[1], []byte("\n")))
	next, err := domain.Append(goldenTenant, entries[4].Head(), domain.Draft{
		EventID: uuid.New(), Source: domain.SourceDirect, Action: "identity.person.signed_in",
		Actor: "unknown", OccurredAt: time.Now(), AggregateType: "person", AggregateID: "p", Summary: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	extra, _ := next.Line()

	for _, tc := range []struct {
		name     string
		manifest []byte
		lines    []byte
		keys     []domain.PublicKey
		check    domain.Check
		line     int64
	}{
		{"a line removed from the middle", m, join(lines[0], lines[1], lines[3], lines[4]), nil, domain.CheckSequence, 3},
		{"the first line removed", m, join(lines[1:]...), nil, domain.CheckSequence, 1},
		{"the last line removed", m, join(lines[:4]...), nil, domain.CheckRange, 0},
		{"two lines swapped", m, join(lines[0], lines[2], lines[1], lines[3], lines[4]), nil, domain.CheckSequence, 2},
		{"truncated inside a line", m, l[:len(l)-40], nil, domain.CheckLine, 5},
		{"truncated before the last newline", m, l[:len(l)-1], nil, domain.CheckLine, 5},
		{"a line appended", m, join(append(lines, append(extra, '\n'))...), nil, domain.CheckRange, 6},
		{"an empty file", m, nil, nil, domain.CheckRange, 0},
		{"an empty line", m, join(lines[0], []byte("\n"), lines[1]), nil, domain.CheckLine, 2},
		{"not JSON", m, join(lines[0], []byte("not json\n")), nil, domain.CheckLine, 2},
		{"an unknown member", m, join(lines[0], edit(lines[1], `{"action"`, `{"zzz":1,"action"`)), nil, domain.CheckLine, 2},
		{"whitespace in a line", m, join(lines[0], edit(lines[1], `,"actor"`, `, "actor"`)), nil, domain.CheckNotCanonical, 2},
		{"an edited actor", m, join(lines[0], edit(lines[1], `"actor":"person:`, `"actor":"token:`)), nil, domain.CheckHash, 2},
		{"an edited hash", m, join(lines[0], edit(lines[1], domain.HexHash(e1.Hash), strings.Repeat("ab", 32))), nil, domain.CheckHash, 2},
		{"a broken link", m, join(lines[0], edit(lines[1], domain.HexHash(e1.PrevHash), strings.Repeat("ab", 32))), nil, domain.CheckLink, 2},
		{"another tenant's line", m, join(lines[0], edit(lines[1], goldenTenant.String(), uuid.Nil.String())), nil, domain.CheckTenant, 2},
		{"an unknown key", m, l, []domain.PublicKey{testKey(t, "other").Public()}, domain.CheckUnknownKey, 0},
		{"no key at all", m, l, []domain.PublicKey{}, domain.CheckUnknownKey, 0},
		{"the right id, the wrong key", m, l, []domain.PublicKey{testKey(t, "audit-2026").Public()}, domain.CheckSignature, 0},
		{"the manifest's last hash edited after signing", edit(m, `"last_hash":"`+domain.HexHash(entries[4].Hash), `"last_hash":"`+domain.HexHash(entries[3].Hash)), l, nil, domain.CheckSignature, 0},
		{"the manifest's created_at edited after signing", edit(m, "2026-10-02T12", "2026-10-03T12"), l, nil, domain.CheckSignature, 0},
		{"the manifest's count edited", edit(m, `"entry_count":5`, `"entry_count":4`), l, nil, domain.CheckManifest, 0},
		{"the manifest not JSON", []byte("{"), l, nil, domain.CheckManifest, 0},
		{"the signature removed", resignNone(t, m), l, nil, domain.CheckManifest, 0},
		{"a re-signed manifest naming another digest", resign(func(mm *domain.Manifest) { mm.Entries.SHA256 = strings.Repeat("0", 64) }), l, nil, domain.CheckDigest, 0},
		{"a re-signed manifest naming a shorter range", resign(func(mm *domain.Manifest) {
			mm.Range.LastSequence, mm.EntryCount, mm.Range.LastHash = 4, 4, domain.HexHash(entries[3].Hash)
		}), l, nil, domain.CheckRange, 5},
		{"a re-signed manifest naming another last hash", resign(func(mm *domain.Manifest) { mm.Range.LastHash = domain.HexHash(entries[3].Hash) }), l, nil, domain.CheckRange, 0},
		{"a re-signed manifest naming a range from 1 with another first prev_hash", resign(func(mm *domain.Manifest) { mm.Range.FirstPrevHash = strings.Repeat("ab", 32) }), l, nil, domain.CheckManifest, 0},
		{"a re-signed manifest with a time range the entries are outside", resign(func(mm *domain.Manifest) {
			mm.Occurred = &domain.TimeRange{From: "2026-10-01T09:02:00.000000Z", To: "2026-10-01T10:00:00.000000Z"}
		}), l, nil, domain.CheckRange, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := tc.keys
			if keys == nil {
				keys = []domain.PublicKey{key.Public()}
			}
			expectFailure(t, domain.VerifyExport(tc.manifest, bytes.NewReader(tc.lines), keys), tc.check, tc.line)
		})
	}
}

func resignNone(t *testing.T, m []byte) []byte {
	t.Helper()
	out, err := jcs.Without(m, "signature")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRetiredKeysStillVerify(t *testing.T) {
	old, current := testKey(t, "audit-2025"), testKey(t, "audit-2026")
	m, l := export(t, domain.Head{}, longChain(t, 2), old)
	set, err := domain.NewKeySet(current, []domain.PublicKey{old.Public()})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := domain.KeyDocument(set.PublicKeys())
	if err != nil {
		t.Fatal(err)
	}
	keys, err := domain.ParseKeyDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || !keys[0].Active || keys[1].Active || keys[0].ID != "audit-2026" {
		t.Fatalf("key document %s", doc)
	}
	if r := verify(m, l, keys...); !r.OK {
		t.Fatalf("an export signed by a retired key does not verify: %v", r.Failure)
	}
}

func TestKeySetRefusals(t *testing.T) {
	k := testKey(t, "a")
	if _, err := domain.NewKeySet(domain.SigningKey{}, nil); !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("no active key: %v", err)
	}
	if _, err := domain.NewKeySet(k, []domain.PublicKey{testKey(t, "a").Public()}); !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("a duplicate id: %v", err)
	}
	same := k.Public()
	same.ID = "b"
	if _, err := domain.NewKeySet(k, []domain.PublicKey{same}); !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("the active key as a retired one: %v", err)
	}
	for _, bad := range []string{"", "a b", strings.Repeat("x", 65)} {
		if _, err := domain.ParseSigningKey(bad, base64.StdEncoding.EncodeToString(make([]byte, 32))); err == nil {
			t.Errorf("key id %q accepted", bad)
		}
	}
	if _, err := domain.ParseSigningKey("a", base64.StdEncoding.EncodeToString(make([]byte, 31))); err == nil {
		t.Error("a 31-byte seed accepted")
	}
	if _, err := domain.ParsePublicKeyPair("no-equals"); err == nil {
		t.Error("a pair without = accepted")
	}
}

func TestExportRefusesWhatWouldNotVerify(t *testing.T) {
	key := testKey(t, "audit-2026")
	entries := longChain(t, 4)
	opts := domain.ExportOptions{CreatedAt: exportCreated}
	if _, _, err := domain.Export(goldenTenant, domain.Head{}, []domain.Entry{entries[0], entries[2]}, key, opts); !errors.Is(err, domain.ErrInvalidExport) {
		t.Errorf("a gap: %v", err)
	}
	if _, _, err := domain.Export(goldenTenant, domain.Head{}, entries[1:], key, opts); !errors.Is(err, domain.ErrInvalidExport) {
		t.Errorf("a range that does not follow its head: %v", err)
	}
	if _, _, err := domain.Export(uuid.New(), domain.Head{}, entries, key, opts); !errors.Is(err, domain.ErrInvalidExport) {
		t.Errorf("another tenant's entries: %v", err)
	}
	ranged := domain.ExportOptions{CreatedAt: exportCreated,
		From: entries[1].OccurredAt, To: entries[3].OccurredAt}
	if _, _, err := domain.Export(goldenTenant, domain.Head{}, entries, key, ranged); !errors.Is(err, domain.ErrInvalidExport) {
		t.Errorf("an entry outside the time range: %v", err)
	}
	m, l, err := domain.Export(goldenTenant, entries[0].Head(), entries[1:3], key, ranged)
	if err != nil {
		t.Fatal(err)
	}
	if r := verify(m, l, key.Public()); !r.OK || r.Manifest.Occurred == nil {
		t.Fatalf("a time-ranged export: %+v", r.Failure)
	}
	if _, _, err := domain.Export(goldenTenant, domain.Head{}, entries, domain.SigningKey{}, opts); !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("no key: %v", err)
	}
}
