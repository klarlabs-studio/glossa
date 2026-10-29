package translationapp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translation"
)

type inMemoryRepo struct {
	upserted translation.Translation
}

func (r *inMemoryRepo) Upsert(_ context.Context, t translation.Translation) (translation.Translation, error) {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	r.upserted = t
	return t, nil
}

func (r *inMemoryRepo) Find(_ context.Context, _, _ uuid.UUID) (translation.Translation, error) {
	return translation.Translation{}, translation.ErrNotFound
}

func (r *inMemoryRepo) ListBundle(_ context.Context, _, _ uuid.UUID) ([]translation.BundleEntry, error) {
	return nil, errors.New("unused in these tests")
}

func TestUpdateTranslation_DefaultsStatusToNeedsReview(t *testing.T) {
	repo := &inMemoryRepo{}
	uc := translationapp.NewUpdateTranslation(repo)
	out, err := uc.Execute(context.Background(), translationapp.UpdateInput{
		KeyID:     uuid.New(),
		LocaleID:  uuid.New(),
		Value:     "Freigeben",
		UpdatedBy: uuid.New(),
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Status != translation.StatusNeedsReview {
		t.Errorf("expected default status needs_review, got %q", out.Status)
	}
}

func TestUpdateTranslation_PassesThroughExplicitStatus(t *testing.T) {
	repo := &inMemoryRepo{}
	uc := translationapp.NewUpdateTranslation(repo)
	out, err := uc.Execute(context.Background(), translationapp.UpdateInput{
		KeyID:     uuid.New(),
		LocaleID:  uuid.New(),
		Value:     "Freigeben",
		Status:    string(translation.StatusApproved),
		UpdatedBy: uuid.New(),
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Status != translation.StatusApproved {
		t.Errorf("expected approved, got %q", out.Status)
	}
}

func TestUpdateTranslation_RejectsInvalidStatus(t *testing.T) {
	repo := &inMemoryRepo{}
	uc := translationapp.NewUpdateTranslation(repo)
	_, err := uc.Execute(context.Background(), translationapp.UpdateInput{
		KeyID:     uuid.New(),
		LocaleID:  uuid.New(),
		Value:     "x",
		Status:    "yolo",
		UpdatedBy: uuid.New(),
	})
	if !errors.Is(err, translation.ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestUpdateTranslation_RejectsZeroIDs(t *testing.T) {
	repo := &inMemoryRepo{}
	uc := translationapp.NewUpdateTranslation(repo)
	_, err := uc.Execute(context.Background(), translationapp.UpdateInput{})
	if !errors.Is(err, translationapp.ErrInvalidIDs) {
		t.Fatalf("expected ErrInvalidIDs, got %v", err)
	}
}

// An API-key caller has no user behind it. handlePatchTranslation says so
// directly — "API-key path optionally reads it from the body; otherwise zero
// (CLI / system change)" — and then passes that zero here, where the guard
// rejected it. The two disagreed, and the result was that a write-scoped key
// could not write: every CLI or service PATCH returned 422 unprocessable.
//
// updated_by is a nullable column with no foreign key, so a system write has
// somewhere to land. KeyID and LocaleID stay required; those genuinely cannot
// be nil.
func TestUpdateTranslation_AllowsSystemActorForAPIKeyWrites(t *testing.T) {
	repo := &inMemoryRepo{}
	uc := translationapp.NewUpdateTranslation(repo)

	got, err := uc.Execute(context.Background(), translationapp.UpdateInput{
		KeyID:    uuid.New(),
		LocaleID: uuid.New(),
		Value:    "Veränderungen",
		Status:   "approved",
		// UpdatedBy deliberately left nil: this is the CLI / system path.
	})
	if err != nil {
		t.Fatalf("system write rejected: %v", err)
	}
	if got.Value != "Veränderungen" {
		t.Fatalf("value = %q, want %q", got.Value, "Veränderungen")
	}
	if got.UpdatedBy != uuid.Nil {
		t.Fatalf("UpdatedBy = %v, want the nil UUID to mark a system change", got.UpdatedBy)
	}
}

func TestUpdateTranslation_StillRejectsZeroKeyOrLocale(t *testing.T) {
	repo := &inMemoryRepo{}
	uc := translationapp.NewUpdateTranslation(repo)

	for _, tc := range []struct {
		name string
		in   translationapp.UpdateInput
	}{
		{"no key", translationapp.UpdateInput{LocaleID: uuid.New(), UpdatedBy: uuid.New()}},
		{"no locale", translationapp.UpdateInput{KeyID: uuid.New(), UpdatedBy: uuid.New()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := uc.Execute(context.Background(), tc.in); !errors.Is(err, translationapp.ErrInvalidIDs) {
				t.Fatalf("expected ErrInvalidIDs, got %v", err)
			}
		})
	}
}
