package domain

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
)

// Check names the first thing about an export that did not verify. The
// values are stable: `glossa audit verify --json` prints them.
type Check string

// What VerifyExport can find, in the order it looks.
const (
	// CheckManifest: manifest.json is not a glossa.audit/v1 manifest, or
	// contradicts itself.
	CheckManifest Check = "manifest_invalid"
	// CheckUnknownKey: none of the trusted keys has the manifest's key id.
	CheckUnknownKey Check = "unknown_key"
	// CheckSignature: the signature does not verify with that key — the
	// manifest was edited after signing, or signed by another key.
	CheckSignature Check = "signature_invalid"
	// CheckUnreadable: entries.jsonl could not be read.
	CheckUnreadable Check = "entries_unreadable"
	// CheckLine: a line is not an entry (or not terminated: a truncated
	// file).
	CheckLine Check = "line_invalid"
	// CheckNotCanonical: a line parses but is not the entry's one
	// spelling.
	CheckNotCanonical Check = "line_not_canonical"
	// The chain's own breaks (Break): another tenant's entry, a gap or a
	// reordering, a broken link, a hash that is not the entry's.
	CheckTenant   Check = Check(BreakTenant)
	CheckSequence Check = Check(BreakSequence)
	CheckLink     Check = Check(BreakLink)
	CheckHash     Check = Check(BreakHash)
	// CheckRange: the lines are a valid chain, but not the range the
	// manifest signs (lines missing at the end, extra lines, another last
	// hash, an entry outside the time range).
	CheckRange Check = "range_mismatch"
	// CheckDigest: entries.jsonl is not the file the manifest signs.
	CheckDigest Check = "digest_mismatch"
)

// ExportFailure is the first thing that did not verify.
type ExportFailure struct {
	Check Check
	// Line is the 1-based line of entries.jsonl, 0 when the failure is
	// not about one line.
	Line int64
	// Sequence is the line's sequence when it has one.
	Sequence int64
	Reason   string
}

func (f *ExportFailure) Error() string {
	where := ""
	switch {
	case f.Line > 0 && f.Sequence > 0:
		where = fmt.Sprintf("line %d (sequence %d): ", f.Line, f.Sequence)
	case f.Line > 0:
		where = fmt.Sprintf("line %d: ", f.Line)
	}
	return fmt.Sprintf("%s%s [%s]", where, f.Reason, f.Check)
}

// ExportReport is what VerifyExport found.
type ExportReport struct {
	// OK is true only when every check passed.
	OK bool
	// Manifest is the parsed manifest, nil when it did not parse.
	Manifest *Manifest
	// Verified counts the lines whose entry verified.
	Verified int64
	// Failure is the first thing that did not verify; nil when OK.
	Failure *ExportFailure
}

func failed(r ExportReport, f *ExportFailure) ExportReport {
	r.OK, r.Failure = false, f
	return r
}

