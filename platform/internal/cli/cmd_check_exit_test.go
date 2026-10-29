package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa check`'s exit codes are a contract CI branches on
// (RFC 0005 §4.4): 0 clean · 1 the policy failed the run · 2 the policy
// couldn't be resolved, an unknown layer was named or the configuration
// is wrong · 3 the server is out of reach *and* there is no cached
// policy · 4 some layers ran and some couldn't.
//
// There is one test per code, because the distinction between them is
// the whole point: a broken policy is not a clean build, and a check
// that lost a layer is not a check that found nothing.

// writePolicyCache writes .glossa/policy.json as a run of `glossa
// check` against the server would have left it.
func writePolicyCache(t *testing.T, w *workspace, server string, p checkpolicy.Policy) {
	t.Helper()
	body, err := json.Marshal(policyCache{
		Schema: policyCacheSchema, Server: server, Project: "shop", Policy: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	w.write(policyCachePath, string(body))
}

// TestCheckExitsZeroWhenNothingFailsThePolicy is exit 0: no finding at
// or above fail_on after the policy had its say.
func TestCheckExitsZeroWhenNothingFailsThePolicy(t *testing.T) {
	srv, w := pushed(t)
	srv.checkPolicy = map[string]any{"require_complete": "none", "fail_on": "error", "missing_translations": "error"}
	var doc checkJSON
	w.json(&doc, "check").want(t, ExitOK)
	if !doc.Passed || doc.Errors != 0 {
		t.Errorf("check = %+v", doc)
	}
}

// TestCheckExitsOneWhenThePolicyFailsTheRun is exit 1, the only code CI
// should branch on.
func TestCheckExitsOneWhenThePolicyFailsTheRun(t *testing.T) {
	srv, w := pushed(t)
	srv.checkPolicy = map[string]any{"require_complete": "all", "fail_on": "error", "missing_translations": "error"}
	var doc checkJSON
	w.json(&doc, "check").want(t, ExitCheckFailed)
	if doc.Passed || doc.Errors == 0 {
		t.Errorf("check = %+v", doc)
	}
}

// TestCheckExitsTwoOnAnUnknownLayer is exit 2: a name that is not a
// layer is a configuration mistake, not a failed check.
func TestCheckExitsTwoOnAnUnknownLayer(t *testing.T) {
	_, w := pushed(t)
	var doc errorDoc
	w.json(&doc, "check", "--layer", "spelling").want(t, ExitUsage)
	if doc.Error.Code != "invalid_usage" || !strings.Contains(doc.Error.Message, "spelling") {
		t.Errorf("error = %+v", doc.Error)
	}
}

// TestCheckExitsTwoWhenThePolicyCannotBeResolved is the other half of
// exit 2: a cached policy that isn't one. A broken policy is not a
// clean build, and it is not a failed check either.
func TestCheckExitsTwoWhenThePolicyCannotBeResolved(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{"en": `{"home.title": "Welcome"}`})
	w.write(policyCachePath, `{"schema":"glossa.check-policy-cache/v1","policy":{"fail_on":"sometimes"}}`)
	var doc errorDoc
	w.json(&doc, "check", "--offline").want(t, ExitUsage)
	if doc.Error.Code != "invalid_policy_cache" {
		t.Errorf("error = %+v", doc.Error)
	}
}

// TestCheckExitsThreeWithNoServerAndNoCache is exit 3: the server is
// unreachable and nothing was cached, so the run has no policy to grade
// itself against and refuses to guess.
func TestCheckExitsThreeWithNoServerAndNoCache(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": `{"home.title": "Welcome"}`})
	srv.close()
	var doc errorDoc
	w.json(&doc, "check").want(t, ExitNetwork)
	if doc.Error.Code != "network" {
		t.Errorf("error = %+v", doc.Error)
	}
}

// TestCheckRunsOfflineOffTheCachedPolicy is exit 3's exception: with a
// cache the check runs against the local catalogs and concludes.
func TestCheckRunsOfflineOffTheCachedPolicy(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{
		"en": `{"home.title": "Welcome"}`,
		"de": `{}`,
	})
	writePolicyCache(t, w, srv.URL(), checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error})
	srv.close()

	var doc checkJSON
	w.json(&doc, "check").want(t, ExitCheckFailed)
	if doc.Policy.Source != "cache" || doc.Policy.Version != 7 || !doc.Policy.Offline {
		t.Errorf("policy = %+v", doc.Policy)
	}
	// The same cache, loosened, concludes the other way: the run really
	// grades against the cached document. It still exits 4, because the
	// style layer's guides are the server's and this run had none —
	// nothing failed the policy, and a layer could not run.
	writePolicyCache(t, w, srv.URL(), checkpolicy.Policy{Version: 8, FailOn: checkpolicy.Never})
	w.json(&doc, "check").want(t, ExitPartial)
	if !doc.Passed || len(doc.Unavailable) != 1 || doc.Unavailable[0].Layer != domain.LayerStyle {
		t.Errorf("passed = %v, unavailable = %+v", doc.Passed, doc.Unavailable)
	}
	if doc.Policy.Version != 8 {
		t.Errorf("policy = %+v", doc.Policy)
	}
}

