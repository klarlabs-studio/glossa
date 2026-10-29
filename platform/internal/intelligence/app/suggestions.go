package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// MessageSource is a suggestion's message as it is now: its key,
// namespace and state, and its current source as authored and in
// canonical MF2 — what a reviewer compares the suggestion with.
type MessageSource struct {
	Key       string
	Namespace string
	Active    bool
	// Revision is the current source revision; a suggestion made against
	// an older one is outdated.
	Revision int
	Text     string
	Syntax   string
	Model    mf.Message
	// MF2 is Model's canonical MF2 syntax.
	MF2 string
}

// SuggestionSources returns, by message ID, the current source of the
// suggestions' messages: one Catalog read per project among them (a
// page of one project's suggestions is one read). Messages that no
// longer exist are left out. Needs catalog.read, like any read of the
// catalog.
func (s *Service) SuggestionSources(ctx context.Context, rs []domain.SuggestionRecord) (map[uuid.UUID]MessageSource, error) {
	byProject := map[uuid.UUID][]uuid.UUID{}
	var projects []uuid.UUID
	for _, r := range rs {
		if _, seen := byProject[r.ProjectID]; !seen {
			projects = append(projects, r.ProjectID)
		}
		if !slices.Contains(byProject[r.ProjectID], r.MessageID) {
			byProject[r.ProjectID] = append(byProject[r.ProjectID], r.MessageID)
		}
	}
	out := map[uuid.UUID]MessageSource{}
	for _, p := range projects {
		// A project outside the caller's scope shows no source, like a
		// project that is gone (RFC 0006 §4.1).
		if err := authz.InProject(ctx, p); errors.Is(err, authz.ErrNotVisible) {
			continue
		} else if err != nil {
			return nil, err
		}
		msgs, err := s.Catalog.MessagesByIDs(ctx, p, byProject[p])
		if errors.Is(err, ErrProjectNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			text, err := mf.Stringify(m.Source)
			if err != nil {
				return nil, fmt.Errorf("intelligence: source of %s: %w", m.Key, err)
			}
			out[m.ID] = MessageSource{
				Key: m.Key, Namespace: m.Namespace, Active: m.Active, Revision: m.Revision,
				Text: m.SourceText, Syntax: m.SourceSyntax, Model: m.Source, MF2: text,
			}
		}
	}
	return out, nil
}

// ListSuggestions lists suggestions, newest first. Needs
// intelligence.read.
func (s *Service) ListSuggestions(ctx context.Context, f SuggestionFilter, page pagination.Page) ([]domain.SuggestionRecord, *string, error) {
	scope, err := authz.Projects(ctx, authz.IntelligenceRead)
	if err != nil {
		return nil, nil, err
	}
	f.Projects = scope.IDs()
	if f.Locale != "" {
		t, err := bcp47.Parse(f.Locale)
		if err != nil {
			return nil, nil, err
		}
		f.Locale = t.String()
	}
	before, err := parseCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.SuggestionRecord
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.Suggestions(ctx, f, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r domain.SuggestionRecord) string {
		return Cursor{At: r.CreatedAt, ID: r.ID}.String()
	})
	return items, next, nil
}

// GetSuggestion returns one suggestion. Needs intelligence.read.
func (s *Service) GetSuggestion(ctx context.Context, id uuid.UUID) (domain.SuggestionRecord, error) {
	var r domain.SuggestionRecord
	if err := authz.RequireRow(ctx, authz.IntelligenceRead, func() (uuid.UUID, error) {
		err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
			var err error
			r, err = st.Suggestion(ctx, id, false)
			return err
		})
		return r.ProjectID, err
	}); err != nil {
		return domain.SuggestionRecord{}, err
	}
	return r, nil
}

