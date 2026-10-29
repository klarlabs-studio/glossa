package tools_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// The fakes below stand in for the bounded contexts' application
// services. Each one keys its data by tenant and reads the tenant from
// the context, which is exactly what row-level security does in
// Postgres: a project of another tenant is not refused, it is not
// there. That is what makes the isolation tests real rather than a
// restatement of the tools' own arguments.

// scoped reports whether ctx is scoped to tenant.
func scoped(ctx context.Context, tenant tenancy.ID) bool {
	got, ok := tenancy.FromContext(ctx)
	return ok && got == tenant
}

// ── Catalog ─────────────────────────────────────────────────────────

type catalogRow struct {
	tenant  tenancy.ID
	project uuid.UUID
	unused  bool
	detail  tools.MessageDetail
}

type fakeCatalog struct {
	rows []catalogRow
	// projects is which tenant owns each project. Catalog reads the
	// project before it lists anything, so a project this tenant cannot
	// see is a not-found and not an empty page — the difference matters,
	// because an empty page reads like "this project has no messages".
	projects map[uuid.UUID]tenancy.ID
}

func (f *fakeCatalog) exists(ctx context.Context, project uuid.UUID) bool {
	owner, ok := f.projects[project]
	return ok && scoped(ctx, owner)
}

func (f *fakeCatalog) SearchMessages(
	ctx context.Context, project uuid.UUID, filter tools.CatalogFilter, cursor string, limit int,
) ([]tools.MessageSummary, string, error) {
	if !f.exists(ctx, project) {
		return nil, "", tools.ErrNotFound
	}
	var out []tools.MessageSummary
	for _, r := range f.visible(ctx, project) {
		switch {
		case !strings.HasPrefix(r.detail.Key, filter.KeyPrefix),
			filter.Namespace != "" && r.detail.Namespace != filter.Namespace,
			filter.State != "" && r.detail.State != filter.State,
			filter.Unused && !r.unused,
			r.detail.Key <= cursor:
			continue
		}
		out = append(out, r.detail.MessageSummary)
	}
	slices.SortFunc(out, func(a, b tools.MessageSummary) int { return strings.Compare(a.Key, b.Key) })
	if len(out) > limit {
		return out[:limit], out[limit-1].Key, nil
	}
	return out, "", nil
}

func (f *fakeCatalog) Message(ctx context.Context, project uuid.UUID, key string) (tools.MessageDetail, error) {
	for _, r := range f.visible(ctx, project) {
		if r.detail.Key == key {
			return r.detail, nil
		}
	}
	return tools.MessageDetail{}, tools.ErrNotFound
}

// visible is the fake's row-level security: only rows of the tenant the
// context is scoped to, and only of the project asked for.
func (f *fakeCatalog) visible(ctx context.Context, project uuid.UUID) []catalogRow {
	var out []catalogRow
	for _, r := range f.rows {
		if scoped(ctx, r.tenant) && r.project == project {
			out = append(out, r)
		}
	}
	return out
}

// ── Localization ────────────────────────────────────────────────────

type translationRow struct {
	tenant  tenancy.ID
	project uuid.UUID
	tr      tools.Translation
}

type fakeTranslations struct{ rows []translationRow }

func (f *fakeTranslations) Translation(
	ctx context.Context, project uuid.UUID, key, locale string,
) (tools.Translation, error) {
	for _, r := range f.rows {
		if scoped(ctx, r.tenant) && r.project == project && r.tr.Key == key && r.tr.Locale == locale {
			return r.tr, nil
		}
	}
	return tools.Translation{}, tools.ErrNotFound
}

// ── Context ─────────────────────────────────────────────────────────

type usageRow struct {
	tenant  tenancy.ID
	project uuid.UUID
	key     string
	message uuid.UUID
	usages  []tools.Usage
	// captures is how many current captures show the message.
	captures   int
	neighbours []tools.Neighbour
}

type fakeUsages struct{ rows []usageRow }

func (f *fakeUsages) row(ctx context.Context, project uuid.UUID, key string) (usageRow, bool) {
	for _, r := range f.rows {
		if scoped(ctx, r.tenant) && r.project == project && r.key == key {
			return r, true
		}
	}
	return usageRow{}, false
}

func (f *fakeUsages) OfKey(
	ctx context.Context, project uuid.UUID, key, branch string, limit int,
) (tools.Usages, error) {
	r, ok := f.row(ctx, project, key)
	if !ok {
		return tools.Usages{}, tools.ErrNotFound
	}
	out := tools.Usages{MessageID: r.message.String(), Key: key, Branch: branch, Usages: r.usages}
	if len(out.Usages) > limit {
		out.Usages, out.Truncated = out.Usages[:limit], true
	}
	return out, nil
}

