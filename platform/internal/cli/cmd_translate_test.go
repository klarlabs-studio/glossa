package cli

import (
	"strings"
	"testing"
)

// withSourceKey is sourceEN with one more message.
func withSourceKey(entry string) string {
	i := strings.LastIndex(sourceEN, "}")
	return sourceEN[:i] + ", " + entry + sourceEN[i:]
}

func refusalCodes(rs []refusalJSON) string {
	var codes []string
	for _, r := range rs {
		codes = append(codes, r.Code)
	}
	return strings.Join(codes, ",")
}

func TestTranslateDryRunReportsPlanAndRefusals(t *testing.T) {
	srv, w := seeded(t)
	var out translateJSON
	// Consent is off and no provider is configured: the plan stands, the
	// refusals say why jobs would do little, and the exit code says so.
	w.json(&out, "translate", "--locale", "ja", "--dry-run").want(t, ExitCheckFailed)
	if out.Schema != "glossa.cli.translate/v1" || !out.DryRun || len(out.Plan) != 1 || out.Plan[0].Queue != 1 ||
		strings.Join(out.Plan[0].Keys, ",") != "checkout.pay" || len(out.Fills) != 0 || out.Wait != nil ||
		refusalCodes(out.Refusals) != "provider_consent_off,no_provider" {
		t.Fatalf("dry run = %+v", out)
	}
	if srv.countRequests("POST /v1/tenants/ten_1/projects/prj_1/ai-fills") != 0 {
		t.Fatal("the dry run queued a fill")
	}
	// The server's preview decides it: one request, no message listing,
	// no settings, budget or provider reads of its own.
	for _, route := range []string{"GET /v1/tenants/ten_1/projects/prj_1/messages", "GET /v1/tenants/ten_1/ai-settings",
		"GET /v1/tenants/ten_1/ai-budget", "GET /v1/tenants/ten_1/ai-providers"} {
		if n := srv.countRequests(route); n != 0 {
			t.Errorf("the dry run sent %d × %s", n, route)
		}
	}
	if srv.countRequests("POST /v1/tenants/ten_1/projects/prj_1/ai-fill-previews") != 1 || out.Plan[0].Refused["provider_consent"] != 1 ||
		out.Plan[0].Provider != 0 || out.Cost == nil || out.Cost.Estimated != 0 {
		t.Errorf("preview = %+v", out)
	}
	h := w.run("translate", "--locale", "ja", "--dry-run")
	for _, want := range []string{"Dry run: 1 AI job would be queued", "ja  1 message", "checkout.pay", "provider consent", "fix:"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human dry run lacks %q:\n%s", want, h.stdout)
		}
	}

	srv.mu.Lock()
	srv.kn.consent = true
	srv.kn.nsTags["default"] = []string{"sensitive"}
	srv.mu.Unlock()
	srv.addProvider()
	w.json(&out, "translate", "--locale", "ja,de", "--dry-run").want(t, ExitOK)
	if len(out.Refusals) != 0 || out.Skipped["sensitive"] != 1 || out.Plan[0].Queue != 0 {
		t.Errorf("sensitive dry run = %+v", out)
	}
	srv.mu.Lock()
	srv.kn.nsTags = map[string][]string{}
	srv.mu.Unlock()
	w.json(&out, "translate", "--locale", "ja", "--dry-run").want(t, ExitOK)
	if out.Plan[0].Provider != 1 || out.Cost.Estimated != 1000 || out.Cost.Max != 50000 {
		t.Errorf("priced dry run = %+v", out)
	}
	if h := w.run("translate", "--locale", "ja", "--dry-run"); !strings.Contains(h.stdout, "1 by a provider") ||
		!strings.Contains(h.stdout, "estimated cost $0.0010 (at most $0.0500)") {
		t.Errorf("human priced dry run:\n%s", h.stdout)
	}
	srv.mu.Lock()
	srv.kn.budget = 0
	srv.mu.Unlock()
	w.json(&out, "translate", "--locale", "ja", "--dry-run").want(t, ExitCheckFailed)
	if refusalCodes(out.Refusals) != "no_budget" || out.Plan[0].Refused["budget_exceeded"] != 1 {
		t.Errorf("no budget dry run = %+v", out)
	}
	srv.mu.Lock()
	srv.kn.budget = 5_000_000
	srv.kn.nsTags["default"] = []string{"sensitive"}
	srv.mu.Unlock()

	// --outdated plans the outdated translations instead.
	w.write("locales/en.json", strings.Replace(sourceEN, `"Checkout"`, `"Check out"`, 1))
	w.run("push").want(t, ExitOK)
	srv.mu.Lock()
	srv.kn.nsTags = map[string][]string{}
	srv.mu.Unlock()
	w.json(&out, "translate", "--locale", "ja", "--locale", "de", "--outdated", "--dry-run").want(t, ExitOK)
	if len(out.Plan) != 2 || strings.Join(out.Plan[0].Keys, ",") != "cart.checkout" || strings.Join(out.Plan[1].Keys, ",") != "cart.checkout" {
		t.Errorf("outdated dry run = %+v", out)
	}
	w.json(&out, "translate", "--locale", "ja", "--missing", "--outdated", "--dry-run").want(t, ExitOK)
	if strings.Join(out.Plan[0].Keys, ",") != "cart.checkout,checkout.pay" || !out.Filter.Missing || !out.Filter.Outdated {
		t.Errorf("missing+outdated dry run = %+v", out)
	}

	w.run("translate").want(t, ExitUsage)
	w.run("translate", "--locale", "en").want(t, ExitUsage) // the source locale
	var e errorDoc
	w.json(&e, "translate", "--locale", "fr").want(t, ExitUsage)
	if e.Error.Code != "locale_not_found" {
		t.Errorf("unknown locale = %+v", e)
	}
	w.run("translate", "--locale", "ja", "--dry-run", "--wait").want(t, ExitUsage)
}

