package main

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
)

// signInFacts is what GET /v1/meta asks Identity.
type signInFacts interface {
	SignInMethods() []string
	EmailEnabled() bool
}

// metaAPI answers GET /v1/meta: facts about this deployment clients
// adapt to before signing in, gathered from Identity and the
// configuration. It holds no state of its own.
type metaAPI struct {
	signIn  signInFacts
	edgeURL string
}

func (m *metaAPI) GetMeta(context.Context, apiv1.GetMetaRequestObject) (apiv1.GetMetaResponseObject, error) {
	methods := m.signIn.SignInMethods()
	out := apiv1.GetMeta200JSONResponse{
		SignInMethods: make([]apiv1.MetaSignInMethods, len(methods)),
		EmailDelivery: m.signIn.EmailEnabled(),
	}
	for i, method := range methods {
		out.SignInMethods[i] = apiv1.MetaSignInMethods(method)
	}
	if m.edgeURL != "" {
		out.EdgeUrl = &m.edgeURL
	}
	return out, nil
}
