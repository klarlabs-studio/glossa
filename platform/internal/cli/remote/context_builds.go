package remote

import (
	"bytes"
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// ContextBuild is a usages upload as the Context API stores it (RFC
// 0004 §2.2), re-exported so commands don't import the generated
// package.
type ContextBuild = apiclient.ContextBuild

// UploadedBuild is the answer to a usages upload.
type UploadedBuild struct {
	Build ContextBuild
	// Replayed is true when the same document was uploaded before: the
	// build is the first upload's.
	Replayed bool
}

// Usage sources: the collector that wrote a document.
const (
	SourcePlugin  = string(apiclient.ContextSourcePlugin)
	SourceExtract = string(apiclient.ContextSourceExtract)
)

// UploadUsages posts a glossa.usages/v1 document to the project's
// context builds (POST …/context-builds?source=…). source is the
// collector (plugin, extract, runtime, capture); whether the build is of
// the default branch is the project's setting. The upload is idempotent
// by the document's digest, so it is retried like a GET.
func (c *Client) UploadUsages(ctx context.Context, s Scope, doc []byte, source string) (UploadedBuild, error) {
	r, err := c.api.CreateContextBuildWithBodyWithResponse(idempotent(ctx), s.Tenant, s.Project,
		&apiclient.CreateContextBuildParams{Source: apiclient.ContextSource(source)}, "application/json", bytes.NewReader(doc))
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/context-builds", s.Tenant, s.Project)); err != nil {
		return UploadedBuild{}, err
	}
	if r.JSON201 != nil {
		return UploadedBuild{Build: *r.JSON201}, nil
	}
	if r.JSON200 != nil {
		return UploadedBuild{Build: *r.JSON200, Replayed: true}, nil
	}
	return UploadedBuild{}, &APIError{Method: http.MethodPost, URL: c.path("/v1/tenants/%s/projects/%s/context-builds", s.Tenant, s.Project),
		Status: r.StatusCode(), Code: "unexpected_response", Detail: "the server answered without a build"}
}
