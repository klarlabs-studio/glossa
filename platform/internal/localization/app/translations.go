package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"

	"github.com/google/uuid"
)

// TranslationView is a translation with the message's current source
// revision, from which Outdated is derived.
type TranslationView struct {
	domain.Translation
	CurrentSourceRevision int
	// ReviewByAuthorAllowed is set only when the caller could review the
	// translation's locale: true when no one else could, so the caller
	// may approve or reject text they wrote, and save text as approved
	// (recorded as a self-review); false when someone else must. Nil
	// otherwise.
	ReviewByAuthorAllowed *bool
}

// annotate sets ReviewByAuthorAllowed on the views the caller could review.
func (s *Service) annotate(ctx context.Context, project uuid.UUID, vs ...*TranslationView) error {
	p, ok := authz.From(ctx)
	if !ok {
		return nil
	}
	me := p.Actor.String()
	for _, v := range vs {
		if !allowedForIn(ctx, authz.TranslationsReview, v.Locale, project) {
			continue
		}
		others, err := s.othersCanReview(ctx, project, v.Locale, me)
		if err != nil {
			return err
		}
		allowed := !others
		v.ReviewByAuthorAllowed = &allowed
	}
	return nil
}

// Outdated reports whether the source moved on since the text was made.
func (v TranslationView) Outdated() bool { return v.Translation.Outdated(v.CurrentSourceRevision) }

func view(r TranslationRow) TranslationView {
	return TranslationView{Translation: r.Translation, CurrentSourceRevision: r.CurrentSourceRevision}
}

// TranslationInput is new text for a translation.
type TranslationInput struct {
	Text string
	// Syntax "" means the project's default.
	Syntax string
	// State is the requested review state; nil lets the project's
	// policy decide.
	State *string
	// Origin "" means human.
	Origin       string
	OriginDetail json.RawMessage
	// SourceRevision is the source revision the text was made against;
	// nil means the current one.
	SourceRevision *int
}

// WriteStatus is what a translation write did.
type WriteStatus string

// Write statuses.
const (
	WriteCreated   WriteStatus = "created"
	WriteRevised   WriteStatus = "revised"
	WriteReviewed  WriteStatus = "reviewed"
	WriteUnchanged WriteStatus = "unchanged"
)

// GetTranslation returns a message's translation in a locale. For an
// assigned member a unit outside their assignments is not found (RFC
// 0006 §3.3).
func (s *Service) GetTranslation(ctx context.Context, project uuid.UUID, key, locale string) (TranslationView, error) {
	vis, err := authz.Visible(ctx, authz.TranslationsRead, project)
	if err != nil {
		return TranslationView{}, err
	}
	tag, err := localeFromPath(locale)
	if err != nil {
		return TranslationView{}, err
	}
	msg, err := s.catalog.Message(ctx, project, key)
	if err != nil {
		return TranslationView{}, err
	}
	if !vis.Unit(msg.ID, tag.String()) {
		return TranslationView{}, ErrNotFound
	}
	var v TranslationView
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		row, err := st.Translation(ctx, msg.ID, tag)
		v = view(row)
		return err
	})
	if err != nil {
		return v, err
	}
	return v, s.annotate(ctx, project, &v)
}

// ListTranslations lists a message's translations by locale; an
// assigned member's list holds only their units of it, filtered in the
// query (RFC 0006 §3.3).
func (s *Service) ListTranslations(ctx context.Context, project uuid.UUID, key string, page pagination.Page) ([]TranslationView, *string, error) {
	vis, err := authz.Visible(ctx, authz.TranslationsRead, project)
	if err != nil {
		return nil, nil, err
	}
	msg, err := s.catalog.Message(ctx, project, key)
	if err != nil {
		return nil, nil, err
	}
	only := unitLocales(vis, msg.ID)
	var rows []TranslationView
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		stored, err := st.TranslationsOfMessage(ctx, msg.ID, page.After, only, page.Limit())
		for _, r := range stored {
			rows = append(rows, view(r))
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(v TranslationView) string { return v.Locale.String() })
	for i := range items {
		if err := s.annotate(ctx, project, &items[i]); err != nil {
			return nil, nil, err
		}
	}
	return items, next, nil
}

