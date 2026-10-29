package httpapi

import (
	"errors"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
)

// A device session is the person's session presented as a bearer: it
// is refused, before anything is looked up, on an operation that does
// not accept a session — whatever else that operation accepts.
func TestDeviceBearerOnlyWhereASessionIs(t *testing.T) {
	a := &API{} // no service: a refusal must come before any lookup
	r := httptest.NewRequest("GET", "/v1/anything", nil)
	r.Header.Set("Authorization", "Bearer glossa_dev_"+strings.Repeat("a", 43))
	for _, req := range []apiv1.Requirement{{Bearer: true}, {InContext: true}, {Bearer: true, InContext: true}} {
		if _, err := a.authenticate(r, req); !errors.Is(err, app.ErrUnauthenticated) {
			t.Errorf("%+v: err = %v", req, err)
		}
	}
}

func TestClientAddressBelievesOnlyTrustedProxies(t *testing.T) {
	a := &API{}
	req := func(peer string, xff ...string) string {
		r := httptest.NewRequest("POST", "/v1/auth/device-authorizations", nil)
		r.RemoteAddr = peer
		for _, h := range xff {
			r.Header.Add("X-Forwarded-For", h)
		}
		return a.clientAddress(r)
	}
	// No trusted proxy: the header is anybody's to write.
	if got := req("203.0.113.9:4711", "198.51.100.1"); got != "203.0.113.9" {
		t.Errorf("untrusted peer: %s", got)
	}
	a.SetTrustedProxies([]netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")})
	cases := []struct {
		peer string
		xff  []string
		want string
	}{
		{"10.42.0.7:80", []string{"198.51.100.1"}, "198.51.100.1"},
		// A client that sends its own header is named by what the
		// proxy appended, not by what it wrote.
		{"10.42.0.7:80", []string{"1.2.3.4, 198.51.100.1"}, "198.51.100.1"},
		{"10.42.0.7:80", []string{"1.2.3.4", "198.51.100.1, 10.42.0.9"}, "198.51.100.1"},
		{"10.42.0.7:80", nil, "10.42.0.7"},
		{"10.42.0.7:80", []string{"garbage, 198.51.100.1"}, "198.51.100.1"},
		{"10.42.0.7:80", []string{"198.51.100.1, garbage"}, "10.42.0.7"},
		{"[::ffff:203.0.113.9]:1", []string{"198.51.100.1"}, "203.0.113.9"},
	}
	for _, c := range cases {
		if got := req(c.peer, c.xff...); got != c.want {
			t.Errorf("peer %s, X-Forwarded-For %q: %s, want %s", c.peer, c.xff, got, c.want)
		}
	}
}
