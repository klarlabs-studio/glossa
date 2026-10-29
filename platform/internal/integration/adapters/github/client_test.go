package github_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.klarlabs.de/fortify/ferrors"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github/githubtest"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

const (
	appID     = 99001
	instA     = 4242
	repoA     = 10101
	instB     = 5353
	repoB     = 20202
	headSHA   = "9f1c2e3d4b5a69788796a5b4c3d2e1f0a9b8c7d6"
	checkName = "Glossa"
)

var (
	tenantA = uuid.MustParse("0191f0c2-0000-7000-8000-00000000000a")
	tenantB = uuid.MustParse("0191f0c2-0000-7000-8000-00000000000b")
)

// clock is a settable clock shared by the fake and the client.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// syncBuffer is a log sink safe for concurrent writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type fixture struct {
	srv    *githubtest.Server
	client *github.Client
	logs   *syncBuffer
	clock  *clock

	mu    sync.Mutex
	calls []github.CallInfo
}

func (f *fixture) callInfos() []github.CallInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]github.CallInfo(nil), f.calls...)
}

type setupOpts struct {
	realClock bool
	options   func(*github.Options)
	key       func(*github.Config)
}

// setup starts a fake GitHub Enterprise Server (under /api/v3) with two
// installations and a client for it.
func setup(t *testing.T, so setupOpts) *fixture {
	t.Helper()
	f := &fixture{logs: &syncBuffer{}, clock: &clock{t: time.Now()}}
	now := f.clock.Now
	if so.realClock {
		now = time.Now
	}
	f.srv = githubtest.New(t, githubtest.Options{
		AppID: appID, PublicKey: &appKey(t).PublicKey, Prefix: "/api/v3", Now: now,
	})
	f.srv.AddInstallation(instA, "acme", repoA)
	f.srv.AddInstallation(instB, "globex", repoB)
	opts := github.Options{
		HTTPClient:   f.srv.Client(),
		Timeout:      2 * time.Second,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
		Logger:       slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:          now,
		OnCall: func(ci github.CallInfo) {
			f.mu.Lock()
			f.calls = append(f.calls, ci)
			f.mu.Unlock()
		},
	}
	if so.options != nil {
		so.options(&opts)
	}
	cfg := github.Config{
		AppID: appID, AppSlug: "glossa", PrivateKey: appKey(t), WebhookSecret: []byte("s"),
		ClientID: "Iv1.abc", ClientSecret: "client-secret", APIURL: f.srv.URL, WebURL: f.srv.WebURL,
	}
	if so.key != nil {
		so.key(&cfg)
	}
	c, err := github.New(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	f.client = c
	return f
}

func targetA() app.GitHubTarget {
	return app.GitHubTarget{TenantID: tenantA, InstallationID: instA, RepositoryID: repoA}
}

func targetB() app.GitHubTarget {
	return app.GitHubTarget{TenantID: tenantB, InstallationID: instB, RepositoryID: repoB}
}

func (f *fixture) ensure(t *testing.T, tg app.GitHubTarget) app.CheckRun {
	t.Helper()
	run, err := f.client.EnsureCheckRun(context.Background(), tg, app.CheckRunKey{HeadSHA: headSHA, Name: checkName}, "ext-1", 0)
	if err != nil {
		t.Fatalf("EnsureCheckRun: %v", err)
	}
	return run
}

var inProgress = app.CheckRunUpdate{Status: app.CheckInProgress}

func TestNewRequiresAppIdentity(t *testing.T) {
	if _, err := github.New(github.Config{}, github.Options{}); err == nil {
		t.Fatal("New accepted an empty config")
	}
}

// --- tokens ---

func TestInstallationTokenIsMintedOnceForConcurrentCalls(t *testing.T) {
	f := setup(t, setupOpts{})
	run := f.ensure(t, targetA())
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			errs <- f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, inProgress)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.srv.TokensIssued(instA); n != 1 {
		t.Fatalf("minted %d tokens, want 1", n)
	}
	// The token request authenticated as the App (a JWT), not with a
	// stored secret.
	req := f.srv.Requests(githubtest.RouteAccessToken)[0]
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ey") || req.Path != "/api/v3/app/installations/4242/access_tokens" {
		t.Fatalf("token request %s with %q", req.Path, req.Header.Get("Authorization"))
	}
}

