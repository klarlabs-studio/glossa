package app

import (
	"context"
	"encoding/json"
	"errors"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"

	"github.com/google/uuid"
)

// MaxBatch bounds a bulk import.
const MaxBatch = 500

// ImportItem is one translation of a bulk import (the v0.3 importer,
// XLIFF and JSON import).
type ImportItem struct {
	Key          string
	Locale       string
	Text         string
	Syntax       string
	State        *string
	OriginDetail json.RawMessage
	// SourceRevision nil means the message's current source revision.
	SourceRevision *int
}

// ItemError is a per-item failure with a stable problem code.
type ItemError struct {
	Code     string
	Detail   string
	Findings []mf.Finding
}

// ImportResult reports one item, in request order.
type ImportResult struct {
	Key         string
	Locale      string
	Status      WriteStatus // "" when Error is set
	Translation *TranslationView
	Error       *ItemError
}

// ImportOptions tune a bulk import (the Integration context's jobs).
type ImportOptions struct {
	// KeepApproved never lowers an approved translation: other text for
	// it fails the item with approved_translation_conflict, the same
	// text in a lower review state is left unchanged. Decided under the
	// translation's row lock, so a concurrent approval is honoured.
	KeepApproved bool
	// DryRun runs every check and write, reports what would happen, and
	// rolls the transaction back: nothing is stored or published.
	DryRun bool
}

// ErrApprovedConflict is the item error of KeepApproved.
var ErrApprovedConflict = errors.New("localization: an approved translation has other text; the import keeps it")

// errDryRun rolls a dry run's transaction back.
var errDryRun = errors.New("localization: dry run")

// ImportTranslations writes translations with provenance "import", one
// transaction for the batch. Items fail on their own (unknown message or
// locale, no permission for the locale, invalid text, structural QA
// errors) without failing the batch. Importing the same text twice
// leaves the translation unchanged.
func (s *Service) ImportTranslations(ctx context.Context, project uuid.UUID, items []ImportItem) ([]ImportResult, error) {
	return s.ImportTranslationsWith(ctx, project, items, ImportOptions{})
}

// ImportTranslationsWith is ImportTranslations with options.
func (s *Service) ImportTranslationsWith(ctx context.Context, project uuid.UUID, items []ImportItem, opts ImportOptions) ([]ImportResult, error) {
	by, err := actorIn(ctx, authz.TranslationsRead, project)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > MaxBatch {
		return nil, ErrTooManyItems
	}
	p, err := s.catalog.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.Key
	}
	msgs, err := s.catalog.MessagesByKeys(ctx, project, keys)
	if err != nil {
		return nil, err
	}
	results := make([]ImportResult, len(items))
	cmds := make([]*writeCmd, len(items))
	for i, it := range items {
		results[i] = ImportResult{Key: it.Key, Locale: it.Locale}
		cmd, ierr, err := s.prepareImport(ctx, p, msgs, it, by)
		if err != nil {
			return nil, err
		}
		if ierr != nil {
			results[i].Error = ierr
			continue
		}
		results[i].Locale = cmd.locale.String()
		cmd.keepApproved = opts.KeepApproved
		cmds[i] = &cmd
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		locales, err := st.AllLocales(ctx, project)
		if err != nil {
			return err
		}
		known := map[bcp47.Tag]bool{}
		for _, l := range locales {
			known[l.Code] = true
		}
		seen := map[TranslationSlot]bool{}
		var writes []int // indexes of the items that write
		for i, cmd := range cmds {
			if cmd == nil {
				continue
			}
			slot := TranslationSlot{MessageID: cmd.msg.ID, Locale: cmd.locale}
			switch {
			case !known[cmd.locale]:
				results[i].Error = &ItemError{Code: "locale_not_found", Detail: "the project has no locale " + cmd.locale.String()}
				continue
			case seen[slot]:
				results[i].Error = &ItemError{Code: "duplicate_item", Detail: "the same message and locale appear earlier in this batch"}
				continue
			}
			seen[slot] = true
			writes = append(writes, i)
		}
		if err := s.writeAll(ctx, st, cmds, writes, results); err != nil {
			return err
		}
		if opts.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	return results, err
}

