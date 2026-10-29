package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// ReviewState is where a translation is in review.
type ReviewState string

// Review states.
const (
	StateDraft       ReviewState = "draft"
	StateNeedsReview ReviewState = "needs_review"
	StateApproved    ReviewState = "approved"
	StateRejected    ReviewState = "rejected"
)

// ParseReviewState validates s.
func ParseReviewState(s string) (ReviewState, error) {
	switch ReviewState(s) {
	case StateDraft, StateNeedsReview, StateApproved, StateRejected:
		return ReviewState(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidReviewState, s)
}

// ReviewFlow is the review process as data: which state changes are
// allowed and which target states are a reviewer's decision. It is the
// seam where the Phase-4 workflow engine (a statekit statechart per
// project, RFC 0002 §4) takes over; nothing else in this package
// hard-codes review rules.
type ReviewFlow struct {
	Transitions map[ReviewState][]ReviewState
	// ReviewerOnly lists states only someone with the review permission
	// may put a translation in.
	ReviewerOnly []ReviewState
}

// DefaultReviewFlow lets any state move to any other; approving and
// rejecting are reviewer decisions.
func DefaultReviewFlow() ReviewFlow {
	all := []ReviewState{StateDraft, StateNeedsReview, StateApproved, StateRejected}
	t := map[ReviewState][]ReviewState{}
	for _, from := range all {
		t[from] = slices.DeleteFunc(slices.Clone(all), func(to ReviewState) bool { return to == from })
	}
	return ReviewFlow{Transitions: t, ReviewerOnly: []ReviewState{StateApproved, StateRejected}}
}

// Allows reports whether from → to is a permitted change.
func (f ReviewFlow) Allows(from, to ReviewState) bool {
	return slices.Contains(f.Transitions[from], to)
}

// NeedsReviewer reports whether only a reviewer may move to s.
func (f ReviewFlow) NeedsReviewer(s ReviewState) bool { return slices.Contains(f.ReviewerOnly, s) }

// WritePolicy decides the review state a new translation text gets.
type WritePolicy struct {
	// ReviewRequired is the project setting: new text waits for review
	// unless a reviewer approves it as they write it.
	ReviewRequired bool
	Flow           ReviewFlow
}

// StateOnWrite returns the state for new text and whether landing there
// is a self-review. requested is what the writer asked for (nil: the
// policy's default); canReview says whether they hold the review
// permission for the locale. New text never keeps an earlier approval:
// it is re-decided here.
//
// A person who writes text and asks for it approved is approving their
// own work (RFC 0006 §15 Q6, amended 2026-10-08): when anyone else could
// review the locale (othersCanReview) it lands as needs_review instead,
// and when no one could it lands approved, marked as a self-review.
// Imports (OriginImport) and writes by tokens are not subject to it.
func (p WritePolicy) StateOnWrite(requested *ReviewState, canReview bool, w Write, othersCanReview bool) (ReviewState, bool, error) {
	if requested == nil {
		if p.ReviewRequired {
			return StateNeedsReview, false, nil
		}
		return StateApproved, false, nil
	}
	switch s := *requested; {
	case s == StateRejected:
		return "", false, ErrWriteCannotReject
	case p.Flow.NeedsReviewer(s) && p.ReviewRequired && !canReview:
		return "", false, ErrReviewForbidden
	case s == StateApproved && p.ReviewRequired && canReview && selfApproves(w.Provenance):
		if othersCanReview {
			return StateNeedsReview, false, nil
		}
		return s, true, nil
	default:
		return s, false, nil
	}
}

// selfApproves reports whether a write with provenance p is a person
// approving the text they just wrote.
func selfApproves(p Provenance) bool {
	return strings.HasPrefix(p.By, "person:") && p.Origin != OriginImport
}

// Origin says how a translation revision came to be (intent §22).
type Origin string

// Origins.
//
// OriginAI and OriginAgent are both machine-written, and they are not
// the same act. `ai` is a person asking the platform to translate for
// them; `agent` is an autonomous agent writing through a long-lived
// token, which is what an MCP write is (RFC 0005 §7.3). Telling them
// apart is the revision log's job, so it is an origin of its own and
// not a field inside OriginDetail.
const (
	OriginHuman              Origin = "human"
	OriginAI                 Origin = "ai"
	OriginAgent              Origin = "agent"
	OriginTranslationMemory  Origin = "translation_memory"
	OriginMachineTranslation Origin = "machine_translation"
	OriginImport             Origin = "import"
	OriginAdaptation         Origin = "adaptation"
)

// ParseOrigin validates s; "" means def.
func ParseOrigin(s string, def Origin) (Origin, error) {
	switch Origin(s) {
	case "":
		return def, nil
	case OriginHuman, OriginAI, OriginAgent, OriginTranslationMemory, OriginMachineTranslation,
		OriginImport, OriginAdaptation:
		return Origin(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidOrigin, s)
}

// MaxOriginDetail bounds a revision's origin detail.
const MaxOriginDetail = 16 << 10

// Provenance records why a revision exists: how it was made (Origin),
// the specifics (model and prompt version, TM match, import job, the
// locale it was adapted from …) and who made it.
type Provenance struct {
	Origin Origin
	// Detail is a JSON object; "{}" when there is nothing to add.
	Detail json.RawMessage
	// By is the acting principal, "person:<id>" or "token:<id>".
	By string
}

// NewProvenance validates detail (nil or a JSON object).
func NewProvenance(origin Origin, detail json.RawMessage, by string) (Provenance, error) {
	if len(bytes.TrimSpace(detail)) == 0 {
		detail = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if len(detail) > MaxOriginDetail || json.Unmarshal(detail, &obj) != nil || obj == nil {
		return Provenance{}, ErrInvalidOriginInfo
	}
	return Provenance{Origin: origin, Detail: detail, By: by}, nil
}

// RevisionKind distinguishes new text from a review decision in the log.
type RevisionKind string

// Revision kinds.
const (
	KindContent RevisionKind = "content"
	KindReview  RevisionKind = "review"
)

// Revision is one entry of a translation's append-only log: a full
// snapshot (text, state, source revision) plus who did it and how. The
// translation's current state is always its latest revision.
type Revision struct {
	TranslationID  TranslationID
	Number         int
	Kind           RevisionKind
	Content        mfcontent.Content
	State          ReviewState
	Provenance     Provenance
	SourceRevision int
	// Findings are the structural QA warnings the text had when written.
	Findings []mf.Finding
	// SelfReview marks a review decision made by the author of the text
	// it decided, which is allowed only when no one else could review
	// (RFC 0006 §15 Q6, amended 2026-10-08).
	SelfReview bool
	CreatedAt  time.Time
}

// Translation is one message's current text in one locale. Whether it
// is outdated is not stored: it is outdated exactly when the source
// revision it was made against is older than the message's current one.
type Translation struct {
	ID        TranslationID
	ProjectID uuid.UUID
	MessageID uuid.UUID
	Locale    bcp47.Tag
	Content   mfcontent.Content
	State     ReviewState
	// Origin and By are the provenance of the current text.
	Origin Origin
	By     string
	// SourceRevision is the source revision the text was made against.
	SourceRevision int
	Warnings       []mf.Finding
	// Revision is the number of the latest log entry; Version is the
	// ETag and equals Revision (every change appends to the log).
	Revision  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Outdated reports whether the source changed since this text was made.
func (t Translation) Outdated(currentSourceRevision int) bool {
	return t.SourceRevision < currentSourceRevision
}

// Write is new text for a translation.
type Write struct {
	Content        mfcontent.Content
	Provenance     Provenance
	SourceRevision int
	// State is the requested review state; nil means the policy default.
	State *ReviewState
	// QA is the structural check of Content against the source.
	QA QAResult
}

// NewTranslation creates the first revision of message's text in locale.
func NewTranslation(project, message uuid.UUID, locale bcp47.Tag, w Write, policy WritePolicy, canReview, othersCanReview bool, now time.Time) (Translation, Revision, error) {
	if err := w.QA.Gate(); err != nil {
		return Translation{}, Revision{}, err
	}
	state, self, err := policy.StateOnWrite(w.State, canReview, w, othersCanReview)
	if err != nil {
		return Translation{}, Revision{}, err
	}
	t := Translation{
		ID: NewTranslationID(), ProjectID: project, MessageID: message, Locale: locale, CreatedAt: now,
	}
	rev := t.apply(KindContent, w.Content, state, w.Provenance, w.SourceRevision, w.QA.Warnings, now)
	rev.SelfReview = self
	return t, rev, nil
}

// Revise records new text. Text with the same model, made against the
// same source revision, is no new revision unless it asks for a
// different review state — then it is a review decision. Re-submitting
// unchanged text against a newer source revision is a content revision:
// it confirms the text still fits and clears "outdated".
func (t *Translation) Revise(w Write, policy WritePolicy, canReview, othersCanReview bool, now time.Time) (Revision, bool, error) {
	if err := w.QA.Gate(); err != nil {
		return Revision{}, false, err
	}
	if t.Content.SameModel(w.Content) && t.SourceRevision == w.SourceRevision {
		if w.State == nil || *w.State == t.State {
			return Revision{}, false, nil
		}
		rev, err := t.Review(*w.State, w.Provenance.By, policy.Flow, canReview, othersCanReview, now)
		return rev, err == nil, err
	}
	state, self, err := policy.StateOnWrite(w.State, canReview, w, othersCanReview)
	if err != nil {
		return Revision{}, false, err
	}
	rev := t.apply(KindContent, w.Content, state, w.Provenance, w.SourceRevision, w.QA.Warnings, now)
	rev.SelfReview = self
	return rev, true, nil
}

// Review moves the translation to state to, as a review decision by by.
// othersCanReview says whether anyone besides by could review this
// translation's locale in its project: the author of the text never
// approves or rejects it then; otherwise they may, and the revision is
// marked as a self-review.
func (t *Translation) Review(to ReviewState, by string, flow ReviewFlow, canReview, othersCanReview bool, now time.Time) (Revision, error) {
	if !flow.Allows(t.State, to) {
		return Revision{}, fmt.Errorf("%w: %s → %s", ErrTransition, t.State, to)
	}
	if flow.NeedsReviewer(to) && !canReview {
		return Revision{}, ErrReviewForbidden
	}
	own := t.isOwnText(to, by)
	if own && othersCanReview {
		return Revision{}, ErrOwnText
	}
	// The text's provenance stays; the log entry names the reviewer.
	prov := Provenance{Origin: t.Origin, Detail: json.RawMessage(`{}`), By: by}
	rev := t.apply(KindReview, t.Content, to, prov, t.SourceRevision, t.Warnings, now)
	rev.SelfReview = own
	return rev, nil
}

// isOwnText reports the four-eyes case (RFC 0006 §15 Q6): approving or
// rejecting by the principal who wrote the current text. Imports carry
// the importer, not an author, so they stay open to review.
func (t Translation) isOwnText(to ReviewState, by string) bool {
	if to != StateApproved && to != StateRejected {
		return false
	}
	return by != "" && by == t.By && t.Origin != OriginImport
}

func (t *Translation) apply(kind RevisionKind, c mfcontent.Content, state ReviewState, prov Provenance, sourceRev int, warnings []mf.Finding, now time.Time) Revision {
	t.Revision++
	t.Content, t.State, t.SourceRevision, t.Warnings, t.UpdatedAt = c, state, sourceRev, warnings, now
	if kind == KindContent {
		t.Origin, t.By = prov.Origin, prov.By
	}
	return Revision{
		TranslationID: t.ID, Number: t.Revision, Kind: kind, Content: c, State: state, Provenance: prov,
		SourceRevision: sourceRev, Findings: warnings, CreatedAt: now,
	}
}