func TestInstallationTokenIsRefreshedFiveMinutesBeforeExpiry(t *testing.T) {
	f := setup(t, setupOpts{})
	run := f.ensure(t, targetA())
	update := func() {
		t.Helper()
		if err := f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, inProgress); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.Advance(54 * time.Minute)
	update()
	if n := f.srv.TokensIssued(instA); n != 1 {
		t.Fatalf("after 54 min: minted %d tokens, want 1", n)
	}
	f.clock.Advance(time.Minute + time.Second)
	update()
	if n := f.srv.TokensIssued(instA); n != 2 {
		t.Fatalf("within 5 min of expiry: minted %d tokens, want 2", n)
	}
}

func TestRevokedTokenIsReplaced(t *testing.T) {
	f := setup(t, setupOpts{})
	run := f.ensure(t, targetA())
	f.srv.RevokeTokens()
	if err := f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, inProgress); err != nil {
		t.Fatal(err)
	}
	if n := f.srv.TokensIssued(instA); n != 2 {
		t.Fatalf("minted %d tokens, want 2", n)
	}
}

func TestTokenMintFailures(t *testing.T) {
	t.Run("uninstalled", func(t *testing.T) {
		f := setup(t, setupOpts{})
		tg := targetA()
		tg.InstallationID = 777
		err := f.client.UpdateCheckRun(context.Background(), tg, 1, inProgress)
		if !errors.Is(err, app.ErrGitHubNotFound) {
			t.Fatalf("err = %v, want not found", err)
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		other := newKey(t)
		f := setup(t, setupOpts{key: func(c *github.Config) { c.PrivateKey = other }})
		err := f.client.UpdateCheckRun(context.Background(), targetA(), 1, inProgress)
		if !errors.Is(err, app.ErrGitHubRejected) {
			t.Fatalf("err = %v, want rejected", err)
		}
		if n := len(f.srv.Requests(githubtest.RouteAccessToken)); n != 1 {
			t.Fatalf("a bad key was retried: %d token requests", n)
		}
	})
}

// --- checks ---

func TestEnsureCheckRunIsIdempotent(t *testing.T) {
	f := setup(t, setupOpts{})
	first := f.ensure(t, targetA())
	if first.ID == 0 || first.Status != app.CheckQueued || first.ExternalID != "ext-1" || first.HeadSHA != headSHA {
		t.Fatalf("created %+v", first)
	}
	// A retry that lost the stored ID finds the run instead of creating
	// a second.
	again := f.ensure(t, targetA())
	if again.ID != first.ID {
		t.Fatalf("second ensure returned %d, want %d", again.ID, first.ID)
	}
	stored, err := f.client.EnsureCheckRun(context.Background(), targetA(), app.CheckRunKey{HeadSHA: headSHA, Name: checkName}, "ext-1", first.ID)
	if err != nil || stored.ID != first.ID {
		t.Fatalf("stored ensure = %+v, %v", stored, err)
	}
	if runs := f.srv.CheckRuns(repoA); len(runs) != 1 {
		t.Fatalf("%d check runs, want 1", len(runs))
	}
	if n := len(f.srv.Requests(githubtest.RouteCheckCreate)); n != 1 {
		t.Fatalf("%d creates, want 1", n)
	}
	var body map[string]any
	_ = json.Unmarshal(f.srv.Requests(githubtest.RouteCheckCreate)[0].Body, &body)
	if body["status"] != "queued" || body["external_id"] != "ext-1" || body["name"] != checkName {
		t.Fatalf("create body %v", body)
	}
	list := f.srv.Requests(githubtest.RouteCheckList)[0]
	if list.Query.Get("check_name") != checkName || list.Query.Get("app_id") != "99001" {
		t.Fatalf("list query %v", list.Query)
	}
}

func TestEnsureCheckRunValidates(t *testing.T) {
	f := setup(t, setupOpts{})
	_, err := f.client.EnsureCheckRun(context.Background(), targetA(), app.CheckRunKey{HeadSHA: "main", Name: checkName}, "x", 0)
	if !errors.Is(err, app.ErrGitHubRejected) || len(f.srv.Requests()) != 0 {
		t.Fatalf("err = %v with %d requests", err, len(f.srv.Requests()))
	}
}

func annotations(n int) []app.CheckAnnotation {
	out := make([]app.CheckAnnotation, n)
	for i := range out {
		out[i] = app.CheckAnnotation{Path: "src/app.vue", StartLine: i + 1, Level: "failure", Title: "Invalid message", Message: "unbalanced braces"}
	}
	return out
}

func TestUpdateCheckRunBatchesAnnotations(t *testing.T) {
	f := setup(t, setupOpts{})
	run := f.ensure(t, targetA())
	err := f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, app.CheckRunUpdate{
		Status: app.CheckCompleted, Conclusion: app.ConclusionFailure,
		Title: "5 new keys, 1 invalid message", Summary: "| locale | missing |\n|---|---|\n| es | 5 |",
		Annotations: annotations(120),
	})
	if err != nil {
		t.Fatal(err)
	}
	patches := f.srv.Requests(githubtest.RouteCheckUpdate)
	if len(patches) != 3 {
		t.Fatalf("%d PATCHes, want 3", len(patches))
	}
	for i, want := range []int{50, 50, 20} {
		var body struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			Output     struct {
				Annotations []json.RawMessage `json:"annotations"`
			} `json:"output"`
		}
		if err := json.Unmarshal(patches[i].Body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Output.Annotations) != want {
			t.Errorf("PATCH %d: %d annotations, want %d", i, len(body.Output.Annotations), want)
		}
		last := i == 2
		if (body.Status == app.CheckCompleted) != last || (body.Conclusion != "") != last {
			t.Errorf("PATCH %d: status %q conclusion %q; the run must complete on the last only", i, body.Status, body.Conclusion)
		}
	}
	got := f.srv.CheckRuns(repoA)[0]
	if got.Status != "completed" || got.Conclusion != "failure" || len(got.Annotations) != 120 || !strings.HasPrefix(got.Summary, "| locale") {
		t.Fatalf("stored run %+v", got)
	}
}

