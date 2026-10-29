// Package domain is Workflow's model (RFC 0006 §2): the
// glossa.workflow/v1 document, the closed vocabulary its charts are
// written in, compiling a definition through statekit.FromJSON with
// lint-on-save, immutable versions, and bindings.
//
// The package is pure. Guards are functions of a Step — the event that
// arrived and a read-only snapshot of its subject — and actions record
// Effects on it. Carrying an effect out (assigning, asking for an
// approval, setting a review state) is the instance runner's, through
// the owning context's ports and as the actor whose event caused the
// transition (§2.5); nothing here can approve, write or call anything.
package domain

// SubjectKind is what a workflow instance is about (RFC 0006 §2.5).
type SubjectKind string

// Subjects in M5.
const (
	// SubjectTranslation is a translation unit: one message in one
	// locale.
	SubjectTranslation SubjectKind = "translation"
	// SubjectReleaseRequest is a request to publish into an environment
	// whose policy asks for approval (§5.1).
	SubjectReleaseRequest SubjectKind = "release_request"
)

// SubjectKinds lists every subject kind.
var SubjectKinds = []SubjectKind{SubjectTranslation, SubjectReleaseRequest}

// Valid reports whether k is a subject kind.
func (k SubjectKind) Valid() bool { return k == SubjectTranslation || k == SubjectReleaseRequest }

// Subject is the read-only snapshot a transition's guards read, loaded
// once per transition by the runner (§2.4). It holds facts about the
// subject, never who the tenant is.
type Subject struct {
	Kind SubjectKind
	// Locale and Namespace locate a translation unit. Locale is a
	// canonical BCP 47 tag.
	Locale    string
	Namespace string
	// ReviewState is one of Localization's four.
	ReviewState string
	// Origin is the current revision's provenance (human, ai, …).
	Origin string
	// Author is the actor who wrote the current revision.
	Author string
	// SourceRevision is the source message revision the translation
	// corresponds to.
	SourceRevision int
	// Band is the routing band of the latest suggestion, as
	// Intelligence's routing policy names it ("" when there is none).
	Band string
	// Findings are the open findings, counted by layer and severity.
	Findings []FindingCount
	// Approvers are the actors who have granted an approval so far, one
	// entry per approval.
	Approvers []string
	// TMMatch is the best translation-memory match score, in [0, 1].
	TMMatch float64
	// Required and RequiredFrom are the subject's own approval
	// requirement: for a release request, its environment's `approval`
	// (RFC 0006 §5.1) — how many people, and of which party. Zero for a
	// translation unit. For a release request Author is the requester.
	Required     int
	RequiredFrom Party
}

// FindingCount is how many open findings a subject has in one layer at
// one severity.
type FindingCount struct {
	Layer    string
	Severity string
	Count    int
}

// Trigger is the event that is moving an instance, and who raised it.
type Trigger struct {
	Event EventName
	// Actor is who caused the event; actions run as them (§2.5).
	Actor string
	// Permissions are the permissions the actor holds for this subject
	// (for a translation, in its locale).
	Permissions []string
}

// Effect is one action a transition asks for: which local name in the
// definition, which primitive, and its validated parameters. The runner
// executes it through the owning context's port.
type Effect struct {
	Name   string
	Use    string
	Params any
}

// Step is the statekit context a definition's machine runs over: the
// trigger and the subject going in, the effects coming out.
type Step struct {
	Subject Subject
	Trigger Trigger
	Effects []Effect
}
