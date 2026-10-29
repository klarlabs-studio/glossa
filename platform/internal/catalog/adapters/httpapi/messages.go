package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

func toMessage(m domain.Message) apiv1.Message {
	return apiv1.Message{
		Id: m.ID.String(), Key: string(m.Key), Namespace: string(m.Namespace), Description: m.Description,
		MaxLength: m.MaxLength, State: apiv1.MessageState(m.State), Source: apiconv.Content(m.Source),
		SourceRevision: m.Revision, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func syntax(s *apiv1.Syntax) string {
	if s == nil {
		return ""
	}
	return string(*s)
}

func (a *API) ListMessages(ctx context.Context, req apiv1.ListMessagesRequestObject) (apiv1.ListMessagesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	q := app.MessageQuery{
		Namespace: str(req.Params.Namespace), KeyPrefix: str(req.Params.KeyPrefix),
		MissingIn: str(req.Params.MissingIn), OutdatedIn: str(req.Params.OutdatedIn),
	}
	if req.Params.State != nil {
		q.State = string(*req.Params.State)
	}
	ms, next, err := a.svc.ListMessages(ctx, project, q, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListMessages200JSONResponse{Items: make([]apiv1.Message, len(ms)), NextPageToken: next}
	for i, m := range ms {
		out.Items[i] = toMessage(m)
	}
	return out, nil
}

func (a *API) ListNamespaces(ctx context.Context, req apiv1.ListNamespacesRequestObject) (apiv1.ListNamespacesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	ns, next, err := a.svc.ListNamespaces(ctx, project, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListNamespaces200JSONResponse{Items: make([]apiv1.NamespaceSummary, len(ns)), NextPageToken: next}
	for i, n := range ns {
		out.Items[i] = apiv1.NamespaceSummary{Name: string(n.Name), ActiveMessages: n.Active, ObsoleteMessages: n.Obsolete}
	}
	return out, nil
}

func (a *API) CreateMessage(ctx context.Context, req apiv1.CreateMessageRequestObject) (apiv1.CreateMessageResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	var key string
	if req.Params.IdempotencyKey != nil {
		key = *req.Params.IdempotencyKey
	}
	m, replayed, err := a.svc.CreateMessage(ctx, project, app.NewMessage{
		Key: req.Body.Key, Namespace: str(req.Body.Namespace), Description: str(req.Body.Description),
		MaxLength: req.Body.MaxLength, Text: req.Body.Text, Syntax: syntax(req.Body.Syntax),
	}, key)
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateMessage201ResponseHeaders{
		ETag: apiconv.ETag(m.Version), Location: apiconv.Ptr(projectPath(ctx, project, "/messages/"+string(m.Key))),
	}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateMessage201JSONResponse{Body: toMessage(m), Headers: h}, nil
}

func (a *API) GetMessage(ctx context.Context, req apiv1.GetMessageRequestObject) (apiv1.GetMessageResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.GetMessage(ctx, project, req.Message)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetMessage200JSONResponse{Body: toMessage(m), Headers: apiv1.GetMessage200ResponseHeaders{ETag: apiconv.ETag(m.Version)}}, nil
}

func (a *API) UpdateMessage(ctx context.Context, req apiv1.UpdateMessageRequestObject) (apiv1.UpdateMessageResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.UpdateMessage(ctx, project, req.Message, ifMatch, app.MessageChange{
		Namespace: req.Body.Namespace, Description: req.Body.Description, MaxLength: req.Body.MaxLength,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.UpdateMessage200JSONResponse{Body: toMessage(m), Headers: apiv1.UpdateMessage200ResponseHeaders{ETag: apiconv.ETag(m.Version)}}, nil
}

func (a *API) ReviseMessageSource(ctx context.Context, req apiv1.ReviseMessageSourceRequestObject) (apiv1.ReviseMessageSourceResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.ReviseSource(ctx, project, req.Message, ifMatch, req.Body.Text, syntax(req.Body.Syntax))
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ReviseMessageSource200JSONResponse{Body: toMessage(m), Headers: apiv1.ReviseMessageSource200ResponseHeaders{ETag: apiconv.ETag(m.Version)}}, nil
}

func (a *API) ObsoleteMessage(ctx context.Context, req apiv1.ObsoleteMessageRequestObject) (apiv1.ObsoleteMessageResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.ObsoleteMessage(ctx, project, req.Message, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ObsoleteMessage200JSONResponse{Body: toMessage(m), Headers: apiv1.ObsoleteMessage200ResponseHeaders{ETag: apiconv.ETag(m.Version)}}, nil
}

func (a *API) RenameMessage(ctx context.Context, req apiv1.RenameMessageRequestObject) (apiv1.RenameMessageResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.RenameMessage(ctx, project, req.Message, req.Body.Key, ifMatch)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.RenameMessage200JSONResponse{Body: toMessage(m), Headers: apiv1.RenameMessage200ResponseHeaders{
		ETag: apiconv.ETag(m.Version), Location: apiconv.Ptr(projectPath(ctx, project, "/messages/"+string(m.Key))),
	}}, nil
}

func (a *API) ListSourceRevisions(ctx context.Context, req apiv1.ListSourceRevisionsRequestObject) (apiv1.ListSourceRevisionsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	revs, next, err := a.svc.SourceRevisions(ctx, project, req.Message, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListSourceRevisions200JSONResponse{Items: make([]apiv1.SourceRevision, len(revs)), NextPageToken: next}
	for i, r := range revs {
		out.Items[i] = apiv1.SourceRevision{
			Revision: r.Number, Text: r.Content.Text, Syntax: apiv1.Syntax(r.Content.Syntax),
			Model: apiconv.Model(r.Content), Author: string(r.Author), CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

func (a *API) UpsertMessages(ctx context.Context, req apiv1.UpsertMessagesRequestObject) (apiv1.UpsertMessagesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	items := make([]app.UpsertItem, len(req.Body.Items))
	for i, it := range req.Body.Items {
		items[i] = app.UpsertItem{
			Key: it.Key, Text: it.Text, Syntax: syntax(it.Syntax), Namespace: it.Namespace,
			Description: it.Description, MaxLength: it.MaxLength, BaseRevision: it.BaseRevision,
		}
	}
	results, err := a.svc.UpsertMessages(ctx, project, items)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.UpsertMessages200JSONResponse{Results: make([]apiv1.MessageUpsertItemResult, len(results))}
	for i, r := range results {
		res := apiv1.MessageUpsertItemResult{Key: r.Key, Status: apiv1.MessageUpsertItemResultStatus(r.Status)}
		if r.Message != nil {
			res.Message = apiconv.Ptr(toMessage(*r.Message))
		}
		if r.Error != nil {
			res.Error = &apiv1.ItemError{Code: r.Error.Code, Detail: r.Error.Detail}
		}
		out.Results[i] = res
	}
	return out, nil
}
