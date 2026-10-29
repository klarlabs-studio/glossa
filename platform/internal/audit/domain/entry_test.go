package domain_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
)

var (
	goldenTenant  = uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	goldenProject = uuid.MustParse("0190a1b2-0000-7000-8000-000000000001")
)

// goldenDrafts are two entries whose canonical form and hashes are
// pinned below. If this test fails, the canonical encoding changed —
// and every chain already written would stop verifying. Change it only
// by adding a new CanonicalFormat, never by editing this one.
func goldenDrafts() []domain.Draft {
	return []domain.Draft{
		{
			EventID:       uuid.MustParse("0190a1b2-0000-7000-8000-00000000000a"),
			Source:        domain.SourceOutbox,
			Action:        "localization.translation.revised",
			Actor:         "person:0190a1b2-0000-7000-8000-0000000000aa",
			OccurredAt:    time.Date(2026, 10, 1, 9, 30, 0, 123456789, time.UTC),
			AggregateType: "translation",
			AggregateID:   "0190a1b2-0000-7000-8000-0000000000bb",
			Project:       uuid.NullUUID{UUID: goldenProject, Valid: true},
			Locale:        "de-CH",
			Summary:       json.RawMessage(`{"state": "needs_review", "revision": 3, "text": "string(len=12)"}`),
			TraceID:       "4bf92f3577b34da6a3ce929d0e0e4736",
		},
		{
			EventID:       uuid.MustParse("0190a1b2-0000-7000-8000-00000000000b"),
			Source:        domain.SourceDirect,
			Action:        domain.ActionSignInFailed,
			Actor:         "unknown",
			OccurredAt:    time.Date(2026, 10, 1, 9, 31, 0, 0, time.FixedZone("CEST", 2*60*60)),
			AggregateType: domain.AggregatePerson,
			AggregateID:   "0190a1b2-0000-7000-8000-0000000000aa",
			Summary:       json.RawMessage(`{"reason":"invalid_credentials","method":"password"}`),
			RequestID:     "req-42",
		},
	}
}

// The canonical forms, written out by hand from RFC 8785: members sorted
// by name, no whitespace, absent optionals as null, the time in UTC with
// six fractional digits (Postgres keeps microseconds).
const (
	goldenCanonical1 = `{"action":"localization.translation.revised","actor":"person:0190a1b2-0000-7000-8000-0000000000aa",` +
		`"aggregate_id":"0190a1b2-0000-7000-8000-0000000000bb","aggregate_type":"translation",` +
		`"event_id":"0190a1b2-0000-7000-8000-00000000000a","format":"glossa.audit.entry/1","locale":"de-CH",` +
		`"occurred_at":"2026-10-01T09:30:00.123456Z","project_id":"0190a1b2-0000-7000-8000-000000000001",` +
		`"request_id":null,"sequence":1,"source":"outbox",` +
		`"summary":{"revision":3,"state":"needs_review","text":"string(len=12)"},` +
		`"tenant_id":"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`
	goldenCanonical2 = `{"action":"identity.person.sign_in_failed","actor":"unknown",` +
		`"aggregate_id":"0190a1b2-0000-7000-8000-0000000000aa","aggregate_type":"person",` +
		`"event_id":"0190a1b2-0000-7000-8000-00000000000b","format":"glossa.audit.entry/1","locale":null,` +
		`"occurred_at":"2026-10-01T07:31:00.000000Z","project_id":null,"request_id":"req-42","sequence":2,` +
		`"source":"direct","summary":{"method":"password","reason":"invalid_credentials"},` +
		`"tenant_id":"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","trace_id":null}`
	// The pinned hashes: sha256(prev_hash ‖ canonical). The first was
	// also checked outside Go:
	//   (head -c 32 /dev/zero; printf '%s' "$goldenCanonical1") | shasum -a 256
	goldenHash1 = "2d5202032e51001c51fe57b7d7ce4e9b89bf166395ff6083a34805691aa2caf5"
	goldenHash2 = "7ca45202b96151639a7ebed645e7d259436b5071d021837454f3d2200f80f212"
)