// writeAll is write for the items at writes, in a few statements
// however many there are (#77): it brings the message projection up to
// the sources the items read, locks their translations in one read,
// decides each item in memory, and stores the outcome in one statement
// per table and one outbox insert. A client error fails its item only.
func (s *Service) writeAll(ctx context.Context, st Store, cmds []*writeCmd, writes []int, results []ImportResult) error {
	if len(writes) == 0 {
		return nil
	}
	// As write does, each message's projection first, sparing the locale
	// of its first item: the translation this import brings current.
	var states []MessageState
	except := map[uuid.UUID]bcp47.Tag{}
	slots := make([]TranslationSlot, len(writes))
	for j, i := range writes {
		cmd := cmds[i]
		if _, ok := except[cmd.msg.ID]; !ok {
			except[cmd.msg.ID] = cmd.locale
			states = append(states, stateOf(cmd.msg))
		}
		slots[j] = TranslationSlot{MessageID: cmd.msg.ID, Locale: cmd.locale}
	}
	if err := s.applyMessageStates(ctx, st, states, except, projectionActor); err != nil {
		return err
	}
	stored, err := st.LockTranslations(ctx, slots)
	if err != nil {
		return err
	}
	var (
		inserts []domain.Translation
		updates []TranslationUpdate
		revs    []domain.Revision
		events  []outbox.Event
	)
	for j, i := range writes {
		cmd := *cmds[i]
		policy := domain.WritePolicy{ReviewRequired: cmd.project.ReviewRequired, Flow: s.flow}
		t, found := stored[slots[j]]
		o, err := s.decide(cmd, policy, t, found)
		if ierr := importError(err); ierr != nil {
			results[i].Error = ierr
			continue
		}
		if err != nil {
			return err
		}
		if o.rev != nil {
			if o.created {
				inserts = append(inserts, o.t)
			} else {
				updates = append(updates, TranslationUpdate{Translation: o.t, Expected: o.expected})
			}
			revs = append(revs, *o.rev)
			events = append(events, revisionEvent(o.t, *o.rev))
		}
		results[i].Status = o.status
		results[i].Translation = &TranslationView{Translation: o.t, CurrentSourceRevision: cmd.msg.Revision}
	}
	if err := st.InsertTranslations(ctx, inserts); err != nil {
		return err
	}
	if err := st.UpdateTranslations(ctx, updates); err != nil {
		return err
	}
	if err := st.AppendRevisions(ctx, revs); err != nil {
		return err
	}
	return st.PublishAll(ctx, events)
}

// prepareImport checks one item. Client mistakes become item errors;
// anything else (the catalog unreachable) fails the batch.
func (s *Service) prepareImport(ctx context.Context, p ProjectInfo, msgs map[string]SourceMessage, it ImportItem, by string) (writeCmd, *ItemError, error) {
	locale, err := bcp47.Parse(it.Locale)
	if err != nil {
		return writeCmd{}, &ItemError{Code: "invalid_locale", Detail: err.Error()}, nil
	}
	if _, err := actorFor(ctx, authz.TranslationsWrite, locale); err != nil {
		return writeCmd{}, &ItemError{Code: "forbidden", Detail: "no translations.write for " + locale.String()}, nil
	}
	msg, ok := msgs[it.Key]
	if !ok {
		return writeCmd{}, &ItemError{Code: "message_not_found", Detail: "no message with this key"}, nil
	}
	cmd, err := s.prepare(ctx, p, msg, locale, TranslationInput{
		Text: it.Text, Syntax: it.Syntax, State: it.State, Origin: string(domain.OriginImport),
		OriginDetail: it.OriginDetail, SourceRevision: it.SourceRevision,
	}, domain.OriginImport, by)
	if ierr := importError(err); ierr != nil {
		return writeCmd{}, ierr, nil
	}
	if errors.Is(err, ErrNotFound) {
		return writeCmd{}, &ItemError{Code: "invalid_source_revision", Detail: "no such source revision"}, nil
	}
	return cmd, nil, err
}

// importError turns a client error into an item error; nil for success
// and for errors that must fail the whole batch (storage errors, which
// abort the transaction).
func importError(err error) *ItemError {
	if err == nil {
		return nil
	}
	var (
		qa      *domain.QAError
		invalid *mfcontent.InvalidError
	)
	switch {
	case errors.As(err, &qa):
		return &ItemError{Code: "structural_qa_failed", Detail: err.Error(), Findings: qa.Findings}
	case errors.As(err, &invalid):
		return &ItemError{Code: "invalid_message", Detail: string(invalid.Code) + ": " + invalid.Message}
	}
	for code, target := range map[string]error{
		"approved_translation_conflict": ErrApprovedConflict,
		"source_locale":                 domain.ErrSourceLocale,
		"invalid_source_revision":       domain.ErrInvalidSourceRev,
		"invalid_state":                 domain.ErrInvalidReviewState,
		"write_cannot_reject":           domain.ErrWriteCannotReject,
		"review_forbidden":              domain.ErrReviewForbidden,
		"invalid_origin_detail":         domain.ErrInvalidOriginInfo,
		"invalid_syntax":                mfcontent.ErrInvalidSyntax,
		"message_too_long":              mfcontent.ErrTooLong,
		"invalid_transition":            domain.ErrTransition,
	} {
		if errors.Is(err, target) {
			return &ItemError{Code: code, Detail: err.Error()}
		}
	}
	return nil
}