// TestCheckExitsFourWhenALayerCouldNotRun is exit 4: the findings are
// reported, the layers that couldn't run are named, and CI decides.
// Silently dropping a layer is the one behaviour a check may never
// have.
func TestCheckExitsFourWhenALayerCouldNotRun(t *testing.T) {
	srv, w := pushed(t)
	srv.checkPolicy = map[string]any{"require_complete": "none", "fail_on": "error", "missing_translations": "error"}
	var doc checkJSON
	w.json(&doc, "check", "--layer", "completeness,linguistic").want(t, ExitPartial)
	if len(doc.Unavailable) != 1 || doc.Unavailable[0].Layer != domain.LayerLinguistic {
		t.Fatalf("unavailable = %+v", doc.Unavailable)
	}
	if doc.Unavailable[0].Why == "" {
		t.Error("an unavailable layer has to say why")
	}
	if !doc.Passed {
		t.Errorf("a layer that couldn't run is not a failed check: %+v", doc)
	}
	r := w.run("check", "--layer", "completeness,linguistic")
	if !strings.Contains(r.stdout, "linguistic") {
		t.Errorf("the human output doesn't name the skipped layer:\n%s", r.stdout)
	}
}

// TestCheckFailedBeatsPartial: a run that both failed the policy and
// lost a layer exits 1, because 1 is the code CI branches on.
func TestCheckFailedBeatsPartial(t *testing.T) {
	srv, w := pushed(t)
	srv.checkPolicy = map[string]any{"require_complete": "all", "fail_on": "error", "missing_translations": "error"}
	w.json(&checkJSON{}, "check", "--layer", "completeness,linguistic").want(t, ExitCheckFailed)
}

// TestCheckLayerSelectsTheLayersThatRun: --layer is repeatable and
// comma-separated, like every other list flag.
func TestCheckLayerSelectsTheLayersThatRun(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{
		"en": `{"cart.items": "{count, plural, one {# item} other {# items}}"}`,
		"de": `{"cart.items": "{n, plural, one {# Artikel} other {# Artikel}}"}`,
	})
	// Everything: parity sees the renamed argument, completeness is happy.
	var all checkJSON
	w.json(&all, "check", "--offline").want(t, ExitCheckFailed)
	if !hasLayer(all.Layers, domain.LayerParity) || !hasLayer(all.Layers, domain.LayerCompleteness) {
		t.Fatalf("layers = %v", all.Layers)
	}
	// Only completeness: the parity findings are not computed, so the
	// run passes and says which layers it ran.
	var only checkJSON
	w.json(&only, "check", "--offline", "--layer", "completeness").want(t, ExitOK)
	if len(only.Layers) != 1 || only.Layers[0] != domain.LayerCompleteness {
		t.Errorf("layers = %v", only.Layers)
	}
	for _, f := range only.Findings {
		if f.Layer != domain.LayerCompleteness {
			t.Errorf("a layer nobody asked for reported %+v", f)
		}
	}
	if !only.Policy.Overridden {
		t.Error("--layer is a local override and has to print as one")
	}
	// Repeated and comma-separated say the same thing.
	var repeated, joined checkJSON
	w.json(&repeated, "check", "--offline", "--layer", "structure", "--layer", "parity").want(t, ExitCheckFailed)
	w.json(&joined, "check", "--offline", "--layer", "structure,parity").want(t, ExitCheckFailed)
	if len(repeated.Layers) != 2 || len(joined.Layers) != len(repeated.Layers) {
		t.Errorf("repeated = %v, comma-separated = %v", repeated.Layers, joined.Layers)
	}
}

// TestCheckCachesTheServersPolicy: a run that reached the server leaves
// the document behind, so the next run can grade itself without one.
func TestCheckCachesTheServersPolicy(t *testing.T) {
	srv, w := pushed(t)
	srv.checkPolicy = map[string]any{"require_complete": "none", "fail_on": "error", "missing_translations": "error"}
	var doc checkJSON
	w.json(&doc, "check").want(t, ExitOK)
	if doc.Policy.Source != "server" {
		t.Errorf("policy source = %q, want server", doc.Policy.Source)
	}
	var cached policyCache
	if err := json.Unmarshal([]byte(w.read(policyCachePath)), &cached); err != nil {
		t.Fatalf("no usable %s: %v", policyCachePath, err)
	}
	if cached.Schema != policyCacheSchema || cached.Server != srv.URL() {
		t.Fatalf("cache = %+v", cached)
	}
	if len(cached.Policy.RequireComplete) != 0 || cached.Policy.RequireComplete == nil {
		t.Errorf("the cached document is not the one the server issued: %+v", cached.Policy)
	}
	// And it is what the next offline run grades against — exit 4,
	// because offline the style layer has no guides to grade against
	// and is named rather than dropped.
	var offline checkJSON
	w.json(&offline, "check", "--offline").want(t, ExitPartial)
	if offline.Policy.Source != "cache" {
		t.Errorf("offline policy = %+v", offline.Policy)
	}
}

