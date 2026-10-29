//go:build system

package m5_test

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── §12.5 Audit export ───────────────────────────────────────────────

type auditReport struct {
	// Recorded is the harness's own count of successful mutating calls
	// in §12.1–§12.4, by criterion.
	Recorded map[string]int
	// Entries is how many lines the export held.
	Entries int
	// Unmatched are recorded calls with no entry, for the report.
	Unmatched []string
	Leaked    []string
}

// auditEntry is one glossa.audit/v1 line (RFC 0006 §6.1).
type auditEntry struct {
	Sequence      int64     `json:"sequence"`
	OccurredAt    time.Time `json:"occurred_at"`
	Actor         string    `json:"actor"`
	Action        string    `json:"action"`
	AggregateType string    `json:"aggregate_type"`
	AggregateID   string    `json:"aggregate_id"`
	Project       string    `json:"project_id"`
	PrevHash      string    `json:"prev_hash"`
	Hash          string    `json:"hash"`
}

func (s *scenario) auditExport() {
	const id = "12.5"
	started := time.Now()
	calls := s.log.snapshot()
	s.audit.Recorded = map[string]int{}
	var owed []call
	for _, c := range calls {
		if c.ok() && strings.HasPrefix(c.Criterion, "12.") {
			s.audit.Recorded[c.Criterion]++
			owed = append(owed, c)
		}
	}
	s.note(id, "The harness recorded %d successful mutating calls in §12.1–§12.4 itself, never from the platform: %v.",
		len(owed), s.audit.Recorded)

	s.step(id, "the tenant's audit entries can be listed", func() error {
		if _, err := list[auditEntry](s.owner, s.auditEntriesPath(), nil); err != nil {
			return missing("listing audit entries (GET "+s.auditEntriesPath()+")", err)
		}
		return nil
	})

	// The deployment publishes the audit key's public half for offline
	// verification (RFC 0006 §6.2, amended in wave 4). The verify steps
	// below trust the key the harness configured, not this document.
	s.step(id, "the audit public keys are published at "+auditKeysPath, func() error {
		st, body, err := s.owner.get(auditKeysPath)
		if err != nil || st != http.StatusOK {
			return missing("reading the audit public keys (GET "+auditKeysPath+")", fmt.Errorf("status %d: %v", st, err))
		}
		var doc struct {
			Format string `json:"format"`
			Keys   []struct {
				KeyID     string `json:"key_id"`
				Algorithm string `json:"algorithm"`
				PublicKey string `json:"public_key"`
				Active    bool   `json:"active"`
			} `json:"keys"`
		}
		if err := json.Unmarshal(body, &doc); err != nil || doc.Format != "glossa.audit.keys/1" {
			return fmt.Errorf("%s is not a glossa.audit.keys/1 document: %v: %s", auditKeysPath, err, body)
		}
		want := ed25519.NewKeyFromSeed(auditSeed).Public().(ed25519.PublicKey)
		for _, k := range doc.Keys {
			raw, _ := base64.RawURLEncoding.DecodeString(k.PublicKey)
			if k.KeyID == auditKeyID && k.Active && k.Algorithm == "Ed25519" && want.Equal(ed25519.PublicKey(raw)) {
				return nil
			}
		}
		return fmt.Errorf("%s does not publish the configured audit key %s as active: %s", auditKeysPath, auditKeyID, body)
	})

	dir := filepath.Join(s.t.TempDir(), "audit-export")
	exported := s.step(id, "export the run's range as an audit export job", func() error {
		return s.exportAudit(dir, started)
	})

	// verify runs whether or not there is an export: without one it
	// still says whether the command exists at all.
	verifyDir := dir
	if !exported {
		verifyDir = s.t.TempDir()
	}
	s.step(id, "`glossa audit verify` passes on the export, offline", func() error {
		res := glossa(s.t.TempDir(), map[string]string{}, append(cliAuditVerify, verifyDir, "--public-key", auditPublicKey())...)
		if res.code != 0 {
			if !exported {
				return fmt.Errorf("there is no export to verify, and `glossa audit verify` exits %d: %s", res.code, res.String())
			}
			return fmt.Errorf("`glossa audit verify` exits %d on the platform's own export: %s", res.code, res.String())
		}
		return nil
	})
	if !exported {
		s.unreached(id, "an entry for every call the harness recorded, with its actor",
			"one altered byte makes `glossa audit verify` fail",
			"no canary string appears anywhere in the export")
		return
	}
	entries, raw, err := readExport(dir)
	s.step(id, "an entry for every call the harness recorded, with its actor", func() error {
		if err != nil {
			return err
		}
		s.audit.Entries = len(entries)
		for _, c := range owed {
			if !hasEntry(entries, c) {
				s.audit.Unmatched = append(s.audit.Unmatched, fmt.Sprintf("§%s %s %s as %s", c.Criterion, c.Method, c.Path, c.Actor))
			}
		}
		if len(s.audit.Unmatched) > 0 {
			return fmt.Errorf("%d of %d recorded calls have no entry with the right actor (first: %s)",
				len(s.audit.Unmatched), len(owed), s.audit.Unmatched[0])
		}
		return nil
	})
	s.step(id, "one altered byte makes `glossa audit verify` fail", func() error {
		tampered := filepath.Join(s.t.TempDir(), "tampered")
		if err := copyDir(dir, tampered); err != nil {
			return err
		}
		if err := alterOneByte(tampered); err != nil {
			return err
		}
		res := glossa(s.t.TempDir(), map[string]string{}, append(cliAuditVerify, tampered, "--public-key", auditPublicKey())...)
		if res.code == 0 {
			return fmt.Errorf("a tampered export verifies")
		}
		// 1 is "does not verify"; anything else (a usage error) would
		// pass this step without the chain having been checked.
		if res.code != 1 {
			return fmt.Errorf("`glossa audit verify` on a tampered export exits %d, not 1: %s", res.code, res.String())
		}
		return nil
	})
	s.step(id, "no canary string appears anywhere in the export", func() error {
		for _, c := range canaries {
			if bytes.Contains(raw, []byte(c)) {
				s.audit.Leaked = append(s.audit.Leaked, c)
			}
		}
		if len(s.audit.Leaked) > 0 {
			return fmt.Errorf("message text leaked into the audit export: %s", strings.Join(s.audit.Leaked, ", "))
		}
		return nil
	})
}

