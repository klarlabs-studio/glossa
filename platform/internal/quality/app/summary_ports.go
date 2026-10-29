package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The quality summary's sources (RFC 0005 §8).
//
// Five of the seven numbers belong to other contexts, and Quality reads
// every one of them through an application port of the context that
// owns it — never out of its tables (RFC 0002 §4). Each port is a
// question in Quality's own words; the adapter answers it with an
// ordinary authorized use case of the owning service, so the summary
// shows exactly what its caller could have read operation by operation
// through the API, and nothing more.
//
// Every port is optional. A deployment that does not wire one leaves
// its number **not measured**, which the summary says out loud instead
// of reporting a zero nobody computed.

// Translations is Localization's port: how much of the catalog is
// translated, and the source-to-translation half of the lead time.
type Translations interface {
	// TranslationCoverage is per-locale coverage of the project's active
	// messages, the source locale included.
	TranslationCoverage(ctx context.Context, project uuid.UUID) ([]LocaleCoverage, error)
	// LeadTimeSamples are recent (source changed, translation caught up)
	// pairs, bounded by q.Limit.
	LeadTimeSamples(ctx context.Context, project uuid.UUID, q LeadTimeQuery) ([]LeadTimeSample, error)
}

// LocaleCoverage is one locale's share of the catalog.
type LocaleCoverage struct {
	Locale string
	// Direction is the locale's text direction ("ltr" or "rtl"), so a
	// dashboard row can be laid out without parsing the tag.
	Direction string
	IsSource  bool
	// Messages is the project's active messages — the same number for
	// every locale, and the denominator of the other three.
	Messages   int
	Translated int
	Missing    int
	Outdated   int
}

// LeadTimeQuery bounds the lead-time sample.
type LeadTimeQuery struct {
	// Since is the oldest source change to consider.
	Since time.Time
	// States are the review states the target environment ships, so a
	// draft nobody will publish is not counted as work that landed.
	States []string
	// Locales narrows the sample; empty is every locale.
	Locales []string
	// Limit bounds the rows, newest source change first.
	Limit int
}

// LeadTimeSample is one message's leg from a source change to the
// translation that caught up with it, in one locale.
type LeadTimeSample struct {
	Locale          string
	SourceChangedAt time.Time
	TranslatedAt    time.Time
}

// Suggestions is Intelligence's port: what people did with the
// machine's suggestions, and what is still waiting for them.
type Suggestions interface {
	// Acceptance is the acceptance rate and mean edit distance per
	// locale, for decisions taken since a time.
	Acceptance(ctx context.Context, project uuid.UUID, since time.Time) ([]LocaleAcceptance, error)
	// QueueAges is the review queue's depth and waiting age per locale,
	// as of now.
	QueueAges(ctx context.Context, project uuid.UUID, locales []string, now time.Time) ([]LocaleQueue, error)
}

// LocaleAcceptance is one locale's decisions on suggestions.
type LocaleAcceptance struct {
	Locale   string
	Accepted int
	Edited   int
	Rejected int
	// AcceptanceRate is accepted / (accepted + rejected).
	AcceptanceRate float64
	// MeanEditDistance counts a suggestion accepted as it is as 0.
	MeanEditDistance float64
}

// LocaleQueue is one locale's review queue.
type LocaleQueue struct {
	Locale string
	// Waiting is the depth: how many items are pending.
	Waiting int
	// Age is how long they have been waiting. A queue of depth 0 is a
	// measured 0 with no age, which is the truthful pair.
	Age domain.Spread
}

// Usage is Context's port: the share of the catalog the product's own
// code and screenshots account for.
type Usage interface {
	// UsageCoverage is the active messages with a usage in a current
	// build of the default branch.
	UsageCoverage(ctx context.Context, project uuid.UUID) (domain.Share, error)
	// RegionCoverage is the active messages with a visible region on a
	// capture of a current build.
	RegionCoverage(ctx context.Context, project uuid.UUID) (domain.Share, error)
}

// Deployments is Release's port: what an environment ships and when it
// last shipped it.
type Deployments interface {
	// Timeline is the environment's eligibility states and the times
	// content became live in it since a time, oldest first.
	Timeline(ctx context.Context, project uuid.UUID, environment string, since time.Time) (Timeline, error)
}

// Timeline is one environment's publishing history in a window.
type Timeline struct {
	// States are the review states the environment's policy ships: what
	// counts as a translation that will actually go out.
	States []string
	// PublishedAt is every deployment to it in the window, oldest first.
	// Empty means nothing was published, which makes the lead time not
	// measured rather than zero.
	PublishedAt []time.Time
}

// Checks is Integration's port: how the pull-request check is doing.
type Checks interface {
	// CheckHealth is the project's concluded pull-request checks since a
	// time, with how long they took.
	CheckHealth(ctx context.Context, project uuid.UUID, since time.Time) (CheckHealth, error)
}

// CheckHealth is the pull-request check's record over a window.
type CheckHealth struct {
	// Concluded is every check that reached a verdict; Succeeded, Failed
	// and Neutral partition it.
	Concluded int
	Succeeded int
	Failed    int
	Neutral   int
	// Latency is from the pull-request event to the conclusion.
	Latency domain.Spread
}

// PassRate is successes over the checks that passed or failed. Neutral
// is left out of both halves: it is what a check concludes when it had
// nothing to grade, and counting it either way would move the rate for
// a reason nobody chose.
func (h CheckHealth) PassRate() (rate float64, measured bool) {
	graded := h.Succeeded + h.Failed
	if graded == 0 {
		return 0, false
	}
	return float64(h.Succeeded) / float64(graded), true
}

// SummarySources are the other contexts' ports the quality summary
// reads. Any of them may be nil, and that number is then not measured.
type SummarySources struct {
	Coverage    Translations
	Suggestions Suggestions
	Usage       Usage
	Deployments Deployments
	Checks      Checks
}
