package github_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github/githubtest"
)

var hookSecret = []byte("whsec-test-only")

func verifier(t *testing.T) *github.WebhookVerifier {
	t.Helper()
	v, err := github.NewWebhookVerifier(hookSecret)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func fixtureDelivery(t *testing.T, name string) github.Delivery {
	t.Helper()
	req, err := githubtest.FixtureRequest("http://glossa.test/v1/integrations/github/webhooks", hookSecret, name, "delivery-"+name)
	if err != nil {
		t.Fatal(err)
	}
	d, err := verifier(t).Verify(req.Header, req.Body)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return d
}

func TestNewWebhookVerifierNeedsASecret(t *testing.T) {
	if _, err := github.NewWebhookVerifier(nil); err == nil {
		t.Fatal("an empty secret was accepted")
	}
}

func TestVerifyAcceptsSignedDeliveries(t *testing.T) {
	d := fixtureDelivery(t, "pull_request.opened")
	if d.Event != "pull_request" || d.ID != "delivery-pull_request.opened" || d.HookID != "1" {
		t.Fatalf("delivery %+v", d)
	}
	if !bytes.Equal(d.Body, githubtest.Fixture("pull_request.opened")) {
		t.Fatal("the body is not the raw bytes")
	}
}

type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

func TestVerifyRejects(t *testing.T) {
	body := githubtest.Fixture("pull_request.opened")
	good := func() http.Header {
		req, _ := githubtest.WebhookRequest("http://x", hookSecret, "pull_request", "d-1", body)
		return req.Header
	}
	tamper := func(f func(h http.Header)) http.Header { h := good(); f(h); return h }
	for name, tc := range map[string]struct {
		header http.Header
		body   []byte
		want   error
	}{
		"no signature":        {tamper(func(h http.Header) { h.Del("X-Hub-Signature-256") }), body, github.ErrWebhookSignature},
		"sha1 only":           {tamper(func(h http.Header) { h.Set("X-Hub-Signature-256", "sha1=abc") }), body, github.ErrWebhookSignature},
		"bad hex":             {tamper(func(h http.Header) { h.Set("X-Hub-Signature-256", "sha256=zz") }), body, github.ErrWebhookSignature},
		"other secret":        {tamper(func(h http.Header) { h.Set("X-Hub-Signature-256", githubtest.Sign([]byte("x"), body)) }), body, github.ErrWebhookSignature},
		"tampered body":       {good(), append([]byte(" "), body...), github.ErrWebhookSignature},
		"no event header":     {tamper(func(h http.Header) { h.Del("X-GitHub-Event") }), body, github.ErrWebhookHeaders},
		"no delivery header":  {tamper(func(h http.Header) { h.Del("X-GitHub-Delivery") }), body, github.ErrWebhookHeaders},
		"oversized delivery ": {good(), bytes.Repeat([]byte("a"), github.MaxWebhookBytes+1), github.ErrWebhookTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := verifier(t).Verify(tc.header, bytes.NewReader(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// The cap stops reading: a huge body is not slurped.
	r := &countingReader{r: io.LimitReader(zeros{}, 64<<20)}
	if _, err := verifier(t).Verify(good(), r); !errors.Is(err, github.ErrWebhookTooLarge) || r.read > github.MaxWebhookBytes+1 {
		t.Fatalf("err = %v after reading %d bytes", err, r.read)
	}
	// Exactly at the cap is fine as far as size goes.
	at := bytes.Repeat([]byte("a"), github.MaxWebhookBytes)
	h := good()
	h.Set("X-Hub-Signature-256", githubtest.Sign(hookSecret, at))
	if _, err := verifier(t).Verify(h, bytes.NewReader(at)); err != nil {
		t.Fatalf("a body at the cap: %v", err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestParseEvents(t *testing.T) {
	for _, name := range []string{"pull_request.opened", "pull_request.synchronize", "pull_request.reopened", "pull_request.closed"} {
		ev, err := github.ParseEvent(fixtureDelivery(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pr, ok := ev.(*github.PullRequestEvent)
		if !ok {
			t.Fatalf("%s: %T", name, ev)
		}
		if ev.Name() != name || ev.InstallationID() != 4242 || pr.Repository.ID != 10101 || pr.Number != 7 ||
			pr.PullRequest.Head.Ref != "feature/payment-copy" || len(pr.PullRequest.Head.SHA) != 40 || pr.PullRequest.Base.Ref != "main" || pr.FromFork() {
			t.Fatalf("%s: %+v", name, pr)
		}
		if name == "pull_request.closed" && (!pr.PullRequest.Merged || pr.PullRequest.MergeCommitSHA == "") {
			t.Fatalf("closed: %+v", pr.PullRequest)
		}
	}

	ev, err := github.ParseEvent(fixtureDelivery(t, "installation.created"))
	if err != nil {
		t.Fatal(err)
	}
	in := ev.(*github.InstallationEvent)
	if in.Action != "created" || in.Installation.ID != 4242 || in.Installation.AppID != 99001 ||
		in.Installation.Account.Login != "acme" || len(in.Repositories) != 2 || in.Repositories[1].ID != 10102 {
		t.Fatalf("installation %+v", in)
	}
	if _, err := github.ParseEvent(fixtureDelivery(t, "installation.deleted")); err != nil {
		t.Fatal(err)
	}

	ev, err = github.ParseEvent(fixtureDelivery(t, "installation_repositories.added"))
	if err != nil {
		t.Fatal(err)
	}
	ir := ev.(*github.InstallationRepositoriesEvent)
	if ir.InstallationID() != 4242 || len(ir.RepositoriesAdded) != 1 || ir.RepositoriesAdded[0].ID != 10103 || len(ir.RepositoriesRemoved) != 0 {
		t.Fatalf("installation_repositories %+v", ir)
	}
	ev, err = github.ParseEvent(fixtureDelivery(t, "installation_repositories.removed"))
	if err != nil || ev.(*github.InstallationRepositoriesEvent).RepositoriesRemoved[0].ID != 10102 {
		t.Fatalf("removed: %v %v", ev, err)
	}

	ev, err = github.ParseEvent(fixtureDelivery(t, "check_run.rerequested"))
	if err != nil {
		t.Fatal(err)
	}
	cr := ev.(*github.CheckRunEvent)
	if cr.CheckRun.ID != 5550001 || cr.CheckRun.App.ID != 99001 || cr.CheckRun.ExternalID == "" ||
		len(cr.CheckRun.PullRequests) != 1 || cr.CheckRun.PullRequests[0].Number != 7 || cr.Repository.ID != 10101 {
		t.Fatalf("check_run %+v", cr)
	}
}

func TestEveryFixtureParses(t *testing.T) {
	for _, name := range githubtest.Fixtures() {
		if _, err := github.ParseEvent(fixtureDelivery(t, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseEventIgnoresAndRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		event, body string
		want        error
	}{
		"push":                 {"push", `{"ref":"refs/heads/main"}`, github.ErrWebhookIgnored},
		"ping":                 {"ping", `{"zen":"Keep it logically awesome."}`, github.ErrWebhookIgnored},
		"pr edited":            {"pull_request", `{"action":"edited","installation":{"id":1}}`, github.ErrWebhookIgnored},
		"check_run completed":  {"check_run", `{"action":"completed","installation":{"id":1}}`, github.ErrWebhookIgnored},
		"not json":             {"pull_request", `{"action":`, github.ErrWebhookPayload},
		"no installation":      {"pull_request", `{"action":"opened","number":7,"repository":{"id":1},"pull_request":{"number":7,"head":{"sha":"9f1c2e3d4b5a69788796a5b4c3d2e1f0a9b8c7d6"}}}`, github.ErrWebhookPayload},
		"no repository id":     {"pull_request", `{"action":"opened","number":7,"installation":{"id":1},"pull_request":{"number":7,"head":{"sha":"9f1c2e3d4b5a69788796a5b4c3d2e1f0a9b8c7d6"}}}`, github.ErrWebhookPayload},
		"bad head sha":         {"pull_request", `{"action":"opened","number":7,"installation":{"id":1},"repository":{"id":1},"pull_request":{"number":7,"head":{"sha":"main"}}}`, github.ErrWebhookPayload},
		"installation w/o id":  {"installation", `{"action":"created","installation":{}}`, github.ErrWebhookPayload},
		"check_run w/o run id": {"check_run", `{"action":"rerequested","installation":{"id":1},"repository":{"id":1},"check_run":{}}`, github.ErrWebhookPayload},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := github.ParseEvent(github.Delivery{ID: "d", Event: tc.event, Body: []byte(tc.body)})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if strings.Contains(errString(err), "refs/heads") {
				t.Fatal("the payload leaked into the error")
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestForkPullRequest(t *testing.T) {
	body := strings.Replace(string(githubtest.Fixture("pull_request.opened")),
		`"head": {
      "label": "octocat:feature/payment-copy",
      "ref": "feature/payment-copy",
      "sha": "9f1c2e3d4b5a69788796a5b4c3d2e1f0a9b8c7d6",
      "repo": { "id": 10101, "name": "shop", "full_name": "acme/shop", "fork": false }`,
		`"head": {
      "label": "mallory:feature/payment-copy",
      "ref": "feature/payment-copy",
      "sha": "9f1c2e3d4b5a69788796a5b4c3d2e1f0a9b8c7d6",
      "repo": { "id": 666, "name": "shop", "full_name": "mallory/shop", "fork": true }`, 1)
	ev, err := github.ParseEvent(github.Delivery{ID: "d", Event: "pull_request", Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.(*github.PullRequestEvent).FromFork() {
		t.Fatal("a fork's pull request was not recognised")
	}
}
