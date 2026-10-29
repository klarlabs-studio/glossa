package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// ── device sign-in (RFC 8628) ───────────────────────────────────────

func (a *API) StartDeviceAuthorization(ctx context.Context, req apiv1.StartDeviceAuthorizationRequestObject) (apiv1.StartDeviceAuthorizationResponseObject, error) {
	d, err := a.svc.StartDeviceAuthorization(ctx, req.Body.ClientName)
	if err != nil {
		return nil, err
	}
	return apiv1.StartDeviceAuthorization200JSONResponse{
		DeviceCode:              d.DeviceCode,
		UserCode:                d.UserCode,
		VerificationUri:         d.VerificationURI,
		VerificationUriComplete: d.VerificationURIComplete,
		ExpiresIn:               int(d.ExpiresIn.Seconds()),
		Interval:                int(d.Interval.Seconds()),
	}, nil
}

func (a *API) GetDeviceAuthorization(ctx context.Context, req apiv1.GetDeviceAuthorizationRequestObject) (apiv1.GetDeviceAuthorizationResponseObject, error) {
	p, err := browserPerson(ctx)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.DeviceAuthorizationFor(ctx, p, req.UserCode)
	if err != nil {
		return nil, err
	}
	return apiv1.GetDeviceAuthorization200JSONResponse{
		UserCode:    v.UserCode,
		ClientName:  v.ClientName,
		RequestedAt: v.RequestedAt,
		ExpiresAt:   v.ExpiresAt,
	}, nil
}

func (a *API) DecideDeviceAuthorization(ctx context.Context, req apiv1.DecideDeviceAuthorizationRequestObject) (apiv1.DecideDeviceAuthorizationResponseObject, error) {
	p, err := browserPerson(ctx)
	if err != nil {
		return nil, err
	}
	approve := req.Body.Decision == apiv1.DeviceApprovalDecisionApproved
	if err := a.svc.DecideDeviceAuthorization(ctx, p, req.Body.UserCode, approve); err != nil {
		return nil, err
	}
	return apiv1.DecideDeviceAuthorization204Response{}, nil
}

func (a *API) RedeemDeviceAuthorization(ctx context.Context, req apiv1.RedeemDeviceAuthorizationRequestObject) (apiv1.RedeemDeviceAuthorizationResponseObject, error) {
	s, err := a.svc.RedeemDeviceAuthorization(ctx, req.Body.DeviceCode)
	if err != nil {
		return nil, err
	}
	return apiv1.RedeemDeviceAuthorization200JSONResponse{
		AccessToken: s.AccessToken,
		TokenType:   apiv1.Bearer,
		ExpiresAt:   s.ExpiresAt,
		PersonId:    s.Person.UUID(),
	}, nil
}

// browserPerson is the person behind a browser session, and nobody
// else: a device code is looked up and decided only in Studio, so a
// signed-in device can never sign further devices in.
func browserPerson(ctx context.Context) (domain.PersonID, error) {
	c, ok := callerFrom(ctx)
	if !ok || c.session == "" {
		return domain.PersonID{}, app.ErrForbidden
	}
	return person(ctx)
}
