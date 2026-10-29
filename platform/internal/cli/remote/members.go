package remote

import (
	"context"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Member is a tenant's member or open invitation.
type Member = apiclient.Member

// Members lists a tenant's members and open invitations.
func (c *Client) Members(ctx context.Context, tenant string) ([]Member, error) {
	size := pageSize
	return collect(func(tok *string) ([]Member, *string, error) {
		r, err := c.api.ListMembersWithResponse(ctx, tenant, &apiclient.ListMembersParams{PageSize: &size, PageToken: tok})
		if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/members", tenant)); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// Invitation is someone to invite into a tenant.
type Invitation struct {
	Email   string
	Roles   []string
	Locales []string
}

// InviteMember opens an invitation. key is its Idempotency-Key: a retry
// with the same key returns the first invitation (replayed).
func (c *Client) InviteMember(ctx context.Context, tenant string, in Invitation, key string) (m Member, replayed bool, err error) {
	roles := make([]apiclient.Role, len(in.Roles))
	for i, r := range in.Roles {
		roles[i] = apiclient.Role(r)
	}
	body := apiclient.AddMember{Email: openapi_types.Email(in.Email), Roles: roles}
	if len(in.Locales) > 0 {
		body.Locales = &in.Locales
	}
	params := &apiclient.AddMemberParams{}
	if key != "" {
		params.IdempotencyKey = &key
	}
	r, err := c.api.AddMemberWithResponse(ctx, tenant, params, body)
	if err := check(r, err, http.MethodPost, c.path("/v1/tenants/%s/members", tenant)); err != nil {
		return Member{}, false, err
	}
	return *r.JSON201, r.HTTPResponse.Header.Get("Idempotent-Replayed") == "true", nil
}
