//go:build system

package m4_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The two spellings glossa.finding/v1 fixes.
var (
	fingerprintPattern = regexp.MustCompile(`^f_[0-9a-f]{16}$`)
	codePattern        = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// setup creates the organization, the project with its five locales and
// the application, the tokens §12.6 needs, a second tenant for the
// isolation case, and the provider guard.
func (s *scenario) setup() {
	t := s.t
	s.owner = s.d.signIn(t, "lena@brotwerk.example")
	var org struct{ ID string }
	s.owner.do(http.MethodPost, "/v1/tenants", map[string]string{"slug": "brotwerk", "name": "Brotwerk"}, http.StatusCreated, &org)
	s.tenant = org.ID
	var project struct{ ID string }
	s.owner.do(http.MethodPost, s.tenantPath("/projects"),
		map[string]any{"slug": "shop", "name": "Brotwerk Shop", "source_locale": "de"}, http.StatusCreated, &project)
	s.project = project.ID
	for _, l := range repoLocales[1:] {
		s.owner.do(http.MethodPost, s.projectPath("/locales"), map[string]string{"code": l}, http.StatusCreated, nil)
	}
	var app struct{ ID string }
	s.owner.do(http.MethodPost, s.projectPath("/applications"),
		map[string]string{"slug": application, "name": "Shop", "platform": "web"}, http.StatusCreated, &app)
	s.application = app.ID

	s.ciToken = s.mintToken(s.owner, s.tenant, "ci", "read", "write")
	s.readToken = s.mintToken(s.owner, s.tenant, "agent-read", "read")
	s.writeToken = s.mintToken(s.owner, s.tenant, "agent-write", "read", "write")

	// The second tenant of §12.6: another organization, its own
	// project, its own token. It must see nothing of the first.
	other := s.d.signIn(t, "mara@kraftsport.example")
	var org2 struct{ ID string }
	other.do(http.MethodPost, "/v1/tenants", map[string]string{"slug": "kraftsport", "name": "Kraftsport"}, http.StatusCreated, &org2)
	s.secondTenant = org2.ID
	var project2 struct{ ID string }
	other.do(http.MethodPost, "/v1/tenants/"+s.secondTenant+"/projects",
		map[string]any{"slug": "gym", "name": "Kraftsport", "source_locale": "de"}, http.StatusCreated, &project2)
	s.secondProject = project2.ID
	s.otherToken = s.mintToken(other, s.secondTenant, "other-read", "read")

	// The provider guard: the tenant's only model provider is a
	// loopback endpoint that refuses everything. Nothing in §12 should
	// reach it.
	s.owner.do(http.MethodPost, s.tenantPath("/ai-providers"), map[string]any{
		"name": fakeProviderName, "kind": "openai_compatible",
		"base_url": s.d.provider.baseURL(), "api_key": fakeAPIKey, "models": []string{"never-called"},
	}, http.StatusCreated, nil)
}

func (s *scenario) mintToken(as *client, tenant, name string, scopes ...string) string {
	s.t.Helper()
	var token struct {
		Secret string `json:"secret"`
	}
	as.do(http.MethodPost, "/v1/tenants/"+tenant+"/tokens",
		map[string]any{"name": name, "scopes": scopes}, http.StatusCreated, &token)
	return token.Secret
}

// ── §12.1 ────────────────────────────────────────────────────────────

// fixtureRepository materializes the repository and saves the project's
// policy — three saves, so the policy the run grades against is v3, as
// §12.1 writes it: `de` and `en` complete, `terminology` an error in
// the `legal` namespace, `visual` in `warn`.
func (s *scenario) fixtureRepository() {
	t := s.t
	dir := filepath.Join(t.TempDir(), "repo")
	s.repo = materialize(t, dir)
	s.ci = &runner{t: t, dir: dir, env: map[string]string{
		"GLOSSA_SERVER":  s.d.base,
		"GLOSSA_TENANT":  s.tenant,
		"GLOSSA_PROJECT": s.project,
		"GLOSSA_TOKEN":   s.ciToken,
		// This is a CI runner, and the workflow's steps have to run as
		// ones: `glossa check` and `glossa capture --check` record their
		// run by default in CI (RFC 0005 §9), and the run they record is
		// what the pull request's check renders (§12.3). Without it the
		// test would run the workflow's commands as a laptop does, and
		// §12.3 would be measuring something no product does.
		//
		// The branch and the commit are not set here on purpose: the
		// steps that are of a commit pass `--commit` and `--branch`
		// themselves, as the workflow does, and a branch in the
		// environment would silently retarget the pushes to `main`.
		"CI": "true",
	}}

	// The workflow is what the test runs the commands of; it has to be
	// in the repository and it has to run `glossa check`.
	wf, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "quality.yml"))
	if err != nil {
		s.gap("12.1", "the repository has no `.github/workflows` job: %v", err)
		return
	}
	for _, want := range []string{"glossa push", "glossa context push", "glossa check --json", "glossa capture --check"} {
		if !strings.Contains(string(wf), want) {
			s.gap("12.1", "the workflow does not run `%s`", want)
		}
	}
	s.note("12.1", "`.github/workflows/quality.yml` runs `glossa push`, `glossa context push`, "+
		"`glossa check --json` and `glossa capture --check --upload`; the test runs those commands.")

	// Three saves: a project that has changed its policy twice before
	// is the project §12.1 describes, and the version has to be 3.
	s.savePolicy(policyV1(), 14)
	s.savePolicy(policyV2(), 14)
	state := s.savePolicy(policyV3(), 14)
	s.policyVersion, s.gradedVersion = state.Version, state.Version
	if state.Version != 3 {
		s.gap("12.1", "the project's policy is v%d, want v3", state.Version)
	}
	s.note("12.1", "The project's check policy is v%d: `require_complete: [de, en]`, "+
		"`terminology` an error in the `legal` namespace, `visual` in `warn`.", state.Version)
	s.note("12.1", "The repository is the M3 fixture application (150 messages, eight routes, source `de`, "+
		"targets `en`, `es`, `fr`, `ja`) plus M4's workflow, capture plan and pay-button stylesheet.")
}

