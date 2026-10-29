// Package githubtest is a fake GitHub for tests: an httptest server with
// exactly the REST endpoints Glossa's GitHub App adapter calls (RFC 0004
// §6), request recording, fault injection, and signed webhook fixtures.
// The adapter tests use it, and so will the M3 system test. It verifies
// what the real API would reject: the App JWT (RS256, issuer, lifetime),
// installation tokens scoped to their repositories, and at most 50
// annotations per check-run request.
package githubtest

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Route names, as Request.Route and Inject use them.
const (
	RouteAccessToken       = "tokens.create"
	RouteUserInstallations = "user.installations"
	RouteOAuthToken        = "oauth.access_token"
	RouteInstallationRepos = "installation.repositories"
	RouteCheckCreate       = "checks.create"
	RouteCheckUpdate       = "checks.update"
	RouteCheckList         = "checks.list"
	RouteCommentCreate     = "comments.create"
	RouteCommentUpdate     = "comments.update"
	RouteCommentList       = "comments.list"
)

// Options configures a fake.
type Options struct {
	// AppID is the App's ID; the JWT's issuer must match it.
	AppID int64
	// PublicKey verifies App JWTs.
	PublicKey *rsa.PublicKey
	// Prefix mounts the API under a path, like GitHub Enterprise Server's
	// "/api/v3". URL includes it.
	Prefix string
	// TokenTTL is an installation token's lifetime (default 1h).
	TokenTTL time.Duration
	// Now is the fake's clock (default time.Now).
	Now func() time.Time
	// RateLimit is the per-token primary limit reported in headers
	// (default 5000).
	RateLimit int
}

