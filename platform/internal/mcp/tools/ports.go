package tools

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// ErrNotFound is what every port answers for something this tenant
// cannot see, whether it does not exist at all or belongs to another
// tenant. The two are deliberately the same answer (see the package
// doc): an adapter translates its context's own not-found into this and
// never distinguishes the cases.
var ErrNotFound = domain.ErrNotFound

// ── Catalog ─────────────────────────────────────────────────────────

// CatalogFilter narrows catalog_search. Every field is optional and an
// empty one is no filter.
type CatalogFilter struct {
	KeyPrefix  string
	Namespace  string
	State      string
	MissingIn  string
	OutdatedIn string
	// Unused selects the active messages no current build refers to
	// (RFC 0004 §2.2). It is its own mode: Context answers it, and only
	// KeyPrefix narrows it further.
	Unused bool
}

// MessageSummary is one message as a search returns it: enough to
// decide whether it is the message the agent meant, and the id and key
// to ask anything further.
//
// Omitted: arguments and markup (derived from Source, and message_get
// returns them), description, max_length, version and timestamps. A
// search result is a shortlist, not a record.
type MessageSummary struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Namespace string `json:"namespace,omitempty"`
	State     string `json:"state"`
	// Source is the current source text in its own syntax, and Revision
	// the source revision it is.
	Source   string `json:"source"`
	Syntax   string `json:"syntax"`
	Revision int    `json:"revision"`
}

// MessageDetail is one message in full, as message_get returns it.
//
// Omitted: the source revision history (a message can have hundreds),
// version and timestamps, and the message's translations — one locale
// at a time through translation_get, because a catalog with forty
// locales would otherwise answer a single call with forty texts.
type MessageDetail struct {
	MessageSummary
	Description string `json:"description,omitempty"`
	MaxLength   *int   `json:"max_length,omitempty"`
	// Arguments are the placeholders the source declares, by name.
	Arguments []string `json:"arguments,omitempty"`
}

// Catalog is Catalog's application service, as MCP reads it.
type Catalog interface {
	// SearchMessages lists a project's messages in key order. after is
	// the keyset of the previous page ("" for the first) and limit
	// bounds the rows; it returns the cursor for the next page, or "".
	SearchMessages(
		ctx context.Context, project uuid.UUID, f CatalogFilter, after string, limit int,
	) (msgs []MessageSummary, next string, err error)
	// Message returns one message by key, ErrNotFound when this tenant
	// has no such message in no such project.
	Message(ctx context.Context, project uuid.UUID, key string) (MessageDetail, error)
}

// ── Localization ────────────────────────────────────────────────────

// Translation is one locale's text for one message.
//
// Omitted: the revision log (its own resource), the translation's own
// id — a translation is addressed by message key and locale everywhere
// — and the structural QA warnings' detail, which findings_list reports
// properly; only their codes are kept, as a hint that there is
// something to look at.
type Translation struct {
	MessageID string `json:"message_id"`
	Key       string `json:"key"`
	Locale    string `json:"locale"`
	Text      string `json:"text"`
	Syntax    string `json:"syntax"`
	// State is the review state and Origin the provenance of the current
	// text ("human", "ai", "translation-memory", …), By who wrote it.
	State  string `json:"state"`
	Origin string `json:"origin"`
	By     string `json:"by,omitempty"`
	// SourceRevision is the source this text was made against;
	// CurrentSourceRevision the source that ships now. Outdated is the
	// two disagreeing.
	SourceRevision        int      `json:"source_revision"`
	CurrentSourceRevision int      `json:"current_source_revision"`
	Outdated              bool     `json:"outdated"`
	Revision              int      `json:"revision"`
	Warnings              []string `json:"warnings,omitempty"`
}

// Translations is Localization's application service, as MCP reads it.
type Translations interface {
	// Translation returns a message's text in one locale, ErrNotFound
	// when the project, the message or the translation is not this
	// tenant's to see.
	Translation(ctx context.Context, project uuid.UUID, key, locale string) (Translation, error)
}