func TestTranslateQueuesAndWaits(t *testing.T) {
	srv, w := seeded(t)
	srv.addProvider()
	srv.mu.Lock()
	srv.kn.consent = true
	srv.mu.Unlock()

	var out translateJSON
	w.json(&out, "translate", "--locale", "ja").want(t, ExitOK)
	if out.DryRun || len(out.Fills) != 1 || out.Fills[0].JobsCreated != 1 || out.Fills[0].JobStates["queued"] != 1 || out.Wait != nil ||
		len(out.Refusals) != 0 || out.Cost != nil {
		t.Fatalf("fill = %+v", out)
	}

	// --outdated fills by state in one request: no message listing, no
	// key lists.
	w.write("locales/en.json", strings.Replace(sourceEN, `"Checkout"`, `"Check out"`, 1))
	w.run("push").want(t, ExitOK)
	w.json(&out, "translate", "--locale", "ja", "--locale", "de", "--outdated").want(t, ExitOK)
	if len(out.Fills) != 1 || out.Fills[0].JobsCreated != 2 || out.Fills[0].Keys != 0 {
		t.Errorf("outdated fill = %+v", out)
	}
	srv.mu.Lock()
	bodies := srv.kn.fillBodies
	srv.mu.Unlock()
	if last := bodies[len(bodies)-1]; last.Select != "outdated" || last.Keys != nil || strings.Join(last.Locales, ",") != "ja,de" {
		t.Errorf("outdated fill request = %+v", last)
	}
	if n := srv.countRequests("GET /v1/tenants/ten_1/projects/prj_1/messages"); n != 0 {
		t.Errorf("translate listed messages %d times", n)
	}
	w.json(&out, "translate", "--locale", "ja", "--missing", "--outdated").want(t, ExitOK)
	srv.mu.Lock()
	last := srv.kn.fillBodies[len(srv.kn.fillBodies)-1]
	srv.mu.Unlock()
	if last.Select != "missing_or_outdated" {
		t.Errorf("missing+outdated select = %q", last.Select)
	}

	// --wait polls until every job is final; a failed job exits 4.
	w.write("locales/en.json", withSourceKey(`"cart.empty": "Your cart is empty"`))
	w.run("push").want(t, ExitOK)
	srv.mu.Lock()
	srv.kn.failKeys["cart.empty"] = true
	srv.mu.Unlock()
	w.json(&out, "translate", "--locale", "ja", "--locale", "de", "--wait", "--poll-interval", "1ms").want(t, ExitPartial)
	if out.Wait == nil || out.Wait.JobStates["succeeded"] != 1 || out.Wait.JobStates["failed"] != 2 || len(out.Wait.Failed) != 2 ||
		out.Wait.Failed[0].Key != "cart.empty" || out.Wait.Failed[0].Locale != "de" || out.Wait.Failed[0].FailureCode != "invalid_output" {
		t.Fatalf("wait = %+v", out.Wait)
	}

	srv.mu.Lock()
	srv.kn.failKeys = map[string]bool{}
	srv.mu.Unlock()
	w.write("locales/en.json", withSourceKey(`"cart.full": "Your cart is full"`))
	w.run("push").want(t, ExitOK)
	h := w.run("translate", "--locale", "de", "--wait", "--poll-interval", "1ms")
	h.want(t, ExitOK)
	for _, want := range []string{"Queued 2 AI jobs for de", "2 jobs finished: 2 succeeded", "glossa review list --locale de"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human wait lacks %q:\n%s", want, h.stdout)
		}
	}
	if !strings.Contains(h.stderr, "2/2 jobs finished") {
		t.Errorf("no progress on stderr:\n%s", h.stderr)
	}
}

func TestTranslateWithoutConsentWarnsAndFails(t *testing.T) {
	_, w := seeded(t)
	var out translateJSON
	w.json(&out, "translate", "--locale", "ja", "--wait", "--poll-interval", "1ms").want(t, ExitPartial)
	if refusalCodes(out.Refusals) != "provider_consent_off,no_provider" || out.Wait == nil || len(out.Wait.Failed) != 1 ||
		out.Wait.Failed[0].FailureCode != "provider_consent" {
		t.Fatalf("no consent = %+v", out)
	}
	var e errorDoc
	w.json(&e, "translate", "--locale", "ja", "--wait", "--poll-interval", "1h", "--timeout", "1ns").want(t, ExitNetwork)
	if e.Error.Code != "wait_timeout" {
		t.Errorf("timeout = %+v", e)
	}
}

