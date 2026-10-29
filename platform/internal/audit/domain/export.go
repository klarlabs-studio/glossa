package domain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
)

// The glossa.audit/v1 export (RFC 0006 §6.2; the file format is written
// out in platform/README.md, "Audit export format").
//
// An export is a directory of two files:
//
//   - entries.jsonl: one line per entry, in sequence order. A line is
//     the RFC 8785 form of the entry's canonical object (glossa.audit.
//     entry/1, what its hash covers) with two more members, prev_hash
//     and hash, as lowercase hex, and ends with "\n". A reader recomputes
//     each hash as sha256(prev_hash ‖ canonical(line without prev_hash
//     and hash)) and walks the chain without anything but the file.
//   - manifest.json: what the lines are — the tenant, the sequence
//     range, the first prev_hash and the last hash, the entry count, the
//     SHA-256 and length of entries.jsonl — signed with the audit key
//     (Ed25519 over the RFC 8785 form of the manifest without its
//     signature member).
//
// The manifest binds the file's digest and the chain's two ends, so a
// changed, added, removed, reordered or truncated line is caught twice:
// by the chain, which says where, and by the digest, which the
// signature covers.

// ExportFormat names the export (the manifest's format member).
const ExportFormat = "glossa.audit/v1"

// The export's two files.
const (
	EntriesFile  = "entries.jsonl"
	ManifestFile = "manifest.json"
)

// maxLineBytes bounds one line. Entries are content-free and small; a
// longer line is not an entry.
const maxLineBytes = 1 << 20

// ErrInvalidExport is returned when an export can't be written: a range
// that is not a chain, or an entry outside the requested time range.
var ErrInvalidExport = errors.New("audit: invalid export")

