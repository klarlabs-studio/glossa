// Package apikeyapp issues + revokes project API keys. Raw keys
// are generated here, hashed once, returned to the caller exactly
// once. The hash is what hits storage.
package apikeyapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/domain/apikey"
)

// ErrInvalidLabel is the belt-and-suspender guard for labels that
// would surprise an operator scanning a long list.
var ErrInvalidLabel = errors.New("apikeyapp: label must be 1-100 chars")

// IssueInput describes a new-key request.
type IssueInput struct {
	ProjectID uuid.UUID
	Scope     apikey.Scope
	Label     string
}

// IssueOutput carries the persisted row plus the raw key. The raw
// key is the only artifact ever returned to the human — once they
// dismiss the reveal dialog it's gone.
type IssueOutput struct {
	Key apikey.Key
	Raw string
}

// IssueAPIKey mints a new (project, scope, label) row.
type IssueAPIKey struct {
	repo apikey.Repository
}

// NewIssueAPIKey wires the use case.
func NewIssueAPIKey(repo apikey.Repository) *IssueAPIKey {
	return &IssueAPIKey{repo: repo}
}

// Execute validates input + persists the new key.
func (uc *IssueAPIKey) Execute(ctx context.Context, in IssueInput) (IssueOutput, error) {
	if in.ProjectID == uuid.Nil {
		return IssueOutput{}, errors.New("apikeyapp: project_id required")
	}
	if !in.Scope.IsValid() {
		return IssueOutput{}, apikey.ErrInvalidScope
	}
	if l := len(in.Label); l == 0 || l > 100 {
		return IssueOutput{}, ErrInvalidLabel
	}
	raw, hash, err := GenerateAPIKey()
	if err != nil {
		return IssueOutput{}, err
	}
	k, err := uc.repo.Create(ctx, in.ProjectID, hash, in.Scope, in.Label)
	if err != nil {
		return IssueOutput{}, err
	}
	return IssueOutput{Key: k, Raw: raw}, nil
}

// GenerateAPIKey returns (raw, sha256hash). The raw key carries a
// `glossa_` prefix so a leaked one is grep-able in customer logs.
// Exposed so adjacent use cases (project create) can reuse it.
func GenerateAPIKey() (string, []byte, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	raw := "glossa_" + hex.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

// RevokeAPIKey marks a key revoked. Existing consumers using that
// key get 401s on the next request.
type RevokeAPIKey struct {
	repo apikey.Repository
}

// NewRevokeAPIKey wires the use case.
func NewRevokeAPIKey(repo apikey.Repository) *RevokeAPIKey {
	return &RevokeAPIKey{repo: repo}
}

// Execute revokes a key by ID.
func (uc *RevokeAPIKey) Execute(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return errors.New("apikeyapp: id required")
	}
	return uc.repo.Revoke(ctx, id)
}
