//go:build system

package m2_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m2/fixture"
)

// TestM2Exit is RFC 0003 §1's exit test: a partially translated catalog
// in new locales is filled by the platform and handed to reviewers as a
// short, risk-ordered queue, with everything else routed by policy —
// then approved, published and rendered, code → message → translation →
// release → runtime with AI in the middle. Everything goes through the
// public /v1 API of a real glossa-server.
func TestM2Exit(t *testing.T) {
	started := time.Now()
	f, err := fixture.Load("testdata")
	if err != nil {
		t.Fatal(err)
	}
	s := &scenario{t: t, f: f, phases: map[string]time.Duration{}, results: map[string]*localeResult{}}
	for _, l := range f.FillLocales {
		s.results[l] = &localeResult{locale: l, expect: f.Expect[l], jobs: map[string]int{}, failures: map[string]int{},
			slipsCaught: map[string]int{}, reviewReasons: map[string]int{}}
	}
	s.phase("deploy", func() { s.d = deploy(t) })
	s.phase("tenant and project", s.setup)
	s.phase("import catalog (XLIFF)", s.importCatalog)
	s.phase("import knowledge (TBX, TMX, style guides)", s.importKnowledge)
	s.phase("configure AI", s.configureAI)
	s.phase("preview fill", s.previewFill)
	s.phase("fill es, fr, ja", s.fill)
	s.phase("collect results", s.collect)
	s.phase("check routing", s.checkRouting)
	s.phase("check review queue", s.checkQueue)
	s.phase("check privacy and spend", s.checkPrivacyAndSpend)
	s.phase("accept approve_recommended", s.acceptRecommended)
	s.phase("check provenance", s.checkProvenance)
	s.phase("publish to staging", s.publish)
	s.phase("render through glossa-edge", s.render)
	if t.Failed() {
		return
	}
	report := s.report()
	if err := os.WriteFile("REPORT.md", report, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.phaseNames {
		t.Logf("%-45s %6.1fs", p, s.phases[p].Seconds())
	}
	t.Logf("total %.1fs; wrote REPORT.md", time.Since(started).Seconds())
}

type scenario struct {
	t        *testing.T
	f        *fixture.Fixture
	d        *deployment
	owner    *client
	tenant   string
	project  string
	provider *fakeProvider

	phases     map[string]time.Duration
	phaseNames []string

	imports     []importResult
	preview     fillPreview
	fillDoc     fillDoc
	jobs        []job
	suggestions []suggestion
	queue       []suggestion
	disclosures []disclosure
	budget      budgetDoc
	metrics     metricsDoc
	release     releaseDoc
	samples     []renderSample
	results     map[string]*localeResult
}

func (s *scenario) phase(name string, fn func()) {
	if s.t.Failed() {
		return
	}
	start := time.Now()
	fn()
	s.phases[name] = time.Since(start)
	s.phaseNames = append(s.phaseNames, name)
}

func (s *scenario) tenantPath(p string) string { return "/v1/tenants/" + s.tenant + p }
func (s *scenario) projectPath(p string) string {
	return "/v1/tenants/" + s.tenant + "/projects/" + s.project + p
}

// localeResult is what the fill did for one locale.
type localeResult struct {
	locale          string
	expect          fixture.LocaleExpectation
	jobs            map[string]int
	failures        map[string]int
	tm, ai          int
	approve, review int
	repaired        int
	slipsCaught     map[string]int
	reviewReasons   map[string]int
	termIDs         int
	accepted        int
	approvedAfter   int
	releaseMessages int
}

// setup creates the organization, the project (German source) and the
// target locales.
func (s *scenario) setup() {
	t := s.t
	s.owner = s.d.signIn(t, "lena@example.com")
	var org struct{ ID string }
	s.owner.do(http.MethodPost, "/v1/tenants", map[string]string{"slug": "brotwerk", "name": "Brotwerk"}, http.StatusCreated, &org)
	s.tenant = org.ID
	var project struct{ ID string }
	s.owner.do(http.MethodPost, s.tenantPath("/projects"),
		map[string]any{"slug": "cloud", "name": "Brotwerk Cloud", "source_locale": s.f.SourceLocale}, http.StatusCreated, &project)
	s.project = project.ID
	for _, l := range s.f.Locales {
		s.owner.do(http.MethodPost, s.projectPath("/locales"), map[string]string{"code": l}, http.StatusCreated, nil)
	}
}

type importResult struct {
	file    string
	summary importSummary
}

type importSummary struct {
	Created, Updated, Unchanged, Conflict, Invalid int
	ByKind                                         map[string]struct {
		Created, Updated, Unchanged, Conflict, Invalid int
	} `json:"by_kind"`
}

type importJob struct {
	ID             string        `json:"id"`
	State          string        `json:"state"`
	UploadURL      string        `json:"upload_url"`
	FailureCode    string        `json:"failure_code"`
	FailureMessage string        `json:"failure_message"`
	Summary        importSummary `json:"summary"`
}

// runImport creates an import job, uploads the file and waits for it.
func (s *scenario) runImport(format, file string, projectScoped bool) importSummary {
	t := s.t
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"format": format, "file_name": file}
	if projectScoped {
		body["project_id"] = s.project
	}
	var j importJob
	s.owner.do(http.MethodPost, s.tenantPath("/import-jobs"), body, http.StatusCreated, &j, "Idempotency-Key", "m2-import-"+file)
	if j.UploadURL == "" {
		t.Fatalf("import job %s has no upload_url", j.ID)
	}
	upload := j.UploadURL
	if u, err := url.Parse(upload); err == nil && u.IsAbs() {
		upload = u.RequestURI()
	}
	if _, err := s.owner.send(http.MethodPut, upload, bytes.NewReader(raw), "application/octet-stream", http.StatusOK, nil); err != nil {
		t.Fatalf("upload %s: %v", file, err)
	}
	eventually(t, 2*time.Minute, "import of "+file, func() (bool, string) {
		s.owner.do(http.MethodGet, s.tenantPath("/import-jobs/"+j.ID), nil, http.StatusOK, &j)
		return j.State == "succeeded" || j.State == "failed" || j.State == "cancelled", j.State
	})
	if j.State != "succeeded" {
		t.Fatalf("import of %s ended %s (%s: %s)", file, j.State, j.FailureCode, j.FailureMessage)
	}
	s.imports = append(s.imports, importResult{file: file, summary: j.Summary})
	return j.Summary
}