// unitLocales is the locales of message an authz view includes: nil for
// the whole project, an assigned member's units of message otherwise.
func unitLocales(vis authz.View, message uuid.UUID) []string {
	if vis.All() {
		return nil
	}
	out := []string{}
	for _, u := range vis.Units() {
		if u.Message == message {
			out = append(out, u.Locale)
		}
	}
	return out
}

// writeCmd is a prepared, checked translation write.
type writeCmd struct {
	project   ProjectInfo
	msg       SourceMessage
	locale    bcp47.Tag
	write     domain.Write
	canReview bool
	// othersCanReview is set only for a write that asks for approval or
	// rejection: whether anyone but the writer could review the locale.
	othersCanReview bool
	// ifMatch applies the create-or-replace rule when enforce is set.
	ifMatch *int
	enforce bool
	// keepApproved never lowers an approved translation (ImportOptions).
	keepApproved bool
}

// prepare validates a write outside any transaction: it reads the
// source through Catalog's port, parses the text for the target locale
// and runs structural QA against the source revision it was made from.
func (s *Service) prepare(ctx context.Context, p ProjectInfo, msg SourceMessage, locale bcp47.Tag, in TranslationInput, defaultOrigin domain.Origin, by string) (writeCmd, error) {
	if locale == p.SourceLocale {
		return writeCmd{}, domain.ErrSourceLocale
	}
	sourceRev := msg.Revision
	if in.SourceRevision != nil {
		sourceRev = *in.SourceRevision
	}
	if sourceRev < 1 || sourceRev > msg.Revision {
		return writeCmd{}, domain.ErrInvalidSourceRev
	}
	source := msg.Content
	if sourceRev != msg.Revision {
		var err error
		if source, err = s.catalog.SourceAt(ctx, p.ID, msg.ID, sourceRev); err != nil {
			return writeCmd{}, err
		}
	}
	syntax, err := mfcontent.ParseSyntax(in.Syntax, p.DefaultSyntax)
	if err != nil {
		return writeCmd{}, err
	}
	content, err := mfcontent.Parse(syntax, in.Text, locale)
	if err != nil {
		return writeCmd{}, err
	}
	origin, err := domain.ParseOrigin(in.Origin, defaultOrigin)
	if err != nil {
		return writeCmd{}, err
	}
	prov, err := domain.NewProvenance(origin, in.OriginDetail, by)
	if err != nil {
		return writeCmd{}, err
	}
	w := domain.Write{
		Content: content, Provenance: prov, SourceRevision: sourceRev,
		QA: domain.CheckStructure(source, content, locale, msg.MaxLength),
	}
	if in.State != nil {
		st, err := domain.ParseReviewState(*in.State)
		if err != nil {
			return writeCmd{}, err
		}
		w.State = &st
	}
	if err := w.QA.Gate(); err != nil {
		return writeCmd{}, err
	}
	canReview := allowedFor(ctx, authz.TranslationsReview, locale)
	others := false
	if canReview && origin != domain.OriginImport && w.State != nil && (*w.State == domain.StateApproved || *w.State == domain.StateRejected) {
		if others, err = s.othersCanReview(ctx, p.ID, locale, by); err != nil {
			return writeCmd{}, err
		}
	}
	return writeCmd{project: p, msg: msg, locale: locale, write: w, canReview: canReview, othersCanReview: others}, nil
}