// VerifyExport checks an export offline: the manifest's signature with
// one of keys (the trust anchor, which never comes from the export
// itself), then every line of entries as a link of the chain the
// manifest describes, then that the lines are exactly the range and the
// file the manifest signs. It reports the first thing that fails.
func VerifyExport(manifest []byte, entries io.Reader, keys []PublicKey) ExportReport {
	var r ExportReport
	m, start, err := parseManifest(manifest)
	if err != nil {
		return failed(r, &ExportFailure{Check: CheckManifest, Reason: err.Error()})
	}
	r.Manifest = &m
	if f := checkSignature(manifest, m, keys); f != nil {
		return failed(r, f)
	}

	tenant := uuid.MustParse(m.TenantID)
	chain := NewVerifier(tenant, start)
	digest := sha256.New()
	counted := &countingReader{r: io.TeeReader(entries, digest)}
	lines := bufio.NewReaderSize(counted, 64<<10)
	tr := m.Occurred
	for n := int64(1); ; n++ {
		line, err := readLine(lines)
		if errors.Is(err, io.EOF) && len(line) == 0 {
			break
		}
		var bad badLine
		switch {
		case errors.As(err, &bad):
			return failed(r, &ExportFailure{Check: CheckLine, Line: n, Reason: bad.Error()})
		case err != nil:
			return failed(r, &ExportFailure{Check: CheckUnreadable, Reason: err.Error()})
		}
		e, err := ParseLine(line)
		if err != nil {
			return failed(r, &ExportFailure{Check: CheckLine, Line: n, Reason: err.Error()})
		}
		if want, err := e.Line(); err != nil || !bytes.Equal(want, line) {
			return failed(r, &ExportFailure{Check: CheckNotCanonical, Line: n, Sequence: e.Sequence,
				Reason: "the line is not the RFC 8785 form of its entry"})
		}
		if err := chain.Next(e); err != nil {
			var ce *ChainError
			if errors.As(err, &ce) {
				return failed(r, &ExportFailure{Check: Check(ce.Kind), Line: n, Sequence: e.Sequence, Reason: ce.Reason})
			}
			return failed(r, &ExportFailure{Check: CheckHash, Line: n, Sequence: e.Sequence, Reason: err.Error()})
		}
		if e.Sequence > m.Range.LastSequence {
			return failed(r, &ExportFailure{Check: CheckRange, Line: n, Sequence: e.Sequence,
				Reason: fmt.Sprintf("the manifest's range ends at sequence %d", m.Range.LastSequence)})
		}
		if tr != nil {
			at := Instant(e.OccurredAt).Format(timeLayout)
			if at < tr.From || at >= tr.To {
				return failed(r, &ExportFailure{Check: CheckRange, Line: n, Sequence: e.Sequence,
					Reason: fmt.Sprintf("occurred at %s, outside the export's time range [%s, %s)", at, tr.From, tr.To)})
			}
		}
		r.Verified++
	}

	head := chain.Head()
	switch {
	case r.Verified != m.EntryCount:
		return failed(r, &ExportFailure{Check: CheckRange,
			Reason: fmt.Sprintf("the file holds %d entries; the manifest signs %d (truncated?)", r.Verified, m.EntryCount)})
	case HexHash(head.hash()) != m.Range.LastHash:
		return failed(r, &ExportFailure{Check: CheckRange, Reason: "the last entry's hash is not the manifest's last_hash"})
	}
	if sum := HexHash(digest.Sum(nil)); counted.n != m.Entries.Bytes || sum != m.Entries.SHA256 {
		return failed(r, &ExportFailure{Check: CheckDigest,
			Reason: fmt.Sprintf("%s is %d bytes with SHA-256 %s; the manifest signs %d bytes with %s",
				EntriesFile, counted.n, sum, m.Entries.Bytes, m.Entries.SHA256)})
	}
	r.OK = true
	return r
}