// ReviewQueue lists a project's pending suggestions ordered by risk
// (RFC 0003 §3.3): lowest confidence first, then the most risk tags
// (legal, marketing, forbidden terms, max length, missing plural
// categories) — not by key. Needs intelligence.read.
func (s *Service) ReviewQueue(ctx context.Context, project uuid.UUID, locales []string, page pagination.Page) ([]domain.SuggestionRecord, *string, error) {
	if err := authz.RequireIn(ctx, authz.IntelligenceRead, project); err != nil {
		return nil, nil, err
	}
	if _, err := s.Catalog.Project(ctx, project); err != nil {
		return nil, nil, err
	}
	canonical, err := canonicalLocales(locales)
	if err != nil {
		return nil, nil, err
	}
	after, err := parseQueueCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.SuggestionRecord
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.ReviewQueue(ctx, project, canonical, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r domain.SuggestionRecord) string {
		return QueueCursor{Score: r.Confidence.Score, Risk: len(r.RiskTags), ID: r.ID}.String()
	})
	return items, next, nil
}

// String encodes a queue cursor.
func (c QueueCursor) String() string {
	return strconv.FormatFloat(c.Score, 'g', -1, 64) + "|" + strconv.Itoa(c.Risk) + "|" + c.ID.String()
}

func parseQueueCursor(s string) (*QueueCursor, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, "|")
	if len(parts) != 3 {
		return nil, invalid("invalid_page_token", "page_token is not one this list issued")
	}
	score, err1 := strconv.ParseFloat(parts[0], 64)
	risk, err2 := strconv.Atoi(parts[1])
	id, err3 := uuid.Parse(parts[2])
	if errors.Join(err1, err2, err3) != nil {
		return nil, invalid("invalid_page_token", "page_token is not one this list issued")
	}
	return &QueueCursor{Score: score, Risk: risk, ID: id}, nil
}

// AcceptInput accepts a suggestion as is, or edited (Text set).
type AcceptInput struct {
	// Text is the edited translation; empty accepts the suggestion.
	Text string
	// Syntax is Text's syntax: mf2 (default) or mf1.
	Syntax string
	// IfMatch is the suggestion's version, when sent.
	IfMatch *int
	// InContext is set when the person accepted while looking at the
	// running product: the route and viewport go into the revision's
	// origin_detail (RFC 0004 §5.3). Only the in-product editor sends
	// it; auto-apply never does.
	InContext *domain.InContext
}

// AcceptSuggestion makes a pending suggestion (or the caller's edit of
// it) the message's translation: a revision with provenance ai or
// translation_memory and origin_detail naming provider, model, prompt
// version, TM units, terms, style version, score and explanation. The
// state is approved when the caller may review the locale, else what
// the project's review policy says. An edit records a structured diff
// (edit distance, terms and style fields changed) for the metrics.
// Needs intelligence.translate (and, through Localization,
// translations.write) for the locale.
func (s *Service) AcceptSuggestion(ctx context.Context, id uuid.UUID, in AcceptInput) (domain.SuggestionRecord, error) {
	r, err := s.pending(ctx, id, in.IfMatch)
	if err != nil {
		return domain.SuggestionRecord{}, err
	}
	by, err := actorFor(ctx, authz.IntelligenceTranslate, r.Locale)
	if err != nil {
		return domain.SuggestionRecord{}, err
	}
	msg, err := s.Catalog.MessageByID(ctx, r.ProjectID, r.MessageID)
	if err != nil {
		return domain.SuggestionRecord{}, err
	}
	if msg.Revision != r.SourceRevision {
		return domain.SuggestionRecord{}, fmt.Errorf("%w: it was made for source revision %d, the message is at %d", domain.ErrSuggestionOutdated, r.SourceRevision, msg.Revision)
	}
	text, decision := r.Message, &domain.Decision{}
	if in.Text != "" {
		if text, decision.Edit, err = s.edit(ctx, r, in); err != nil {
			return domain.SuggestionRecord{}, err
		}
	}
	var state *string
	if allowedFor(ctx, authz.TranslationsReview, r.Locale) {
		approved := "approved"
		state = &approved
	}
	rev, err := s.write(ctx, r, text, state, decision.Edit != nil, in.InContext)
	if err != nil {
		return domain.SuggestionRecord{}, err
	}
	now := s.Now()
	next := r
	next.Status, next.TranslationRevision, next.DecidedBy, next.DecidedAt = domain.StatusAccepted, &rev, by, &now
	if decision.Edit != nil {
		next.Decision = decision
	}
	if err := s.decide(ctx, next, r.Version); err != nil {
		return domain.SuggestionRecord{}, err
	}
	ratio := 0.0
	if decision.Edit != nil {
		ratio = decision.Edit.Ratio
	}
	s.Metrics.SuggestionDecided(r.Locale, domain.StatusAccepted, ratio)
	next.Version = r.Version + 1
	return next, nil
}