// PutTranslation creates or revises a message's translation in a
// locale. ifMatch must be the translation's ETag when it exists and
// absent when it doesn't. Error-severity structural findings reject the
// write (*domain.QAError); warnings are stored and returned.
//
// An assigned member writes only in the units their assignments cover;
// any other is not found to them (RFC 0006 §3.3).
func (s *Service) PutTranslation(ctx context.Context, project uuid.UUID, key, locale string, in TranslationInput, ifMatch *int) (TranslationView, WriteStatus, error) {
	tag, err := bcp47.Parse(locale)
	if err != nil {
		return TranslationView{}, "", err
	}
	var by string
	if !assigned(ctx) {
		if by, err = actorForIn(ctx, authz.TranslationsWrite, tag, project); err != nil {
			return TranslationView{}, "", err
		}
	}
	p, err := s.catalog.Project(ctx, project)
	if err != nil {
		return TranslationView{}, "", err
	}
	msg, err := s.catalog.Message(ctx, project, key)
	if err != nil {
		return TranslationView{}, "", err
	}
	if assigned(ctx) {
		if by, err = unitActor(ctx, authz.TranslationsWrite, project, msg.ID, tag); err != nil {
			return TranslationView{}, "", err
		}
	}
	cmd, err := s.prepare(ctx, p, msg, tag, in, domain.OriginHuman, by)
	if err != nil {
		return TranslationView{}, "", err
	}
	cmd.ifMatch, cmd.enforce = ifMatch, true
	var (
		v      TranslationView
		status WriteStatus
	)
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.requireLocale(ctx, st, p, tag); err != nil {
			return err
		}
		t, stat, err := s.write(ctx, st, cmd)
		if err != nil {
			return err
		}
		v, status = TranslationView{Translation: t, CurrentSourceRevision: msg.Revision}, stat
		return nil
	})
	if err != nil {
		return v, status, err
	}
	return v, status, s.annotate(ctx, project, &v)
}

// requireLocale checks that the project translates into locale.
func (s *Service) requireLocale(ctx context.Context, st Store, p ProjectInfo, locale bcp47.Tag) error {
	_, err := st.Locale(ctx, p.ID, locale)
	if errors.Is(err, ErrNotFound) {
		return ErrLocaleNotFound
	}
	return err
}

// write applies a prepared write in st: it brings the message
// projection up to the source it just read, then creates or revises the
// translation, appends the revision and publishes the event.
func (s *Service) write(ctx context.Context, st Store, cmd writeCmd) (domain.Translation, WriteStatus, error) {
	if err := s.applyMessageState(ctx, st, stateOf(cmd.msg), cmd.locale, projectionActor); err != nil {
		return domain.Translation{}, "", err
	}
	policy := domain.WritePolicy{ReviewRequired: cmd.project.ReviewRequired, Flow: s.flow}
	t, found, err := st.LockTranslation(ctx, cmd.msg.ID, cmd.locale)
	if err != nil {
		return domain.Translation{}, "", err
	}
	if cmd.enforce {
		version := 0
		if found {
			version = t.Revision
		}
		if err := checkIfMatch(version, cmd.ifMatch); err != nil {
			return domain.Translation{}, "", err
		}
	}
	o, err := s.decide(cmd, policy, t, found)
	if err != nil || o.rev == nil {
		return o.t, o.status, err
	}
	if o.created {
		err = st.InsertTranslation(ctx, o.t)
	} else {
		err = st.UpdateTranslation(ctx, o.t, o.expected)
	}
	if err != nil {
		return domain.Translation{}, "", err
	}
	return o.t, o.status, s.record(ctx, st, o.t, *o.rev)
}

// writeOutcome is what a write does to a translation: rev is nil when
// it leaves it unchanged; otherwise the translation is created, or
// updated from revision expected, and rev appended.
type writeOutcome struct {
	t        domain.Translation
	status   WriteStatus
	rev      *domain.Revision
	created  bool
	expected int
}

// decide applies cmd to the stored translation t (found false: none) in
// memory, without storing anything.
func (s *Service) decide(cmd writeCmd, policy domain.WritePolicy, t domain.Translation, found bool) (writeOutcome, error) {
	if !found {
		t, rev, err := domain.NewTranslation(cmd.project.ID, cmd.msg.ID, cmd.locale, cmd.write, policy, cmd.canReview, cmd.othersCanReview, s.now())
		if err != nil {
			return writeOutcome{}, err
		}
		return writeOutcome{t: t, status: WriteCreated, rev: &rev, created: true}, nil
	}
	if cmd.keepApproved && t.State == domain.StateApproved {
		if !t.Content.SameModel(cmd.write.Content) {
			return writeOutcome{t: t}, ErrApprovedConflict
		}
		if cmd.write.State != nil && *cmd.write.State != domain.StateApproved {
			return writeOutcome{t: t, status: WriteUnchanged}, nil
		}
	}
	expected := t.Revision
	rev, changed, err := t.Revise(cmd.write, policy, cmd.canReview, cmd.othersCanReview, s.now())
	if err != nil || !changed {
		return writeOutcome{t: t, status: WriteUnchanged}, err
	}
	status := WriteRevised
	if rev.Kind == domain.KindReview {
		status = WriteReviewed
	}
	return writeOutcome{t: t, status: status, rev: &rev, expected: expected}, nil
}