// policyState is `GET|POST …/check-policy`'s document with its
// bookkeeping.
type policyState struct {
	Version       int             `json:"version"`
	Document      json.RawMessage `json:"document"`
	EffectiveFrom string          `json:"effective_from"`
	GraceUntil    string          `json:"grace_until"`
	PinnedVersion int             `json:"pinned_version"`
}

type policySaved struct {
	DryRun bool         `json:"dry_run"`
	Policy policyState  `json:"policy"`
	Impact policyImpact `json:"impact"`
}

type policyImpact struct {
	Findings         int      `json:"findings"`
	Runs             int      `json:"runs"`
	Raised           int      `json:"raised"`
	Lowered          int      `json:"lowered"`
	Silenced         int      `json:"silenced"`
	NewlyFailing     int      `json:"newly_failing"`
	NoLongerFailing  int      `json:"no_longer_failing"`
	OpenPullRequests int      `json:"open_pull_requests"`
	NewlyFailingRefs []string `json:"newly_failing_refs"`
	// PullRequests names the ones OpenPullRequests counts.
	PullRequests []struct {
		Ref    string `json:"ref"`
		Number int    `json:"number"`
		URL    string `json:"url"`
	} `json:"newly_failing_pull_requests"`
	NoLongerFailingRefs []string `json:"no_longer_failing_refs"`
	Rules               []struct {
		Rule         int            `json:"rule"`
		Selector     map[string]any `json:"selector"`
		Matched      int            `json:"matched"`
		Changed      int            `json:"changed"`
		NewlyFailing int            `json:"newly_failing"`
	} `json:"rules"`
}

func (s *scenario) savePolicy(doc map[string]any, graceDays int) policyState {
	s.t.Helper()
	var out policySaved
	s.owner.do(http.MethodPost, s.projectPath("/check-policy"),
		map[string]any{"policy": doc, "grace_days": graceDays}, http.StatusOK, &out)
	return out.Policy
}

func (s *scenario) previewPolicy(doc map[string]any, graceDays int) policySaved {
	s.t.Helper()
	var out policySaved
	s.owner.do(http.MethodPost, s.projectPath("/check-policy"),
		map[string]any{"policy": doc, "grace_days": graceDays, "dry_run": true}, http.StatusOK, &out)
	return out
}

// policyV1 is where the project started: every locale required, errors
// fail.
func policyV1() map[string]any {
	return map[string]any{
		"schema": "glossa.check-policy/v1", "require_complete": "all",
		"fail_on": "error", "missing_translations": "error",
	}
}

// policyV2 narrowed the requirement to the two locales that ship.
func policyV2() map[string]any {
	return map[string]any{
		"schema": "glossa.check-policy/v1", "require_complete": "listed", "locales": []string{"de", "en"},
		"fail_on": "error", "missing_translations": "error",
	}
}

// policyV3 is §12.1's: the legal namespace's terminology is an error,
// and the visual layer reports without gating.
func policyV3() map[string]any {
	p := policyV2()
	p["environments"] = map[string]any{
		"production": map[string]any{"require_complete": "listed", "locales": []string{"de", "en", "fr"}},
	}
	p["rules"] = []map[string]any{
		{"layer": "terminology", "namespace": "legal", "severity": "error", "mode": "enforce"},
		{"layer": "visual", "severity": "warning", "mode": "warn"},
	}
	return p
}

// policyV4 promotes the visual layer from `warn` to `enforce` (§12.4).
func policyV4() map[string]any {
	p := policyV3()
	p["rules"] = []map[string]any{
		{"layer": "terminology", "namespace": "legal", "severity": "error", "mode": "enforce"},
		{"layer": "visual", "severity": "error", "mode": "enforce"},
	}
	return p
}

// ── §12.2 ────────────────────────────────────────────────────────────

type pushItem struct {
	Key    string `json:"key"`
	Locale string `json:"locale"`
	Status string `json:"status"`
	Error  *struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
}

type pushJSON struct {
	Summary      map[string]int `json:"summary"`
	Messages     []pushItem     `json:"messages"`
	Translations []pushItem     `json:"translations"`
	Branch       *struct {
		Name     string   `json:"name"`
		State    string   `json:"state"`
		NewKeys  []string `json:"new_keys"`
		PRNumber int      `json:"pr_number"`
	} `json:"branch"`
}

type contextBuild struct {
	ID          string `json:"id"`
	Commit      string `json:"commit"`
	Branch      string `json:"branch"`
	OnDefault   bool   `json:"on_default_branch"`
	Source      string `json:"source"`
	Usages      int    `json:"usages"`
	UnknownKeys int    `json:"unknown_keys"`
}