// Request is one recorded request.
type Request struct {
	Route  string
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// Response is an injected answer.
type Response struct {
	Status int
	Header http.Header
	Body   string
	// Delay holds the answer back (to provoke client timeouts).
	Delay time.Duration
}

// Annotation is a stored check-run annotation.
type Annotation struct {
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	AnnotationLevel string `json:"annotation_level"`
	Title           string `json:"title,omitempty"`
	Message         string `json:"message"`
}

// CheckRun is a stored check run.
type CheckRun struct {
	ID          int64
	RepoID      int64
	Name        string
	HeadSHA     string
	ExternalID  string
	Status      string
	Conclusion  string
	Title       string
	Summary     string
	Annotations []Annotation
	// Patches counts the PATCH requests applied to it.
	Patches int
}

// Comment is a stored issue comment.
type Comment struct {
	ID     int64
	RepoID int64
	Issue  int
	Body   string
	// AppID is the App that wrote it (0 for a person).
	AppID int64
	Login string
}

type installation struct {
	id      int64
	account string
	repos   map[int64]bool
}

type token struct {
	installation int64
	expires      time.Time
	remaining    int
}

// Server is the fake. Its methods are safe for concurrent use.
type Server struct {
	// URL is the API base URL (with Prefix), for the adapter's config.
	URL string
	// WebURL is the web host, where the App is authorized and OAuth
	// codes are redeemed (Config.WebURL).
	WebURL string
	srv    *httptest.Server

	opts Options

	mu            sync.Mutex
	nextID        int64
	requests      []Request
	faults        map[string][]Response
	installations map[int64]*installation
	users         map[string][]int64
	codes         map[string]string
	repos         map[int64]Repository
	tokens        map[string]*token
	issued        map[int64]int
	checks        map[int64]*CheckRun
	comments      map[int64]*Comment
}

// New starts a fake and stops it when the test ends.
func New(t testing.TB, opts Options) *Server {
	t.Helper()
	if opts.TokenTTL <= 0 {
		opts.TokenTTL = time.Hour
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RateLimit <= 0 {
		opts.RateLimit = 5000
	}
	opts.Prefix = strings.TrimRight(opts.Prefix, "/")
	s := &Server{
		opts:          opts,
		nextID:        1000,
		faults:        map[string][]Response{},
		installations: map[int64]*installation{},
		users:         map[string][]int64{},
		codes:         map[string]string{},
		repos:         map[int64]Repository{},
		tokens:        map[string]*token{},
		issued:        map[int64]int{},
		checks:        map[int64]*CheckRun{},
		comments:      map[int64]*Comment{},
	}
	mux := http.NewServeMux()
	p := opts.Prefix
	s.handle(mux, "POST "+p+"/app/installations/{id}/access_tokens", RouteAccessToken, s.accessToken)
	s.handle(mux, "GET "+p+"/user/installations", RouteUserInstallations, s.userInstallations)
	s.handle(mux, "GET "+p+"/installation/repositories", RouteInstallationRepos, s.installationRepos)
	// The OAuth exchange lives on the web host, not under the API prefix.
	s.handle(mux, "POST /login/oauth/access_token", RouteOAuthToken, s.oauthToken)
	s.handle(mux, "POST "+p+"/repositories/{repo}/check-runs", RouteCheckCreate, s.repo(s.createCheck))
	s.handle(mux, "PATCH "+p+"/repositories/{repo}/check-runs/{id}", RouteCheckUpdate, s.repo(s.updateCheck))
	s.handle(mux, "GET "+p+"/repositories/{repo}/commits/{sha}/check-runs", RouteCheckList, s.repo(s.listChecks))
	s.handle(mux, "POST "+p+"/repositories/{repo}/issues/{issue}/comments", RouteCommentCreate, s.repo(s.createComment))
	s.handle(mux, "PATCH "+p+"/repositories/{repo}/issues/comments/{id}", RouteCommentUpdate, s.repo(s.updateComment))
	s.handle(mux, "GET "+p+"/repositories/{repo}/issues/{issue}/comments", RouteCommentList, s.repo(s.listComments))
	s.srv = httptest.NewServer(mux)
	s.URL, s.WebURL = s.srv.URL+p, s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// Client returns an HTTP client for the fake.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// AddInstallation registers an installation on account with access to
// the repositories (by numeric ID).
func (s *Server) AddInstallation(id int64, account string, repos ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := &installation{id: id, account: account, repos: map[int64]bool{}}
	for _, r := range repos {
		in.repos[r] = true
	}
	s.installations[id] = in
}

// AddUser registers a user OAuth token that sees the installations.
func (s *Server) AddUser(oauthToken string, installations ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[oauthToken] = append([]int64(nil), installations...)
}

// AddOAuthCode registers a one-time authorization code that redeems to
// oauthToken, as the install flow's round trip produces one. A second
// redemption of the same code is refused, as GitHub refuses it.
func (s *Server) AddOAuthCode(code, oauthToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = oauthToken
}

// Repository is a repository the App can see.
type Repository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

// AddRepository describes a repository, so the installation listing can
// name it. A repository an installation holds but nothing describes is
// listed with a generated name.
func (s *Server) AddRepository(r Repository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[r.ID] = r
}

// Inject queues a one-shot answer for the next request to route; it is
// served instead of the real handler (after recording the request).
func (s *Server) Inject(route string, r Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[route] = append(s.faults[route], r)
}

// Requests returns the recorded requests, optionally only for routes.
func (s *Server) Requests(routes ...string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Request
	for _, r := range s.requests {
		if len(routes) == 0 || contains(routes, r.Route) {
			out = append(out, r)
		}
	}
	return out
}

// TokensIssued counts the installation tokens minted for installation.
func (s *Server) TokensIssued(installation int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issued[installation]
}

// RevokeTokens invalidates every installation token (as an uninstall or
// key rotation would); the next call answers 401.
func (s *Server) RevokeTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = map[string]*token{}
}

// CheckRuns returns repo's check runs in creation order.
func (s *Server) CheckRuns(repo int64) []CheckRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []CheckRun
	for _, c := range s.checks {
		if c.RepoID == repo {
			cp := *c
			cp.Annotations = append([]Annotation(nil), c.Annotations...)
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Comments returns the comments on repo's issue (or pull request).
func (s *Server) Comments(repo int64, issue int) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueComments(repo, issue)
}

// AddComment seeds a comment, as the App (appID) or a person (0).
func (s *Server) AddComment(repo int64, issue int, body string, appID int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addComment(repo, issue, body, appID)
}

// DeleteComment removes a comment, as someone deleting it on GitHub.
func (s *Server) DeleteComment(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.comments, id)
}

// --- plumbing ---

type handler func(w http.ResponseWriter, r *http.Request, body []byte) (int, any)

func (s *Server) handle(mux *http.ServeMux, pattern, route string, h handler) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, Request{
			Route: route, Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(),
			Header: r.Header.Clone(), Body: body,
		})
		var fault *Response
		if q := s.faults[route]; len(q) > 0 {
			fault = &q[0]
			s.faults[route] = q[1:]
		}
		s.mu.Unlock()
		if fault != nil {
			s.serveFault(w, r, *fault)
			return
		}
		status, out := h(w, r, body)
		writeJSON(w, status, out)
	})
}

