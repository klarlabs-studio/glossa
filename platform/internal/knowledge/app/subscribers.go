package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// The event contracts Knowledge reads. The payload types are Knowledge's
// own (an anti-corruption layer): it depends on the published JSON, not
// on Localization's or Catalog's Go types.
const (
	localizationTranslationRevised  = "localization.translation.revised"
	localizationTranslationReviewed = "localization.translation.reviewed"
	catalogProjectDeleted           = "catalog.project.deleted"
)

// Subscriber names are stored with each event; never rename them.
const (
	subscriberDeriveTM    = "knowledge.derive_tm"
	subscriberDropProject = "knowledge.drop_project"
)

type translationEvent struct {
	TranslationID string `json:"translation_id"`
	ProjectID     string `json:"project_id"`
}

type projectEvent struct {
	ProjectID string `json:"project_id"`
}

// Subscribe registers Knowledge's subscribers.
//
// Translation memory is derived from both translation events: a review
// approves or takes back an approval (reviewed), and new text either
// arrives approved — a project without required review — or replaces
// approved text (revised). Derivation is a batch subscriber (#89).
func (s *Service) Subscribe(r *outbox.Registry) error {
	derive := outbox.BatchHandlerFuncs{Event: s.handleTranslationEvent, Batch: s.handleTranslationEvents}
	if err := r.SubscribeBatch(subscriberDeriveTM, derive, localizationTranslationRevised, localizationTranslationReviewed); err != nil {
		return err
	}
	return r.Subscribe(catalogProjectDeleted, subscriberDropProject, outbox.HandlerFunc(s.handleProjectDeleted))
}

// handleTranslationEvent brings the translation's TM unit in line with
// the translation's current state, read through Localization's port as
// the background principal knowledge.derive_tm. The event is only a
// trigger: whatever revision it names, the current state decides, and a
// state older than one already applied changes nothing. So duplicates
// and reordering converge.
func (s *Service) handleTranslationEvent(ctx context.Context, d outbox.Delivery) error {
	return s.deriveFor(ctx, []outbox.Delivery{d})[0]
}

// handleTranslationEvents is handleTranslationEvent for a delivered
// batch: the translations' current states are read together (a few
// reads per project, not per translation), then every derivation is
// written in one transaction and a constant number of statements (#109).
// Since the current state decides, the order of the events does not
// matter, and a translation named twice is derived once.
func (s *Service) handleTranslationEvents(ctx context.Context, ds []outbox.Delivery) []error {
	return s.deriveFor(ctx, ds)
}

// deriveFor derives the TM for ds' translations. A delivery that cannot
// be read fails alone. Several translations are written set-based in one
// transaction; if that fails — one of them cannot be derived, say — each
// is derived on its own, so that only the one at fault fails.
func (s *Service) deriveFor(ctx context.Context, ds []outbox.Delivery) []error {
	errs := make([]error, len(ds))
	bg, err := authz.Background(ctx, subscriberDeriveTM, authz.TranslationsRead, authz.CatalogRead)
	if err != nil {
		for i := range errs {
			errs[i] = outbox.Permanent(err)
		}
		return errs
	}
	todo := s.currentOf(bg, ds, errs)
	if len(todo) == 0 {
		return errs
	}
	// Translation order is the order the derivation locks are taken in,
	// whoever takes them.
	slices.SortStableFunc(todo, func(a, b pending) int {
		return slices.Compare(a.cur.TranslationID[:], b.cur.TranslationID[:])
	})
	p, _ := authz.From(bg)
	by := p.Actor.String()
	if len(todo) > 1 {
		err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
			return s.deriveAll(ctx, st, todo, by)
		})
		if err == nil {
			return errs
		}
		s.logger.WarnContext(ctx, "knowledge: batch derivation failed, deriving one by one",
			"translations", len(todo), "error", err)
	}
	for _, t := range todo {
		err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
			return s.derive(ctx, st, t.cur, by)
		})
		for _, i := range t.at {
			errs[i] = err
		}
	}
	return errs
}

// pending is a translation to be derived, and the deliveries that name
// it.
type pending struct {
	at  []int
	cur CurrentTranslation
}

// currentOf reads the current state of the translations ds name, one
// bulk read per project, in the order the projects first appear. A
// delivery that cannot be decoded, or whose project cannot be read,
// gets its error in errs[i] and the rest are unaffected; a translation
// that no longer exists is skipped (deleted with its project;
// knowledge.drop_project erases the rest). A translation several
// deliveries name is listed once.
func (s *Service) currentOf(bg context.Context, ds []outbox.Delivery, errs []error) []pending {
	var projects []uuid.UUID
	byProject := map[uuid.UUID][]int{}
	ids := make([]uuid.UUID, len(ds))
	for i, d := range ds {
		project, translation, err := translationOf(d)
		if err != nil {
			errs[i] = err
			continue
		}
		if _, ok := byProject[project]; !ok {
			projects = append(projects, project)
		}
		byProject[project] = append(byProject[project], i)
		ids[i] = translation
	}
	var todo []pending
	listed := map[uuid.UUID]int{}
	for _, project := range projects {
		at := byProject[project]
		want := make([]uuid.UUID, len(at))
		for j, i := range at {
			want[j] = ids[i]
		}
		found, err := s.translations.Currents(bg, project, want)
		if errors.Is(err, ErrProjectNotFound) {
			continue
		}
		for _, i := range at {
			if err != nil {
				errs[i] = err
			} else if cur, ok := found[ids[i]]; !ok {
				continue
			} else if n, dup := listed[ids[i]]; dup {
				todo[n].at = append(todo[n].at, i)
			} else {
				listed[ids[i]] = len(todo)
				todo = append(todo, pending{at: []int{i}, cur: cur})
			}
		}
	}
	return todo
}