func TestReviewListAcceptReject(t *testing.T) {
	srv, w := seeded(t)
	srv.addProvider()
	srv.mu.Lock()
	srv.kn.consent = true
	srv.mu.Unlock()
	w.write("locales/en.json", withSourceKey(`"cart.empty": "Your cart is empty"`))
	w.run("push").want(t, ExitOK)
	w.run("translate", "--locale", "ja", "--locale", "de", "--wait", "--poll-interval", "1ms").want(t, ExitOK)

	var list reviewListJSON
	w.json(&list, "review", "list").want(t, ExitOK)
	if list.Schema != "glossa.cli.review.list/v1" || len(list.Suggestions) != 3 || list.Suggestions[0].Score > list.Suggestions[2].Score {
		t.Fatalf("list = %+v", list)
	}
	s := list.Suggestions[0]
	if s.Origin != "ai" || s.Provider != "anthropic" || len(s.Explanation) != 1 || s.Status != "pending" || len(s.RiskTags) != 1 {
		t.Errorf("suggestion = %+v", s)
	}
	w.json(&list, "review", "list", "--locale", "ja").want(t, ExitOK)
	if len(list.Suggestions) != 2 {
		t.Errorf("ja queue = %+v", list)
	}
	h := w.run("review", "list", "--locale", "de")
	if !strings.Contains(h.stdout, "1 suggestion to review (de)") || !strings.Contains(h.stdout, "marketing, tm_match") {
		t.Errorf("human list:\n%s", h.stdout)
	}

	var d reviewDecisionJSON
	w.json(&d, "review", "accept", "checkout.pay", "--locale", "ja").want(t, ExitOK)
	if d.Schema != "glossa.cli.review.decision/v1" || d.Decision != "accepted" || d.Edited || d.Suggestion.Status != "accepted" ||
		d.Suggestion.TranslationRevision == nil {
		t.Fatalf("accept = %+v", d)
	}
	// A key with suggestions in two locales needs --locale.
	var e errorDoc
	w.json(&e, "review", "accept", "cart.empty").want(t, ExitUsage)
	if e.Error.Code != "suggestion_ambiguous" {
		t.Errorf("ambiguous = %+v", e)
	}
	w.json(&d, "review", "accept", "cart.empty", "--locale", "de", "--text", "{count} Artikel?", "--syntax", "mf2").want(t, ExitOK)
	if !d.Edited || d.Suggestion.Text != "{count} Artikel? [mf2]" {
		t.Errorf("edited accept = %+v", d)
	}
	id := d.Suggestion.ID
	w.json(&e, "review", "reject", id).want(t, ExitNetwork)
	if e.Error.Code != "suggestion_decided" || e.Error.Fix == "" {
		t.Errorf("decided = %+v", e)
	}
	w.json(&d, "review", "reject", "cart.empty", "--reason", "tone").want(t, ExitOK)
	if d.Decision != "rejected" || d.Suggestion.Locale != "ja" {
		t.Errorf("reject = %+v", d)
	}
	if h := w.run("review", "list"); !strings.Contains(h.stdout, "Nothing to review") {
		t.Errorf("empty queue:\n%s", h.stdout)
	}
	w.json(&e, "review", "accept", "cart.full").want(t, ExitNetwork)
	if e.Error.Code != "suggestion_not_found" {
		t.Errorf("no suggestion = %+v", e)
	}
	w.run("review", "accept").want(t, ExitUsage)
	w.run("review", "accept", "x", "--syntax", "icu").want(t, ExitUsage)
	w.run("review", "approve", "x").want(t, ExitUsage)
	w.run("review").want(t, ExitUsage)
}

func TestAIStatus(t *testing.T) {
	srv, w := seeded(t)
	var out aiStatusJSON
	w.json(&out, "ai", "status").want(t, ExitOK)
	if out.Schema != "glossa.cli.ai.status/v1" || out.Consent.Enabled || len(out.Providers) != 0 || out.Budget.MonthlyMicroUSD != 5_000_000 ||
		out.Budget.SpentMicroUSD != 1_250_000 || len(out.Budget.ByProvider) != 1 || strings.Join(out.Project.AutoTranslateLocales, ",") != "de" ||
		out.Project.Review.RecommendMin != 0.75 {
		t.Fatalf("status = %+v", out)
	}
	srv.addProvider()
	srv.mu.Lock()
	srv.kn.consent = true
	srv.mu.Unlock()
	h := w.run("ai", "status")
	h.want(t, ExitOK)
	for _, want := range []string{"✓ provider consent on", "budget $5.00 this month, $1.25 spent, $3.75 left (3 calls",
		"anthropic", "set", "auto-translate  de", "approve recommended from 0.75, auto-approve off"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human status lacks %q:\n%s", want, h.stdout)
		}
	}
	if strings.Contains(h.stdout, "sk-") {
		t.Error("a key leaked")
	}
	w.run("ai").want(t, ExitUsage)
	w.run("ai", "providers").want(t, ExitUsage)
}