// TestCheckWithNoServerAndNoCacheSaysSoLoudly: the built-in default is
// allowed to stand, and a check must never silently grade itself.
func TestCheckWithNoServerAndNoCacheSaysSoLoudly(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{"en": `{"home.title": "Welcome"}`, "de": `{}`})
	var doc checkJSON
	r := w.json(&doc, "check", "--offline")
	r.want(t, ExitCheckFailed)
	if doc.Policy.Source != "default" {
		t.Errorf("policy source = %q, want default", doc.Policy.Source)
	}
	h := w.run("check", "--offline")
	if !strings.Contains(h.stdout, "built-in default") {
		t.Errorf("the output doesn't say it graded itself:\n%s", h.stdout)
	}
}

// TestCheckExplainPolicyNamesTheDecidingRule is RFC 0005 §4.3: "why did
// this fail?" has a mechanical answer — which rule matched, why that
// one, and whether it could fail the run.
func TestCheckExplainPolicyNamesTheDecidingRule(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{
		"en": `{"home.title": "Welcome"}`,
		"de": `{}`,
	})
	writePolicyCache(t, w, "http://unused.invalid", checkpolicy.Policy{
		Version: 7, FailOn: checkpolicy.Error,
		Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "completeness"}, Severity: checkpolicy.Warning},
			{
				Selector: checkpolicy.Selector{Layer: "completeness", Locale: "de"},
				Severity: checkpolicy.Error, Mode: checkpolicy.ModeWarn,
			},
		},
	})
	var doc checkJSON
	// The more specific rule wins, and it is in warn mode, so the error
	// it gives the finding cannot fail the run. The run exits 4 all the
	// same: offline the style layer has no guides to grade against.
	w.json(&doc, "check", "--offline", "--explain-policy").want(t, ExitPartial)
	if len(doc.Explain) != len(doc.Findings) || len(doc.Explain) == 0 {
		t.Fatalf("explain = %+v for %d findings", doc.Explain, len(doc.Findings))
	}
	e := doc.Explain[0]
	if e.Rule == nil || e.Rule.Index != 1 || e.Rule.Selector.Locale != "de" {
		t.Fatalf("explain = %+v, want the more specific rule", e)
	}
	if e.Mode != string(checkpolicy.ModeWarn) || e.Fails {
		t.Errorf("a warn-mode rule may not fail the run: %+v", e)
	}
	if e.Severity != domain.Error || !strings.Contains(e.Why, "specific") {
		t.Errorf("explain = %+v", e)
	}
	// Without the flag the document says nothing about the decisions.
	var quiet checkJSON
	w.json(&quiet, "check", "--offline").want(t, ExitPartial)
	if len(quiet.Explain) != 0 {
		t.Errorf("explain without --explain-policy = %+v", quiet.Explain)
	}
	h := w.run("check", "--offline", "--explain-policy")
	if !strings.Contains(h.stdout, "rule 1") || !strings.Contains(h.stdout, "warn") {
		t.Errorf("the human explanation doesn't name the rule:\n%s", h.stdout)
	}
}

// TestCheckExplainPolicySaysWhenNoRuleMatched: a finding the document
// says nothing about keeps the severity its layer gave it, and the
// explanation says exactly that rather than nothing.
func TestCheckExplainPolicySaysWhenNoRuleMatched(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{"en": `{"home.title": "Welcome"}`, "de": `{}`})
	var doc checkJSON
	w.json(&doc, "check", "--offline", "--explain-policy").want(t, ExitCheckFailed)
	if len(doc.Explain) == 0 {
		t.Fatal("no explanations")
	}
	if doc.Explain[0].Rule != nil || !strings.Contains(doc.Explain[0].Why, "no rule") {
		t.Errorf("explain = %+v", doc.Explain[0])
	}
}

// TestPolicyCacheIsForThisProjectAndServer: a cache written for another
// server is not this project's policy, and is ignored rather than
// trusted.
func TestPolicyCacheIsForThisProjectAndServer(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{"en": `{"home.title": "Welcome"}`, "de": `{}`})
	writePolicyCache(t, w, "http://somewhere.else.invalid", checkpolicy.Policy{Version: 9, FailOn: checkpolicy.Never})
	var doc checkJSON
	w.json(&doc, "check", "--offline").want(t, ExitCheckFailed)
	if doc.Policy.Source != "default" || doc.Policy.Version != 0 {
		t.Errorf("policy = %+v, want another server's cache ignored", doc.Policy)
	}
}