// ── Context ─────────────────────────────────────────────────────────

// Usage is one place in the product where a message is asked for.
//
// Omitted: the build, application and commit ids, the usage's position
// in its document, and the source tool that reported it. An agent wants
// the file and the line; provenance of the upload is the API's.
type Usage struct {
	File      string `json:"file"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	Component string `json:"component,omitempty"`
	Route     string `json:"route,omitempty"`
	// Kind is the call shape the collector recognized (t, component,
	// element, accessor, template).
	Kind string `json:"kind,omitempty"`
	// Branch is the branch the build came from, and OnDefaultBranch says
	// whether that is the project's default branch.
	Branch          string `json:"branch,omitempty"`
	OnDefaultBranch bool   `json:"on_default_branch"`
}

// Usages is a message's usages, up to the limit asked for.
type Usages struct {
	MessageID string  `json:"message_id"`
	Key       string  `json:"key"`
	Branch    string  `json:"branch,omitempty"`
	Usages    []Usage `json:"usages"`
	// Truncated says more usages exist than were returned. Context's
	// query is a bounded read and issues no cursor, so this is how a
	// caller learns it has not seen everything.
	Truncated bool `json:"truncated"`
}

// Neighbour is a message shown together with another one.
type Neighbour struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// UsageReader is the Context context, as MCP reads it.
type UsageReader interface {
	// OfKey returns the current usages of the message a key names,
	// ErrNotFound when there is no such message.
	OfKey(ctx context.Context, project uuid.UUID, key, branch string, limit int) (Usages, error)
	// CaptureCount counts the current captures that show the message.
	CaptureCount(ctx context.Context, project uuid.UUID, key string, limit int) (n int, truncated bool, err error)
	// Neighbours lists messages shown together with this one.
	Neighbours(ctx context.Context, project uuid.UUID, message uuid.UUID, limit int) ([]Neighbour, error)
}

// ── Knowledge ───────────────────────────────────────────────────────

// TMMatch is one translation-memory match.
//
// Omitted: the unit's normalized forms, its data model, its hit count
// and timestamps, and the translation it was derived from. What decides
// whether to reuse a match is the score, the text and where it came
// from.
type TMMatch struct {
	Unit   string `json:"unit"`
	Score  int    `json:"score"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Target string `json:"target"`
	Syntax string `json:"syntax"`
	// MessageKey and Namespace are the message the unit was derived
	// from, which is what makes a 101 an in-context match.
	MessageKey string `json:"message_key,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	// Adapted says the target's variables were renamed to the query's.
	Adapted bool `json:"adapted"`
}

// TermUse is a term of a concept in the target locale.
type TermUse struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// Term is one termbase term recognized in a text.
//
// Omitted: the concept's version, its author and timestamps, its
// product reference and its terms in every other locale.
type Term struct {
	Concept string `json:"concept"`
	Text    string `json:"text"`
	Status  string `json:"status"`
	// Start and End delimit the match in the text sent, in bytes.
	Start int `json:"start"`
	End   int `json:"end"`
	// Definition is the concept's definition, Domain its subject area.
	Definition string `json:"definition,omitempty"`
	Domain     string `json:"domain,omitempty"`
	// Targets are the concept's terms in the target locale, allowed
	// ones first; empty when no target locale was asked for.
	Targets []TermUse `json:"targets,omitempty"`
}

// StyleRule is one explicit rule of the effective style guide.
type StyleRule struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Rationale string   `json:"rationale,omitempty"`
	Good      []string `json:"good,omitempty"`
	Bad       []string `json:"bad,omitempty"`
}

// StyleGuideRef names a guide that was merged into the effective style.
type StyleGuideRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	// Scope is how the guide was scoped: "tenant", "project", a locale,
	// or "<locale>/<namespace>".
	Scope string `json:"scope"`
}

// Style is the effective style for a project, locale and namespace.
//
// Omitted: disabled rules, which are an authoring concept — a rule a
// narrower guide switched off is simply not in Rules.
type Style struct {
	// Fields are the mechanical fields the guides set — formality, tone,
	// punctuation, numbers, dates — merged and spelled exactly as the
	// style guide schema spells them. They are passed through rather
	// than reshaped: an agent asked to follow a style guide should see
	// the guide's own words, and every unset leaf inherits, so what is
	// absent here is genuinely unconstrained.
	Fields json.RawMessage `json:"fields"`
	Rules  []StyleRule     `json:"rules"`
	// Sources are the guides merged, broadest first.
	Sources []StyleGuideRef `json:"sources"`
}

// Knowledge is the Knowledge context, as MCP reads it.
type Knowledge interface {
	// SearchTM looks text up in translation memory for a locale pair.
	SearchTM(ctx context.Context, q TMSearch) ([]TMMatch, error)
	// LookupTerms recognizes termbase terms in a text.
	LookupTerms(ctx context.Context, q TermSearch) ([]Term, error)
	// Style returns the effective style guide.
	Style(ctx context.Context, project uuid.UUID, locale, namespace string) (Style, error)
}

// TMSearch is a translation-memory lookup.
type TMSearch struct {
	Project      uuid.UUID
	Text         string
	Syntax       string
	SourceLocale string
	TargetLocale string
	MessageKey   string
	Namespace    string
	Limit        int
	MinScore     int
}

// TermSearch is a termbase recognition request.
type TermSearch struct {
	Project      uuid.UUID
	Text         string
	Locale       string
	TargetLocale string
}

// ── Quality ─────────────────────────────────────────────────────────

// CheckRun is the stored run a findings page came from.
type CheckRun struct {
	ID  string `json:"id"`
	Ref string `json:"ref"`
	// Trigger is what asked for the run (cli, pull_request, write,
	// capture, api).
	Trigger    string `json:"trigger"`
	Conclusion string `json:"conclusion,omitempty"`
	// PolicyVersion is the check policy the run graded itself against.
	PolicyVersion int `json:"policy_version"`
	// Layers are the layers the run computed, so a caller can tell
	// "clean" from "not looked at".
	Layers      []string `json:"layers,omitempty"`
	Errors      int      `json:"errors"`
	Warnings    int      `json:"warnings"`
	Waived      int      `json:"waived"`
	StartedAt   string   `json:"started_at"`
	CompletedAt string   `json:"completed_at,omitempty"`
}

// Finding is one stored finding.
//
// It is identified by its fingerprint, which is what a waiver names and
// what survives a re-run, and not by the row id: findings are immutable
// and a run's copy of a problem is not a thing to address.
//
// Omitted: the evidence object and the fix hint, which are free-form
// per code and can be large, the byte spans, the capture and region,
// and the source revision. An agent reads what is wrong and where; the
// Quality API serves the full record.
type Finding struct {
	Fingerprint string `json:"fingerprint"`
	Layer       string `json:"layer"`
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Key         string `json:"key,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	// Explanation is the prose for a person; Subject the thing the
	// finding is about (the argument, the term, the markup element).
	Explanation string `json:"explanation"`
	Subject     string `json:"subject,omitempty"`
	// Waiver is the waiver that accepted this finding, when it is
	// waived.
	Waiver string `json:"waiver,omitempty"`
}