// parseManifest reads manifest.json strictly and checks it agrees with
// itself. It returns the chain head the range starts after.
func parseManifest(data []byte) (Manifest, Head, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, Head{}, fmt.Errorf("not a %s manifest: %w", ExportFormat, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Manifest{}, Head{}, errors.New("data after the manifest")
	}
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if m.Format != ExportFormat {
		bad("format is %q, not %q", m.Format, ExportFormat)
	}
	if t, err := uuid.Parse(m.TenantID); err != nil || t == uuid.Nil || t.String() != m.TenantID {
		bad("tenant_id %q", m.TenantID)
	}
	rg := m.Range
	first, err1 := parseHash(rg.FirstPrevHash)
	if err1 != nil {
		bad("range.first_prev_hash: %v", err1)
	}
	if _, err := parseHash(rg.LastHash); err != nil {
		bad("range.last_hash: %v", err)
	}
	switch {
	case rg.FirstSequence < 1:
		bad("range.first_sequence %d: sequences start at 1", rg.FirstSequence)
	case rg.LastSequence < rg.FirstSequence-1:
		bad("range.last_sequence %d is before first_sequence %d", rg.LastSequence, rg.FirstSequence)
	case m.EntryCount != rg.LastSequence-rg.FirstSequence+1:
		bad("entry_count %d is not the range's %d", m.EntryCount, rg.LastSequence-rg.FirstSequence+1)
	}
	if err1 == nil && rg.FirstSequence == 1 && !bytes.Equal(first, GenesisHash()) {
		bad("range.first_prev_hash of a range from sequence 1 must be 32 zero bytes")
	}
	if m.EntryCount == 0 && rg.LastHash != rg.FirstPrevHash {
		bad("an empty range's last_hash must be its first_prev_hash")
	}
	if m.Occurred != nil {
		f, errF := time.Parse(timeLayout, m.Occurred.From)
		t, errT := time.Parse(timeLayout, m.Occurred.To)
		if errF != nil || errT != nil || !f.Before(t) {
			bad("occurred is not a time range [from, to)")
		}
	}
	if m.Entries.Path != EntriesFile {
		bad("entries.path is %q, not %q", m.Entries.Path, EntriesFile)
	}
	if _, err := parseHash(m.Entries.SHA256); err != nil {
		bad("entries.sha256: %v", err)
	}
	if m.Entries.Bytes < 0 {
		bad("entries.bytes %d", m.Entries.Bytes)
	}
	if _, err := time.Parse(timeLayout, m.CreatedAt); err != nil {
		bad("created_at %q", m.CreatedAt)
	}
	if !keyIDPattern.MatchString(m.KeyID) {
		bad("key_id %q", m.KeyID)
	}
	if m.Signature == nil {
		bad("no signature")
	} else if m.Signature.Algorithm != SignatureAlgorithm {
		bad("signature algorithm %q, not %s", m.Signature.Algorithm, SignatureAlgorithm)
	}
	if len(errs) > 0 {
		return Manifest{}, Head{}, errors.New(strings.Join(errs, "; "))
	}
	return m, Head{Sequence: rg.FirstSequence - 1, Hash: first}, nil
}

func checkSignature(raw []byte, m Manifest, keys []PublicKey) *ExportFailure {
	var key *PublicKey
	ids := make([]string, 0, len(keys))
	for i := range keys {
		ids = append(ids, keys[i].ID)
		if keys[i].ID == m.KeyID {
			key = &keys[i]
		}
	}
	if key == nil {
		sort.Strings(ids)
		given := "none"
		if len(ids) > 0 {
			given = strings.Join(ids, ", ")
		}
		return &ExportFailure{Check: CheckUnknownKey,
			Reason: fmt.Sprintf("the manifest is signed with key %q; the trusted keys are: %s", m.KeyID, given)}
	}
	sig, err := base64.RawURLEncoding.DecodeString(m.Signature.Value)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return &ExportFailure{Check: CheckSignature, Reason: "the signature is not a base64url Ed25519 signature"}
	}
	unsigned, err := jcs.Without(raw, "signature")
	if err != nil {
		return &ExportFailure{Check: CheckManifest, Reason: err.Error()}
	}
	if len(key.Key) != ed25519.PublicKeySize || !ed25519.Verify(key.Key, unsigned, sig) {
		return &ExportFailure{Check: CheckSignature,
			Reason: fmt.Sprintf("the signature does not verify with key %q: the manifest was changed after signing, or another key signed it", m.KeyID)}
	}
	return nil
}

// badLine is a line that can't be an entry whatever it holds.
type badLine string

func (b badLine) Error() string { return string(b) }

const (
	errLineTooLong  badLine = "the line is longer than an entry can be"
	errUnterminated badLine = "the last line has no newline: the file is truncated"
	errEmptyLine    badLine = "an empty line"
)

// readLine returns the next line without its newline. At the end of the
// file it returns io.EOF with no line; a final line with no newline is
// errUnterminated.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLineBytes {
			return line, errLineTooLong
		}
		switch {
		case err == nil:
			line = line[:len(line)-1]
			if len(line) == 0 {
				return line, errEmptyLine
			}
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, io.EOF
			}
			return line, errUnterminated
		default:
			return line, err
		}
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