func TestUpdateCheckRunErrors(t *testing.T) {
	f := setup(t, setupOpts{})
	for name, u := range map[string]app.CheckRunUpdate{
		"bad conclusion":          {Status: app.CheckCompleted, Conclusion: "cancelled"},
		"conclusion w/o complete": {Status: app.CheckInProgress, Conclusion: app.ConclusionSuccess},
		"annotations w/o output":  {Status: app.CheckInProgress, Annotations: annotations(1)},
		"empty":                   {},
	} {
		if err := f.client.UpdateCheckRun(context.Background(), targetA(), 1, u); !errors.Is(err, app.ErrGitHubRejected) {
			t.Errorf("%s: err = %v, want rejected", name, err)
		}
	}
	if n := len(f.srv.Requests()); n != 0 {
		t.Fatalf("invalid updates reached GitHub: %d requests", n)
	}
	err := f.client.UpdateCheckRun(context.Background(), targetA(), 424242, inProgress)
	if !errors.Is(err, app.ErrGitHubNotFound) {
		t.Fatalf("missing run: err = %v, want not found", err)
	}
	// A repository outside the installation is not found either.
	tg := targetA()
	tg.RepositoryID = repoB
	if err := f.client.UpdateCheckRun(context.Background(), tg, 1, inProgress); !errors.Is(err, app.ErrGitHubNotFound) {
		t.Fatalf("foreign repo: err = %v, want not found", err)
	}
}

// --- sticky comment ---