func (f *fakeUsages) CaptureCount(ctx context.Context, project uuid.UUID, key string, limit int) (int, bool, error) {
	r, ok := f.row(ctx, project, key)
	if !ok {
		return 0, false, tools.ErrNotFound
	}
	if r.captures > limit {
		return limit, true, nil
	}
	return r.captures, false, nil
}

func (f *fakeUsages) Neighbours(
	ctx context.Context, project, message uuid.UUID, limit int,
) ([]tools.Neighbour, error) {
	for _, r := range f.rows {
		if scoped(ctx, r.tenant) && r.project == project && r.message == message {
			if len(r.neighbours) > limit {
				return r.neighbours[:limit], nil
			}
			return r.neighbours, nil
		}
	}
	return nil, tools.ErrNotFound
}

// ── Knowledge ───────────────────────────────────────────────────────

type knowledgeRow struct {
	tenant  tenancy.ID
	project uuid.UUID
	matches []tools.TMMatch
	terms   []tools.Term
	style   tools.Style
}

type fakeKnowledge struct {
	rows []knowledgeRow
	// lastTM records the query the tool passed on, so a test can assert
	// the limit it enforced actually reached the context.
	lastTM tools.TMSearch
}

func (f *fakeKnowledge) row(ctx context.Context, project uuid.UUID) (knowledgeRow, bool) {
	for _, r := range f.rows {
		if scoped(ctx, r.tenant) && r.project == project {
			return r, true
		}
	}
	return knowledgeRow{}, false
}

func (f *fakeKnowledge) SearchTM(ctx context.Context, q tools.TMSearch) ([]tools.TMMatch, error) {
	f.lastTM = q
	r, ok := f.row(ctx, q.Project)
	if !ok {
		return nil, tools.ErrNotFound
	}
	out := r.matches
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (f *fakeKnowledge) LookupTerms(ctx context.Context, q tools.TermSearch) ([]tools.Term, error) {
	r, ok := f.row(ctx, q.Project)
	if !ok {
		return nil, tools.ErrNotFound
	}
	return r.terms, nil
}

func (f *fakeKnowledge) Style(ctx context.Context, project uuid.UUID, _, _ string) (tools.Style, error) {
	r, ok := f.row(ctx, project)
	if !ok {
		return tools.Style{}, tools.ErrNotFound
	}
	return r.style, nil
}

// ── Quality ─────────────────────────────────────────────────────────

type qualityRow struct {
	tenant   tenancy.ID
	project  uuid.UUID
	run      tools.CheckRun
	findings []tools.Finding
}

type fakeQuality struct {
	rows []qualityRow
	// lastQuery records what the tool asked for.
	lastQuery tools.FindingsQuery
}

func (f *fakeQuality) Findings(
	ctx context.Context, project uuid.UUID, q tools.FindingsQuery,
) (tools.CheckRun, []tools.Finding, string, error) {
	f.lastQuery = q
	for _, r := range f.rows {
		if !scoped(ctx, r.tenant) || r.project != project {
			continue
		}
		var out []tools.Finding
		for _, fi := range r.findings {
			if q.Layer != "" && fi.Layer != q.Layer {
				continue
			}
			if q.Locale != "" && fi.Locale != q.Locale {
				continue
			}
			out = append(out, fi)
		}
		if len(out) > q.Limit {
			return r.run, out[:q.Limit], out[q.Limit-1].Fingerprint, nil
		}
		return r.run, out, "", nil
	}
	return tools.CheckRun{}, nil, "", tools.ErrNotFound
}

// ── Release ─────────────────────────────────────────────────────────

type deliveryRow struct {
	tenant      tenancy.ID
	project     uuid.UUID
	environment string
	served      tools.Served
	// carried is locale → namespace → the keys that artifact holds.
	carried map[string]map[string][]string
}

type fakeDelivery struct{ rows []deliveryRow }

func (f *fakeDelivery) row(ctx context.Context, project uuid.UUID) (deliveryRow, bool) {
	for _, r := range f.rows {
		if scoped(ctx, r.tenant) && r.project == project {
			return r, true
		}
	}
	return deliveryRow{}, false
}

func (f *fakeDelivery) Served(
	ctx context.Context, project uuid.UUID, environment string, _ uuid.UUID,
) (tools.Served, error) {
	r, ok := f.row(ctx, project)
	if !ok || r.environment != environment {
		return tools.Served{}, tools.ErrNotFound
	}
	return r.served, nil
}

func (f *fakeDelivery) ArtifactHasMessage(
	ctx context.Context, project, _ uuid.UUID, digest, key string,
) (bool, error) {
	r, ok := f.row(ctx, project)
	if !ok {
		return false, tools.ErrNotFound
	}
	for locale, namespaces := range r.carried {
		for ns, keys := range namespaces {
			if r.served.Artifacts[locale][ns] == digest && slices.Contains(keys, key) {
				return true, nil
			}
		}
	}
	return false, nil
}

// jsonOf re-marshals a tool's payload so a test can read it as data.
func jsonOf(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}
