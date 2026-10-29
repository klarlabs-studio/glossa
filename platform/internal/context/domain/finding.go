package domain

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/google/uuid"
)

// The visual findings a capture carries (RFC 0005 §5): what the probe
// pass measured live in the page, while `scrollWidth`,
// `getComputedStyle`, `document.fonts.check()` and `explain()` still
// existed. The server re-measures nothing — it cannot, the page is long
// gone — so all it can do is hold the upload to the shape a probe is
// allowed to write, and complete the two members the page could not
// know.
//
// Context validates and carries them; Quality owns them. A finding is
// not a capture's data — it is one evaluation of a project, identified
// by a fingerprint only the server can compute — so nothing here is
// stored with the capture. The ingest hands these to Quality through
// the Findings port, and Quality makes ordinary findings of them.

// The finding members a capture's probe pass writes verbatim.
const (
	// FindingSchema is the schema every finding names.
	FindingSchema = "glossa.finding/v1"
	// FindingLayerVisual is the only layer a capture may carry: a page
	// reports what a page can see.
	FindingLayerVisual = "visual"
	// FindingSeverityWarning is the only severity a page may emit. A
	// visual finding becomes eligible for `error` only when the same
	// fingerprint appears in two consecutive captures (RFC 0005 §5.2),
	// which only the server can see — and a page that graded itself
	// could fail anyone's build.
	FindingSeverityWarning = "warning"
)

// Finding limits (RFC 0005 §10, and the finding.v1 schema's).
const (
	// MaxFindingsPerCapture bounds one capture's visual findings. It is
	// the cap the probe pass applies in the page; the server applies it
	// again, because a page is not to be trusted with how much storage
	// it may take.
	MaxFindingsPerCapture = 500
	// MaxFindingsPerUpload bounds the whole upload's, matching the
	// 10 000 findings one check run may hold: the ingest records one run
	// for the upload, and an upload past it is refused rather than
	// silently truncated. A capture plan that genuinely finds more than
	// 10 000 problems is split, as an upload past 200 MB is.
	MaxFindingsPerUpload = 10_000
	// MaxFindingMessageLen bounds the explanation, as the column and the
	// schema do.
	MaxFindingMessageLen = 4000
	// MaxFindingSubjectLen bounds the subject.
	MaxFindingSubjectLen = 500
	// MaxFindingCodeLen bounds the code.
	MaxFindingCodeLen = 64
	// MaxEvidenceBytes bounds one finding's evidence object in its JSON
	// form. The schema leaves evidence free-form per code, which without
	// a bound is an invitation to upload a page's DOM.
	MaxEvidenceBytes = 4096
)

// The finding schema's patterns, verbatim.
var (
	findingCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	// A probe names a region of the capture it is on, by its index into
	// that capture's regions.
	findingRegionPattern = regexp.MustCompile(`^r_(?:0|[1-9][0-9]*)$`)
)

// DocumentFinding is one entry of a capture's findings: a
// glossa.finding/v1 finding as the probe pass writes it in the page.
// Two members of that shape are deliberately absent, because the page
// cannot know them and a guess would be worse than a gap — the
// fingerprint, which hashes the catalog message ID a browser never has,
// and locus.capture, which the server mints. The ingest completes both.
type DocumentFinding struct {
	Schema   string         `json:"schema"`
	Layer    string         `json:"layer"`
	Code     string         `json:"code"`
	Severity string         `json:"severity"`
	Locus    DocumentLocus  `json:"locus"`
	Message  string         `json:"message"`
	Subject  *string        `json:"subject,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// DocumentLocus is as much of a finding's locus as the page knows.
type DocumentLocus struct {
	Key    *string `json:"key,omitempty"`
	Locale *string `json:"locale,omitempty"`
	Region *string `json:"region,omitempty"`
}

// VisualFinding is one validated probe finding of a capture, on its way
// to Quality. The key is resolved to the message's ID at ingest, as a
// region's is, so a later rename doesn't touch the finding.
type VisualFinding struct {
	// Code is the rule the probe applied (text-clipped, region-overlap,
	// runtime-format).
	Code string
	// Key is the message the finding is about; empty where the probe had
	// none (a runtime error about a message nobody can name).
	Key string
	// MessageID is the message the key named at ingest; nil for a key
	// the catalog didn't know.
	MessageID *uuid.UUID
	// Locale is the locale the finding is about. Empty where the probe
	// named none, and the ingest then reads the capture's.
	Locale string
	// Region is `r_<index into the capture's regions>`, where the probe
	// pointed at one.
	Region string
	// Message explains the finding to a person.
	Message string
	// Subject is what the finding names: the other message it overlaps,
	// the screen's locale.
	Subject string
	// Evidence is what the probe measured, free-form per code.
	Evidence map[string]any
}