// exportLine is one entries.jsonl line.
type exportLine struct {
	canonicalEntry
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

// Line returns e's entries.jsonl line, without the newline.
func (e Entry) Line() ([]byte, error) {
	if len(e.PrevHash) != HashSize || len(e.Hash) != HashSize {
		return nil, fmt.Errorf("%w: an entry without its hashes", ErrInvalidEntry)
	}
	out, err := jcs.Marshal(exportLine{canonicalEntry: e.canonical(), PrevHash: HexHash(e.PrevHash), Hash: HexHash(e.Hash)})
	if err != nil {
		return nil, fmt.Errorf("%w: export line: %w", ErrInvalidEntry, err)
	}
	return out, nil
}

// ParseLine reads one entries.jsonl line (without its newline) back
// into the entry it records. It refuses a line that is not exactly the
// line that entry renders to, so an export has one spelling.
func ParseLine(line []byte) (Entry, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var l exportLine
	if err := dec.Decode(&l); err != nil {
		return Entry{}, fmt.Errorf("not a %s line: %w", ExportFormat, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Entry{}, fmt.Errorf("not a %s line: data after the entry", ExportFormat)
	}
	if l.Format != CanonicalFormat {
		return Entry{}, fmt.Errorf("entry format is %q, not %q", l.Format, CanonicalFormat)
	}
	e := Entry{Sequence: l.Sequence}
	var err error
	if e.Tenant, err = uuid.Parse(l.TenantID); err != nil {
		return Entry{}, fmt.Errorf("tenant_id: %w", err)
	}
	if e.EventID, err = uuid.Parse(l.EventID); err != nil {
		return Entry{}, fmt.Errorf("event_id: %w", err)
	}
	if e.OccurredAt, err = time.Parse(timeLayout, l.OccurredAt); err != nil {
		return Entry{}, fmt.Errorf("occurred_at: %w", err)
	}
	if l.ProjectID != nil {
		p, err := uuid.Parse(*l.ProjectID)
		if err != nil {
			return Entry{}, fmt.Errorf("project_id: %w", err)
		}
		e.Project = uuid.NullUUID{UUID: p, Valid: true}
	}
	if len(l.Summary) == 0 || bytes.Equal(l.Summary, []byte("null")) {
		return Entry{}, errors.New("summary: missing")
	}
	e.Source, e.Action, e.Actor = Source(l.Source), l.Action, l.Actor
	e.AggregateType, e.AggregateID, e.Summary = l.AggregateType, l.AggregateID, l.Summary
	e.Locale, e.RequestID, e.TraceID = deref(l.Locale), deref(l.RequestID), deref(l.TraceID)
	if e.PrevHash, err = parseHash(l.PrevHash); err != nil {
		return Entry{}, fmt.Errorf("prev_hash: %w", err)
	}
	if e.Hash, err = parseHash(l.Hash); err != nil {
		return Entry{}, fmt.Errorf("hash: %w", err)
	}
	return e, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func parseHash(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != HashSize || HexHash(b) != s {
		return nil, fmt.Errorf("want %d bytes of lowercase hex", HashSize)
	}
	return b, nil
}

// Manifest is manifest.json.
type Manifest struct {
	Format   string `json:"format"`
	TenantID string `json:"tenant_id"`
	// Range is the chain segment the lines hold. FirstPrevHash is the
	// hash of the entry before FirstSequence (32 zero bytes when it is
	// 1); an empty export has LastSequence = FirstSequence - 1 and
	// LastHash = FirstPrevHash.
	Range ManifestRange `json:"range"`
	// Occurred is the time range the export was asked for, [from, to);
	// null for an export of a sequence range.
	Occurred   *TimeRange    `json:"occurred"`
	EntryCount int64         `json:"entry_count"`
	Entries    EntriesDigest `json:"entries"`
	CreatedAt  string        `json:"created_at"`
	KeyID      string        `json:"key_id"`
	// Signature covers the RFC 8785 form of every other member.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// ManifestRange is the chain segment an export holds.
type ManifestRange struct {
	FirstSequence int64  `json:"first_sequence"`
	LastSequence  int64  `json:"last_sequence"`
	FirstPrevHash string `json:"first_prev_hash"`
	LastHash      string `json:"last_hash"`
}

// TimeRange is [From, To) in the canonical time form.
type TimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// EntriesDigest names entries.jsonl and pins its bytes.
type EntriesDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// ManifestSignature is the manifest's one signature.
type ManifestSignature struct {
	Algorithm string `json:"algorithm"`
	// Value is the Ed25519 signature, base64url without padding.
	Value string `json:"value"`
}

// ExportOptions are what an export records beyond its entries.
type ExportOptions struct {
	// CreatedAt is when the export was made (now, for a job).
	CreatedAt time.Time
	// From and To, when both are set, are the time range [From, To) the
	// export was asked for: every entry must fall in it.
	From, To time.Time
}

func (o ExportOptions) timeRange() (*TimeRange, error) {
	if o.From.IsZero() && o.To.IsZero() {
		return nil, nil
	}
	if o.From.IsZero() || o.To.IsZero() || !Instant(o.From).Before(Instant(o.To)) {
		return nil, fmt.Errorf("%w: the time range needs from before to", ErrInvalidExport)
	}
	return &TimeRange{From: Instant(o.From).Format(timeLayout), To: Instant(o.To).Format(timeLayout)}, nil
}

// ExportWriter streams a range of one tenant's chain to entries.jsonl
// and then signs its manifest, so a range of any length is exported in
// constant memory. It refuses to write anything that is not the next
// link of the chain: what it signs, verifies.
type ExportWriter struct {
	w        io.Writer
	digest   hash.Hash
	bytes    int64
	tenant   uuid.UUID
	from     Head
	opts     ExportOptions
	occurred *TimeRange
	chain    *Verifier
}

// NewExportWriter starts an export of tenant's chain after from (the
// zero Head for a range from the first entry) to w.
func NewExportWriter(w io.Writer, tenant uuid.UUID, from Head, opts ExportOptions) (*ExportWriter, error) {
	if tenant == uuid.Nil {
		return nil, fmt.Errorf("%w: no tenant", ErrInvalidExport)
	}
	if from.Sequence < 0 || (from.Sequence > 0 && len(from.Hash) != HashSize) {
		return nil, fmt.Errorf("%w: a malformed chain head", ErrInvalidExport)
	}
	if opts.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: no creation time", ErrInvalidExport)
	}
	tr, err := opts.timeRange()
	if err != nil {
		return nil, err
	}
	from = Head{Sequence: from.Sequence, Hash: bytes.Clone(from.hash())}
	return &ExportWriter{
		w: w, digest: sha256.New(), tenant: tenant, from: from, opts: opts, occurred: tr,
		chain: NewVerifier(tenant, from),
	}, nil
}

// Write appends e's line. e must be the next entry of the chain.
func (x *ExportWriter) Write(e Entry) error {
	if err := x.chain.Next(e); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidExport, err)
	}
	if x.occurred != nil {
		t := Instant(e.OccurredAt)
		if t.Before(Instant(x.opts.From)) || !t.Before(Instant(x.opts.To)) {
			return fmt.Errorf("%w: entry %d occurred outside the time range", ErrInvalidExport, e.Sequence)
		}
	}
	line, err := e.Line()
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := io.MultiWriter(x.w, x.digest).Write(line); err != nil {
		return err
	}
	x.bytes += int64(len(line))
	return nil
}

// Finish returns the signed manifest.json for what was written.
func (x *ExportWriter) Finish(key SigningKey) ([]byte, Manifest, error) {
	if !keyIDPattern.MatchString(key.ID) || len(key.Key) != ed25519.PrivateKeySize {
		return nil, Manifest{}, fmt.Errorf("%w: no usable signing key", ErrInvalidKey)
	}
	last := x.chain.Head()
	m := Manifest{
		Format:   ExportFormat,
		TenantID: x.tenant.String(),
		Range: ManifestRange{
			FirstSequence: x.from.Sequence + 1,
			LastSequence:  last.Sequence,
			FirstPrevHash: HexHash(x.from.Hash),
			LastHash:      HexHash(last.hash()),
		},
		Occurred:   x.occurred,
		EntryCount: x.chain.Count(),
		Entries:    EntriesDigest{Path: EntriesFile, SHA256: hex.EncodeToString(x.digest.Sum(nil)), Bytes: x.bytes},
		CreatedAt:  Instant(x.opts.CreatedAt).Format(timeLayout),
		KeyID:      key.ID,
	}
	unsigned, err := jcs.Marshal(m)
	if err != nil {
		return nil, Manifest{}, err
	}
	m.Signature = &ManifestSignature{
		Algorithm: SignatureAlgorithm,
		Value:     base64.RawURLEncoding.EncodeToString(ed25519.Sign(key.Key, unsigned)),
	}
	out, err := jcs.Marshal(m)
	if err != nil {
		return nil, Manifest{}, err
	}
	return out, m, nil
}

// Export writes a whole range at once: the manifest and the lines of
// entries, which follow from in tenant's chain.
func Export(tenant uuid.UUID, from Head, entries []Entry, key SigningKey, opts ExportOptions) (manifest, lines []byte, err error) {
	var buf bytes.Buffer
	x, err := NewExportWriter(&buf, tenant, from, opts)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if err := x.Write(e); err != nil {
			return nil, nil, err
		}
	}
	manifest, _, err = x.Finish(key)
	if err != nil {
		return nil, nil, err
	}
	return manifest, buf.Bytes(), nil
}