// fakePolicySource stands in for the server, so the resolution is
// tested without one: no network, no fake HTTP, just the port.
type fakePolicySource struct {
	policy checkpolicy.Policy
	err    error
	calls  int
}

func (f *fakePolicySource) FetchPolicy(context.Context) (checkpolicy.Policy, error) {
	f.calls++
	return f.policy, f.err
}

// TestFetchPolicyWritesTheCache holds the port to its two jobs: hand
// back what the server says, and leave it on disk for the next run.
func TestFetchPolicyWritesTheCache(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{"en": `{}`})
	inv := &invocation{env: Env{Dir: w.dir, Getenv: func(k string) string { return w.env[k] }}}
	cfg, err := inv.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	src := &fakePolicySource{policy: checkpolicy.Policy{Version: 4, FailOn: checkpolicy.Warning}}
	got, err := inv.fetchPolicy(context.Background(), cfg, src)
	if err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 || got.Origin != policyFromServer || got.Policy.Version != 4 {
		t.Fatalf("fetched = %+v after %d calls", got, src.calls)
	}
	cached, ok, err := inv.cachedPolicy(cfg)
	if err != nil || !ok {
		t.Fatalf("cache = %+v, %v, %v", cached, ok, err)
	}
	if cached.Origin != policyFromCache || cached.Policy.Version != 4 || cached.Policy.FailOn != checkpolicy.Warning {
		t.Errorf("cached = %+v", cached)
	}
	if _, err := os.Stat(filepath.Join(w.dir, filepath.FromSlash(policyCachePath))); err != nil {
		t.Errorf("no cache file: %v", err)
	}
}

// TestCachedPolicyRefusesADocumentItCannotRead is exit 2's source: the
// cache is the policy, and an unreadable policy is not a verdict.
func TestCachedPolicyRefusesADocumentItCannotRead(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{"en": `{}`})
	inv := &invocation{env: Env{Dir: w.dir, Getenv: func(k string) string { return w.env[k] }}}
	cfg, err := inv.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	w.write(policyCachePath, "not json")
	_, _, err = inv.cachedPolicy(cfg)
	var e *Error
	if !errors.As(err, &e) || e.Exit != ExitUsage || e.Code != "invalid_policy_cache" {
		t.Fatalf("err = %v", err)
	}
}

func hasLayer(ls []domain.Layer, l domain.Layer) bool {
	for _, k := range ls {
		if k == l {
			return true
		}
	}
	return false
}

// TestCheckGradesAgainstTheServedPolicyDocument proves `glossa check`
// reads the policy from `GET …/check-policy` and not from the three
// fields of settings.check_policy.
//
// The two are set to disagree: the settings say nothing fails, the
// document says a missing translation is an error. Only one of them can
// produce the verdict, and it must be the document — otherwise the
// rules, environments and version the policy API exists to carry never
// reach the run.
func TestCheckGradesAgainstTheServedPolicyDocument(t *testing.T) {
	srv, w := pushed(t)
	srv.checkPolicy = map[string]any{"require_complete": "none", "fail_on": "never", "missing_translations": "warning"}
	srv.policyDoc = map[string]any{
		"version": 11,
		"document": map[string]any{
			"schema":               "glossa.check-policy/v1",
			"require_complete":     "all",
			"fail_on":              "error",
			"missing_translations": "error",
		},
	}

	var doc checkJSON
	w.json(&doc, "check").want(t, ExitCheckFailed)
	if doc.Policy.Source != "server" {
		t.Errorf("policy source = %q, want server", doc.Policy.Source)
	}
	if doc.Policy.Version != 11 {
		t.Errorf("policy version = %d, want 11 (the document's, not settings')", doc.Policy.Version)
	}
}

// TestCheckFallsBackToSettingsOnAnOlderServer: a server without the
// endpoint answers 404, which is not a failure. The three stored fields
// still decide, exactly as they did before the policy API existed.
func TestCheckFallsBackToSettingsOnAnOlderServer(t *testing.T) {
	srv, w := pushed(t)
	srv.policyDoc = nil // the route 404s
	srv.checkPolicy = map[string]any{"require_complete": "all", "fail_on": "error", "missing_translations": "error"}

	var doc checkJSON
	w.json(&doc, "check").want(t, ExitCheckFailed)
	if doc.Policy.Source != "server" {
		t.Errorf("policy source = %q, want server", doc.Policy.Source)
	}
	if doc.Policy.Version != 0 {
		t.Errorf("policy version = %d, want 0 (settings carry none)", doc.Policy.Version)
	}
}
