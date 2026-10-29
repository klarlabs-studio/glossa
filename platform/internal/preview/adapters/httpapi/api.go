// Package httpapi is the message preview's HTTP edge: POST
// /v1/message-previews on the generated /v1 strict server.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/preview/app"
)

// API serves the preview operation.
type API struct{ svc *app.Service }

// New returns the API.
func New(svc *app.Service) *API { return &API{svc: svc} }

func (a *API) PreviewMessage(ctx context.Context, req apiv1.PreviewMessageRequestObject) (apiv1.PreviewMessageResponseObject, error) {
	in := app.Input{Source: req.Body.Source, Locale: req.Body.Locale, BidiIsolation: req.Body.BidiIsolation}
	if req.Body.Syntax != nil {
		in.Syntax = string(*req.Body.Syntax)
	}
	if req.Body.Values != nil {
		in.Values = *req.Body.Values
	}
	res, err := a.svc.Preview(ctx, in)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.PreviewMessage200JSONResponse{
		Valid: res.Valid(), Arguments: []apiv1.Argument{}, Markup: []apiv1.MarkupElement{}, Formatted: res.Formatted,
		Errors: make([]apiv1.MessagePreviewError, len(res.Errors)),
	}
	if res.Valid() { // an invalid source has no model to render
		c := apiconv.Content(res.Content)
		out.Message, out.Mf2, out.Arguments, out.Markup = &c.Model, &res.MF2, c.Arguments, c.Markup
	}
	for i, e := range res.Errors {
		out.Errors[i] = apiv1.MessagePreviewError{Stage: apiv1.MessagePreviewErrorStage(e.Stage), Code: e.Code, Message: e.Message}
	}
	return out, nil
}

// problems maps the preview's errors to the codes documented in
// api/openapi.yaml; a code is part of the contract.
var problems = []struct {
	err    error
	status int
	code   problem.Code
}{
	{mfcontent.ErrTooLong, http.StatusBadRequest, "message_too_long"},
	{mfcontent.ErrInvalidSyntax, http.StatusBadRequest, "invalid_syntax"},
	{bcp47.ErrInvalid, http.StatusBadRequest, "invalid_locale"},
	{app.ErrInvalidValues, http.StatusBadRequest, "invalid_values"},
	{app.ErrRateLimited, http.StatusTooManyRequests, problem.CodeRateLimited},
}

// mapError turns the preview's errors into problem details; others
// (authentication) pass through to the shared error hook.
func mapError(err error) error {
	for _, p := range problems {
		if errors.Is(err, p.err) {
			return problem.New(p.status, p.code, err.Error())
		}
	}
	return err
}