// edit parses a person's edit of a suggestion for the target locale and
// diffs it against the suggestion.
func (s *Service) edit(ctx context.Context, r domain.SuggestionRecord, in AcceptInput) (string, *domain.EditDiff, error) {
	syntax, err := mfcontent.ParseSyntax(in.Syntax, mfcontent.MF2)
	if err != nil {
		return "", nil, err
	}
	loc, err := bcp47.Parse(r.Locale)
	if err != nil {
		return "", nil, err
	}
	c, err := mfcontent.Parse(syntax, in.Text, loc)
	if err != nil {
		return "", nil, err
	}
	text, err := sourceText(c.Model)
	if err != nil {
		return "", nil, err
	}
	scope := domain.Scope{TenantID: tenantOf(ctx).String(), ProjectID: r.ProjectID.String()}
	before, err := s.Knowledge.Terms(ctx, scope, r.Locale, domain.PlainText(r.Model))
	if err != nil {
		return "", nil, err
	}
	after, err := s.Knowledge.Terms(ctx, scope, r.Locale, domain.PlainText(c.Model))
	if err != nil {
		return "", nil, err
	}
	style, err := s.Knowledge.EffectiveStyle(ctx, scope, r.Locale, r.Namespace)
	if err != nil {
		return "", nil, err
	}
	d := domain.Diff(r.Model, c.Model, before, after, style.Pronoun)
	return text, &d, nil
}

// RejectSuggestion rejects a pending suggestion; nothing is written.
// Needs intelligence.translate for the locale.
func (s *Service) RejectSuggestion(ctx context.Context, id uuid.UUID, reason string, ifMatch *int) (domain.SuggestionRecord, error) {
	if len(reason) > 2000 {
		return domain.SuggestionRecord{}, invalid("invalid_reason", "a reason is at most 2000 bytes")
	}
	r, err := s.pending(ctx, id, ifMatch)
	if err != nil {
		return domain.SuggestionRecord{}, err
	}
	by, err := actorFor(ctx, authz.IntelligenceTranslate, r.Locale)
	if err != nil {
		return domain.SuggestionRecord{}, err
	}
	now := s.Now()
	next := r
	next.Status, next.DecidedBy, next.DecidedAt, next.Decision = domain.StatusRejected, by, &now, &domain.Decision{Reason: reason}
	if err := s.decide(ctx, next, r.Version); err != nil {
		return domain.SuggestionRecord{}, err
	}
	s.Metrics.SuggestionDecided(r.Locale, domain.StatusRejected, 0)
	next.Version = r.Version + 1
	return next, nil
}

// pending reads a suggestion that still awaits a decision.
func (s *Service) pending(ctx context.Context, id uuid.UUID, ifMatch *int) (domain.SuggestionRecord, error) {
	r, err := s.GetSuggestion(ctx, id)
	if err != nil {
		return domain.SuggestionRecord{}, err
	}
	if err := checkOptionalIfMatch(r.Version, ifMatch); err != nil {
		return domain.SuggestionRecord{}, err
	}
	if r.Status != domain.StatusPending {
		return domain.SuggestionRecord{}, fmt.Errorf("%w: it is %s", domain.ErrSuggestionDecided, r.Status)
	}
	return r, nil
}