func TestStickyCommentIsNeverDuplicated(t *testing.T) {
	ctx := context.Background()
	f := setup(t, setupOpts{})
	id, err := f.client.UpsertStickyComment(ctx, targetA(), 7, 0, "## Glossa\nv1")
	if err != nil {
		t.Fatal(err)
	}
	comments := f.srv.Comments(repoA, 7)
	if len(comments) != 1 || comments[0].ID != id || !strings.Contains(comments[0].Body, github.StickyMarker) {
		t.Fatalf("after create: %+v", comments)
	}

	// Updated in place by its stored ID.
	if got, err := f.client.UpsertStickyComment(ctx, targetA(), 7, id, "## Glossa\nv2"); err != nil || got != id {
		t.Fatalf("update = %d, %v", got, err)
	}
	// A lost stored ID (a retry) finds it by the marker.
	if got, err := f.client.UpsertStickyComment(ctx, targetA(), 7, 0, "## Glossa\nv3"); err != nil || got != id {
		t.Fatalf("rediscover = %d, %v", got, err)
	}
	// A stale stored ID (404) falls back to the marker too.
	if got, err := f.client.UpsertStickyComment(ctx, targetA(), 7, 1, "## Glossa\nv4"); err != nil || got != id {
		t.Fatalf("stale id = %d, %v", got, err)
	}
	comments = f.srv.Comments(repoA, 7)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Body, "## Glossa\nv4") {
		t.Fatalf("want exactly one comment at v4: %+v", comments)
	}
	if n := len(f.srv.Requests(githubtest.RouteCommentCreate)); n != 1 {
		t.Fatalf("%d creates, want 1", n)
	}

	// Deleted on GitHub: the stored ID 404s, no marker is found, and a
	// new one is created.
	f.srv.DeleteComment(id)
	next, err := f.client.UpsertStickyComment(ctx, targetA(), 7, id, "## Glossa\nv5")
	if err != nil || next == id {
		t.Fatalf("recreate = %d, %v", next, err)
	}
	if comments := f.srv.Comments(repoA, 7); len(comments) != 1 || comments[0].ID != next {
		t.Fatalf("after recreate: %+v", comments)
	}
}

func TestStickyCommentSearchSkipsPeopleAndPages(t *testing.T) {
	ctx := context.Background()
	f := setup(t, setupOpts{})
	// A person quoting the marker is not the App's comment.
	f.srv.AddComment(repoA, 7, "copying "+github.StickyMarker, 0)
	for range 150 {
		f.srv.AddComment(repoA, 7, "LGTM", 0)
	}
	ours := f.srv.AddComment(repoA, 7, "old\n\n"+github.StickyMarker, appID)
	got, err := f.client.UpsertStickyComment(ctx, targetA(), 7, 0, "new")
	if err != nil || got != ours {
		t.Fatalf("upsert = %d, %v; want %d", got, err, ours)
	}
	if n := len(f.srv.Requests(githubtest.RouteCommentList)); n != 2 {
		t.Fatalf("%d list pages, want 2", n)
	}
	if n := len(f.srv.Requests(githubtest.RouteCommentCreate)); n != 0 {
		t.Fatalf("created %d comments, want 0", n)
	}
}

// --- ownership ---

func TestVerifyInstallationOwner(t *testing.T) {
	ctx := context.Background()
	f := setup(t, setupOpts{})
	many := make([]int64, 0, 151)
	for i := range 150 {
		many = append(many, int64(90000+i))
	}
	f.srv.AddUser("gho_owner", append(many, instA)...)
	f.srv.AddUser("gho_other", instB)

	in, err := f.client.VerifyInstallationOwner(ctx, "gho_owner", instA)
	if err != nil || in.ID != instA || in.AccountLogin != "acme" {
		t.Fatalf("owner: %+v, %v", in, err)
	}
	if n := len(f.srv.Requests(githubtest.RouteUserInstallations)); n != 2 {
		t.Fatalf("%d pages, want 2", n)
	}
	if got := f.srv.Requests(githubtest.RouteUserInstallations)[0].Header.Get("Authorization"); got != "Bearer gho_owner" {
		t.Fatalf("authorization %q", got)
	}
	if _, err := f.client.VerifyInstallationOwner(ctx, "gho_other", instA); !errors.Is(err, app.ErrInstallationNotVisible) {
		t.Fatalf("other: err = %v", err)
	}
	if _, err := f.client.VerifyInstallationOwner(ctx, "gho_revoked", instA); !errors.Is(err, app.ErrInstallationNotVisible) {
		t.Fatalf("bad token: err = %v", err)
	}
}