// importCatalog imports the XLIFF catalogs: de→en creates the 600
// messages with their namespaces, descriptions and max lengths and the
// complete English; de→es and de→fr add the partial translations. All
// translations are approved (state final, imported by an owner).
func (s *scenario) importCatalog() {
	t := s.t
	for _, l := range s.f.Locales {
		file := fixture.XLIFFFile(s.f.SourceLocale, l)
		if _, err := os.Stat(filepath.Join("testdata", file)); err != nil {
			continue // ja: nothing translated yet
		}
		sum := s.runImport("xliff", file, true)
		e := s.existing(l)
		msgs, trs := sum.ByKind["message"], sum.ByKind["translation"]
		switch {
		case l == "en" && (msgs.Created != len(s.f.Messages) || trs.Created != e):
			t.Fatalf("import %s: %+v", file, sum)
		case l != "en" && (msgs.Unchanged != e || trs.Created != e):
			t.Fatalf("import %s: %+v", file, sum)
		case sum.Conflict+sum.Invalid > 0:
			t.Fatalf("import %s: %+v", file, sum)
		}
	}
}

// existing counts the fixture's translations in l before the fill.
func (s *scenario) existing(l string) int {
	n := 0
	for _, m := range s.f.Messages {
		if t := m.Translations[l]; t != nil && t.Existing {
			n++
		}
	}
	return n
}