// decide stores a decision if the suggestion is still at version.
func (s *Service) decide(ctx context.Context, r domain.SuggestionRecord, version int) error {
	return s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		cur, err := st.Suggestion(ctx, r.ID, true)
		if err != nil {
			return err
		}
		if cur.Version != version || cur.Status != domain.StatusPending {
			return fmt.Errorf("%w: it is %s", domain.ErrSuggestionDecided, cur.Status)
		}
		return st.DecideSuggestion(ctx, r, version)
	})
}

// LocaleMetrics are a locale's AI acceptance and edit distance (intent
// §70): people's decisions since a time.
type LocaleMetrics struct {
	LocaleDecisions
	// AcceptanceRate is accepted / (accepted + rejected).
	AcceptanceRate float64
}

// AcceptanceMetrics returns the per-locale acceptance rate and edit
// distance of a project's suggestions decided since since (the last 30
// days when zero). Needs intelligence.read.
func (s *Service) AcceptanceMetrics(ctx context.Context, project uuid.UUID, since time.Time) ([]LocaleMetrics, time.Time, error) {
	if err := authz.RequireIn(ctx, authz.IntelligenceRead, project); err != nil {
		return nil, time.Time{}, err
	}
	if _, err := s.Catalog.Project(ctx, project); err != nil {
		return nil, time.Time{}, err
	}
	if since.IsZero() {
		since = sinceDefault(s.Now())
	}
	var rows []LocaleDecisions
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.DecisionStats(ctx, project, since)
		return err
	})
	out := make([]LocaleMetrics, len(rows))
	for i, r := range rows {
		out[i] = LocaleMetrics{LocaleDecisions: r}
		if n := r.Accepted + r.Rejected; n > 0 {
			out[i].AcceptanceRate = float64(int(float64(r.Accepted)/float64(n)*1000+0.5)) / 1000
		}
	}
	return out, since, err
}

// ReviewQueueAges is the review queue's depth and waiting age per
// locale, as of now (RFC 0005 §8, the dashboard's fourth number). The
// queue is what `ai-review-queue` already pages; this is how long the
// people it is waiting for have been letting it wait.
//
// A locale with nothing pending has no row: the queue is a list of what
// is waiting, and a locale that is not waiting is not in it. Reading
// that as a depth of 0 is the caller's job, and it is the truthful
// reading — a locale nobody has ever suggested for and a locale whose
// queue was just cleared both have nothing waiting. Needs
// `intelligence.read`.
func (s *Service) ReviewQueueAges(
	ctx context.Context, project uuid.UUID, locales []string, now time.Time,
) ([]LocaleQueueAge, error) {
	if err := authz.RequireIn(ctx, authz.IntelligenceRead, project); err != nil {
		return nil, err
	}
	if _, err := s.Catalog.Project(ctx, project); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = s.Now()
	}
	var rows []LocaleQueueAge
	err := s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.QueueAges(ctx, project, locales, now)
		return err
	})
	return rows, err
}

// ListDisclosures lists which provider saw which message, newest first
// (RFC 0003 §7). Needs intelligence.read.
func (s *Service) ListDisclosures(ctx context.Context, f DisclosureFilter, page pagination.Page) ([]DisclosureRecord, *string, error) {
	scope, err := authz.Projects(ctx, authz.IntelligenceRead)
	if err != nil {
		return nil, nil, err
	}
	f.Projects = scope.IDs()
	before, err := parseCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []DisclosureRecord
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.Disclosures(ctx, f, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(d DisclosureRecord) string { return Cursor{At: d.OccurredAt, ID: d.ID}.String() })
	return items, next, nil
}