func TestExchangeUserCodeRedeemsOnce(t *testing.T) {
	ctx := context.Background()
	f := setup(t, setupOpts{})
	f.srv.AddOAuthCode("code-1", "gho_owner")

	tok, err := f.client.ExchangeUserCode(ctx, "code-1")
	if err != nil || tok != "gho_owner" {
		t.Fatalf("exchange: %q, %v", tok, err)
	}
	req := f.srv.Requests(githubtest.RouteOAuthToken)
	if len(req) != 1 {
		t.Fatalf("%d exchanges, want 1", len(req))
	}
	if got := req[0].Header.Get("Accept"); got != "application/json" {
		t.Fatalf("accept %q", got)
	}
	// A second redemption of the same code is refused, and nothing of
	// the code or the credentials reaches the log.
	if _, err := f.client.ExchangeUserCode(ctx, "code-1"); !errors.Is(err, app.ErrOAuthCodeRejected) {
		t.Fatalf("replay: err = %v", err)
	}
	if _, err := f.client.ExchangeUserCode(ctx, ""); !errors.Is(err, app.ErrGitHubRejected) {
		t.Fatalf("empty code: err = %v", err)
	}
	for _, s := range []string{"code-1", "client-secret", "gho_owner"} {
		if strings.Contains(f.logs.String(), s) {
			t.Fatalf("%q leaked into the log", s)
		}
	}
}

func TestRepositoriesPagesThroughTheInstallation(t *testing.T) {
	f := setup(t, setupOpts{})
	many := make([]int64, 0, 150)
	for i := range 150 {
		many = append(many, int64(700000+i))
	}
	f.srv.AddInstallation(instA, "acme", append(many, repoA)...)
	f.srv.AddRepository(githubtest.Repository{ID: repoA, Name: "shop", FullName: "acme/shop", DefaultBranch: "main"})

	repos, err := f.client.Repositories(context.Background(), targetA())
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 151 {
		t.Fatalf("%d repositories, want 151", len(repos))
	}
	if n := len(f.srv.Requests(githubtest.RouteInstallationRepos)); n != 2 {
		t.Fatalf("%d pages, want 2", n)
	}
	var found bool
	for _, r := range repos {
		if r.ID == repoA {
			found = r.FullName == "acme/shop" && r.DefaultBranch == "main"
		}
	}
	if !found {
		t.Fatalf("the described repository is missing or unnamed: %+v", repos)
	}
}

// --- resilience ---

