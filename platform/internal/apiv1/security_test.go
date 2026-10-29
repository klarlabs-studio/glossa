package apiv1_test

import (
	"net/http"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
)

func TestRequirementsCoverEveryRoute(t *testing.T) {
	reqs, err := apiv1.Requirements()
	if err != nil {
		t.Fatal(err)
	}

	// Every route the generated router registers has a requirement.
	mux := http.NewServeMux()
	apiv1.HandlerWithOptions(apiv1.NewStrictHandler(nil, nil), apiv1.StdHTTPServerOptions{BaseRouter: mux})
	for pattern := range reqs {
		method, path, _ := cut(pattern)
		r, _ := http.NewRequest(method, "http://x"+fill(path), nil)
		if _, got := mux.Handler(r); got != pattern {
			t.Errorf("route for %s is %q; the router and the requirements disagree", pattern, got)
		}
	}

	for pattern, want := range map[string]apiv1.Requirement{
		"POST /v1/auth/magic-links":                   {Public: true},
		"GET /v1/me":                                  {Session: true},
		"DELETE /v1/auth/session":                     {Session: true, CSRF: true},
		"GET /v1/tenants/{tenant}/members":            {Session: true, Bearer: true},
		"POST /v1/tenants/{tenant}/tokens":            {Session: true, CSRF: true, Bearer: true},
		"PATCH /v1/tenants/{tenant}/members/{member}": {Session: true, CSRF: true, Bearer: true},
	} {
		got := reqs[pattern]
		if got.OperationID == "" {
			t.Errorf("%s has no operation id", pattern)
		}
		if got.OperationID = ""; got != want {
			t.Errorf("%s = %+v, want %+v", pattern, got, want)
		}
	}
}

func cut(pattern string) (method, path string, ok bool) {
	for i := range pattern {
		if pattern[i] == ' ' {
			return pattern[:i], pattern[i+1:], true
		}
	}
	return "", pattern, false
}

// fill replaces {wildcards} with a value so the path can be routed.
func fill(path string) string {
	out := []byte{}
	skip := false
	for i := 0; i < len(path); i++ {
		switch {
		case path[i] == '{':
			skip = true
			out = append(out, "x1"...)
		case path[i] == '}':
			skip = false
		case !skip:
			out = append(out, path[i])
		}
	}
	return string(out)
}
