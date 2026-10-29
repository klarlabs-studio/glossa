//go:build integration

package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

type metaBody struct {
	SignInMethods []string `json:"sign_in_methods"`
	EmailDelivery bool     `json:"email_delivery"`
	EdgeURL       *string  `json:"edge_url"`
}

func TestMetaOverHTTP(t *testing.T) {
	s := startServer(t) // the development log mailer, no passkeys, no edge URL
	r := s.do(call{method: "GET", path: "/v1/meta"})
	r.want(t, http.StatusOK, "")
	var m metaBody
	r.decode(t, &m)
	if !slices.Equal(m.SignInMethods, []string{"password", "magic_link"}) || !m.EmailDelivery || m.EdgeURL != nil {
		t.Errorf("meta %s", r.body)
	}
}

// A deployment without email: the email flows answer email_disabled,
// and a password account registers and signs in without verification.
func TestWithoutEmailOverHTTP(t *testing.T) {
	s := startServerWith(t, map[string]string{
		"GLOSSA_MAIL_DRIVER":     "none",
		"GLOSSA_EDGE_PUBLIC_URL": "https://edge.glossa.test/",
	})
	r := s.do(call{method: "GET", path: "/v1/meta"})
	r.want(t, http.StatusOK, "")
	var m metaBody
	r.decode(t, &m)
	if !slices.Equal(m.SignInMethods, []string{"password"}) || m.EmailDelivery || m.EdgeURL == nil || *m.EdgeURL != "https://edge.glossa.test" {
		t.Fatalf("meta %s", r.body)
	}

	email := map[string]string{"email": "ada@example.com"}
	s.do(call{method: "POST", path: "/v1/auth/magic-links", body: email}).want(t, http.StatusNotFound, "email_disabled")
	s.do(call{method: "POST", path: "/v1/auth/magic-link-redemptions", body: map[string]string{"token": "x"}}).
		want(t, http.StatusNotFound, "email_disabled")
	s.do(call{method: "POST", path: "/v1/auth/password-resets", body: email}).want(t, http.StatusNotFound, "email_disabled")
	s.do(call{method: "POST", path: "/v1/auth/password-reset-redemptions", body: map[string]string{"token": "x", "password": "a new long password"}}).
		want(t, http.StatusNotFound, "email_disabled")

	creds := map[string]string{"email": "ada@example.com", "password": "correct horse battery"}
	s.do(call{method: "POST", path: "/v1/auth/registrations", body: creds}).want(t, http.StatusAccepted, "")
	r = s.do(call{method: "POST", path: "/v1/auth/password-sessions", body: creds})
	r.want(t, http.StatusOK, "")
	cookie, _, _ := strings.Cut(strings.TrimPrefix(r.header.Get("Set-Cookie"), "__Host-glossa_session="), ";")
	var me struct {
		Person struct {
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
		} `json:"person"`
		Memberships []any `json:"memberships"`
	}
	r = s.do(call{method: "GET", path: "/v1/me", cookie: cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &me)
	if me.Person.Email != "ada@example.com" || me.Person.EmailVerified || len(me.Memberships) != 1 {
		t.Errorf("me %s", r.body)
	}
	if strings.Contains(s.logs.String(), "#token=") {
		t.Error("a link was written to the log without a mailer")
	}
}