func TestRateLimitsAreWaitedOut(t *testing.T) {
	for name, fault := range map[string]githubtest.Response{
		"secondary 403 with Retry-After": {Status: http.StatusForbidden, Header: http.Header{"Retry-After": {"1"}},
			Body: `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`},
		"429 with x-ratelimit-reset": {Status: http.StatusTooManyRequests, Header: http.Header{
			"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"RESET"}}, Body: `{"message":"API rate limit exceeded"}`},
	} {
		t.Run(name, func(t *testing.T) {
			f := setup(t, setupOpts{realClock: true})
			run := f.ensure(t, targetA())
			if v := fault.Header.Get("X-Ratelimit-Reset"); v == "RESET" {
				fault.Header.Set("X-Ratelimit-Reset", jsonInt(time.Now().Add(1500*time.Millisecond).Unix()))
			}
			f.srv.Inject(githubtest.RouteCheckUpdate, fault)
			start := time.Now()
			if err := f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, inProgress); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d < 900*time.Millisecond {
				t.Fatalf("retried after %s; the rate limit asked for about 1s", d)
			}
			if n := len(f.srv.Requests(githubtest.RouteCheckUpdate)); n != 2 {
				t.Fatalf("%d attempts, want 2", n)
			}
		})
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestLongRateLimitFailsWithItsWait(t *testing.T) {
	f := setup(t, setupOpts{})
	run := f.ensure(t, targetA())
	f.srv.Inject(githubtest.RouteCheckUpdate, githubtest.Response{
		Status: http.StatusForbidden, Header: http.Header{"Retry-After": {"3600"}},
		Body: `{"message":"You have exceeded a secondary rate limit."}`,
	})
	err := f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, inProgress)
	if !errors.Is(err, app.ErrGitHubRateLimited) {
		t.Fatalf("err = %v, want rate limited", err)
	}
	if d, ok := app.GitHubRetryAfter(err); !ok || d != time.Hour {
		t.Fatalf("RetryAfter = %v, %v", d, ok)
	}
	if n := len(f.srv.Requests(githubtest.RouteCheckUpdate)); n != 1 {
		t.Fatalf("%d attempts, want 1", n)
	}
}

func TestFailuresAreRetriedOnlyWhenSafe(t *testing.T) {
	ctx := context.Background()
	f := setup(t, setupOpts{})
	run := f.ensure(t, targetA())

	f.srv.Inject(githubtest.RouteCheckUpdate, githubtest.Response{Status: http.StatusBadGateway})
	if err := f.client.UpdateCheckRun(ctx, targetA(), run.ID, inProgress); err != nil {
		t.Fatalf("a PATCH is retried after a 502: %v", err)
	}

	f.srv.Inject(githubtest.RouteCheckUpdate, githubtest.Response{Status: http.StatusForbidden, Body: `{"message":"Resource not accessible by integration"}`})
	err := f.client.UpdateCheckRun(ctx, targetA(), run.ID, inProgress)
	if !errors.Is(err, app.ErrGitHubRejected) {
		t.Fatalf("permission 403: err = %v, want rejected", err)
	}

	// A POST that may have been applied is not repeated; the idempotent
	// helpers recover on the next call.
	f.srv.Inject(githubtest.RouteCommentCreate, githubtest.Response{Status: http.StatusBadGateway})
	if _, err := f.client.UpsertStickyComment(ctx, targetA(), 8, 0, "x"); !errors.Is(err, app.ErrGitHubUnavailable) {
		t.Fatalf("POST 502: err = %v, want unavailable", err)
	}
	if n := len(f.srv.Requests(githubtest.RouteCommentCreate)); n != 1 {
		t.Fatalf("a POST was repeated: %d creates", n)
	}
	// Rate-limited POSTs were refused, so they are retried.
	f.srv.Inject(githubtest.RouteCommentCreate, githubtest.Response{Status: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"0"}}})
	if _, err := f.client.UpsertStickyComment(ctx, targetA(), 8, 0, "x"); err != nil {
		t.Fatalf("rate-limited POST: %v", err)
	}
}

func TestAttemptTimeout(t *testing.T) {
	f := setup(t, setupOpts{options: func(o *github.Options) { o.Timeout = 100 * time.Millisecond }})
	run := f.ensure(t, targetA())
	f.srv.Inject(githubtest.RouteCheckUpdate, githubtest.Response{Status: http.StatusOK, Body: `{}`, Delay: time.Second})
	start := time.Now()
	if err := f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, inProgress); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("took %s: the slow attempt was not cut off", d)
	}
}