func chain(t *testing.T) []domain.Entry {
	t.Helper()
	var head domain.Head
	var out []domain.Entry
	for _, d := range goldenDrafts() {
		e, err := domain.Append(goldenTenant, head, d)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		out = append(out, e)
		head = e.Head()
	}
	return out
}

func TestCanonicalEncodingIsStable(t *testing.T) {
	entries := chain(t)
	for i, want := range []string{goldenCanonical1, goldenCanonical2} {
		got, err := entries[i].Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("entry %d canonical form\n got: %s\nwant: %s", i+1, got, want)
		}
	}

	// The hash definition, recomputed here from the hand-written forms
	// rather than through the code under test.
	prev := make([]byte, 32)
	for i, canon := range []string{goldenCanonical1, goldenCanonical2} {
		sum := sha256.Sum256(append(bytes.Clone(prev), canon...))
		if !bytes.Equal(entries[i].PrevHash, prev) {
			t.Errorf("entry %d prev_hash = %x, want %x", i+1, entries[i].PrevHash, prev)
		}
		if !bytes.Equal(entries[i].Hash, sum[:]) {
			t.Errorf("entry %d hash = %x, want sha256(prev ‖ canonical) = %x", i+1, entries[i].Hash, sum)
		}
		prev = sum[:]
	}
}

func TestGoldenHashesArePinned(t *testing.T) {
	entries := chain(t)
	for i, want := range []string{goldenHash1, goldenHash2} {
		if got := hex.EncodeToString(entries[i].Hash); got != want {
			t.Errorf("entry %d hash = %s, want %s: the canonical encoding or the hash changed", i+1, got, want)
		}
	}
}

func TestSequenceAndGenesis(t *testing.T) {
	entries := chain(t)
	if entries[0].Sequence != 1 || entries[1].Sequence != 2 {
		t.Errorf("sequences %d, %d", entries[0].Sequence, entries[1].Sequence)
	}
	if !bytes.Equal(entries[0].PrevHash, domain.GenesisHash()) || len(domain.GenesisHash()) != 32 {
		t.Error("the first entry does not link to 32 zero bytes")
	}
	if !bytes.Equal(entries[1].PrevHash, entries[0].Hash) {
		t.Error("the second entry does not link to the first")
	}
	if err := domain.Verify(goldenTenant, domain.Head{}, entries); err != nil {
		t.Errorf("an untouched chain does not verify: %v", err)
	}
}