// FindingsQuery narrows findings_list.
type FindingsQuery struct {
	Ref        string
	Layer      string
	Locale     string
	Severity   string
	MessageKey string
	WaivedOnly bool
	Limit      int
	After      string
}

// Quality is the Quality context, as MCP reads it.
type Quality interface {
	// Findings returns a page of the latest check run's findings,
	// ErrNotFound when the project has no stored run.
	Findings(
		ctx context.Context, project uuid.UUID, q FindingsQuery,
	) (run CheckRun, findings []Finding, next string, err error)
}

// ── Release and delivery ────────────────────────────────────────────

// Served is what an environment serves: the release, and everything
// locale resolution reads from its manifest.
type Served struct {
	ReleaseID    string
	Version      int
	Digest       string
	CreatedAt    string
	Environment  string
	SourceLocale string
	Locales      []string
	Fallback     map[string][]string
	// Artifacts maps locale → namespace → the artifact's SHA-256.
	Artifacts map[string]map[string]string
}

// Delivery is the Release context, as MCP reads it.
type Delivery interface {
	// Served returns what environment currently serves, or the release
	// named by release when it is not uuid.Nil. ErrNotFound covers an
	// unknown project, environment or release, and an environment that
	// has never published.
	Served(ctx context.Context, project uuid.UUID, environment string, release uuid.UUID) (Served, error)
	// ArtifactHasMessage reports whether one artifact of a release
	// carries a message key.
	ArtifactHasMessage(ctx context.Context, project uuid.UUID, release uuid.UUID, digest, key string) (bool, error)
}