func TestCircuitBreakerIsPerInstallation(t *testing.T) {
	ctx := context.Background()
	f := setup(t, setupOpts{options: func(o *github.Options) {
		o.MaxAttempts = 1
		o.BreakerFailures = 2
		o.BreakerCooldown = time.Hour
	}})
	run := f.ensure(t, targetA())
	runB := f.ensure(t, targetB())
	for range 2 {
		f.srv.Inject(githubtest.RouteCheckUpdate, githubtest.Response{Status: http.StatusServiceUnavailable})
		if err := f.client.UpdateCheckRun(ctx, targetA(), run.ID, inProgress); !errors.Is(err, app.ErrGitHubUnavailable) {
			t.Fatalf("err = %v", err)
		}
	}
	before := len(f.srv.Requests())
	err := f.client.UpdateCheckRun(ctx, targetA(), run.ID, inProgress)
	if !errors.Is(err, app.ErrGitHubUnavailable) || !errors.Is(err, ferrors.ErrCircuitOpen) {
		t.Fatalf("open circuit: err = %v", err)
	}
	if len(f.srv.Requests()) != before {
		t.Fatal("an open circuit still called GitHub")
	}
	if err := f.client.UpdateCheckRun(ctx, targetB(), runB.ID, inProgress); err != nil {
		t.Fatalf("another installation is unaffected: %v", err)
	}
}

func TestBulkheadIsPerTenant(t *testing.T) {
	ctx := context.Background()
	f := setup(t, setupOpts{options: func(o *github.Options) {
		o.TenantConcurrency = 1
		o.TenantQueue = 1
		o.TenantQueueWait = 50 * time.Millisecond
	}})
	run := f.ensure(t, targetA())
	runB := f.ensure(t, targetB())
	f.srv.Inject(githubtest.RouteCheckUpdate, githubtest.Response{Status: http.StatusOK, Body: `{}`, Delay: 500 * time.Millisecond})
	slow := make(chan error, 1)
	go func() { slow <- f.client.UpdateCheckRun(ctx, targetA(), run.ID, inProgress) }()
	waitFor(t, func() bool { return len(f.srv.Requests(githubtest.RouteCheckUpdate)) == 1 })

	if err := f.client.UpdateCheckRun(ctx, targetA(), run.ID, inProgress); !errors.Is(err, app.ErrGitHubBusy) {
		t.Fatalf("second call of a saturated tenant: err = %v, want busy", err)
	}
	if err := f.client.UpdateCheckRun(ctx, targetB(), runB.ID, inProgress); err != nil {
		t.Fatalf("another tenant is unaffected: %v", err)
	}
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- observability ---

func TestRateLimitIsExposedAndSecretsNeverLogged(t *testing.T) {
	f := setup(t, setupOpts{})
	f.srv.AddUser("gho_secret_user_token", instA)
	if _, ok := f.client.RateLimit(instA); ok {
		t.Fatal("a rate limit before any call")
	}
	run := f.ensure(t, targetA())
	if err := f.client.UpdateCheckRun(context.Background(), targetA(), run.ID, app.CheckRunUpdate{
		Status: app.CheckInProgress, Title: "t", Summary: "PAYLOAD-SUMMARY",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.UpsertStickyComment(context.Background(), targetA(), 7, 0, "PAYLOAD-COMMENT"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.VerifyInstallationOwner(context.Background(), "gho_secret_user_token", instA); err != nil {
		t.Fatal(err)
	}
	rl, ok := f.client.RateLimit(instA)
	if !ok || rl.Limit != 5000 || rl.Remaining >= 5000 || rl.Reset.IsZero() {
		t.Fatalf("rate limit %+v, %v", rl, ok)
	}
	ops := map[string]bool{}
	for _, ci := range f.callInfos() {
		ops[ci.Op] = true
		if ci.Status == 0 {
			t.Errorf("call %s without a status", ci.Op)
		}
	}
	for _, op := range []string{"tokens.create", "checks.list", "checks.create", "checks.update", "comments.list", "comments.create", "user.installations"} {
		if !ops[op] {
			t.Errorf("no CallInfo for %s", op)
		}
	}
	logs := f.logs.String()
	if !strings.Contains(logs, `"installation_id":4242`) {
		t.Fatalf("logs lack installation IDs:\n%s", logs)
	}
	token := strings.TrimPrefix(f.srv.Requests(githubtest.RouteCheckUpdate)[0].Header.Get("Authorization"), "Bearer ")
	for _, secret := range []string{token, "gho_secret_user_token", "PAYLOAD-SUMMARY", "PAYLOAD-COMMENT", "eyJ"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain %q:\n%s", secret, logs)
		}
	}
}
