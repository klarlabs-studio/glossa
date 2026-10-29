package keyapp_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	keyapp "go.klarlabs.de/glossa/apps/api/internal/app/translationkey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translationkey"
)

type inMemoryRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]translationkey.Key
}

func newRepo() *inMemoryRepo {
	return &inMemoryRepo{rows: map[uuid.UUID]translationkey.Key{}}
}

func (r *inMemoryRepo) Upsert(_ context.Context, k translationkey.Key) (translationkey.Key, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k.ID == uuid.Nil {
		k.ID = uuid.New()
	}
	r.rows[k.ID] = k
	return k, nil
}

func (r *inMemoryRepo) ListForProject(_ context.Context, _ uuid.UUID) ([]translationkey.Key, error) {
	return nil, errors.New("unused in these tests")
}

func (r *inMemoryRepo) Find(_ context.Context, _ uuid.UUID, _ translationkey.Name) (translationkey.Key, error) {
	return translationkey.Key{}, errors.New("unused in these tests")
}

func TestUpsertKeys_PreservesOrderAndPerRowErrors(t *testing.T) {
	repo := newRepo()
	uc := keyapp.NewUpsertKeys(repo)
	pid := uuid.New()

	results, err := uc.Execute(context.Background(), pid, []keyapp.UpsertInput{
		{Name: "coach.plan.approve", Description: "Approve plan button"},
		{Name: "INVALID NAME!", Description: ""}, // bad case in the middle
		{Name: "athlete.greeting", Description: ""},
	})
	if err != nil {
		t.Fatalf("unexpected batch-level error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("row 0 should succeed: %v", results[0].Err)
	}
	if !errors.Is(results[1].Err, translationkey.ErrInvalidName) {
		t.Errorf("row 1 should fail with ErrInvalidName, got %v", results[1].Err)
	}
	if results[2].Err != nil {
		t.Errorf("row 2 should succeed: %v", results[2].Err)
	}
	// The valid rows must have been persisted, the bad one must not.
	if len(repo.rows) != 2 {
		t.Errorf("expected 2 persisted rows, got %d", len(repo.rows))
	}
}

func TestUpsertKeys_RejectsBlankProjectID(t *testing.T) {
	uc := keyapp.NewUpsertKeys(newRepo())
	_, err := uc.Execute(context.Background(), uuid.Nil, nil)
	if !errors.Is(err, keyapp.ErrInvalidProjectID) {
		t.Fatalf("expected ErrInvalidProjectID, got %v", err)
	}
}