type contextPushJSON struct {
	Source   string       `json:"source"`
	Replayed bool         `json:"replayed"`
	Build    contextBuild `json:"build"`
}

// pushBase pushes the default branch's catalogs: what `main` holds
// before any pull request changes it.
func (s *scenario) pushBase() {
	var base pushJSON
	s.ci.ok(&base, "push", "--translations")
	s.pushed = base.Summary
}

// seedCatalogs seeds what the default branch's catalogs cannot say —
// the message's max_length, the obsoleted source, the build's usages —
// and pushes the pull request's catalogs, where the German source moves
// under a French and a Japanese translation that do not follow it.
func (s *scenario) seedCatalogs() {
	t := s.t

	// max_length is the message's, and only the server holds it. The
	// French translation already exceeds it.
	etag := s.owner.do(http.MethodGet, s.projectPath("/messages/"+keyMaxLength), nil, http.StatusOK, nil).Get("ETag")
	s.owner.do(http.MethodPatch, s.projectPath("/messages/"+keyMaxLength),
		map[string]any{"max_length": maxLength}, http.StatusOK, nil, "If-Match", etag)

	// The source of `keyOrphan` goes away while its translations stay,
	// which is the only way a stored translation ends up naming a key no
	// active message has — the `unknown-key` the completeness layer
	// reports.
	orphan := s.owner.do(http.MethodGet, s.projectPath("/messages/"+keyOrphan), nil, http.StatusOK, nil).Get("ETag")
	s.owner.do(http.MethodPost, s.projectPath("/messages/"+keyOrphan+"/obsoletion"),
		map[string]any{}, http.StatusOK, nil, "If-Match", orphan)
	// Localization learns of the obsoletion from Catalog's event, and a
	// check reads its orphans from Localization's projection — as every
	// bulk translation read does. The projection is eventual, and the
	// base push left a backlog of events ahead of this one; CI runs its
	// check after the push, not in the same second, and so does this.
	caught, state := softly(2*time.Minute, func() (bool, string) {
		var page struct {
			Items []struct {
				Key string `json:"key"`
			} `json:"items"`
		}
		s.owner.do(http.MethodGet, s.projectPath("/translations?locale=en&message_state=obsolete&key_prefix="+
			url.QueryEscape(keyOrphan)), nil, http.StatusOK, &page)
		return len(page.Items) > 0, fmt.Sprintf("%d translations listed as obsolete", len(page.Items))
	})
	if !caught {
		s.gap("12.2", "Localization never saw `%s` obsoleted, so no check can find its translations: %s", keyOrphan, state)
	}

	// The build's usages, with the one key no catalog has.
	var usages contextPushJSON
	s.ci.ok(&usages, "context", "push", "usages.json")
	if usages.Build.UnknownKeys != 1 {
		s.gap("12.2", "the build's usages carry %d unknown keys, want the one for %s",
			usages.Build.UnknownKeys, keyUnknown)
	}
	s.note("12.2", "`glossa context push` uploaded %d usages, one of them `%s` at `%s:%d` — a key no catalog has.",
		usages.Build.Usages, keyUnknown, unknownFile, unknownLine)

	// The pull request's commit: the sources move.
	s.repo.write(t, s.repo.head)
	var head pushJSON
	res := s.ci.run("push", "--translations", "--json")
	if res.code != int(cli.ExitOK) && res.code != int(cli.ExitPartial) {
		t.Fatalf("glossa push (head): exit %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	if err := json.Unmarshal([]byte(res.stdout), &head); err != nil {
		t.Fatalf("glossa push --json: %v\n%s", err, res.stdout)
	}
	revised := 0
	for _, it := range head.Messages {
		if it.Status == "revised" || it.Status == "updated" {
			revised++
		}
	}
	s.note("12.2", "The head commit revises %d German sources; the French `%s` keeps its text and loses "+
		"`{$amount}`, and the Japanese `%s` keeps the link the source dropped.", revised, keyArgument, keyMarkup)
}

// seedTermbase adds the two concepts §12.2's terminology cases need: a
// forbidden French term in the legal namespace, and a preferred Spanish
// term a translation elsewhere does not use.
func (s *scenario) seedTermbase() {
	s.owner.do(http.MethodPost, s.tenantPath("/term-concepts"), map[string]any{
		"project_id": s.project, "definition": "Wie mit personenbezogenen Daten umgegangen wird.", "domain": "legal",
		"terms": []map[string]any{
			{"locale": "de", "text": termConceptPrivacy, "status": "preferred", "part_of_speech": "noun"},
			{"locale": "fr", "text": termPreferredFR, "status": "preferred", "part_of_speech": "noun"},
			{"locale": "fr", "text": termForbiddenFR, "status": "forbidden", "part_of_speech": "phrase"},
		},
	}, http.StatusCreated, nil)
	s.owner.do(http.MethodPost, s.tenantPath("/term-concepts"), map[string]any{
		"project_id": s.project, "definition": "Die Liste der Artikel vor dem Bezahlen.", "domain": "shop",
		"terms": []map[string]any{
			{"locale": "de", "text": termConceptBasket, "status": "preferred", "part_of_speech": "noun"},
			{"locale": "es", "text": termPreferredES, "status": "preferred", "part_of_speech": "noun"},
		},
	}, http.StatusCreated, nil)
	// The style guide §12.2's `style` case needs: Austrian German is
	// written in the formal form of address, and one translation is not.
	s.owner.do(http.MethodPost, s.tenantPath("/style-guides"), map[string]any{
		"project_id": s.project, "locale": styledLocale, "name": "Brotwerk, Österreich",
		"fields": map[string]any{
			"formality": map[string]any{"register": "formal", "pronoun": "Sie"},
		},
	}, http.StatusCreated, nil)
	s.note("12.2", "`%s`'s effective style guide asks for the formal form of address (`Sie`).", styledLocale)
	s.note("12.2", "The termbase holds `%s` → forbidden French `%s` (legal) and `%s` → preferred Spanish `%s`.",
		termConceptPrivacy, termForbiddenFR, termConceptBasket, termPreferredES)
}

// runCheck runs the workflow's own check command against the server and
// validates what it printed.
func (s *scenario) runCheck() {
	t := s.t
	var out checkJSON
	// Exit 1 is the exit criterion: the policy failed the run.
	res := s.ci.run("check", "--terminology", "--explain-policy", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("glossa check --json: %v\nstdout:\n%s\nstderr:\n%s", err, res.stdout, res.stderr)
	}
	s.cliCheck = out
	if res.code != int(cli.ExitCheckFailed) {
		s.gap("12.2", "`glossa check` exited %d, want 1 (the policy failed the run); conclusion %q, %d errors",
			res.code, out.Conclusion, out.Errors)
	} else {
		s.note("12.2", "`glossa check --json` exited 1 with conclusion `%s`: %d errors, %d warnings, %d waived, "+
			"graded against policy v%d from the %s.", out.Conclusion, out.Errors, out.Warnings, out.Waived,
			out.Policy.Version, out.Policy.Source)
	}
	// Every finding is a glossa.finding/v1 document.
	if bad := validateFindings(out.Findings); len(bad) > 0 {
		s.gap("12.2", "%d findings do not validate against glossa.finding/v1: %s", len(bad), strings.Join(bad, "; "))
	} else {
		s.note("12.2", "All %d findings validate against `glossa.finding/v1` (schema, fingerprint, layer, code, "+
			"severity, locus).", len(out.Findings))
	}
	if len(out.Explain) != len(out.Findings) {
		s.gap("12.2", "--explain-policy explained %d of %d findings", len(out.Explain), len(out.Findings))
	}
}

// validateFindings checks every finding against the shape
// glossa.finding/v1 fixes (runtimes/testdata/schemas/finding.v1.schema.json):
// the schema tag, the fingerprint's spelling, a known layer, a code in
// the code alphabet, and a severity that is one of the three.
func validateFindings(fs []domain.Finding) []string {
	var bad []string
	for i, f := range fs {
		var why []string
		if f.Schema != domain.Schema {
			why = append(why, fmt.Sprintf("schema %q", f.Schema))
		}
		if !fingerprintPattern.MatchString(f.Fingerprint) {
			why = append(why, fmt.Sprintf("fingerprint %q", f.Fingerprint))
		}
		if !f.Layer.Valid() {
			why = append(why, fmt.Sprintf("layer %q", f.Layer))
		}
		if !codePattern.MatchString(f.Code) {
			why = append(why, fmt.Sprintf("code %q", f.Code))
		}
		switch f.Severity {
		case domain.Error, domain.Warning, domain.Waived:
		default:
			why = append(why, fmt.Sprintf("severity %q", f.Severity))
		}
		if f.Message == "" {
			why = append(why, "no message")
		}
		if f.Severity == domain.Waived && f.Waiver == "" {
			why = append(why, "waived with no waiver")
		}
		if f.Locus.Region != "" && f.Locus.Capture == "" {
			why = append(why, "a region with no capture")
		}
		if len(why) > 0 {
			bad = append(bad, fmt.Sprintf("#%d (%s/%s): %s", i, f.Layer, f.Code, strings.Join(why, ", ")))
		}
	}
	return bad
}

// nineLayers is §12.2's checklist: each of the nine layers it names,
// the case it names for it, and what the run actually produced.
func (s *scenario) nineLayers() {
	// The workflow runs two commands on one commit, and no single
	// command produces every layer: `glossa check --terminology` reads
	// the server's termbase but never opens a browser, and `glossa
	// capture --check` measures the page but runs `checkFlags{}`, which
	// does not ask for the terminology layer at all
	// (cli/capture_check.go's startCheck). The nine cases below are
	// therefore counted over both runs together, which is what the
	// workflow's two steps leave behind.
	findings := unionOf(s.cliCheck.Findings, captureFindings(s.cliCapture))
	s.cliLayers = byLayer(findings)
	if s.cliCapture.Check != nil {
		s.note("12.2", "No single command computes all nine layers: `glossa check --terminology` has the "+
			"terminology layer and no browser, `glossa capture --check` has the visual layer and takes no "+
			"`--terminology` flag. The table below counts both runs of the workflow together.")
	}

	wants := []struct {
		layer domain.Layer
		want  string
		// codes that satisfy the case, any one of them.
		codes []string
	}{
		{domain.LayerStructure, "one translation whose MF2 doesn't parse", []string{"invalid-translation", "invalid-message"}},
		{domain.LayerParity, "one French translation missing `{$amount}`, one Japanese one adding markup the source doesn't have",
			[]string{"missing-argument", "markup-extra"}},
		{domain.LayerCompleteness, "three missing `fr`, two outdated `ja`, one unknown key with its file:line",
			[]string{"missing-translation", "outdated-translation", "unknown-key"}},
		{domain.LayerTerminology, "one `term_forbidden` in `legal` (error), one `term_missing` elsewhere (warning)",
			[]string{"term_forbidden", "term_missing"}},
		{domain.LayerStyle, "one German translation using `du` under a `Sie` guide",
			[]string{"formality-mismatch"}},
		{domain.LayerLength, "one French button over its `max_length`, one over its region's width",
			[]string{"max-length-exceeded", "layout-overflow-predicted"}},
		{domain.LayerLocale, "one French translation writing `1,234.50`, one Arabic one with a stray U+202B",
			[]string{"number-convention", "bidi-stray-control"}},
		{domain.LayerSource, "one `3 item(s)` and one `ambiguous-short`",
			[]string{"manual-plural", "ambiguous-short"}},
		{domain.LayerVisual, "a real one: Chrome over the fixture, the Japanese checkout button clips", []string{"text-clipped"}},
	}
	for _, w := range wants {
		codes := codesOf(findings, w.layer)
		count := s.cliLayers[w.layer]
		v := layerVerdict{Layer: w.layer, Want: w.want, Codes: codes,
			Got: fmt.Sprintf("%d findings (%d error, %d warning)", count.total(), count.Errors, count.Warnings)}
		switch {
		case w.layer == domain.LayerStructure && len(codes) == 0:
			// §12.2 as amended in wave 7: a server project cannot hold
			// text that does not parse, so the layer is proven where it
			// can run and the refusal that makes it unreachable is
			// asserted, not explained.
			v = s.proveStructure(w.want)
		case len(codes) > 0:
			v.OK = true
			missing := missingCodes(w.codes, codes)
			if len(missing) > 0 {
				v.OK = false
				v.Why = "no finding with " + codeList(missing)
			}
			// §12.2 as amended in wave 7: the unknown key is a
			// translation of a message the catalog obsoleted, and it
			// has its file:line where the code still asks for the key.
			if w.layer == domain.LayerCompleteness && v.OK {
				if located, why := s.unknownKey(findings); why != "" {
					v.OK, v.Why = false, why
				} else {
					v.Got += "; " + located
				}
			}
		default:
			v.Got, v.Why = "none", "the layer produced nothing"
		}
		if v.Layer == domain.LayerStyle && !v.OK {
			v.Why = s.whyNoStyle()
		}
		if !v.OK {
			s.gap("12.2", "%s: %s", w.layer, v.Why)
		}
		s.layerStatus = append(s.layerStatus, v)
	}
	// The three completeness cases are counted, not just present.
	missing := keysWith(findings, domain.LayerCompleteness, "missing-translation", "fr")
	if len(missing) != len(missingFrench) {
		s.gap("12.2", "completeness found %d missing French translations (%s), want the %d that were never "+
			"written (%s)", len(missing), codeList(missing), len(missingFrench), codeList(missingFrench))
	}
	outdated := keysWith(findings, domain.LayerCompleteness, "outdated-translation", "ja")
	if len(outdated) < len(outdatedJapanese) {
		s.gap("12.2", "completeness found %d outdated Japanese translations (%s), want at least the %d whose "+
			"German source moved under them (%s)", len(outdated), codeList(outdated),
			len(outdatedJapanese), codeList(outdatedJapanese))
	}
	// Terminology's two cases have to land on the right side of the
	// policy: an error in `legal`, a warning everywhere else.
	// Terminology's two cases have to land on the right side of the
	// policy: an error on the legal-namespace message, a warning
	// elsewhere.
	if !onKey(findings, domain.LayerTerminology, "term_forbidden", keyForbidden, domain.Error) {
		s.gap("12.2", "`%s` has no `term_forbidden` error; terminology produced %s",
			keyForbidden, describe(findings, domain.LayerTerminology))
	}
	if !onKey(findings, domain.LayerTerminology, "term_missing", keyTermMissing, domain.Warning) {
		s.gap("12.2", "`%s` has no `term_missing` warning; terminology produced %s",
			keyTermMissing, describe(findings, domain.LayerTerminology))
	}
	// And the rule that is supposed to decide the first of those has to
	// be able to select it.
	if !anyNamespace(findings, domain.LayerTerminology) {
		s.gap("12.2", "every terminology finding carries an empty `locus.namespace`, so policy v3's rule "+
			"`{layer: terminology, namespace: legal, severity: error}` can never select one: "+
			"`checkpolicy.Selector` matches on `locus.namespace`, and the terminology layer does not set it "+
			"(cli/terminology and the server's termbase check answer by key and locale). The `legal` "+
			"namespace's terminology is an error here only because `term_forbidden` is already one by default")
	}
}

// whyNoStyle tells a fixture that does not trigger the style layer from
// a surface that cannot run it.
//
// The difference matters and is not guessable from the CLI alone. The
// server's own snapshot resolves the effective style guide per locale
// (quality/adapters/snapshot fills `Project.Styles`); the CLI's does
// not. So the test asks the server, through the one read-only surface
// that computes a check rather than reading a stored one — MCP's
// `check_run` — and reports which of the two it is.
func (s *scenario) whyNoStyle() string {
	var guide struct {
		Fields struct {
			Formality *struct {
				Register string `json:"register"`
				Pronoun  string `json:"pronoun"`
			} `json:"formality"`
		} `json:"fields"`
		Sources []map[string]any `json:"sources"`
	}
	path := "/v1/tenants/" + s.tenant + "/effective-style-guide?project=" + s.project + "&locale=" + url.QueryEscape(styledLocale)
	if _, err := s.owner.try(http.MethodGet, path, nil, http.StatusOK, &guide); err != nil {
		return fmt.Sprintf("the layer produced nothing, and `%s`'s effective style guide could not be read: %v",
			styledLocale, err)
	}
	if guide.Fields.Formality == nil || guide.Fields.Formality.Register != "formal" {
		return fmt.Sprintf("no finding, and the fixture is why: `%s`'s effective style guide states no formal "+
			"register (%d source guides merged), so the layer has no rule to grade `du` against",
			styledLocale, len(guide.Sources))
	}
	return fmt.Sprintf("no finding **from the terminal**, and the fixture is not why: `%s`'s effective style "+
		"guide states register `%s` (pronoun %q) and its one translation says `du`. `glossa check` cannot see "+
		"that, because `cli/qa.Project` builds a `layers.Project` with **no `Styles` map at all** — it copies "+
		"locales, messages and translations and never resolves the effective style guide — so "+
		"`layers.Style.Check` finds no guide for any locale and returns before it reads a word. The server's "+
		"own snapshot does resolve it (`quality/adapters/snapshot` fills `Styles` from "+
		"`quality/adapters/style.Port.EffectiveStyle`), so the layer is built and reachable there; the "+
		"terminal is simply not wired to it",
		styledLocale, guide.Fields.Formality.Register, guide.Fields.Formality.Pronoun)
}

func missingCodes(want, got []string) []string {
	have := map[string]bool{}
	for _, c := range got {
		have[c] = true
	}
	var out []string
	for _, c := range want {
		if !have[c] {
			out = append(out, c)
		}
	}
	return out
}

// describe names a layer's findings the way a failure message has to:
// code, key, locale, namespace, severity.
func describe(fs []domain.Finding, layer domain.Layer) string {
	var out []string
	for _, f := range fs {
		if f.Layer != layer {
			continue
		}
		out = append(out, fmt.Sprintf("%s/%s in %s (namespace %q, %s)",
			f.Code, f.Locus.Key, orDash(f.Locus.Locale), f.Locus.Namespace, f.Severity))
	}
	if len(out) == 0 {
		return "nothing"
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

// keysWith are the keys of one layer's findings with one code, in one
// locale. Naming them is what makes a shortfall diagnosable.
func keysWith(fs []domain.Finding, layer domain.Layer, code, locale string) []string {
	var out []string
	for _, f := range fs {
		if f.Layer == layer && f.Code == code && (locale == "" || f.Locus.Locale == locale) {
			out = append(out, f.Locus.Key)
		}
	}
	sort.Strings(out)
	return out
}

// unionOf merges the workflow's two runs by fingerprint, keeping the
// first sighting of each finding.
func unionOf(runs ...[]domain.Finding) []domain.Finding {
	seen := map[string]bool{}
	var out []domain.Finding
	for _, run := range runs {
		for _, f := range run {
			if seen[f.Fingerprint] {
				continue
			}
			seen[f.Fingerprint] = true
			out = append(out, f)
		}
	}
	return out
}

func captureFindings(c captureJSON) []domain.Finding {
	if c.Check == nil {
		return nil
	}
	return c.Check.Findings
}

// unknownKey is §12.2's `unknown-key`, as amended in wave 7: a
// translation whose message the catalog obsoleted, reported by the
// terminal under the obsolete message's ID, and located by the pull
// request at the line where the code still asks for the key.
//
// It answers what was found, or why the case does not hold.
func (s *scenario) unknownKey(findings []domain.Finding) (string, string) {
	var msg struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	s.owner.do(http.MethodGet, s.projectPath("/messages/"+keyOrphan), nil, http.StatusOK, &msg)
	if msg.State != "obsolete" {
		return "", fmt.Sprintf("`%s` is `%s`, not obsolete, so nothing left its translations behind", keyOrphan, msg.State)
	}
	// The terminal: one warning per translation the obsolete message
	// left behind, each identified by the message's ID — the identity
	// the server computes, so a waiver holds across surfaces.
	locales := map[string]bool{}
	for _, f := range findings {
		if f.Code != "unknown-key" || f.Locus.Key != keyOrphan {
			continue
		}
		if f.Locus.Message != msg.ID || f.Severity != domain.Warning {
			return "", fmt.Sprintf("`unknown-key` on `%s` in `%s` is %s against message `%s`; want a warning "+
				"against the obsolete message's ID `%s`", keyOrphan, f.Locus.Locale, f.Severity, f.Locus.Message, msg.ID)
		}
		locales[f.Locus.Locale] = true
	}
	var want []string
	for l, texts := range s.repo.base {
		if l != "de" && texts[keyOrphan] != "" {
			want = append(want, l)
		}
	}
	sort.Strings(want)
	for _, l := range want {
		if !locales[l] {
			return "", fmt.Sprintf("the terminal reports no `unknown-key` for `%s`'s %s translation, which "+
				"its obsoleted message left behind (reported: %s)", keyOrphan, l, codeList(sortedSet(locales)))
		}
	}
	// The pull request: the run CI recorded, located by Context at the
	// line the help page still asks for the key on.
	title := "unknown-key: " + keyOrphan
	ok, state := softly(3*time.Minute, func() (bool, string) {
		for _, r := range s.d.github.CheckRuns(repositoryID) {
			if r.HeadSHA != headCommit || r.Name != "Glossa" {
				continue
			}
			for _, a := range r.Annotations {
				if a.Path == orphanFile && a.StartLine == orphanLine && a.Title == title {
					return a.AnnotationLevel == "warning", "annotated at level " + a.AnnotationLevel
				}
			}
			return false, fmt.Sprintf("%s/%s with %d annotations, none of them `%s` at %s:%d",
				r.Status, r.Conclusion, len(r.Annotations), title, orphanFile, orphanLine)
		}
		return false, "no check run for the head commit"
	})
	if !ok {
		return "", fmt.Sprintf("the terminal reports `unknown-key` on `%s` in %s, and the pull request does not "+
			"put it at `%s:%d`, where the help page still asks for the key: %s",
			keyOrphan, codeList(want), orphanFile, orphanLine, state)
	}
	s.note("12.2", "`%s` was obsoleted while its translations stayed: `glossa check` reported `unknown-key` in %s, "+
		"each a warning against the obsolete message's ID (`%s`), and the pull request annotated it at `%s:%d` — "+
		"the line the help page still asks for the key on, from the build's usages.",
		keyOrphan, codeList(want), short(msg.ID), orphanFile, orphanLine)
	return fmt.Sprintf("`unknown-key` on `%s` at `%s:%d` on the pull request", keyOrphan, orphanFile, orphanLine), ""
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func onKey(fs []domain.Finding, layer domain.Layer, code, key string, want domain.Severity) bool {
	for _, f := range fs {
		if f.Layer == layer && f.Code == code && f.Locus.Key == key && f.Severity == want {
			return true
		}
	}
	return false
}

func anyNamespace(fs []domain.Finding, layer domain.Layer) bool {
	for _, f := range fs {
		if f.Layer == layer && f.Locus.Namespace != "" {
			return true
		}
	}
	return false
}

// structureProof is §12.2's `structure` case as amended in wave 7: the
// layer's finding from `glossa check --offline`, and the server
// refusing, at write time, the text that finding is about.
type structureProof struct {
	Offline      int
	OfflineCodes string
	// Refusals are the write paths asked to store text that does not
	// parse, and what each answered.
	Refusals []step
}

// proveStructure asserts both halves of the amended case. A translation
// and a source revision that do not parse are sent through the API's
// own write paths — the ones `glossa push` and Studio use — and each
// must be refused with `invalid_message` and leave the stored text as
// it was. The layer itself is then shown working where its input can
// exist: a local catalog, checked offline.
func (s *scenario) proveStructure(want string) layerVerdict {
	const unparseable = ".input {$name" // unterminated: no MF2 parse
	v := layerVerdict{Layer: domain.LayerStructure, Want: want}
	var why []string

	n, codes := s.offlineStructure()
	s.structure.Offline, s.structure.OfflineCodes = n, codes
	if n == 0 || !strings.Contains(codes, "invalid-translation") {
		why = append(why, fmt.Sprintf("`glossa check --offline` over a catalog holding `%s` produced %d "+
			"structure findings (%s), want `invalid-translation`", unparseable, n, codes))
	}

	// A translation that does not parse.
	trPath := s.projectPath("/messages/" + keyAmbiguousShort + "/translations/es")
	var before struct {
		Text string `json:"text"`
	}
	etag := s.owner.do(http.MethodGet, trPath, nil, http.StatusOK, &before).Get("ETag")
	why = append(why, s.refused("a Spanish translation of `"+keyAmbiguousShort+"`", http.MethodPut, trPath,
		map[string]any{"text": unparseable, "syntax": "mf2"}, etag, before.Text)...)

	// A source revision that does not parse.
	msgPath := s.projectPath("/messages/" + keyAmbiguousShort)
	var msg struct {
		Source struct {
			Text string `json:"text"`
		} `json:"source"`
	}
	etag = s.owner.do(http.MethodGet, msgPath, nil, http.StatusOK, &msg).Get("ETag")
	why = append(why, s.refused("the German source of `"+keyAmbiguousShort+"`", http.MethodPut, msgPath+"/source",
		map[string]any{"text": unparseable, "syntax": "mf2"}, etag, msg.Source.Text)...)

	if len(why) > 0 {
		v.Got, v.Why = "none", strings.Join(why, "; ")
		return v
	}
	v.OK = true
	v.Codes = []string{"invalid-translation"}
	v.Got = fmt.Sprintf("%d from `glossa check --offline` (%s); the server refused both writes of text that "+
		"does not parse (`invalid_message`) and kept what it had, so no stored message can carry one", n, codes)
	return v
}

// refused sends one write of text that does not parse and says what is
// wrong if it was not refused, or if the stored text moved anyway.
func (s *scenario) refused(what, method, path string, body map[string]any, etag, was string) []string {
	_, err := s.owner.try(method, path, body, http.StatusOK, nil, "If-Match", etag)
	var ae *apiError
	switch {
	case err == nil:
		return []string{fmt.Sprintf("the server stored %s that does not parse", what)}
	case !asAPIError(err, &ae):
		return []string{fmt.Sprintf("writing %s: %v", what, err)}
	case ae.status != http.StatusBadRequest || ae.code() != "invalid_message":
		return []string{fmt.Sprintf("%s that does not parse was refused with %d `%s`, want 400 `invalid_message`",
			what, ae.status, ae.code())}
	}
	s.structure.Refusals = append(s.structure.Refusals, step{
		What: fmt.Sprintf("`%s` %s with `.input {$name`", method, what),
		Then: fmt.Sprintf("%d `%s`", ae.status, ae.code())})
	// Refused, and nothing moved: the stored text is the one before.
	getPath := path
	if strings.HasSuffix(path, "/source") {
		getPath = strings.TrimSuffix(path, "/source")
	}
	var now struct {
		Text   string `json:"text"`
		Source struct {
			Text string `json:"text"`
		} `json:"source"`
	}
	s.owner.do(http.MethodGet, getPath, nil, http.StatusOK, &now)
	if got := now.Text + now.Source.Text; got != was {
		return []string{fmt.Sprintf("%s was refused and the stored text moved anyway: %q, was %q", what, got, was)}
	}
	return nil
}

// offlineStructure is the one place the structure layer can be seen:
// `glossa check --offline` over a local catalog holding text that does
// not parse. It is run so the report can say the layer works, and say
// in the same breath that the server can never hold its input.
func (s *scenario) offlineStructure() (int, string) {
	dir := filepath.Join(s.t.TempDir(), "offline")
	if err := os.CopyFS(dir, os.DirFS(s.repo.dir)); err != nil {
		return 0, err.Error()
	}
	c := readCatalogs(s.t, dir)
	c["es"]["help.note.activity"] = ".input {$name" // unterminated: no MF2 parse
	writeJSON(s.t, filepath.Join(dir, "locales", "es.json"), c["es"])
	var out checkJSON
	r := s.ci.in(dir)
	res := r.run("check", "--offline", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		return 0, "the offline check printed no document: " + res.stderr
	}
	n := 0
	for _, f := range out.Findings {
		if f.Layer == domain.LayerStructure {
			n++
		}
	}
	return n, codeList(codesOf(out.Findings, domain.LayerStructure))
}

// ── the CLI's own JSON documents ─────────────────────────────────────

type policyJSON struct {
	RequireComplete []string `json:"require_complete"`
	FailOn          string   `json:"fail_on"`
	Source          string   `json:"source"`
	Version         int      `json:"version"`
	Overridden      bool     `json:"overridden"`
	Grace           *struct {
		PreviousVersion int    `json:"previous_version"`
		Until           string `json:"until"`
	} `json:"grace"`
}

type checkLocaleJSON struct {
	Code       string `json:"code"`
	IsSource   bool   `json:"is_source"`
	Required   bool   `json:"required"`
	Messages   int    `json:"messages"`
	Translated int    `json:"translated"`
	Missing    int    `json:"missing"`
	Outdated   int    `json:"outdated"`
	Errors     int    `json:"errors"`
	Warnings   int    `json:"warnings"`
	Waived     int    `json:"waived"`
	Complete   bool   `json:"complete"`
}

type unavailableJSON struct {
	Layer domain.Layer `json:"layer"`
	Why   string       `json:"why"`
}

type explainJSON struct {
	Fingerprint string          `json:"fingerprint"`
	Layer       domain.Layer    `json:"layer"`
	Code        string          `json:"code"`
	Severity    domain.Severity `json:"severity"`
	Mode        string          `json:"mode"`
	Fails       bool            `json:"fails"`
	Why         string          `json:"why"`
}

// checkJSON is `glossa check --json`, the document the workflow keeps.
type checkJSON struct {
	Schema      string            `json:"schema"`
	Policy      policyJSON        `json:"policy"`
	Origin      string            `json:"origin"`
	Messages    int               `json:"messages"`
	Locales     []checkLocaleJSON `json:"locales"`
	Layers      []domain.Layer    `json:"layers"`
	Skipped     []domain.Layer    `json:"skipped_layers"`
	Unavailable []unavailableJSON `json:"unavailable_layers"`
	Findings    []domain.Finding  `json:"findings"`
	Explain     []explainJSON     `json:"explain"`
	Errors      int               `json:"errors"`
	Warnings    int               `json:"warnings"`
	Waived      int               `json:"waived"`
	Conclusion  domain.Conclusion `json:"conclusion"`
	Passed      bool              `json:"passed"`
	// Record is what became of putting this run on the project's record
	// (RFC 0005 §9). It is the run the pull request's check renders
	// (§12.3), so a run that was not recorded is a pull request with
	// nothing of the terminal's to show.
	Record *recordJSON `json:"record"`
}

// recordJSON is `glossa check`'s record block.
type recordJSON struct {
	Recorded   bool   `json:"recorded"`
	Run        string `json:"run"`
	Conclusion string `json:"conclusion"`
	Why        string `json:"why"`
}

// recordWhy says why a run was not recorded, for a gap that has to
// name the cause rather than the symptom.
func recordWhy(r *recordJSON) string {
	switch {
	case r == nil:
		return "recording was never asked for: this run did not think it was in CI"
	case r.Why != "":
		return r.Why
	}
	return "no reason given"
}

func layerNames(ls []domain.Layer) string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, string(l))
	}
	sort.Strings(out)
	return codeList(out)
}

func queryFindings(c *client, path string, q url.Values) []domain.Finding {
	return list[domain.Finding](c, path, q)
}