// ── Writes ──────────────────────────────────────────────────────────
//
// The write ports are deliberately narrower than the contexts behind
// them (RFC 0005 §7.2, §7.4). What an agent may not ask for is not an
// argument it is refused, it is an argument that does not exist:
// MessageUpsert cannot obsolete or rename a message, LocaleWriter
// cannot remove a locale, and TranslationProposal carries no review
// state at all, because a proposal always enters review and a field
// that could say otherwise is the bug this shape makes impossible.

// MessageUpsert is source text to create or revise. A nil pointer
// leaves a field as it stands; a pointer to the zero value clears it.
type MessageUpsert struct {
	Key         string
	Namespace   *string
	Description *string
	MaxLength   *int
	Text        string
	// Syntax is the source's own syntax ("mf2", "icu"); "" is the
	// project's default.
	Syntax string
	// BaseRevision is the source revision the caller read before
	// revising, an optimistic lock. nil writes without one.
	BaseRevision *int
}

// MessageWritten is the message an upsert left behind.
type MessageWritten struct {
	MessageSummary
	// Status is what the write did: created, revised (a new source
	// revision), updated (details only) or unchanged.
	Status string `json:"status"`
}

// CatalogWriter is Catalog's application service, as MCP writes it.
type CatalogWriter interface {
	// UpsertMessage creates or revises one source message, ErrNotFound
	// when the project is not this tenant's to see.
	UpsertMessage(ctx context.Context, project uuid.UUID, in MessageUpsert) (MessageWritten, error)
}

// TranslationProposal is a translation an agent offers for a locale.
//
// It carries no review state. A proposal writes a revision that enters
// review whatever the project's routing policy says, for the same
// reason a CI token cannot approve one: review is a human decision and
// no token scope grants it (RFC 0005 §7.4).
type TranslationProposal struct {
	Key    string
	Locale string
	Text   string
	// Syntax is the text's own syntax; "" is the project's default.
	Syntax string
	// SourceRevision is the source revision the text was made against;
	// nil means the current one.
	SourceRevision *int
	// BaseRevision is the translation revision the agent read before
	// proposing, an optimistic lock. nil lets the adapter propose onto
	// the revision that stands now.
	BaseRevision *int
}

// ProposedTranslation is the revision a proposal wrote.
type ProposedTranslation struct {
	Translation
	// Status is what the write did: created, revised or unchanged.
	Status string `json:"status"`
}

// TranslationWriter is Localization's application service, as MCP
// proposes into it.
type TranslationWriter interface {
	// ProposeTranslation writes a revision that is always in review.
	ProposeTranslation(ctx context.Context, project uuid.UUID, in TranslationProposal) (ProposedTranslation, error)
}

// AddedLocale is a project's target locale after locale_add.
type AddedLocale struct {
	Locale    string `json:"locale"`
	Direction string `json:"direction,omitempty"`
	// Created says whether this call added it; false means it was
	// already there, which is not an error.
	Created bool `json:"created"`
}

// LocaleWriter adds a target locale. There is no remover: obsoleting is
// a state change and is available, destroying data is not
// (RFC 0005 §7.2).
type LocaleWriter interface {
	// AddLocale adds a target locale to a project.
	AddLocale(ctx context.Context, project uuid.UUID, code string) (AddedLocale, error)
}

