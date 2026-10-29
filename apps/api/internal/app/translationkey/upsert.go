// Package keyapp wires translation-key use cases.
package keyapp

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/domain/translationkey"
)

// ErrInvalidProjectID guards against nil-UUID inputs slipping through
// from a broken handler.
var ErrInvalidProjectID = errors.New("keyapp: project_id required")

// UpsertInput is one row of the CLI's batch scan request. Description
// is optional; an empty description preserves the existing one in
// the DB (sqlc UPSERT honours COALESCE).
type UpsertInput struct {
	Name        string
	Description string
}

// UpsertKeys is the batch UPSERT use case the CLI's `glossa scan`
// command targets. Idempotent: re-running the scan with the same
// keys is a no-op; new keys insert; descriptions update if changed.
type UpsertKeys struct {
	repo translationkey.Repository
}

// NewUpsertKeys wires the use case.
func NewUpsertKeys(repo translationkey.Repository) *UpsertKeys {
	return &UpsertKeys{repo: repo}
}

// UpsertResult mirrors the input shape with the persisted Key. Order
// is preserved 1:1 with the input slice so the CLI can map errors
// back to a source location.
type UpsertResult struct {
	Input UpsertInput
	Key   translationkey.Key
	Err   error
}

// Execute runs the batch. Each row is processed independently; one
// validation failure does not abort the batch. The returned slice
// has length == len(inputs); callers inspect Err per row.
func (uc *UpsertKeys) Execute(ctx context.Context, projectID uuid.UUID, inputs []UpsertInput) ([]UpsertResult, error) {
	if projectID == uuid.Nil {
		return nil, ErrInvalidProjectID
	}
	out := make([]UpsertResult, len(inputs))
	for i, in := range inputs {
		name, err := translationkey.NewName(in.Name)
		if err != nil {
			out[i] = UpsertResult{Input: in, Err: err}
			continue
		}
		k, err := uc.repo.Upsert(ctx, translationkey.Key{
			ProjectID:   projectID,
			Name:        name,
			Description: in.Description,
		})
		out[i] = UpsertResult{Input: in, Key: k, Err: err}
	}
	return out, nil
}