func (s *Server) serveFault(w http.ResponseWriter, r *http.Request, f Response) {
	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-r.Context().Done():
			return
		}
	}
	for k, v := range f.Header {
		w.Header()[k] = v
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(f.Status)
	_, _ = io.WriteString(w, f.Body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func message(msg string) map[string]string {
	return map[string]string{"message": msg, "documentation_url": "https://docs.github.com/rest"}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	for _, p := range []string{"Bearer ", "bearer ", "token "} {
		if strings.HasPrefix(h, p) {
			return strings.TrimPrefix(h, p)
		}
	}
	return ""
}

func (s *Server) newID() int64 {
	s.nextID++
	return s.nextID
}

// --- App auth ---

func (s *Server) accessToken(w http.ResponseWriter, r *http.Request, _ []byte) (int, any) {
	if err := s.verifyJWT(bearer(r)); err != nil {
		return http.StatusUnauthorized, message("A JSON web token could not be decoded: " + err.Error())
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return http.StatusNotFound, message("Not Found")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.installations[id]; !ok {
		return http.StatusNotFound, message("Not Found")
	}
	s.issued[id]++
	tok := fmt.Sprintf("ghs_fake%d_%d", id, s.newID())
	exp := s.opts.Now().Add(s.opts.TokenTTL).UTC().Truncate(time.Second)
	s.tokens[tok] = &token{installation: id, expires: exp, remaining: s.opts.RateLimit}
	return http.StatusCreated, map[string]any{
		"token":      tok,
		"expires_at": exp.Format(time.RFC3339),
		"permissions": map[string]string{
			"checks": "write", "pull_requests": "write", "metadata": "read",
		},
	}
}

func (s *Server) verifyJWT(raw string) error {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return fmt.Errorf("not a JWT")
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := decodeSegment(parts[0], &header); err != nil || header.Alg != "RS256" {
		return fmt.Errorf("alg must be RS256")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("bad signature encoding")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if s.opts.PublicKey == nil || rsa.VerifyPKCS1v15(s.opts.PublicKey, crypto.SHA256, sum[:], sig) != nil {
		return fmt.Errorf("signature does not verify")
	}
	var claims struct {
		Iss json.RawMessage `json:"iss"`
		Iat int64           `json:"iat"`
		Exp int64           `json:"exp"`
	}
	if err := decodeSegment(parts[1], &claims); err != nil {
		return fmt.Errorf("bad claims")
	}
	if strings.Trim(string(claims.Iss), `"`) != strconv.FormatInt(s.opts.AppID, 10) {
		return fmt.Errorf("'Issuer' claim ('iss') must be the App ID")
	}
	now := s.opts.Now().Unix()
	switch {
	case claims.Iat > now:
		return fmt.Errorf("'Issued at' claim ('iat') must be an Integer representing a time in the past")
	case claims.Exp <= now:
		return fmt.Errorf("'Expiration time' claim ('exp') is in the past")
	case claims.Exp > now+600:
		return fmt.Errorf("'Expiration time' claim ('exp') is too far in the future")
	}
	return nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// --- installation-token endpoints ---

type repoHandler func(w http.ResponseWriter, r *http.Request, repo int64, body []byte) (int, any)

// repo authenticates the installation token, checks the repository is
// in the installation and sets the rate-limit headers.
func (s *Server) repo(h repoHandler) handler {
	return func(w http.ResponseWriter, r *http.Request, body []byte) (int, any) {
		repo, err := strconv.ParseInt(r.PathValue("repo"), 10, 64)
		if err != nil {
			return http.StatusNotFound, message("Not Found")
		}
		s.mu.Lock()
		tok, ok := s.tokens[bearer(r)]
		if !ok || !s.opts.Now().Before(tok.expires) {
			s.mu.Unlock()
			return http.StatusUnauthorized, message("Bad credentials")
		}
		tok.remaining--
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(s.opts.RateLimit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(tok.remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(s.opts.Now().Add(time.Hour).Unix(), 10))
		allowed := s.installations[tok.installation] != nil && s.installations[tok.installation].repos[repo]
		s.mu.Unlock()
		if !allowed {
			return http.StatusNotFound, message("Not Found")
		}
		return h(w, r, repo, body)
	}
}

type checkOutput struct {
	Title       string       `json:"title"`
	Summary     string       `json:"summary"`
	Annotations []Annotation `json:"annotations"`
}

type checkBody struct {
	Name       *string      `json:"name"`
	HeadSHA    *string      `json:"head_sha"`
	ExternalID *string      `json:"external_id"`
	Status     *string      `json:"status"`
	Conclusion *string      `json:"conclusion"`
	Output     *checkOutput `json:"output"`
}

func (s *Server) createCheck(_ http.ResponseWriter, _ *http.Request, repo int64, body []byte) (int, any) {
	var in checkBody
	if err := json.Unmarshal(body, &in); err != nil || in.Name == nil || in.HeadSHA == nil {
		return http.StatusUnprocessableEntity, message("Invalid request: name and head_sha are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &CheckRun{ID: s.newID(), RepoID: repo, Name: *in.Name, HeadSHA: *in.HeadSHA, Status: "queued"}
	if in.ExternalID != nil {
		c.ExternalID = *in.ExternalID
	}
	if status, err := s.applyCheck(c, in); err != nil {
		return status, message(err.Error())
	}
	s.checks[c.ID] = c
	return http.StatusCreated, checkJSON(c)
}

func (s *Server) updateCheck(_ http.ResponseWriter, r *http.Request, repo int64, body []byte) (int, any) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in checkBody
	if err := json.Unmarshal(body, &in); err != nil {
		return http.StatusUnprocessableEntity, message("Problems parsing JSON")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.checks[id]
	if !ok || c.RepoID != repo {
		return http.StatusNotFound, message("Not Found")
	}
	next := *c
	next.Annotations = append([]Annotation(nil), c.Annotations...)
	if status, err := s.applyCheck(&next, in); err != nil {
		return status, message(err.Error())
	}
	next.Patches++
	*c = next
	return http.StatusOK, checkJSON(c)
}

func (s *Server) applyCheck(c *CheckRun, in checkBody) (int, error) {
	if in.Status != nil {
		switch *in.Status {
		case "queued", "in_progress", "completed":
			c.Status = *in.Status
		default:
			return http.StatusUnprocessableEntity, fmt.Errorf("invalid status %q", *in.Status)
		}
	}
	if in.Conclusion != nil {
		c.Conclusion = *in.Conclusion
		c.Status = "completed"
	}
	if c.Status == "completed" && c.Conclusion == "" {
		return http.StatusUnprocessableEntity, fmt.Errorf("conclusion is required when status is completed")
	}
	if o := in.Output; o != nil {
		if o.Title == "" || o.Summary == "" {
			return http.StatusUnprocessableEntity, fmt.Errorf("output requires title and summary")
		}
		if len(o.Annotations) > 50 {
			return http.StatusUnprocessableEntity, fmt.Errorf("only 50 annotations are allowed per request")
		}
		c.Title, c.Summary = o.Title, o.Summary
		c.Annotations = append(c.Annotations, o.Annotations...)
	}
	return 0, nil
}

func checkJSON(c *CheckRun) map[string]any {
	var conclusion any
	if c.Conclusion != "" {
		conclusion = c.Conclusion
	}
	return map[string]any{
		"id": c.ID, "name": c.Name, "head_sha": c.HeadSHA, "external_id": c.ExternalID,
		"status": c.Status, "conclusion": conclusion,
		"output": map[string]any{"title": c.Title, "summary": c.Summary, "annotations_count": len(c.Annotations)},
	}
}

func (s *Server) listChecks(_ http.ResponseWriter, r *http.Request, repo int64, _ []byte) (int, any) {
	sha, name := r.PathValue("sha"), r.URL.Query().Get("check_name")
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := []map[string]any{}
	ids := make([]int64, 0, len(s.checks))
	for id := range s.checks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] }) // newest first, like GitHub
	for _, id := range ids {
		c := s.checks[id]
		if c.RepoID == repo && c.HeadSHA == sha && (name == "" || c.Name == name) {
			runs = append(runs, checkJSON(c))
		}
	}
	return http.StatusOK, map[string]any{"total_count": len(runs), "check_runs": runs}
}

func (s *Server) addComment(repo int64, issue int, body string, appID int64) int64 {
	c := &Comment{ID: s.newID(), RepoID: repo, Issue: issue, Body: body, AppID: appID, Login: "octocat"}
	if appID != 0 {
		c.Login = "glossa[bot]"
	}
	s.comments[c.ID] = c
	return c.ID
}

func (s *Server) issueComments(repo int64, issue int) []Comment {
	var out []Comment
	for _, c := range s.comments {
		if c.RepoID == repo && c.Issue == issue {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func commentJSON(c *Comment) map[string]any {
	out := map[string]any{"id": c.ID, "body": c.Body, "user": map[string]any{"login": c.Login, "type": "User"}}
	if c.AppID != 0 {
		out["user"] = map[string]any{"login": c.Login, "type": "Bot"}
		out["performed_via_github_app"] = map[string]any{"id": c.AppID}
	}
	return out
}

type commentBody struct {
	Body *string `json:"body"`
}

func (s *Server) createComment(_ http.ResponseWriter, r *http.Request, repo int64, body []byte) (int, any) {
	issue, err := strconv.Atoi(r.PathValue("issue"))
	var in commentBody
	if err != nil || json.Unmarshal(body, &in) != nil || in.Body == nil {
		return http.StatusUnprocessableEntity, message("body is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.addComment(repo, issue, *in.Body, s.opts.AppID)
	return http.StatusCreated, commentJSON(s.comments[id])
}

func (s *Server) updateComment(_ http.ResponseWriter, r *http.Request, repo int64, body []byte) (int, any) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in commentBody
	if json.Unmarshal(body, &in) != nil || in.Body == nil {
		return http.StatusUnprocessableEntity, message("body is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.comments[id]
	if !ok || c.RepoID != repo {
		return http.StatusNotFound, message("Not Found")
	}
	if c.AppID != s.opts.AppID {
		return http.StatusForbidden, message("Resource not accessible by integration")
	}
	c.Body = *in.Body
	return http.StatusOK, commentJSON(c)
}

func (s *Server) listComments(_ http.ResponseWriter, r *http.Request, repo int64, _ []byte) (int, any) {
	issue, _ := strconv.Atoi(r.PathValue("issue"))
	perPage, page := paging(r.URL.Query())
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.issueComments(repo, issue)
	out := []map[string]any{}
	for i := (page - 1) * perPage; i < len(all) && i < page*perPage; i++ {
		out = append(out, commentJSON(&all[i]))
	}
	return http.StatusOK, out
}

// installationRepos is GET /installation/repositories, with an
// installation token.
func (s *Server) installationRepos(w http.ResponseWriter, r *http.Request, _ []byte) (int, any) {
	perPage, page := paging(r.URL.Query())
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.tokens[bearer(r)]
	if !ok || !s.opts.Now().Before(tok.expires) {
		return http.StatusUnauthorized, message("Bad credentials")
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(s.opts.RateLimit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(tok.remaining))
	in := s.installations[tok.installation]
	if in == nil {
		return http.StatusNotFound, message("Not Found")
	}
	ids := make([]int64, 0, len(in.repos))
	for id := range in.repos {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	list := []Repository{}
	for i := (page - 1) * perPage; i < len(ids) && i < page*perPage; i++ {
		r, ok := s.repos[ids[i]]
		if !ok {
			name := "repo-" + strconv.FormatInt(ids[i], 10)
			r = Repository{ID: ids[i], Name: name, FullName: in.account + "/" + name, DefaultBranch: "main"}
		}
		list = append(list, r)
	}
	return http.StatusOK, map[string]any{"total_count": len(ids), "repositories": list}
}

// oauthToken is POST /login/oauth/access_token: it redeems a one-time
// code, as the install flow's round trip does.
func (s *Server) oauthToken(_ http.ResponseWriter, _ *http.Request, body []byte) (int, any) {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": "invalid_request"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.codes[form.Get("code")]
	if !ok {
		// GitHub answers 200 with an error body for a bad code.
		return http.StatusOK, map[string]any{
			"error": "bad_verification_code", "error_description": "The code passed is incorrect or expired.",
		}
	}
	delete(s.codes, form.Get("code"))
	return http.StatusOK, map[string]any{"access_token": token, "token_type": "bearer", "scope": ""}
}

func paging(q url.Values) (perPage, page int) {
	perPage, _ = strconv.Atoi(q.Get("per_page"))
	if perPage <= 0 || perPage > 100 {
		perPage = 30
	}
	page, _ = strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	return perPage, page
}

// --- user-token endpoints ---

func (s *Server) userInstallations(_ http.ResponseWriter, r *http.Request, _ []byte) (int, any) {
	perPage, page := paging(r.URL.Query())
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, ok := s.users[bearer(r)]
	if !ok {
		return http.StatusUnauthorized, message("Bad credentials")
	}
	list := []map[string]any{}
	for i := (page - 1) * perPage; i < len(ids) && i < page*perPage; i++ {
		in := s.installations[ids[i]]
		account := ""
		if in != nil {
			account = in.account
		}
		list = append(list, map[string]any{
			"id":      ids[i],
			"app_id":  s.opts.AppID,
			"account": map[string]any{"id": ids[i] * 10, "login": account, "type": "Organization"},
		})
	}
	return http.StatusOK, map[string]any{"total_count": len(ids), "installations": list}
}
