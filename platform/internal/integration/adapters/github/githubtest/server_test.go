package githubtest_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github/githubtest"
)

// GitHub's documented example for validating webhook deliveries.
func TestSignMatchesGitHubsExample(t *testing.T) {
	got := githubtest.Sign([]byte("It's a Secret to Everybody"), []byte("Hello, World!"))
	want := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestFixturesAreDeliveries(t *testing.T) {
	names := githubtest.Fixtures()
	if len(names) < 8 {
		t.Fatalf("only %d fixtures: %v", len(names), names)
	}
	for _, name := range names {
		var body struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(githubtest.Fixture(name), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasSuffix(name, "."+body.Action) {
			t.Errorf("%s: action %q does not match its name", name, body.Action)
		}
	}
	req, err := githubtest.FixtureRequest("http://glossa.test/hook", []byte("s3cret"), "pull_request.opened", "d-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-GitHub-Event") != "pull_request" || req.Header.Get("X-GitHub-Delivery") != "d-1" ||
		req.Header.Get("X-Hub-Signature-256") != githubtest.Sign([]byte("s3cret"), githubtest.Fixture("pull_request.opened")) {
		t.Fatalf("unexpected headers %v", req.Header)
	}
}

func TestUnknownFixturePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	githubtest.Fixture("push.created")
}

func TestServerRejectsBadCredentials(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := githubtest.New(t, githubtest.Options{AppID: 1, PublicKey: &key.PublicKey, Prefix: "/api/v3"})
	srv.AddInstallation(7, "acme", 100)
	for _, tc := range []struct{ method, path, auth string }{
		{http.MethodPost, "/app/installations/7/access_tokens", "Bearer not.a.jwt"},
		{http.MethodGet, "/user/installations", "Bearer unknown"},
		{http.MethodPost, "/repositories/100/check-runs", "token ghs_unknown"},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", tc.auth)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
	}
	if got := len(srv.Requests()); got != 3 {
		t.Fatalf("recorded %d requests, want 3", got)
	}
	if !strings.HasPrefix(srv.Requests()[0].Path, "/api/v3/") {
		t.Fatalf("prefix not applied: %s", srv.Requests()[0].Path)
	}
}