// importKnowledge imports the termbase (TBX) and the legacy memory
// (TMX) tenant-wide, adds the project's style guides, and waits for
// translation memory to derive units from every approved translation.
func (s *scenario) importKnowledge() {
	t := s.t
	if sum := s.runImport("tbx", fixture.FileTBX, false); sum.ByKind["concept"].Created != len(s.f.Concepts) {
		t.Fatalf("import tbx: %+v", sum)
	}
	if sum := s.runImport("tmx", fixture.FileTMX, false); sum.ByKind["tm_unit"].Created != len(s.f.LegacyTM) {
		t.Fatalf("import tmx: %+v", sum)
	}
	for _, g := range s.f.StyleGuides {
		body := map[string]any{"project_id": s.project, "name": g.Name, "fields": g.Fields, "rules": g.Rules}
		if g.Locale != "" {
			body["locale"] = g.Locale
		}
		if g.Namespace != "" {
			body["namespace"] = g.Namespace
		}
		s.owner.do(http.MethodPost, s.tenantPath("/style-guides"), body, http.StatusCreated, nil)
	}
	for _, l := range []string{"es", "fr"} {
		want := s.existing(l)
		eventually(t, 2*time.Minute, "translation memory derived for "+l, func() (bool, string) {
			units := list[struct {
				Origin string `json:"origin"`
			}](s.owner, s.tenantPath("/tm-units"), url.Values{"target_locale": {l}, "project": {s.project}})
			n := 0
			for _, u := range units {
				if u.Origin == "translation" {
					n++
				}
			}
			return n >= want, fmt.Sprintf("%d of %d units", n, want)
		})
	}
}

// configureAI points the tenant at the fake provider (loopback, allowed
// by the deployment), routes translate and assess to it, prices its
// models, gives consent and a budget, tags the legal namespace
// sensitive and keeps auto-approval off.
func (s *scenario) configureAI() {
	t := s.t
	s.provider = newFakeProvider(t, s.f)
	s.owner.do(http.MethodPost, s.tenantPath("/ai-providers"), map[string]any{
		"name": fakeProviderName, "kind": "openai_compatible", "base_url": s.provider.baseURL(), "api_key": fakeAPIKey,
		"models": []string{translateModel, assessModel},
	}, http.StatusCreated, nil)
	s.owner.do(http.MethodPut, s.tenantPath("/ai-routing-policy"), map[string]any{"rules": []map[string]any{
		{"task": "translate", "routes": []map[string]any{{"provider": fakeProviderName, "model": translateModel, "max_tokens": 1024}}},
		{"task": "assess", "routes": []map[string]any{{"provider": fakeProviderName, "model": assessModel, "max_tokens": 256}}},
	}}, http.StatusOK, nil)
	s.owner.do(http.MethodPut, s.tenantPath("/ai-prices"), map[string]any{"overrides": map[string]any{
		fakeProviderName + "/" + translateModel: map[string]float64{"input_per_mtok": 3, "output_per_mtok": 15},
		fakeProviderName + "/" + assessModel:    map[string]float64{"input_per_mtok": 1, "output_per_mtok": 5},
	}}, http.StatusOK, nil)
	s.owner.do(http.MethodPut, s.tenantPath("/ai-settings"), map[string]any{
		"provider_consent": true, "monthly_budget_micro_usd": 200_000_000, "max_concurrent_jobs": 16,
	}, http.StatusOK, nil)

	var ps struct {
		Review map[string]any `json:"review"`
	}
	s.owner.do(http.MethodGet, s.projectPath("/ai-settings"), nil, http.StatusOK, &ps)
	review := ps.Review
	review["auto_approve"] = false
	tags := map[string][]string{}
	for _, ns := range s.f.SensitiveNamespaces {
		tags[ns] = []string{"sensitive", "legal"}
	}
	var saved struct {
		Review struct {
			AutoApprove  bool     `json:"auto_approve"`
			RecommendMin float64  `json:"recommend_min"`
			ForceReview  []string `json:"force_review"`
		} `json:"review"`
		NamespaceTags map[string][]string `json:"namespace_tags"`
	}
	s.owner.do(http.MethodPut, s.projectPath("/ai-settings"), map[string]any{"namespace_tags": tags, "review": review}, http.StatusOK, &saved)
	if saved.Review.AutoApprove || saved.Review.RecommendMin != 0.75 ||
		!slices.Contains(saved.Review.ForceReview, "term_forbidden") || !slices.Contains(saved.Review.ForceReview, "max_length") ||
		!slices.Contains(saved.NamespaceTags["legal"], "sensitive") {
		t.Fatalf("project AI settings = %+v", saved)
	}
}

type fillPreview struct {
	Locales []struct {
		Locale   string         `json:"locale"`
		Keys     []string       `json:"keys"`
		Existing int            `json:"existing"`
		Provider int            `json:"provider"`
		TMExact  int            `json:"tm_exact"`
		Refused  map[string]int `json:"refused"`
		Skipped  map[string]int `json:"skipped"`
		Cost     costEstimate   `json:"cost"`
	} `json:"locales"`
	Cost     costEstimate `json:"cost"`
	Warnings []string     `json:"warnings"`
}