// parse validates one finding of a capture that has regions regions.
func (df DocumentFinding) parse(regions int) (VisualFinding, error) {
	switch {
	case df.Schema != FindingSchema:
		return VisualFinding{}, fmt.Errorf("schema must be %q", FindingSchema)
	case df.Layer != FindingLayerVisual:
		return VisualFinding{}, fmt.Errorf("layer must be %q: a capture carries what a page can see", FindingLayerVisual)
	case df.Severity != FindingSeverityWarning:
		return VisualFinding{}, fmt.Errorf("severity must be %q: a visual finding is promoted by the server, never by the page",
			FindingSeverityWarning)
	case !textWithin(df.Code, 1, MaxFindingCodeLen) || !findingCodePattern.MatchString(df.Code):
		return VisualFinding{}, fmt.Errorf("code must be a finding code of at most %d characters", MaxFindingCodeLen)
	case !textWithin(df.Message, 1, MaxFindingMessageLen):
		return VisualFinding{}, fmt.Errorf("message must be 1–%d characters", MaxFindingMessageLen)
	}
	out := VisualFinding{Code: df.Code, Message: df.Message, Evidence: df.Evidence}
	if df.Subject != nil {
		if !textWithin(*df.Subject, 1, MaxFindingSubjectLen) {
			return VisualFinding{}, fmt.Errorf("subject must be 1–%d characters", MaxFindingSubjectLen)
		}
		out.Subject = *df.Subject
	}
	if err := df.Locus.parse(regions, &out); err != nil {
		return VisualFinding{}, err
	}
	return out, checkEvidence(df.Evidence)
}

func (dl DocumentLocus) parse(regions int, out *VisualFinding) error {
	if dl.Key != nil {
		if !validKey(*dl.Key) {
			return fmt.Errorf("locus.key must be a message key of at most %d characters", MaxKeyLen)
		}
		out.Key = *dl.Key
	}
	if dl.Locale != nil {
		if !validLocale(*dl.Locale) {
			return fmt.Errorf("locus.locale must be a BCP 47 tag of at most %d characters", maxLocaleLen)
		}
		out.Locale = *dl.Locale
	}
	if dl.Region == nil {
		return nil
	}
	// A region names one of this capture's regions. The finding schema's
	// dependentRequired makes `region` valid only beside a `capture`, so
	// the ingest has to mint that pair — and a pair pointing at a region
	// that isn't there could never be drawn on the capture.
	if !findingRegionPattern.MatchString(*dl.Region) {
		return fmt.Errorf("locus.region must name a region of this capture (r_0, r_1, …), not %q", *dl.Region)
	}
	if regionIndex(*dl.Region) >= regions {
		return fmt.Errorf("locus.region %s is not a region of this capture (it has %d)", *dl.Region, regions)
	}
	out.Region = *dl.Region
	return nil
}

// regionIndex is the index a region name points at, saturating at the
// most regions a capture may hold so a long run of digits cannot
// overflow.
func regionIndex(region string) int {
	n := 0
	for _, r := range region[len("r_"):] {
		if n = n*10 + int(r-'0'); n > MaxRegionsPerCapture {
			return MaxRegionsPerCapture
		}
	}
	return n
}

// checkEvidence bounds the free-form evidence object: the schema says
// what it means, not how big it may be.
func checkEvidence(evidence map[string]any) error {
	if len(evidence) == 0 {
		return nil
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("evidence must be a JSON object: %v", err)
	}
	if len(raw) > MaxEvidenceBytes {
		return fmt.Errorf("evidence must be at most %d bytes of JSON, not %d", MaxEvidenceBytes, len(raw))
	}
	return nil
}

// ResolveFindings sets each finding's message ID from ids (key → ID)
// and returns how many name a key ids lacks. A finding about an unknown
// key is kept: it is still true, and its fingerprint falls back to the
// key exactly as an offline check's does.
func ResolveFindings(findings []VisualFinding, ids map[string]uuid.UUID) (unknown int) {
	for i := range findings {
		if findings[i].Key == "" {
			continue
		}
		findings[i].MessageID = lookup(ids, findings[i].Key)
		if findings[i].MessageID == nil {
			unknown++
		}
	}
	return unknown
}

// FindingKeys returns the distinct keys the findings name, without the
// findings that name none.
func FindingKeys(findings []VisualFinding) []string {
	out := make([]string, 0, len(findings))
	seen := make(map[string]bool, len(findings))
	for _, f := range findings {
		if f.Key != "" && !seen[f.Key] {
			seen[f.Key] = true
			out = append(out, f.Key)
		}
	}
	return out
}