// record appends a revision and publishes its event.
func (s *Service) record(ctx context.Context, st Store, t domain.Translation, rev domain.Revision) error {
	if err := st.AppendRevision(ctx, rev); err != nil {
		return err
	}
	return st.Publish(ctx, revisionEvent(t, rev))
}

// eventOf is the payload for rev of t.
func eventOf(t domain.Translation, rev domain.Revision) domain.TranslationEvent {
	e := domain.TranslationEventOf(t, rev.Provenance.By)
	e.SelfReview = rev.SelfReview
	return e
}

// revisionEvent announces rev of t.
func revisionEvent(t domain.Translation, rev domain.Revision) outbox.Event {
	typ := domain.EventTranslationRevised
	if rev.Kind == domain.KindReview {
		typ = domain.EventTranslationReviewed
	}
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateTranslation, AggregateID: t.ID.String(),
		Actor: outbox.Actor(rev.Provenance.By), Payload: eventOf(t, rev),
	}
}

// ReviewTranslation moves a translation to state as a review decision
// (approve, reject, send back to draft or review) if it is still at
// ifMatch. Approving and rejecting need translations.review for the
// locale.
func (s *Service) ReviewTranslation(ctx context.Context, project uuid.UUID, key, locale, state string, ifMatch int) (TranslationView, error) {
	tag, err := localeFromPath(locale)
	if err != nil {
		return TranslationView{}, err
	}
	by, err := actorForIn(ctx, authz.TranslationsWrite, tag, project)
	if err != nil {
		return TranslationView{}, err
	}
	to, err := domain.ParseReviewState(state)
	if err != nil {
		return TranslationView{}, err
	}
	msg, err := s.catalog.Message(ctx, project, key)
	if err != nil {
		return TranslationView{}, err
	}
	canReview := allowedForIn(ctx, authz.TranslationsReview, tag, project)
	others := false
	if canReview && (to == domain.StateApproved || to == domain.StateRejected) {
		// Asked before the transaction: the port reads Identity in its own.
		if others, err = s.othersCanReview(ctx, project, tag, by); err != nil {
			return TranslationView{}, err
		}
	}
	var v TranslationView
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		t, found, err := st.LockTranslation(ctx, msg.ID, tag)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if t.Revision != ifMatch {
			return ErrPreconditionFailed
		}
		rev, err := t.Review(to, by, s.flow, canReview, others, s.now())
		if err != nil {
			return err
		}
		if err := st.UpdateTranslation(ctx, t, ifMatch); err != nil {
			return err
		}
		v = TranslationView{Translation: t, CurrentSourceRevision: msg.Revision}
		return s.record(ctx, st, t, rev)
	})
	if err != nil {
		return v, err
	}
	return v, s.annotate(ctx, project, &v)
}

// TranslationRevisions lists a translation's log, newest first.
func (s *Service) TranslationRevisions(ctx context.Context, project uuid.UUID, key, locale string, page pagination.Page) ([]domain.Revision, *string, error) {
	vis, err := authz.Visible(ctx, authz.TranslationsRead, project)
	if err != nil {
		return nil, nil, err
	}
	tag, err := localeFromPath(locale)
	if err != nil {
		return nil, nil, err
	}
	before, err := beforeRevision(page.After)
	if err != nil {
		return nil, nil, err
	}
	msg, err := s.catalog.Message(ctx, project, key)
	if err != nil {
		return nil, nil, err
	}
	if !vis.Unit(msg.ID, tag.String()) {
		return nil, nil, ErrNotFound
	}
	var rows []domain.Revision
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		t, err := st.Translation(ctx, msg.ID, tag)
		if err != nil {
			return err
		}
		rows, err = st.Revisions(ctx, t.ID, before, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r domain.Revision) string { return strconv.Itoa(r.Number) })
	return items, next, nil
}
