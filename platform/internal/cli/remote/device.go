package remote

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// Device sign-in (RFC 0006 §7.2, RFC 8628): how `glossa login --device`
// signs a person in. Like the GitHub Actions exchange, starting and
// polling carry no Glossa credential, so the Client is built with an
// empty token.

// DeviceAuthorization is what the server hands a device that asks to
// be signed in: the codes, where the person enters the user code, and
// how long and how often to poll.
type DeviceAuthorization = apiclient.DeviceAuthorization

// DeviceSession is the bearer an approved device receives.
type DeviceSession = apiclient.DeviceSession

// Me is the signed-in person and their tenants.
type Me = apiclient.Me

// StartDeviceAuthorization asks for a device code and a user code.
func (c *Client) StartDeviceAuthorization(ctx context.Context, clientName string) (DeviceAuthorization, error) {
	r, err := c.api.StartDeviceAuthorizationWithResponse(ctx, apiclient.StartDeviceAuthorizationJSONRequestBody{ClientName: clientName})
	if err := check(r, err, http.MethodPost, c.server+"/v1/auth/device-authorizations"); err != nil {
		return DeviceAuthorization{}, err
	}
	return *r.JSON200, nil
}

// RedeemDeviceAuthorization polls once. Until the person decides, it
// fails with an *APIError whose Code is RFC 8628 §3.5's:
// authorization_pending, slow_down, access_denied or expired_token.
func (c *Client) RedeemDeviceAuthorization(ctx context.Context, deviceCode string) (DeviceSession, error) {
	r, err := c.api.RedeemDeviceAuthorizationWithResponse(ctx, apiclient.RedeemDeviceAuthorizationJSONRequestBody{DeviceCode: deviceCode})
	if err := check(r, err, http.MethodPost, c.server+"/v1/auth/device-sessions"); err != nil {
		return DeviceSession{}, err
	}
	return *r.JSON200, nil
}

// SignOut ends the session the client authenticates with: for a device
// session, that device's and no other.
func (c *Client) SignOut(ctx context.Context) error {
	r, err := c.api.SignOutWithResponse(ctx)
	return check(r, err, http.MethodDelete, c.server+"/v1/auth/session")
}

// Me is the person behind a session.
func (c *Client) Me(ctx context.Context) (Me, error) {
	r, err := c.api.GetMeWithResponse(ctx)
	if err := check(r, err, http.MethodGet, c.server+"/v1/me"); err != nil {
		return Me{}, err
	}
	return *r.JSON200, nil
}