type costEstimate struct {
	Estimated int64 `json:"estimated_micro_usd"`
	Max       int64 `json:"max_micro_usd"`
	Unpriced  bool  `json:"unpriced"`
}

// previewFill asks what a fill of es, fr and ja would do, and checks it
// against the fixture: which messages reuse translation memory, which
// reach the provider, which are refused as sensitive.
func (s *scenario) previewFill() {
	t := s.t
	s.owner.do(http.MethodPost, s.projectPath("/ai-fill-previews"), map[string]any{"locales": s.f.FillLocales}, http.StatusOK, &s.preview)
	if len(s.preview.Warnings) > 0 || s.preview.Cost.Estimated <= 0 || len(s.preview.Locales) != len(s.f.FillLocales) {
		t.Fatalf("preview = %+v", s.preview)
	}
	for _, lp := range s.preview.Locales {
		e := s.f.Expect[lp.Locale]
		if lp.Existing != 0 || lp.TMExact != e.PreviewTMExact || lp.Provider != e.AIDrafted-e.TMBlocked ||
			lp.Refused["sensitive"] != e.Sensitive || len(lp.Keys) != e.Missing-e.Sensitive {
			t.Errorf("preview %s: %d keys, tm_exact %d, provider %d, refused %v; want %d keys, tm_exact %d, provider %d, sensitive %d",
				lp.Locale, len(lp.Keys), lp.TMExact, lp.Provider, lp.Refused, e.Missing-e.Sensitive, e.PreviewTMExact, e.AIDrafted-e.TMBlocked, e.Sensitive)
		}
	}
}

type fillDoc struct {
	ID           string         `json:"id"`
	JobsCreated  int            `json:"jobs_created"`
	JobsExisting int            `json:"jobs_existing"`
	JobStates    map[string]int `json:"job_states"`
	Skipped      map[string]int `json:"skipped"`
	Warnings     []string       `json:"warnings"`
}

// fill queues the fill and waits until every job has finished.
func (s *scenario) fill() {
	t := s.t
	s.owner.do(http.MethodPost, s.projectPath("/ai-fills"), map[string]any{"locales": s.f.FillLocales}, http.StatusCreated, &s.fillDoc,
		"Idempotency-Key", "m2-fill")
	want, sensitive := 0, 0
	for _, l := range s.f.FillLocales {
		e := s.f.Expect[l]
		want += e.Missing - e.Sensitive
		sensitive += e.Sensitive
	}
	if s.fillDoc.JobsCreated != want || s.fillDoc.Skipped["sensitive"] != sensitive || len(s.fillDoc.Warnings) > 0 {
		t.Fatalf("fill = %+v, want %d jobs and %d sensitive skipped", s.fillDoc, want, sensitive)
	}
	start, last := time.Now(), time.Now()
	defer func() {
		if !t.Failed() {
			return
		}
		all := list[map[string]any](s.owner, s.tenantPath("/ai-jobs"), nil)
		byState := map[string]int{}
		for _, j := range all {
			byState[fmt.Sprint(j["state"], " fill=", j["fill_id"] == s.fillDoc.ID)]++
			if j["state"] == "queued" || j["state"] == "running" {
				delete(j, "audit")
				t.Logf("unfinished job: %v", j)
			}
		}
		t.Logf("all %d jobs: %v", len(all), byState)
		t.Logf("fake provider: %+v", s.provider.snapshot())
	}()
	eventually(t, 3*time.Minute, "the fill's jobs", func() (bool, string) {
		var doc fillDoc // fresh: decoding into the old one would keep states that emptied
		s.owner.do(http.MethodGet, s.tenantPath("/ai-fills/"+s.fillDoc.ID), nil, http.StatusOK, &doc)
		s.fillDoc = doc
		if time.Since(last) > 15*time.Second {
			last = time.Now()
			t.Logf("fill after %s: %v", time.Since(start).Round(time.Second), s.fillDoc.JobStates)
		}
		return s.fillDoc.JobStates["queued"]+s.fillDoc.JobStates["running"] == 0, fmt.Sprint(s.fillDoc.JobStates)
	})
}

