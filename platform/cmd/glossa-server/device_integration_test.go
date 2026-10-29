//go:build integration

package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Device sign-in over HTTP (RFC 0006 §7.2, RFC 8628): the CLI's half
// (start, poll) unauthenticated, the person's half (look up, decide) in
// a browser session with CSRF, and the bearer that comes back the
// person's session wherever a session is accepted.

type deviceStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func (s *server) startDevice(name string) deviceStart {
	s.t.Helper()
	r := s.do(call{method: "POST", path: "/v1/auth/device-authorizations", body: map[string]string{"client_name": name}})
	r.want(s.t, http.StatusOK, "")
	var d deviceStart
	r.decode(s.t, &d)
	return d
}

func (s *server) pollDevice(deviceCode string) reply {
	s.t.Helper()
	return s.do(call{method: "POST", path: "/v1/auth/device-sessions", body: map[string]string{"device_code": deviceCode}})
}

// signInDevice runs the whole flow for a signed-in person and returns
// the device's bearer.
func (s *server) signInDevice(who session) string {
	s.t.Helper()
	d := s.startDevice("glossa CLI")
	s.do(call{method: "POST", path: "/v1/auth/device-approvals", cookie: who.cookie, csrf: who.csrf,
		body: map[string]string{"user_code": d.UserCode, "decision": "approved"}}).want(s.t, http.StatusNoContent, "")
	r := s.pollDevice(d.DeviceCode)
	r.want(s.t, http.StatusOK, "")
	var out struct {
		AccessToken string `json:"access_token"`
	}
	r.decode(s.t, &out)
	return out.AccessToken
}

func TestDeviceSignInOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)

	d := s.startDevice("glossa CLI on build-01")
	if len(d.UserCode) != 9 || d.UserCode[4] != '-' || d.Interval != 5 || d.ExpiresIn != 900 ||
		d.VerificationURI != "https://studio.test/device" || d.VerificationURIComplete != d.VerificationURI+"?code="+d.UserCode {
		t.Fatalf("start = %+v", d)
	}
	s.do(call{method: "POST", path: "/v1/auth/device-authorizations", body: map[string]string{"client_name": ""}}).
		want(t, http.StatusBadRequest, "invalid_request")

	// The device polls: pending, then too fast.
	s.pollDevice(d.DeviceCode).want(t, http.StatusBadRequest, "authorization_pending")
	s.pollDevice(d.DeviceCode).want(t, http.StatusBadRequest, "slow_down")

	// The person's browser looks the code up, typed any way, and sees
	// what asked.
	lookup := "/v1/auth/device-authorizations/" + strings.ToLower(strings.ReplaceAll(d.UserCode, "-", ""))
	s.do(call{method: "GET", path: lookup}).want(t, http.StatusUnauthorized, "unauthenticated")
	r := s.do(call{method: "GET", path: lookup, cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	var view struct {
		UserCode   string    `json:"user_code"`
		ClientName string    `json:"client_name"`
		ExpiresAt  time.Time `json:"expires_at"`
	}
	r.decode(t, &view)
	if view.UserCode != d.UserCode || view.ClientName != "glossa CLI on build-01" || view.ExpiresAt.IsZero() {
		t.Errorf("view = %+v", view)
	}
	approve := call{method: "POST", path: "/v1/auth/device-approvals", cookie: ada.cookie,
		body: map[string]string{"user_code": d.UserCode, "decision": "approved"}}
	s.do(approve).want(t, http.StatusForbidden, "csrf_invalid")
	approve.csrf = ada.csrf
	s.do(approve).want(t, http.StatusNoContent, "")
	s.do(approve).want(t, http.StatusNotFound, "device_authorization_not_found")
	s.do(call{method: "GET", path: lookup, cookie: ada.cookie}).want(t, http.StatusNotFound, "device_authorization_not_found")

	// After the (now ten-second) interval, the poll yields the bearer.
	time.Sleep(10 * time.Second)
	r = s.pollDevice(d.DeviceCode)
	r.want(t, http.StatusOK, "")
	var got struct {
		AccessToken string    `json:"access_token"`
		TokenType   string    `json:"token_type"`
		ExpiresAt   time.Time `json:"expires_at"`
		PersonID    string    `json:"person_id"`
	}
	r.decode(t, &got)
	if !strings.HasPrefix(got.AccessToken, "glossa_dev_") || got.TokenType != "Bearer" || got.ExpiresAt.IsZero() || got.PersonID == "" {
		t.Fatalf("session = %+v", got)
	}
	dev := got.AccessToken

	// It is spent: a second poll is expired_token.
	s.pollDevice(d.DeviceCode).want(t, http.StatusBadRequest, "expired_token")
	s.pollDevice("not-a-device-code").want(t, http.StatusBadRequest, "expired_token")

	// The bearer is Ada's session: GET /v1/me, and unsafe session
	// operations with no CSRF token.
	r = s.do(call{method: "GET", path: "/v1/me", bearer: dev})
	r.want(t, http.StatusOK, "")
	var me struct {
		Person struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"person"`
		CSRFToken   string `json:"csrf_token"`
		Memberships []any  `json:"memberships"`
	}
	r.decode(t, &me)
	if me.Person.Email != "ada@example.com" || me.Person.ID != got.PersonID || me.CSRFToken != "" || len(me.Memberships) != 2 {
		t.Errorf("me through the device = %+v", me)
	}
	r = s.do(call{method: "POST", path: "/v1/tenants/" + org.ID + "/tokens", bearer: dev,
		body: map[string]any{"name": "from-the-cli", "scopes": []string{"read"}}})
	r.want(t, http.StatusCreated, "")
	s.do(call{method: "GET", path: "/v1/tenants/" + org.ID + "/members", bearer: dev}).want(t, http.StatusOK, "")

	// A device can't look codes up or decide them: only a browser
	// signs further devices in.
	other := s.startDevice("someone else")
	s.do(call{method: "GET", path: "/v1/auth/device-authorizations/" + other.UserCode, bearer: dev}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "POST", path: "/v1/auth/device-approvals", bearer: dev,
		body: map[string]string{"user_code": other.UserCode, "decision": "approved"}}).want(t, http.StatusForbidden, "forbidden")
	// Nor can an API token, which has no session behind it at all.
	var tok struct {
		Secret string `json:"secret"`
	}
	r.decode(t, &tok)
	s.do(call{method: "GET", path: "/v1/auth/device-authorizations/" + other.UserCode, bearer: tok.Secret}).
		want(t, http.StatusUnauthorized, "unauthenticated")
	// The device's session is no cookie, and a cookie is no device.
	raw := strings.TrimPrefix(dev, "glossa_dev_")
	s.do(call{method: "GET", path: "/v1/me", cookie: raw}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "GET", path: "/v1/me", bearer: "glossa_dev_" + ada.cookie}).want(t, http.StatusUnauthorized, "unauthenticated")

	// Denied: the device hears access_denied.
	s.do(call{method: "POST", path: "/v1/auth/device-approvals", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"user_code": other.UserCode, "decision": "denied"}}).want(t, http.StatusNoContent, "")
	s.pollDevice(other.DeviceCode).want(t, http.StatusBadRequest, "access_denied")

	// `glossa logout`: DELETE /v1/auth/session with the bearer ends
	// that device and nothing else.
	second := s.signInDevice(ada)
	r = s.do(call{method: "DELETE", path: "/v1/auth/session", bearer: second})
	r.want(t, http.StatusNoContent, "")
	if r.header.Get("Set-Cookie") != "" {
		t.Errorf("a device's sign-out sets a cookie: %q", r.header.Get("Set-Cookie"))
	}
	s.do(call{method: "GET", path: "/v1/me", bearer: second}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "GET", path: "/v1/me", bearer: dev}).want(t, http.StatusOK, "")
	s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie}).want(t, http.StatusOK, "")

	// Signing out everywhere ends the device too.
	s.do(call{method: "DELETE", path: "/v1/auth/sessions", cookie: ada.cookie, csrf: ada.csrf}).want(t, http.StatusNoContent, "")
	s.do(call{method: "GET", path: "/v1/me", bearer: dev}).want(t, http.StatusUnauthorized, "unauthenticated")
}

// A device session carries the person's restriction exactly (RFC 0006
// §3.3, §4.1): a project-scoped member and an assignment-scoped one see
// through their device what they see through their browser — the same
// status and the same body on every read — and nothing more.
func TestDeviceSessionCarriesTheMembersScope(t *testing.T) {
	f := newRestrictedFixture(t)
	s := f.s
	s.do(call{method: "POST", path: f.base + "/members", cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]any{"email": "pat@example.com", "roles": []string{"developer"}, "projects": []string{f.aID}}}).
		want(t, http.StatusCreated, "")
	pat := s.signIn("pat@example.com")

	for name, who := range map[string]session{"project-scoped": pat, "assigned-only": f.vera} {
		dev := s.signInDevice(who)
		for _, path := range []string{
			f.base + "/projects", f.a, f.b, f.a + "/messages", f.b + "/messages",
			f.a + "/messages/pay/translations/de", f.a + "/messages/cancel/translations/de", f.base + "/members",
		} {
			browser := s.do(call{method: "GET", path: path, cookie: who.cookie})
			device := s.do(call{method: "GET", path: path, bearer: dev})
			if browser.status != device.status || !bytes.Equal(browser.body, device.body) {
				t.Errorf("%s %s: browser %d %s, device %d %s", name, path, browser.status, browser.body, device.status, device.body)
			}
		}
		if r := s.do(call{method: "GET", path: f.b, bearer: dev}); r.status != http.StatusNotFound {
			t.Errorf("%s: the device reads a project outside the scope: %d", name, r.status)
		}
	}
}
