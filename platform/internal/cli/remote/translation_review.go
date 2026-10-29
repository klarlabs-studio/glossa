package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Translation reads one message's translation in a locale, with the
// ETag a review has to be based on.
func (c *Client) Translation(ctx context.Context, s Scope, key, locale string) (Translation, string, error) {
	r, err := c.api.GetTranslationWithResponse(ctx, s.Tenant, s.Project, key, locale)
	if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/messages/%s/translations/%s", s.Tenant, s.Project, key, locale)); err != nil {
		return Translation{}, "", err
	}
	return *r.JSON200, r.HTTPResponse.Header.Get("ETag"), nil
}

// ReviewTranslation moves a translation to state if it is still at etag
// and returns it with its new ETag. The transport does not retry it: the
// caller decides what a 412 means.
func (c *Client) ReviewTranslation(ctx context.Context, s Scope, key, locale, etag, state string) (Translation, string, error) {
	r, err := c.api.ReviewTranslationWithResponse(ctx, s.Tenant, s.Project, key, locale,
		&apiclient.ReviewTranslationParams{IfMatch: etag}, apiclient.ReviewTranslation{State: apiclient.ReviewState(state)})
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/projects/%s/messages/%s/translations/%s/reviews", s.Tenant, s.Project, key, locale)); err != nil {
		return Translation{}, "", err
	}
	return *r.JSON200, r.HTTPResponse.Header.Get("ETag"), nil
}
