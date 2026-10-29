package domain

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"
)

// SuggestionStatus is where a stored suggestion is.
type SuggestionStatus string

// Suggestion statuses.
const (
	// StatusPending: in the review queue.
	StatusPending SuggestionStatus = "pending"
	// StatusAccepted: a person accepted it, as is or edited.
	StatusAccepted SuggestionStatus = "accepted"
	StatusRejected SuggestionStatus = "rejected"
	// StatusAutoApplied: the routing policy approved it without a person.
	StatusAutoApplied SuggestionStatus = "auto_applied"
	// StatusSuperseded: a newer suggestion for the message and locale
	// replaced it before anyone decided.
	StatusSuperseded SuggestionStatus = "superseded"
)

// ParseSuggestionStatus validates s.
func ParseSuggestionStatus(s string) (SuggestionStatus, error) {
	switch st := SuggestionStatus(s); st {
	case StatusPending, StatusAccepted, StatusRejected, StatusAutoApplied, StatusSuperseded:
		return st, nil
	}
	return "", errors.New("intelligence: invalid suggestion status " + s)
}

// Suggestion errors.
var (
	ErrSuggestionDecided  = errors.New("intelligence: the suggestion was already decided")
	ErrSuggestionOutdated = errors.New("intelligence: the message's source changed since the suggestion was made")
)

// SuggestionRecord is a stored suggestion: the job's result with where
// it is in review.
type SuggestionRecord struct {
	ID             uuid.UUID
	JobID          uuid.UUID
	ProjectID      uuid.UUID
	MessageID      uuid.UUID
	MessageKey     string
	Namespace      string
	Locale         string
	SourceRevision int
	Suggestion
	// ActionNote explains a routed action that differs from what the
	// policy's bands alone say (auto_approve the environments don't
	// allow).
	ActionNote string
	// RiskTags are the message's policy tags that add risk, for the
	// queue's order.
	RiskTags            []string
	Status              SuggestionStatus
	TranslationRevision *int
	DecidedBy           string
	DecidedAt           *time.Time
	Decision            *Decision
	Version             int
	CreatedAt           time.Time
}

// Decision is what a person decided on a suggestion.
type Decision struct {
	// Edit is set when the suggestion was edited before accepting.
	Edit *EditDiff `json:"edit,omitempty"`
	// Reason is a rejection's reason.
	Reason string `json:"reason,omitempty"`
}

// OriginDetail is the translation revision's origin_detail when the
// suggestion becomes one (RFC 0003 §3.4): provider, model, prompt
// version, TM unit IDs, term IDs, style-guide version, score and
// explanation, and where it came from.
// InContext is where a person was when they accepted a suggestion in
// the running product (RFC 0004 §5.3): the route pattern the page was
// on and the viewport it was seen at. It is recorded beside what the
// suggestion contributed, so history can say an edit was made looking
// at the real screen and which screen that was.
type InContext struct {
	Route    string    `json:"route"`
	Viewport *Viewport `json:"viewport,omitempty"`
}

// Viewport is the size the page was seen at.
type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r SuggestionRecord) OriginDetail(edited bool, in *InContext) json.RawMessage {
	d := map[string]any{
		"suggestion_id": r.ID.String(), "job_id": r.JobID.String(),
		"score": r.Confidence.Score, "explanation": r.Confidence.Explanation, "action": r.Action,
		"repairs": r.Provenance.Repairs,
	}
	put := func(k string, v string) {
		if v != "" {
			d[k] = v
		}
	}
	put("provider", r.Provenance.Provider)
	put("model", r.Provenance.Model)
	put("prompt_version", r.Provenance.PromptVersion)
	put("style_version", r.Provenance.StyleVersion)
	if len(r.Provenance.TMUnitIDs) > 0 {
		d["tm_unit_ids"] = r.Provenance.TMUnitIDs
	}
	if len(r.Provenance.TermIDs) > 0 {
		d["term_ids"] = r.Provenance.TermIDs
	}
	if edited {
		d["edited"] = true
	}
	if in != nil && in.Route != "" {
		d["in_context"] = in
	}
	raw, _ := json.Marshal(d) //nolint:errchkjson // plain maps, strings and numbers
	return raw
}