func TestTamperIsDetected(t *testing.T) {
	tamper := map[string]func(*domain.Entry){
		"action":         func(e *domain.Entry) { e.Action = "localization.translation.reviewed" },
		"actor":          func(e *domain.Entry) { e.Actor = "person:0190a1b2-0000-7000-8000-0000000000cc" },
		"occurred_at":    func(e *domain.Entry) { e.OccurredAt = e.OccurredAt.Add(time.Microsecond) },
		"aggregate_id":   func(e *domain.Entry) { e.AggregateID = "0190a1b2-0000-7000-8000-0000000000bc" },
		"aggregate_type": func(e *domain.Entry) { e.AggregateType = "message" },
		"event_id":       func(e *domain.Entry) { e.EventID = uuid.MustParse("0190a1b2-0000-7000-8000-0000000000ff") },
		"source":         func(e *domain.Entry) { e.Source = domain.SourceDirect },
		"project":        func(e *domain.Entry) { e.Project = uuid.NullUUID{} },
		"locale":         func(e *domain.Entry) { e.Locale = "de" },
		"summary": func(e *domain.Entry) {
			e.Summary = json.RawMessage(`{"state":"approved","revision":3,"text":"string(len=12)"}`)
		},
		"trace_id":  func(e *domain.Entry) { e.TraceID = "" },
		"hash":      func(e *domain.Entry) { e.Hash[0] ^= 1 },
		"prev_hash": func(e *domain.Entry) { e.PrevHash = bytes.Repeat([]byte{1}, 32) },
		"sequence":  func(e *domain.Entry) { e.Sequence = 7 },
		"tenant":    func(e *domain.Entry) { e.Tenant = uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c") },
	}
	for name, edit := range tamper {
		t.Run(name, func(t *testing.T) {
			entries := chain(t)
			edit(&entries[0])
			err := domain.Verify(goldenTenant, domain.Head{}, entries)
			var ce *domain.ChainError
			if !errors.As(err, &ce) || !errors.Is(err, domain.ErrChainBroken) {
				t.Fatalf("a tampered %s verified: %v", name, err)
			}
			if ce.Sequence != entries[0].Sequence {
				t.Errorf("broken at %d, want the tampered entry", ce.Sequence)
			}
		})
	}

	t.Run("a dropped entry", func(t *testing.T) {
		entries := chain(t)
		if err := domain.Verify(goldenTenant, domain.Head{}, entries[1:]); err == nil {
			t.Error("a chain missing its first entry verified")
		}
	})
	t.Run("a reordered range", func(t *testing.T) {
		entries := chain(t)
		if err := domain.Verify(goldenTenant, domain.Head{}, []domain.Entry{entries[1], entries[0]}); err == nil {
			t.Error("a reordered chain verified")
		}
	})
	t.Run("a range from its predecessor", func(t *testing.T) {
		entries := chain(t)
		if err := domain.Verify(goldenTenant, entries[0].Head(), entries[1:]); err != nil {
			t.Errorf("a range verified from the entry before it: %v", err)
		}
		if err := domain.Verify(goldenTenant, domain.Head{Sequence: 1, Hash: make([]byte, 32)}, entries[1:]); err == nil {
			t.Error("a range verified against the wrong predecessor")
		}
	})
}

// Whitespace and member order in a stored summary do not change the
// hash: the canonical form is computed, not stored, so Postgres' jsonb
// normalisation cannot break a chain.
func TestSummaryEncodingDoesNotChangeTheHash(t *testing.T) {
	entries := chain(t)
	e := entries[0]
	e.Summary = json.RawMessage("{ \"text\" : \"string(len=12)\",\n \"revision\":3, \"state\":\"needs_review\" }")
	sum, err := e.ComputeHash()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sum, entries[0].Hash) {
		t.Error("re-encoding the summary changed the hash")
	}
}

func TestAppendRefusesWhatTheChainMustNotHold(t *testing.T) {
	valid := goldenDrafts()[0]
	for name, edit := range map[string]func(*domain.Draft){
		"no event id":        func(d *domain.Draft) { d.EventID = uuid.Nil },
		"a bad source":       func(d *domain.Draft) { d.Source = "import" },
		"a bad action":       func(d *domain.Draft) { d.Action = "Revised Translation" },
		"an unspelled actor": func(d *domain.Draft) { d.Actor = "alice@example.com" },
		"no actor":           func(d *domain.Draft) { d.Actor = "" },
		"no time":            func(d *domain.Draft) { d.OccurredAt = time.Time{} },
		"no aggregate":       func(d *domain.Draft) { d.AggregateID = "" },
		"a bad locale":       func(d *domain.Draft) { d.Locale = "de CH" },
		"a bad trace":        func(d *domain.Draft) { d.TraceID = "nope" },
		"a summary that is text": func(d *domain.Draft) {
			d.Summary = json.RawMessage(`{"text":"` + string(bytes.Repeat([]byte("lorem ipsum "), 20)) + `"}`)
		},
		"a summary that is not an object": func(d *domain.Draft) { d.Summary = json.RawMessage(`["a"]`) },
	} {
		t.Run(name, func(t *testing.T) {
			d := valid
			edit(&d)
			if _, err := domain.Append(goldenTenant, domain.Head{}, d); !errors.Is(err, domain.ErrInvalidEntry) {
				t.Errorf("appended: %v", err)
			}
		})
	}
	if _, err := domain.Append(uuid.Nil, domain.Head{}, valid); !errors.Is(err, domain.ErrInvalidEntry) {
		t.Error("appended to no tenant")
	}
}
