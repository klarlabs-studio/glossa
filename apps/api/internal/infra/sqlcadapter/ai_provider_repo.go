package sqlcadapter

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/aitranslator"
)

// AIProviderRepo is the sqlc-backed adapter for aitranslator.Provider.
type AIProviderRepo struct {
	q *db.Queries
}

// NewAIProviderRepo wires the adapter against a sqlc Queries.
func NewAIProviderRepo(q *db.Queries) *AIProviderRepo {
	return &AIProviderRepo{q: q}
}

// Create persists a new provider.
func (r *AIProviderRepo) Create(ctx context.Context, p aitranslator.Provider) (aitranslator.Provider, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.CreateAIProvider(ctx, db.CreateAIProviderParams{
		TenantID:    toPgUUID(p.TenantID),
		Kind:        string(p.Kind),
		Label:       p.Label,
		BaseUrl:     p.BaseURL,
		Model:       p.Model,
		ApiKeyCt:    p.APIKeyCT,
		ApiKeyNonce: p.APIKeyNonce,
		Enabled:     p.Enabled,
	})
	if err != nil {
		return aitranslator.Provider{}, err
	}
	return aitranslator.Provider{
		ID:        fromPgUUID(row.ID),
		TenantID:  fromPgUUID(row.TenantID),
		Kind:      aitranslator.Kind(row.Kind),
		Label:     row.Label,
		BaseURL:   row.BaseUrl,
		Model:     row.Model,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

// List returns every provider for a tenant.
func (r *AIProviderRepo) List(ctx context.Context, tenantID uuid.UUID) ([]aitranslator.Provider, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListAIProviders(ctx, toPgUUID(tenantID))
	if err != nil {
		return nil, err
	}
	out := make([]aitranslator.Provider, 0, len(rows))
	for _, row := range rows {
		out = append(out, aitranslator.Provider{
			ID:        fromPgUUID(row.ID),
			TenantID:  fromPgUUID(row.TenantID),
			Kind:      aitranslator.Kind(row.Kind),
			Label:     row.Label,
			BaseURL:   row.BaseUrl,
			Model:     row.Model,
			Enabled:   row.Enabled,
			CreatedAt: row.CreatedAt.Time,
			UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return out, nil
}

// ListEnabled returns enabled providers including the ciphertext so a
// worker can decrypt and call out.
func (r *AIProviderRepo) ListEnabled(ctx context.Context, tenantID uuid.UUID) ([]aitranslator.Provider, error) {
	q := db.QueriesFromContext(ctx, r.q)
	rows, err := q.ListEnabledAIProvidersForTenant(ctx, toPgUUID(tenantID))
	if err != nil {
		return nil, err
	}
	out := make([]aitranslator.Provider, 0, len(rows))
	for _, row := range rows {
		out = append(out, aitranslator.Provider{
			ID:          fromPgUUID(row.ID),
			TenantID:    fromPgUUID(row.TenantID),
			Kind:        aitranslator.Kind(row.Kind),
			Label:       row.Label,
			BaseURL:     row.BaseUrl,
			Model:       row.Model,
			APIKeyCT:    row.ApiKeyCt,
			APIKeyNonce: row.ApiKeyNonce,
			Enabled:     true,
		})
	}
	return out, nil
}

// Get loads a provider by id (including ciphertext).
func (r *AIProviderRepo) Get(ctx context.Context, id uuid.UUID) (aitranslator.Provider, error) {
	q := db.QueriesFromContext(ctx, r.q)
	row, err := q.GetAIProvider(ctx, toPgUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return aitranslator.Provider{}, aitranslator.ErrNotFound
		}
		return aitranslator.Provider{}, err
	}
	return aitranslator.Provider{
		ID:          fromPgUUID(row.ID),
		TenantID:    fromPgUUID(row.TenantID),
		Kind:        aitranslator.Kind(row.Kind),
		Label:       row.Label,
		BaseURL:     row.BaseUrl,
		Model:       row.Model,
		APIKeyCT:    row.ApiKeyCt,
		APIKeyNonce: row.ApiKeyNonce,
		Enabled:     row.Enabled,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}, nil
}

// Update mutates the non-secret fields.
func (r *AIProviderRepo) Update(ctx context.Context, id uuid.UUID, label, baseURL, model string, enabled bool) error {
	q := db.QueriesFromContext(ctx, r.q)
	return q.UpdateAIProvider(ctx, db.UpdateAIProviderParams{
		ID:      toPgUUID(id),
		Label:   label,
		BaseUrl: baseURL,
		Model:   model,
		Enabled: enabled,
	})
}

// UpdateKey rotates the encrypted credential.
func (r *AIProviderRepo) UpdateKey(ctx context.Context, id uuid.UUID, ct, nonce []byte) error {
	q := db.QueriesFromContext(ctx, r.q)
	return q.UpdateAIProviderKey(ctx, db.UpdateAIProviderKeyParams{
		ID:          toPgUUID(id),
		ApiKeyCt:    ct,
		ApiKeyNonce: nonce,
	})
}

// Delete removes a provider.
func (r *AIProviderRepo) Delete(ctx context.Context, id uuid.UUID) error {
	q := db.QueriesFromContext(ctx, r.q)
	return q.DeleteAIProvider(ctx, toPgUUID(id))
}