type job struct {
	ID           string `json:"id"`
	MessageID    string `json:"message_id"`
	MessageKey   string `json:"message_key"`
	Namespace    string `json:"namespace"`
	Locale       string `json:"locale"`
	State        string `json:"state"`
	FailureCode  string `json:"failure_code"`
	LastError    string `json:"last_error"`
	SuggestionID string `json:"suggestion_id"`
}

type factor struct {
	Factor       string  `json:"factor"`
	Value        float64 `json:"value"`
	Contribution float64 `json:"contribution"`
	Reason       string  `json:"reason"`
}

type suggestion struct {
	ID          string   `json:"id"`
	JobID       string   `json:"job_id"`
	MessageID   string   `json:"message_id"`
	MessageKey  string   `json:"message_key"`
	Namespace   string   `json:"namespace"`
	Locale      string   `json:"locale"`
	Message     string   `json:"message"`
	Score       float64  `json:"score"`
	Action      string   `json:"action"`
	RiskTags    []string `json:"risk_tags"`
	Status      string   `json:"status"`
	Explanation []factor `json:"explanation"`
	Findings    []struct {
		Code     string `json:"code"`
		Severity string `json:"severity"`
	} `json:"findings"`
	TermFindings []struct {
		Code string `json:"code"`
		Term string `json:"term"`
	} `json:"term_findings"`
	Provenance struct {
		Origin        string   `json:"origin"`
		Provider      string   `json:"provider"`
		Model         string   `json:"model"`
		PromptVersion string   `json:"prompt_version"`
		StyleVersion  string   `json:"style_version"`
		TMUnitIDs     []string `json:"tm_unit_ids"`
		TermIDs       []string `json:"term_ids"`
		Repairs       int      `json:"repairs"`
	} `json:"provenance"`
	CostMicroUSD int64 `json:"cost_micro_usd"`
	Source       *struct {
		MF2 string `json:"mf2"`
	} `json:"source"`
	TranslationRevision *int `json:"translation_revision"`
}

// has reports a factor that lowered the score.
func (sg suggestion) has(name string) bool {
	return slices.ContainsFunc(sg.Explanation, func(f factor) bool { return f.Factor == name && f.Contribution < 0 })
}

type disclosure struct {
	ID        string `json:"id"`
	JobID     string `json:"job_id"`
	MessageID string `json:"message_id"`
	Locale    string `json:"locale"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Task      string `json:"task"`
	Sent      []struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"sent"`
}

type budgetDoc struct {
	Budget    int64 `json:"monthly_budget_micro_usd"`
	Spent     int64 `json:"spent_micro_usd"`
	Remaining int64 `json:"remaining_micro_usd"`
	Calls     int   `json:"calls"`
}

// collect reads what the fill produced: jobs, suggestions, the review
// queue, disclosures and the budget.
func (s *scenario) collect() {
	s.jobs = list[job](s.owner, s.tenantPath("/ai-jobs"), url.Values{"fill": {s.fillDoc.ID}})
	s.suggestions = list[suggestion](s.owner, s.tenantPath("/ai-suggestions"), url.Values{"project": {s.project}})
	s.queue = list[suggestion](s.owner, s.projectPath("/ai-review-queue"), nil)
	s.disclosures = list[disclosure](s.owner, s.tenantPath("/ai-disclosures"), url.Values{"project": {s.project}})
	s.owner.do(http.MethodGet, s.tenantPath("/ai-budget"), nil, http.StatusOK, &s.budget)
	if len(s.jobs) != s.fillDoc.JobsCreated {
		s.t.Fatalf("%d jobs listed, the fill created %d", len(s.jobs), s.fillDoc.JobsCreated)
	}
}

// translation returns the fixture's translation of key in l.
func (s *scenario) translation(key, l string) (*fixture.Message, *fixture.Translation) {
	m, ok := s.f.Message(key)
	if !ok {
		s.t.Fatalf("the platform returned %s, which the fixture doesn't have", key)
	}
	return m, m.Translations[l]
}

func joinKeys(keys []string) string {
	slices.Sort(keys)
	if len(keys) > 8 {
		return strings.Join(keys[:8], ", ") + fmt.Sprintf(" … (%d)", len(keys))
	}
	return strings.Join(keys, ", ")
}