// EditDiff is a structured diff of a person's edit to AI output (RFC
// 0003 §3.3): the character edit distance and which terms and style
// fields changed. It feeds the acceptance and edit-distance metrics.
type EditDiff struct {
	// Distance is the Levenshtein distance between the visible texts,
	// in characters; Ratio divides it by the longer text's length.
	Distance int     `json:"distance"`
	Ratio    float64 `json:"ratio"`
	// TermsAdded and TermsRemoved are target-locale terms the edit
	// introduced or dropped.
	TermsAdded   []string `json:"terms_added,omitempty"`
	TermsRemoved []string `json:"terms_removed,omitempty"`
	// StyleFields names style-guide fields whose use the edit changed:
	// quotes, dash, ellipsis, space_before_punctuation, pronoun.
	StyleFields []string `json:"style_fields,omitempty"`
}

// Diff compares a suggestion's text with a person's edit. termsBefore
// and termsAfter are the target terms recognized in each; pronoun is the
// style guide's form of address, if any.
func Diff(before, after mf.Message, termsBefore, termsAfter []string, pronoun string) EditDiff {
	a, b := PlainText(before), PlainText(after)
	d := EditDiff{Distance: Levenshtein(a, b)}
	if n := max(len([]rune(a)), len([]rune(b))); n > 0 {
		d.Ratio = float64(int(float64(d.Distance)/float64(n)*1000+0.5)) / 1000
	}
	for _, t := range termsAfter {
		if !slices.Contains(termsBefore, t) && !slices.Contains(d.TermsAdded, t) {
			d.TermsAdded = append(d.TermsAdded, t)
		}
	}
	for _, t := range termsBefore {
		if !slices.Contains(termsAfter, t) && !slices.Contains(d.TermsRemoved, t) {
			d.TermsRemoved = append(d.TermsRemoved, t)
		}
	}
	fa, fb := styleFeatures(a, pronoun), styleFeatures(b, pronoun)
	for _, f := range []string{"quotes", "dash", "ellipsis", "space_before_punctuation", "pronoun"} {
		if fa[f] != fb[f] {
			d.StyleFields = append(d.StyleFields, f)
		}
	}
	return d
}

// styleFeatures reads, from text, how it uses what a style guide
// governs.
func styleFeatures(text, pronoun string) map[string]string {
	pick := func(set string) string {
		var out []rune
		for _, r := range text {
			if strings.ContainsRune(set, r) && !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
		slices.Sort(out)
		return string(out)
	}
	f := map[string]string{
		"quotes":   pick("\"'“”„‚‘’«»‹›「」『』"),
		"dash":     pick("‐‑‒–—―"),
		"ellipsis": pick("…"),
	}
	if strings.Contains(text, "...") {
		f["ellipsis"] += "..."
	}
	prev := ' '
	for i, r := range text {
		if i > 0 && strings.ContainsRune("!?:;", r) && unicode.IsSpace(prev) {
			f["space_before_punctuation"] = "yes"
		}
		prev = r
	}
	if pronoun != "" {
		for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !isWordRune(r) }) {
			if w == pronoun {
				f["pronoun"] = "yes"
			}
		}
	}
	return f
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) }

// Levenshtein is the edit distance between a and b in runes.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// RiskTagsOf lists what makes a suggestion risky beyond its score — the
// namespace's legal and marketing tags and the factors that force
// review — for the review queue's order (score first, then the number
// of risk tags).
func RiskTagsOf(tags []string, s Suggestion) []string {
	var out []string
	for _, t := range []string{TagLegal, TagMarketing} {
		if slices.Contains(tags, t) {
			out = append(out, t)
		}
	}
	for _, f := range s.Confidence.Explanation {
		if f.Contribution < 0 && slices.Contains([]string{FactorTermForbidden, FactorMaxLength, FactorMissingPluralCategories}, f.Factor) &&
			!slices.Contains(out, f.Factor) {
			out = append(out, f.Factor)
		}
	}
	return out
}
