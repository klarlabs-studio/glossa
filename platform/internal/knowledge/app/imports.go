package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// The bulk imports the Integration context's jobs run (TMX, TBX): one
// transaction per batch, items failing on their own, re-imports
// unchanged.

// MaxImportBatch bounds one import call.
const MaxImportBatch = 500

// ErrTooManyItems means an import batch is empty or too large.
var ErrTooManyItems = fmt.Errorf("%w: an import batch holds 1 to %d items", ErrInvalidQuery, MaxImportBatch)

// ImportStatus is what an import did with one item.
type ImportStatus string

// Import statuses.
const (
	ImportCreated   ImportStatus = "created"
	ImportUpdated   ImportStatus = "updated"
	ImportUnchanged ImportStatus = "unchanged"
	// ImportConflict: the item differs from what is stored and the
	// import was not asked to overwrite it.
	ImportConflict ImportStatus = "conflict"
	ImportInvalid  ImportStatus = "invalid"
)

// ImportResult reports one item, in request order. Code and Detail
// explain a conflict or an invalid item.
type ImportResult struct {
	Status ImportStatus
	ID     uuid.UUID
	Code   string
	Detail string
}

// errDryRun rolls a dry run's transaction back.
var errDryRun = errors.New("knowledge: dry run")

// inImport runs fn in one tenant transaction, rolled back when dryRun.
func (s *Service) inImport(ctx context.Context, dryRun bool, fn func(context.Context, Store) error) error {
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := fn(ctx, st); err != nil {
			return err
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return nil
	}
	return err
}

// ImportTMUnits adds translation-memory units read from a file (origin
// import), tenant-wide (project nil) or to a project. A unit whose exact
// text is already active in the scope is unchanged, so importing a file
// twice adds nothing; TM imports only ever add. Needs knowledge.write.
func (s *Service) ImportTMUnits(ctx context.Context, project *uuid.UUID, units []domain.ImportedText, dryRun bool) ([]ImportResult, error) {
	by, err := writeScope(ctx, authz.KnowledgeWrite, project)
	if err != nil {
		return nil, err
	}
	if len(units) == 0 || len(units) > MaxImportBatch {
		return nil, ErrTooManyItems
	}
	if err := s.requireProject(ctx, project); err != nil {
		return nil, err
	}
	results := make([]ImportResult, len(units))
	now := s.now()
	err = s.inImport(ctx, dryRun, func(ctx context.Context, st Store) error {
		seen := map[string]bool{}
		for i, in := range units {
			in.ProjectID = project
			u, err := domain.NewImportedUnit(in, by, now)
			if err != nil {
				results[i] = ImportResult{Status: ImportInvalid, Code: "invalid_unit", Detail: err.Error()}
				continue
			}
			slot := u.SourceLocale.String() + "\x00" + u.TargetLocale.String() + "\x00" + u.SourceMF2 + "\x00" + u.TargetMF2
			exists, err := st.HasActiveUnit(ctx, u)
			if err != nil {
				return err
			}
			if exists || seen[slot] {
				results[i] = ImportResult{Status: ImportUnchanged}
				continue
			}
			seen[slot] = true
			if err := st.InsertUnit(ctx, u); err != nil {
				return err
			}
			results[i] = ImportResult{Status: ImportCreated, ID: u.ID}
		}
		return nil
	})
	return results, err
}

// ConceptImport is one concept read from a file, with the ID it is
// stored under (the Integration context derives stable IDs, so a
// re-import finds the concept).
type ConceptImport struct {
	ID    uuid.UUID
	Input domain.ConceptInput
}

// ImportConcepts creates or replaces termbase concepts read from a file,
// tenant-wide (project nil) or in a project. An existing concept with
// the same content is unchanged; with other content it is a conflict
// unless overwrite is set, which replaces it as a new version. Terms the
// concept already has keep their case sensitivity (TBX has no data
// category for it). A concept ID taken by another scope is invalid.
// Needs knowledge.write.
func (s *Service) ImportConcepts(ctx context.Context, project *uuid.UUID, items []ConceptImport, overwrite, dryRun bool) ([]ImportResult, error) {
	by, err := writeScope(ctx, authz.KnowledgeWrite, project)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > MaxImportBatch {
		return nil, ErrTooManyItems
	}
	if err := s.requireProject(ctx, project); err != nil {
		return nil, err
	}
	results := make([]ImportResult, len(items))
	err = s.inImport(ctx, dryRun, func(ctx context.Context, st Store) error {
		seen := map[uuid.UUID]bool{}
		for i, it := range items {
			if seen[it.ID] {
				results[i] = ImportResult{Status: ImportInvalid, ID: it.ID, Code: "duplicate_item", Detail: "the concept appears earlier in this import"}
				continue
			}
			seen[it.ID] = true
			r, err := s.importConcept(ctx, st, project, it, overwrite, by)
			if err != nil {
				return err
			}
			results[i] = r
		}
		return nil
	})
	return results, err
}

func (s *Service) importConcept(ctx context.Context, st Store, project *uuid.UUID, it ConceptImport, overwrite bool, by string) (ImportResult, error) {
	res := ImportResult{ID: it.ID}
	invalid := func(err error) (ImportResult, error) {
		res.Status, res.Code, res.Detail = ImportInvalid, "invalid_concept", err.Error()
		return res, nil
	}
	c, err := st.LockConcept(ctx, it.ID)
	if errors.Is(err, ErrNotFound) {
		created, err := domain.NewConcept(it.ID, project, it.Input, by, s.now())
		if err != nil {
			return invalid(err)
		}
		if _, err := st.InsertConcept(ctx, created); err != nil {
			return res, err
		}
		res.Status = ImportCreated
		return res, s.recordConcept(ctx, st, created, ActionCreated, by)
	}
	if err != nil {
		return res, err
	}
	if !sameProject(c.ProjectID, project) {
		res.Status, res.Code, res.Detail = ImportInvalid, "concept_scope", "a concept with this ID belongs to another scope"
		return res, nil
	}
	in := keepCaseSensitivity(it.Input, c)
	expected := c.Version
	changed, err := c.Replace(in, by, s.now())
	switch {
	case err != nil:
		return invalid(err)
	case !changed:
		res.Status = ImportUnchanged
		return res, nil
	case !overwrite:
		res.Status, res.Code, res.Detail = ImportConflict, "concept_differs", "the stored concept differs from the file's; merge keeps it (overwrite replaces it)"
		return res, nil
	}
	if err := st.UpdateConcept(ctx, c, expected); err != nil {
		return res, err
	}
	res.Status = ImportUpdated
	return res, s.recordConcept(ctx, st, c, ActionUpdated, by)
}

// keepCaseSensitivity copies the case sensitivity of c's terms onto the
// same terms (locale and text) of in.
func keepCaseSensitivity(in domain.ConceptInput, c domain.Concept) domain.ConceptInput {
	out := in
	out.Terms = make([]domain.TermInput, len(in.Terms))
	for i, t := range in.Terms {
		for _, old := range c.Terms {
			if old.Locale.String() == t.Locale && old.Text == t.Text {
				t.CaseSensitive = old.CaseSensitive
			}
		}
		out.Terms[i] = t
	}
	return out
}