// exportAudit runs an audit export job for [from, now] and downloads
// its lines and its signed manifest into dir.
func (s *scenario) exportAudit(dir string, from time.Time) error {
	// The range starts at the fixture: every call of §12.1–§12.4 is in
	// it. 31 days is §9.6's limit; this is minutes.
	var job struct {
		ID    string `json:"id"`
		State string `json:"state"`
		// FailureCode and FailureMessage say why a job failed.
		FailureCode    string `json:"failure_code"`
		FailureMessage string `json:"failure_message"`
	}
	if _, err := s.owner.try(http.MethodPost, s.auditExportJobsPath(), map[string]any{
		"from": from.Add(-time.Hour).UTC().Format(time.RFC3339), "to": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}, http.StatusCreated, &job); err != nil {
		return missing("starting an audit export job (POST "+s.auditExportJobsPath()+")", err)
	}
	ok, state := softly(60*time.Second, func() (bool, string) {
		if _, err := s.owner.try(http.MethodGet, s.auditExportJob(job.ID), nil, http.StatusOK, &job); err != nil {
			return false, err.Error()
		}
		if job.State == "failed" {
			return false, fmt.Sprintf("failed: %s: %s", job.FailureCode, job.FailureMessage)
		}
		return job.State == "succeeded", job.State
	})
	if !ok {
		return fmt.Errorf("the export job never succeeded: %s", state)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, path := range map[string]string{"entries.jsonl": "/file", "manifest.json": "/manifest"} {
		st, body, err := s.owner.get(s.auditExportJob(job.ID) + path)
		if err != nil || st != http.StatusOK {
			return fmt.Errorf("downloading the export's %s answered %d: %v", name, st, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readExport(dir string) ([]auditEntry, []byte, error) {
	var all []byte
	var entries []auditEntry
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, nil, err
		}
		all = append(all, raw...)
		if !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(raw))
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var e auditEntry
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				return nil, nil, fmt.Errorf("an export line is not a glossa.audit/v1 entry: %w", err)
			}
			entries = append(entries, e)
		}
	}
	return entries, all, nil
}

// hasEntry: an entry with the call's actor, inside the call's time
// window, about something the call named.
func hasEntry(entries []auditEntry, c call) bool {
	for _, e := range entries {
		if e.Actor != c.Actor || e.OccurredAt.Before(c.Start.Add(-time.Second)) || e.OccurredAt.After(c.End.Add(10*time.Second)) {
			continue
		}
		if len(c.IDs) == 0 {
			return true
		}
		for _, id := range c.IDs {
			if e.AggregateID == id || e.Project == id {
				return true
			}
		}
	}
	return false
}

func copyDir(from, to string) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	files, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(from, f.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(to, f.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// alterOneByte flips one character in the middle of the export's first
// line: a digit to another digit, so the line still parses and only the
// chain can tell.
func alterOneByte(dir string) error {
	path := filepath.Join(dir, "entries.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	end := bytes.IndexByte(b, '\n')
	if end < 0 {
		end = len(b)
	}
	for i := end / 2; i < end; i++ {
		if b[i] >= '0' && b[i] <= '8' {
			b[i]++
			return os.WriteFile(path, b, 0o644)
		}
	}
	return fmt.Errorf("the export's first line has no digit to alter")
}
