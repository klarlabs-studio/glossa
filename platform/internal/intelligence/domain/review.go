package domain

import (
	"errors"
	"fmt"
	"slices"
)

// The linguistic review (RFC 0005 §3.8), as the Intelligence context
// models it: one translation, one model call, a structurally
// constrained answer.
//
// It reuses everything the translation agent already has — the provider
// port, the routing policy, the per-tenant budget, the sending consent
// and the sensitive-namespace rule — and adds no second anything. What
// is different is the shape of the answer and what happens to a bad
// one: a review is one call with no repair turn, because a model that
// answers with prose instead of the structure has said nothing about
// the translation, and coaxing it would only buy an opinion the second
// attempt invented.

// ErrMalformedReview means the model's review answer was not the
// constrained structure. It wraps ErrInvalidOutput, the same terminal
// failure a draft that never became valid MF2 ends in: nothing is
// emitted, and the job fails rather than reporting half an answer.
var ErrMalformedReview = fmt.Errorf("%w: the review answer was not the requested structure", ErrInvalidOutput)

// ReviewRequest is one translation to review linguistically.
type ReviewRequest struct {
	Scope     Scope  `json:"scope"`
	MessageID string `json:"message_id"`
	Key       string `json:"key"`
	Namespace string `json:"namespace,omitempty"`
	// Revision is the translation revision under review, for the locus.
	Revision string `json:"revision,omitempty"`
	// SourceRevision is the source revision the translation was made
	// against; a waiver dies when it changes.
	SourceRevision *int   `json:"source_revision,omitempty"`
	SourceLocale   string `json:"source_locale"`
	TargetLocale   string `json:"target_locale"`
	// Source and Translation are MF2 syntax. The reviewer shows the
	// model the literal text of each and the placeholders beside it:
	// meaning, tone, grammar and phrasing are judged on the words, and
	// the structure has already been judged by the parity layer.
	Source      string `json:"source"`
	Translation string `json:"translation"`
	// Description is the message's description, where it has one.
	Description string `json:"description,omitempty"`
	// Terms are the termbase concepts recognized in the source, as
	// evidence for the reviewer; the terminology layer is what actually
	// grades them.
	Terms []TermHit `json:"terms,omitempty"`
	// Style is the effective style guide, prose rules included. §3.2
	// keeps the prose rules out of the mechanical style layer precisely
	// so they can be evidence here.
	Style *StyleGuide `json:"style,omitempty"`
	// Tags are the namespace's policy tags (sensitive, legal, marketing).
	Tags []string `json:"tags,omitempty"`
	// ProviderConsent is the tenant's explicit permission to send text
	// to AI providers (RFC 0003 §7). Off by default.
	ProviderConsent bool `json:"provider_consent"`
}

// Validate checks the request is complete.
func (r ReviewRequest) Validate() error {
	switch {
	case r.Scope.TenantID == "":
		return errors.New("review request: tenant is required")
	case r.MessageID == "":
		return errors.New("review request: message id is required")
	case r.SourceLocale == "" || r.TargetLocale == "":
		return errors.New("review request: source and target locale are required")
	case r.Source == "":
		return errors.New("review request: source is required")
	case r.Translation == "":
		return errors.New("review request: translation is required")
	}
	return nil
}

// Sensitive reports whether the message must never reach a provider.
func (r ReviewRequest) Sensitive() bool { return slices.Contains(r.Tags, TagSensitive) }

// ReviewSide says which text a note points into.
type ReviewSide string

// Review sides.
const (
	ReviewSideSource ReviewSide = "source"
	ReviewSideTarget ReviewSide = "target"
)

// Valid reports whether s is a side.
func (s ReviewSide) Valid() bool { return s == ReviewSideSource || s == ReviewSideTarget }

// ReviewNote is one accepted note of a review: what the model said, and
// where in the text it said it. Start and End are byte offsets into the
// literal text of that side — the same text terminology QA measures its
// spans in — resolved from the quote the model returned, never from
// offsets the model counted itself.
type ReviewNote struct {
	Code        string     `json:"code"`
	Side        ReviewSide `json:"side"`
	Quote       string     `json:"quote"`
	Start       int        `json:"start"`
	End         int        `json:"end"`
	Explanation string     `json:"explanation"`
	Suggestion  string     `json:"suggestion,omitempty"`
}
