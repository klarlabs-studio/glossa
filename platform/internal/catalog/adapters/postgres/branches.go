package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres/catalogsql"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// ── branches ────────────────────────────────────────────────────────

func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func (s *store) InsertBranch(ctx context.Context, b domain.Branch, by domain.Author) (bool, error) {
	n, err := s.q.InsertBranch(ctx, catalogsql.InsertBranchParams{
		ID: b.ID.UUID(), ProjectID: b.ProjectID.UUID(), Name: string(b.Name), PrNumber: maxLength(b.PR),
		HeadCommit: b.HeadCommit, State: string(b.State), PreviewUrl: b.PreviewURL, ClosedAt: timestamptz(b.ClosedAt),
		RemovedKeys: keyStrings(b.RemovedKeys), InvalidItems: invalidItemsJSON(b.Invalid),
		Version: int32Of(b.Version), CreatedBy: string(by),
		CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	})
	return n == 1, storeError(err)
}

func branch(r catalogsql.CatalogBranch) domain.Branch {
	b := domain.Branch{
		ID: domain.BranchID(r.ID), ProjectID: domain.ProjectID(r.ProjectID), Name: domain.BranchName(r.Name),
		HeadCommit: r.HeadCommit, State: domain.BranchState(r.State), PreviewURL: r.PreviewUrl,
		Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
	if r.PrNumber.Valid {
		pr := int(r.PrNumber.Int32)
		b.PR = &pr
	}
	if r.ClosedAt.Valid {
		at := r.ClosedAt.Time.UTC()
		b.ClosedAt = &at
	}
	for _, k := range r.RemovedKeys {
		b.RemovedKeys = append(b.RemovedKeys, domain.MessageKey(k))
	}
	// An unreadable list is a report we lost, never a branch we cannot
	// load: the branch itself is still correct without it.
	_ = json.Unmarshal(r.InvalidItems, &b.Invalid)
	return b
}

// invalidItemsJSON marshals the last push's rejected items, always as an
// array so the column's check constraint holds.
func invalidItemsJSON(items []domain.InvalidItem) json.RawMessage {
	if len(items) == 0 {
		return json.RawMessage("[]")
	}
	b, err := json.Marshal(items)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

func (s *store) Branch(ctx context.Context, project domain.ProjectID, name domain.BranchName) (domain.Branch, error) {
	row, err := s.q.GetBranch(ctx, catalogsql.GetBranchParams{ProjectID: project.UUID(), Name: string(name)})
	if err != nil {
		return domain.Branch{}, storeError(err)
	}
	return branch(row), nil
}

func (s *store) LockBranch(ctx context.Context, project domain.ProjectID, name domain.BranchName) (domain.Branch, error) {
	row, err := s.q.LockBranch(ctx, catalogsql.LockBranchParams{ProjectID: project.UUID(), Name: string(name)})
	if err != nil {
		return domain.Branch{}, storeError(err)
	}
	return branch(row), nil
}

func (s *store) BranchByID(ctx context.Context, project domain.ProjectID, id domain.BranchID) (domain.Branch, error) {
	row, err := s.q.GetBranchByID(ctx, catalogsql.GetBranchByIDParams{ProjectID: project.UUID(), ID: id.UUID()})
	if err != nil {
		return domain.Branch{}, storeError(err)
	}
	return branch(row), nil
}

func (s *store) Branches(ctx context.Context, project domain.ProjectID, state domain.BranchState, after domain.BranchName, limit int) ([]domain.Branch, error) {
	rows, err := s.q.ListBranches(ctx, catalogsql.ListBranchesParams{
		ProjectID: project.UUID(), After: string(after), State: pgtype.Text{String: string(state), Valid: state != ""},
		MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Branch, len(rows))
	for i, r := range rows {
		out[i] = branch(r)
	}
	return out, nil
}

func (s *store) BranchesByIDs(ctx context.Context, ids []domain.BranchID) (map[domain.BranchID]domain.Branch, error) {
	uuids := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		uuids[i] = id.UUID()
	}
	rows, err := s.q.GetBranchesByIDs(ctx, uuids)
	if err != nil {
		return nil, storeError(err)
	}
	out := make(map[domain.BranchID]domain.Branch, len(rows))
	for _, r := range rows {
		out[domain.BranchID(r.ID)] = branch(r)
	}
	return out, nil
}

func (s *store) ClosedBranches(ctx context.Context, project domain.ProjectID) (map[domain.BranchName]time.Time, error) {
	rows, err := s.q.ListClosedBranches(ctx, project.UUID())
	if err != nil {
		return nil, storeError(err)
	}
	out := make(map[domain.BranchName]time.Time, len(rows))
	for _, r := range rows {
		if r.ClosedAt.Valid {
			out[domain.BranchName(r.Name)] = r.ClosedAt.Time.UTC()
		}
	}
	return out, nil
}

func (s *store) UpdateBranch(ctx context.Context, b domain.Branch, expected int) error {
	n, err := s.q.UpdateBranch(ctx, catalogsql.UpdateBranchParams{
		ID: b.ID.UUID(), PrNumber: maxLength(b.PR), HeadCommit: b.HeadCommit, State: string(b.State),
		PreviewUrl: b.PreviewURL, ClosedAt: timestamptz(b.ClosedAt), RemovedKeys: keyStrings(b.RemovedKeys),
		InvalidItems: invalidItemsJSON(b.Invalid),
		Version:      int32Of(b.Version), UpdatedAt: b.UpdatedAt, ExpectedVersion: int32Of(expected),
	})
	return affected(n, err)
}

// ── proposals ───────────────────────────────────────────────────────

func (s *store) SaveProposal(ctx context.Context, p domain.Proposal) error {
	var base pgtype.Int4
	if p.Kind == domain.ProposalSourceChange {
		base = pgtype.Int4{Int32: int32Of(p.BaseRevision), Valid: true}
	}
	return storeError(s.q.UpsertProposal(ctx, catalogsql.UpsertProposalParams{
		BranchID: p.BranchID.UUID(), Key: string(p.Key), MessageID: p.MessageID.UUID(), Kind: string(p.Kind),
		Syntax: string(p.Source.Syntax), Text: p.Source.Text, Model: p.Source.ModelJSON(), BaseRevision: base,
		Author: string(p.Author), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}))
}

func (s *store) DeleteProposal(ctx context.Context, b domain.BranchID, key domain.MessageKey) error {
	return storeError(s.q.DeleteProposal(ctx, catalogsql.DeleteProposalParams{BranchID: b.UUID(), Key: string(key)}))
}

// DeleteProposals implements app.Store.
func (s *store) DeleteProposals(ctx context.Context, ps []domain.Proposal) error {
	if len(ps) == 0 {
		return nil
	}
	p := catalogsql.DeleteProposalsParams{BranchIds: make([]uuid.UUID, len(ps)), Keys: make([]string, len(ps))}
	for i, pr := range ps {
		p.BranchIds[i], p.Keys[i] = pr.BranchID.UUID(), string(pr.Key)
	}
	return storeError(s.q.DeleteProposals(ctx, p))
}

func proposals(rows []catalogsql.CatalogProposal) ([]domain.Proposal, error) {
	out := make([]domain.Proposal, 0, len(rows))
	for _, r := range rows {
		c, err := mfcontent.Restore(mfcontent.Syntax(r.Syntax), r.Text, r.Model)
		if err != nil {
			return nil, fmt.Errorf("catalog: stored proposal for %s on branch %s: %w", r.Key, r.BranchID, err)
		}
		out = append(out, domain.Proposal{
			BranchID: domain.BranchID(r.BranchID), Key: domain.MessageKey(r.Key), MessageID: domain.MessageID(r.MessageID),
			Kind: domain.ProposalKind(r.Kind), Source: c, BaseRevision: int(r.BaseRevision.Int32),
			Author: domain.Author(r.Author), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
		})
	}
	return out, nil
}

func (s *store) BranchProposals(ctx context.Context, b domain.BranchID) ([]domain.Proposal, error) {
	rows, err := s.q.ListBranchProposals(ctx, b.UUID())
	if err != nil {
		return nil, storeError(err)
	}
	return proposals(rows)
}

func (s *store) BranchProposalsPage(ctx context.Context, b domain.BranchID, after domain.MessageKey, limit int) ([]domain.Proposal, error) {
	rows, err := s.q.ListBranchProposalsPage(ctx, catalogsql.ListBranchProposalsPageParams{
		BranchID: b.UUID(), After: string(after), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	return proposals(rows)
}

func messageUUIDs(ids []domain.MessageID) []uuid.UUID {
	out := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		out[i] = id.UUID()
	}
	return out
}

func (s *store) ProposalsForMessages(ctx context.Context, ids []domain.MessageID) ([]domain.Proposal, error) {
	rows, err := s.q.ListProposalsForMessages(ctx, messageUUIDs(ids))
	if err != nil {
		return nil, storeError(err)
	}
	return proposals(rows)
}

func (s *store) LockMessagesByIDs(ctx context.Context, project domain.ProjectID, ids []domain.MessageID) (map[domain.MessageID]domain.Message, error) {
	rows, err := s.q.LockMessagesByIDs(ctx, catalogsql.LockMessagesByIDsParams{ProjectID: project.UUID(), Ids: messageUUIDs(ids)})
	if err != nil {
		return nil, storeError(err)
	}
	ms, err := messages(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.MessageID]domain.Message, len(ms))
	for _, m := range ms {
		out[m.ID] = m
	}
	return out, nil
}

func (s *store) ActiveKeysExcept(ctx context.Context, project domain.ProjectID, keys []domain.MessageKey) ([]domain.MessageKey, error) {
	rows, err := s.q.ListActiveKeysExcept(ctx, catalogsql.ListActiveKeysExceptParams{ProjectID: project.UUID(), Keys: keyStrings(keys)})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.MessageKey, len(rows))
	for i, k := range rows {
		out[i] = domain.MessageKey(k)
	}
	return out, nil
}

func (s *store) LockOrphanedProposedMessages(ctx context.Context) ([]domain.Message, error) {
	rows, err := s.q.LockOrphanedProposedMessages(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	return messages(rows)
}
