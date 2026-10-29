// Package httpapi is Knowledge's HTTP edge: its operations of the
// generated /v1 strict server (translation memory, termbase, style
// guides).
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// API serves Knowledge's operations.
type API struct{ svc *app.Service }

// New returns the API.
func New(svc *app.Service) *API { return &API{svc: svc} }

// pathID parses an ID in a URL, where a malformed one is simply not
// found.
func pathID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrNotFound)
	}
	return id, nil
}

// optionalID parses an optional ID in a query or body.
func optionalID(name string, s *string) (*uuid.UUID, error) {
	if s == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil || id == uuid.Nil {
		return nil, mapError(fmt.Errorf("%w: %s is not an id this API issued", app.ErrInvalidQuery, name))
	}
	return &id, nil
}

// optionalLocale parses an optional locale.
func optionalLocale(s *string) (*bcp47.Tag, error) {
	if s == nil {
		return nil, nil
	}
	t, err := bcp47.Parse(*s)
	if err != nil {
		return nil, mapError(err)
	}
	return &t, nil
}

func locale(s string) (bcp47.Tag, error) {
	t, err := bcp47.Parse(s)
	if err != nil {
		return bcp47.Tag{}, mapError(err)
	}
	return t, nil
}

func tenantPath(ctx context.Context, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + sub
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func idPtr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	return apiconv.Ptr(id.String())
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// model renders a message as the MF2 data model JSON object.
func model(m mf.Message) apiv1.MF2Message {
	out := apiv1.MF2Message{}
	if b, err := json.Marshal(m); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func orSyntax(s *apiv1.Syntax, def apiv1.Syntax) apiv1.Syntax {
	if s == nil {
		return def
	}
	return *s
}

// parseMessage parses text in syntax (default MF1) for locale.
func parseMessage(text string, syntax *apiv1.Syntax, loc bcp47.Tag) (mf.Message, error) {
	s, err := mfcontent.ParseSyntax(string(deref(syntax)), mfcontent.MF1)
	if err != nil {
		return mf.Message{}, mapError(err)
	}
	c, err := mfcontent.Parse(s, text, loc)
	if err != nil {
		return mf.Message{}, mapError(err)
	}
	return c.Model, nil
}

// analyzed is the text recognition and checks run over: the text as
// sent, or, with a syntax, the message's visible text.
func analyzed(text string, syntax *apiv1.Syntax, loc bcp47.Tag) (string, error) {
	if syntax == nil {
		return text, nil
	}
	m, err := parseMessage(text, syntax, loc)
	if err != nil {
		return "", err
	}
	return domain.VisibleText(m), nil
}

// codePoints converts a byte offset into s to a code point offset.
func codePoints(s string, byteOffset int) int { return utf8.RuneCountInString(s[:byteOffset]) }

func page(size *int, token *string) (pagination.Page, error) { return pagination.Parse(size, token) }

// ── translation memory ──────────────────────────────────────────────

func toUnit(u domain.TMUnit) apiv1.TMUnit {
	out := apiv1.TMUnit{
		Id: u.ID.String(), ProjectId: idPtr(u.ProjectID), Origin: apiv1.TMUnitOrigin(u.Origin),
		TranslationId: idPtr(u.TranslationID), MessageId: idPtr(u.MessageID), MessageKey: nonEmpty(u.MessageKey),
		Namespace: nonEmpty(u.Namespace), SourceLocale: u.SourceLocale.String(), TargetLocale: u.TargetLocale.String(),
		Source: u.SourceMF2, Target: u.TargetMF2, TargetModel: model(u.Target), SourceNormalized: u.SourceNorm.Text,
		Signature: u.SourceNorm.Signature, State: "active", HitCount: u.HitCount, LastHitAt: u.LastHitAt,
		CreatedBy: u.CreatedBy, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, RetiredAt: u.RetiredAt,
		RetiredBy: nonEmpty(u.RetiredBy),
	}
	if u.TranslationRevision > 0 {
		out.TranslationRevision = apiconv.Ptr(u.TranslationRevision)
	}
	if !u.Active() {
		out.State = "retired"
		out.RetiredReason = apiconv.Ptr(apiv1.TMUnitRetiredReason(u.RetiredReason))
	}
	return out
}

func (a *API) LookupTranslationMemory(ctx context.Context, req apiv1.LookupTranslationMemoryRequestObject) (apiv1.LookupTranslationMemoryResponseObject, error) {
	b := req.Body
	src, err := locale(b.SourceLocale)
	if err != nil {
		return nil, err
	}
	tgt, err := locale(b.TargetLocale)
	if err != nil {
		return nil, err
	}
	project, err := optionalID("project_id", b.ProjectId)
	if err != nil {
		return nil, err
	}
	source, err := parseMessage(b.Source, b.Syntax, src)
	if err != nil {
		return nil, err
	}
	// The targets come in the query's syntax unless another is asked for.
	targetSyntax := orSyntax(b.TargetSyntax, orSyntax(b.Syntax, apiv1.Mf1))
	ms, err := a.svc.LookupTM(ctx, app.TMQuery{
		ProjectID: project, AllProjects: deref(b.AllProjects), SourceLocale: src, TargetLocale: tgt, Source: source,
		MessageKey: deref(b.MessageKey), Namespace: deref(b.Namespace), Limit: deref(b.Limit),
		MinScore: deref(b.MinScore), CountHits: deref(b.CountHits), TargetSyntax: mfcontent.Syntax(targetSyntax),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.LookupTranslationMemory200JSONResponse{
		SourceNormalized: domain.Normalize(source).Text, Matches: make([]apiv1.TMMatch, len(ms)),
	}
	for i, m := range ms {
		out.Matches[i] = apiv1.TMMatch{
			Score: m.Score, Kind: apiv1.TMMatchKind(m.Kind), Target: m.TargetMF2, TargetModel: model(m.Target),
			TargetText: m.TargetText, TargetSyntax: apiv1.Syntax(m.TargetSyntax), TargetSyntaxFallback: m.SyntaxFallback,
			VariablesAdapted: m.Adapted, Unit: toUnit(m.Unit),
		}
	}
	return out, nil
}

// ListUnitTMMatches is the translation workspace's TM read: one unit's
// matches, as text and score — never the unit they came from — and, for
// an `assigned` member, never a key either (RFC 0006 §3.3, §9).
func (a *API) ListUnitTMMatches(ctx context.Context, req apiv1.ListUnitTMMatchesRequestObject) (apiv1.ListUnitTMMatchesResponseObject, error) {
	project, err := pathID(req.Project)
	if err != nil {
		return nil, err
	}
	loc, err := bcp47.Parse(req.Locale)
	if err != nil {
		return nil, mapError(app.ErrNotFound)
	}
	q := app.UnitTMQuery{Project: project, Key: req.Message, Locale: loc, Limit: deref(req.Params.Limit), MinScore: deref(req.Params.MinScore)}
	if req.Params.TargetSyntax != nil {
		q.TargetSyntax = mfcontent.Syntax(*req.Params.TargetSyntax)
	}
	r, err := a.svc.UnitTMMatches(ctx, q)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListUnitTMMatches200JSONResponse{SourceNormalized: r.SourceNormalized, Items: make([]apiv1.UnitTMMatch, len(r.Matches))}
	for i, m := range r.Matches {
		out.Items[i] = apiv1.UnitTMMatch{
			Score: m.Score, Kind: apiv1.UnitTMMatchKind(m.Kind), SourceNormalized: m.SourceNormalized, Target: m.TargetMF2,
			TargetText: m.TargetText, TargetSyntax: apiv1.Syntax(m.TargetSyntax), TargetSyntaxFallback: m.SyntaxFallback,
			VariablesAdapted: m.Adapted, ProjectScoped: m.ProjectScoped,
		}
		if m.MessageKey != "" {
			key := m.MessageKey
			out.Items[i].MessageKey = &key
		}
	}
	return out, nil
}

func (a *API) SearchTranslationMemory(ctx context.Context, req apiv1.SearchTranslationMemoryRequestObject) (apiv1.SearchTranslationMemoryResponseObject, error) {
	p := req.Params
	src, err := optionalLocale(p.SourceLocale)
	if err != nil {
		return nil, err
	}
	tgt, err := optionalLocale(p.TargetLocale)
	if err != nil {
		return nil, err
	}
	project, err := optionalID("project", p.Project)
	if err != nil {
		return nil, err
	}
	ms, err := a.svc.Concordance(ctx, app.ConcordanceQuery{
		Query: p.Q, Side: domain.Side(deref(p.Side)), SourceLocale: src, TargetLocale: tgt, ProjectID: project,
		AllProjects: deref(p.AllProjects), Limit: deref(p.Limit),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.SearchTranslationMemory200JSONResponse{Matches: make([]apiv1.TMConcordanceMatch, len(ms))}
	for i, m := range ms {
		out.Matches[i] = apiv1.TMConcordanceMatch{Similarity: float32(m.Similarity), Unit: toUnit(m.Unit)}
	}
	return out, nil
}

func (a *API) ListTranslationMemoryUnits(ctx context.Context, req apiv1.ListTranslationMemoryUnitsRequestObject) (apiv1.ListTranslationMemoryUnitsResponseObject, error) {
	p := req.Params
	pg, err := page(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	f := app.UnitFilter{State: string(deref(p.State))}
	if f.SourceLocale, err = optionalLocale(p.SourceLocale); err != nil {
		return nil, err
	}
	if f.TargetLocale, err = optionalLocale(p.TargetLocale); err != nil {
		return nil, err
	}
	if f.ProjectID, err = optionalID("project", p.Project); err != nil {
		return nil, err
	}
	if f.TranslationID, err = optionalID("translation", p.Translation); err != nil {
		return nil, err
	}
	us, next, err := a.svc.ListUnits(ctx, f, pg)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListTranslationMemoryUnits200JSONResponse{Items: make([]apiv1.TMUnit, len(us)), NextPageToken: next}
	for i, u := range us {
		out.Items[i] = toUnit(u)
	}
	return out, nil
}

func (a *API) GetTranslationMemoryUnit(ctx context.Context, req apiv1.GetTranslationMemoryUnitRequestObject) (apiv1.GetTranslationMemoryUnitResponseObject, error) {
	id, err := pathID(req.Unit)
	if err != nil {
		return nil, err
	}
	u, err := a.svc.GetUnit(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetTranslationMemoryUnit200JSONResponse(toUnit(u)), nil
}

func (a *API) RetireTranslationMemoryUnit(ctx context.Context, req apiv1.RetireTranslationMemoryUnitRequestObject) (apiv1.RetireTranslationMemoryUnitResponseObject, error) {
	id, err := pathID(req.Unit)
	if err != nil {
		return nil, err
	}
	if err := a.svc.RetireUnit(ctx, id); err != nil {
		return nil, mapError(err)
	}
	return apiv1.RetireTranslationMemoryUnit204Response{}, nil
}