func translationOf(d outbox.Delivery) (project, translation uuid.UUID, err error) {
	var e translationEvent
	if err := d.Decode(&e); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	translation, err1 := uuid.Parse(e.TranslationID)
	project, err2 := uuid.Parse(e.ProjectID)
	if err := errors.Join(err1, err2); err != nil {
		return uuid.Nil, uuid.Nil, outbox.Permanent(fmt.Errorf("knowledge: translation event %s: %w", d.EventID, err))
	}
	return project, translation, nil
}

// derive applies domain.Reconcile under the translation's derivation
// lock. A new unit records the text's author; a retirement records by,
// the background process that noticed the change.
func (s *Service) derive(ctx context.Context, st Store, cur CurrentTranslation, by string) error {
	now := s.now()
	if err := st.EnsureDerivation(ctx, cur.TranslationID, cur.ProjectID, now); err != nil {
		return err
	}
	applied, err := st.LockDerivation(ctx, cur.TranslationID)
	if err != nil {
		return err
	}
	if applied >= cur.Revision {
		return nil
	}
	active, err := st.LockActiveUnit(ctx, cur.TranslationID)
	if err != nil {
		return err
	}
	d, err := settle(active, cur, by, now)
	if err != nil {
		return err
	}
	if d.Retire != "" {
		if err := st.RetireUnit(ctx, *active); err != nil {
			return err
		}
	}
	if d.Touch {
		if err := st.TouchUnit(ctx, active.ID, cur.Revision, now); err != nil {
			return err
		}
	}
	if d.Create != nil {
		if err := st.InsertUnit(ctx, *d.Create); err != nil {
			return err
		}
	}
	return st.SetDerivation(ctx, cur.TranslationID, cur.Revision, now)
}

// settle is what domain.Reconcile decides for a translation's current
// state, with the retirement it calls for already applied to active.
func settle(active *domain.TMUnit, cur CurrentTranslation, by string, now time.Time) (domain.Derivation, error) {
	d, err := domain.Reconcile(active, cur.ApprovedText, cur.Approved, now)
	if err != nil {
		return d, outbox.Permanent(err)
	}
	if d.Retire != "" {
		if err := active.Retire(d.Retire, by, now); err != nil {
			return d, err
		}
	}
	return d, nil
}

// deriveAll is derive for translations (in translation order, each
// once) in a constant number of statements: it locks their derivations
// and active units, decides each as derive would, and writes the
// retirements, confirmations, new units and revisions together.
func (s *Service) deriveAll(ctx context.Context, st Store, todo []pending, by string) error {
	now := s.now()
	refs := make([]DerivationRef, len(todo))
	for i, t := range todo {
		refs[i] = DerivationRef{Translation: t.cur.TranslationID, Project: t.cur.ProjectID}
	}
	applied, err := st.LockDerivations(ctx, refs, now)
	if err != nil {
		return err
	}
	var stale []pending
	var ids []uuid.UUID
	for _, t := range todo {
		if applied[t.cur.TranslationID] < t.cur.Revision {
			stale = append(stale, t)
			ids = append(ids, t.cur.TranslationID)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	active, err := st.LockActiveUnits(ctx, ids)
	if err != nil {
		return err
	}
	var (
		retire, create []domain.TMUnit
		touch          []UnitTouch
		set            []DerivationRevision
	)
	for _, t := range stale {
		var unit *domain.TMUnit
		if u, ok := active[t.cur.TranslationID]; ok {
			unit = &u
		}
		d, err := settle(unit, t.cur, by, now)
		if err != nil {
			return err
		}
		if d.Retire != "" {
			retire = append(retire, *unit)
		}
		if d.Touch {
			touch = append(touch, UnitTouch{Unit: unit.ID, Revision: t.cur.Revision})
		}
		if d.Create != nil {
			create = append(create, *d.Create)
		}
		set = append(set, DerivationRevision{Translation: t.cur.TranslationID, Revision: t.cur.Revision})
	}
	if len(retire) > 0 {
		if err := st.RetireUnits(ctx, retire); err != nil {
			return err
		}
	}
	if len(touch) > 0 {
		if err := st.TouchUnits(ctx, touch, now); err != nil {
			return err
		}
	}
	if len(create) > 0 {
		if err := st.InsertUnits(ctx, create); err != nil {
			return err
		}
	}
	return st.SetDerivations(ctx, set, now)
}

// handleProjectDeleted erases a deleted project's knowledge: its TM
// units, concepts, style guides and their history.
func (s *Service) handleProjectDeleted(ctx context.Context, d outbox.Delivery) error {
	var e projectEvent
	if err := d.Decode(&e); err != nil {
		return err
	}
	id, err := uuid.Parse(e.ProjectID)
	if err != nil {
		return outbox.Permanent(fmt.Errorf("knowledge: project event with project_id %q", e.ProjectID))
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		return st.DeleteProjectData(ctx, id)
	})
}