// CheckRequest narrows a check run.
type CheckRequest struct {
	// Environment grades against the policy's block for it; "" is a
	// branch check, in no environment at all.
	Environment string
	// Layers are the deterministic layers to compute; empty runs every
	// one the policy leaves on.
	Layers []string
	// Limit bounds the findings returned.
	Limit int
}

// CheckReport is a check run's verdict and what it found.
//
// Omitted: the policy's decision per finding (`glossa check
// --explain-policy` prints those and the Quality API serves them) and
// the per-locale coverage table, which translation-stats already
// answers. A run through MCP is a verdict and a work list.
type CheckReport struct {
	// Conclusion is the policy's verdict (success, failure, neutral).
	Conclusion string `json:"conclusion"`
	// PolicyVersion is the policy document the run graded itself
	// against; 0 for a project that has never saved one.
	PolicyVersion int `json:"policy_version"`
	// Layers are the layers that ran, so a caller can tell "clean" from
	// "not looked at"; Skipped are the ones the policy switched off.
	Layers  []string `json:"layers,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	// Messages is the project's active messages and Invalid how many of
	// them do not parse.
	Messages int       `json:"messages"`
	Invalid  int       `json:"invalid_messages"`
	Errors   int       `json:"errors"`
	Warnings int       `json:"warnings"`
	Findings []Finding `json:"findings"`
	// Truncated says the run found more findings than were returned.
	// The counts above are the run's, not the page's.
	Truncated bool `json:"truncated"`
}

// Checks runs the deterministic layers over the project as it stands.
// Nothing is stored: the report answers a question, and recording a run
// is a write that reading the catalog does not carry.
type Checks interface {
	// Run computes the report, ErrNotFound when the project is not this
	// tenant's to see.
	Run(ctx context.Context, project uuid.UUID, in CheckRequest) (CheckReport, error)
}

// TranslateRequest asks for an M2 fill job.
type TranslateRequest struct {
	Locales   []string
	Keys      []string
	Namespace string
	KeyPrefix string
	// Select chooses messages by the state of their translation:
	// missing, outdated or missing_or_outdated. "" is the context's own
	// default.
	Select string
}

// TranslateJob is the fill a translate call started.
//
// No provider, model or key appears here, in either direction: the job
// runs server-side under the tenant's own provider configuration and
// budget, and the MCP client never sees a key (RFC 0005 §7.4).
type TranslateJob struct {
	// Fill is the fill's id, which its jobs are followed by.
	Fill    string   `json:"fill"`
	Locales []string `json:"locales"`
	Select  string   `json:"select"`
	// JobsCreated and JobsExisting count the jobs queued and the ones
	// that already existed and were reused.
	JobsCreated  int `json:"jobs_created"`
	JobsExisting int `json:"jobs_existing"`
	// Skipped counts the messages left out, by reason — a sensitive
	// namespace among them, which is never machine-translated
	// (RFC 0005 §7.4).
	Skipped map[string]int `json:"skipped,omitempty"`
	// Warnings say why jobs will fail or do little: no provider, no
	// budget, provider consent off.
	Warnings []string `json:"warnings,omitempty"`
	// Jobs are the queued jobs' ids, up to the context's own cap.
	Jobs []string `json:"jobs,omitempty"`
}

// Translator is Intelligence's application service, as MCP starts a
// fill through it.
type Translator interface {
	// Translate queues an M2 fill and returns it.
	Translate(ctx context.Context, project uuid.UUID, in TranslateRequest) (TranslateJob, error)
}

// ── Releases ────────────────────────────────────────────────────────
//
// The release port is narrower than the Release context behind it, the
// same way the write ports are. There is no environment creation, no
// policy edit, no delivery-key management and nothing that removes a
// release: M4 exposes the three operations RFC 0005 §7.3 names and
// destroys nothing (§7.2).
//
// There is also no `force`. The Release context gates a publish on the
// environment's completeness requirement and refuses with
// `policy_not_met`; overriding that gate takes an audited reason and is
// a person's decision, like a review. An agent that meets the gate
// publishes and an agent that does not is told why, which is the whole
// value of the gate.

// Release is one immutable release, as MCP reports it.
//
// Omitted: the manifest, its signatures and its artifact digests
// (explain_delivery answers what a release actually serves), the
// release's parent and its full statistics. A release tool answers
// "which release does this environment serve now, and is it the one I
// meant".
type Release struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	// Environment is where the release was *built*, whose policy it
	// records; a promoted release keeps its own.
	Environment string `json:"environment"`
	// Digest is the manifest digest: two releases with equal digests
	// serve exactly the same text.
	Digest string `json:"digest"`
	// Policy is the review states the release ships.
	Policy []string `json:"policy,omitempty"`
	// Branch is the branch whose overlay the release was built with; ""
	// for the main catalog's.
	Branch    string   `json:"branch,omitempty"`
	Locales   []string `json:"locales,omitempty"`
	Messages  int      `json:"messages"`
	Note      string   `json:"note,omitempty"`
	Author    string   `json:"author,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// PublishRequest is what to publish where.
type PublishRequest struct {
	Environment string
	Note        string
	// IdempotencyKey lets a client that retried a timed-out call get the
	// first request's release back instead of publishing a second one.
	// An agent retries more readily than a person does.
	IdempotencyKey string
}

// Held is a publish or a promote into an environment that requires
// release approvals (RFC 0006 §5.1): it became a release request and no
// pointer moved. The release is deployed only once enough people other
// than the requester approve it — people, never an agent: there is no
// approve tool.
type Held struct {
	RequestID   string `json:"release_request_id"`
	Environment string `json:"environment"`
	// Approvals is how many distinct people must approve.
	Approvals int `json:"approvals_required"`
}

// Published is a publish's result.
type Published struct {
	Release Release `json:"release"`
	// Replayed says this call returned an earlier request's release,
	// because it carried the same idempotency key.
	Replayed bool `json:"replayed"`
	// Held is set when the release was recorded but not deployed: it
	// waits for approval, and the environment still serves what it did.
	Held *Held `json:"held,omitempty"`
}

// Deployed is an environment after a pointer moved — or, when Held is
// set, after a promote that moved nothing and waits for approval.
type Deployed struct {
	Environment string  `json:"environment"`
	Release     Release `json:"release"`
	// Moved is false when the environment already served that release,
	// which is not an error, and whenever the promote was held.
	Moved bool `json:"moved"`
	// Held is set when the promote became a release request.
	Held *Held `json:"held,omitempty"`
}

// Releases is Release's application service, as MCP publishes through
// it. Each method is the same use case the REST endpoint calls, so the
// environment's policy, the publish gate, the promotability rules and
// the audit trail are the context's and are never restated here.
type Releases interface {
	// Publish builds a release under the environment's policy and points
	// the environment at it.
	Publish(ctx context.Context, project uuid.UUID, in PublishRequest) (Published, error)
	// Promote points an environment at an existing release of the
	// project. Nothing is rebuilt.
	Promote(ctx context.Context, project uuid.UUID, environment string, release uuid.UUID) (Deployed, error)
	// Rollback points an environment back at a release it served before;
	// uuid.Nil takes the newest one older than the release it serves now.
	Rollback(ctx context.Context, project uuid.UUID, environment string, release uuid.UUID) (Deployed, error)
}

// Sources are the application ports the tools call. A nil port leaves
// its tools unregistered, so a deployment that does not run a context
// does not advertise tools that cannot work.
type Sources struct {
	Catalog      Catalog
	Translations Translations
	Usages       UsageReader
	Knowledge    Knowledge
	Quality      Quality
	Delivery     Delivery
	Checks       Checks
	Messages     CatalogWriter
	Proposals    TranslationWriter
	Locales      LocaleWriter
	Translator   Translator
	Releases     Releases
	// Workflow and ReleaseReads are the M5 read ports (RFC 0006 §8).
	Workflow     Workflow
	ReleaseReads ReleaseReads
}
