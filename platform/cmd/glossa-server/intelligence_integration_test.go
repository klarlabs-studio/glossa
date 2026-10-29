//go:build integration

package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type aiJob struct {
	ID           string `json:"id"`
	MessageKey   string `json:"message_key"`
	State        string `json:"state"`
	FailureCode  string `json:"failure_code"`
	LastError    string `json:"last_error"`
	SuggestionID string `json:"suggestion_id"`
	Audit        []struct {
		Tool string `json:"tool"`
	} `json:"audit"`
}

// Intelligence end to end over HTTP, through the generated server and
// the in-process workers, without a provider call: provider keys are
// write-only, consent is off by default, a fill reuses an exact
// translation-memory match (and fails what would need a provider), the
// suggestion reaches the review queue and becomes a translation with
// provenance on accept; metrics, disclosures and the eval baseline read
// back.
func TestIntelligenceOverHTTP(t *testing.T) {
	tp := newTranslatedProject(t) // fr checkout.pay "Payer {amount, number}" is approved → TM
	s, ada := tp.s, tp.ada
	base := tp.path[:len(tp.path)-len("/projects/")-36]
	p := tp.path
	owner := func(c call) call { c.cookie, c.csrf = ada.cookie, ada.csrf; return c }

	// Providers: the key goes in and never comes out.
	r := s.do(owner(call{method: "POST", path: base + "/ai-providers", body: map[string]any{
		"name": "anthropic", "kind": "anthropic", "api_key": "sk-ant-never-shown",
	}}))
	r.want(t, http.StatusCreated, "")
	var provider struct {
		ID        string `json:"id"`
		APIKeySet bool   `json:"api_key_set"`
	}
	r.decode(t, &provider)
	if !provider.APIKeySet || strings.Contains(string(r.body), "sk-ant") || r.header.Get("ETag") == "" {
		t.Fatalf("created provider = %s", r.body)
	}
	if got := s.do(call{method: "GET", path: base + "/ai-providers", bearer: tp.token}); strings.Contains(string(got.body), "sk-ant") || got.status != 200 {
		t.Errorf("list = %d %s", got.status, got.body)
	}
	s.do(call{method: "POST", path: base + "/ai-providers", bearer: tp.token, body: map[string]any{"name": "x", "kind": "gemini"}}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(owner(call{method: "POST", path: base + "/ai-providers", body: map[string]any{
		"name": "local", "kind": "openai_compatible", "base_url": "https://127.0.0.1:8443/v1",
	}})).want(t, http.StatusBadRequest, "invalid_provider")
	s.do(owner(call{method: "PUT", path: base + "/ai-routing-policy", body: map[string]any{"rules": []map[string]any{
		{"task": "translate", "routes": []map[string]any{{"provider": "openai", "model": "gpt-5", "max_tokens": 1000}}},
	}}})).want(t, http.StatusBadRequest, "invalid_routing_policy")
	s.do(owner(call{method: "PUT", path: base + "/ai-routing-policy", body: map[string]any{"rules": []map[string]any{
		{"task": "translate", "routes": []map[string]any{{"provider": "anthropic", "model": "claude-sonnet-5", "max_tokens": 4000}}},
	}}})).want(t, http.StatusOK, "")
	s.do(owner(call{method: "DELETE", path: base + "/ai-providers/" + provider.ID})).want(t, http.StatusConflict, "provider_in_use")

	// Settings: consent is off until someone turns it on.
	var settings struct {
		ProviderConsent bool `json:"provider_consent"`
		Version         int  `json:"version"`
	}
	got := s.do(call{method: "GET", path: base + "/ai-settings", bearer: tp.token})
	got.decode(t, &settings)
	if settings.ProviderConsent || settings.Version != 0 || got.header.Get("ETag") != `"0"` {
		t.Errorf("default settings = %+v, ETag %s", settings, got.header.Get("ETag"))
	}
	// The ETag of unsaved settings works as If-Match: "0" creates them
	// only while nobody else has.
	putSettings := func(etag string) call {
		c := owner(call{method: "PUT", path: base + "/ai-settings", body: map[string]any{"monthly_budget_micro_usd": 0}})
		c.headers = map[string]string{"If-Match": etag}
		return c
	}
	if r := s.do(putSettings(`"0"`)); r.status != http.StatusOK || r.header.Get("ETag") != `"1"` {
		t.Fatalf("create if absent = %d %s", r.status, r.body)
	}
	s.do(putSettings(`"0"`)).want(t, http.StatusPreconditionFailed, "precondition_failed")
	s.do(putSettings(`"1"`)).want(t, http.StatusOK, "")
	pr := s.do(call{method: "GET", path: p + "/ai-routing-policy", bearer: tp.token})
	if pr.status != http.StatusOK || pr.header.Get("ETag") != `"0"` {
		t.Errorf("inherited project routing = %d, ETag %s", pr.status, pr.header.Get("ETag"))
	}
	s.do(owner(call{method: "PUT", path: p + "/ai-settings", body: map[string]any{
		"review": map[string]any{"auto_approve": true, "auto_approve_min": 0.9, "recommend_min": 0.75, "auto_approve_environments": []string{"nowhere"}},
	}})).want(t, http.StatusUnprocessableEntity, "auto_approve_ineligible")
	s.do(owner(call{method: "PUT", path: p + "/ai-settings", body: map[string]any{
		"namespace_tags": map[string]any{"legal": []string{"sensitive"}},
	}})).want(t, http.StatusOK, "")

	// A fill: order.pay reuses checkout.pay's approved fr text; the new
	// checkout.total would need a provider, which consent forbids.
	s.do(owner(call{method: "POST", path: p + "/message-upserts", body: map[string]any{"items": []map[string]any{
		{"key": "order.pay", "text": "Pay {amount, number}"},
	}}})).want(t, http.StatusOK, "")
	var fill struct {
		ID          string         `json:"id"`
		Select      string         `json:"select"`
		JobsCreated int            `json:"jobs_created"`
		Warnings    []string       `json:"warnings"`
		JobStates   map[string]int `json:"job_states"`
	}
	s.do(owner(call{method: "POST", path: p + "/ai-fills", body: map[string]any{
		"locales": []string{"fr"}, "select": "missing", "include_outdated": true,
	}})).want(t, http.StatusBadRequest, "invalid_query")

	// The preview says it first, and queues nothing.
	var pv struct {
		Select  string `json:"select"`
		Locales []struct {
			Locale   string         `json:"locale"`
			Keys     []string       `json:"keys"`
			TMExact  int            `json:"tm_exact"`
			Provider int            `json:"provider"`
			Refused  map[string]int `json:"refused"`
		} `json:"locales"`
		Warnings []string `json:"warnings"`
		Cost     struct {
			Estimated int64 `json:"estimated_micro_usd"`
		} `json:"cost"`
	}
	r = s.do(owner(call{method: "POST", path: p + "/ai-fill-previews", body: map[string]any{
		"locales": []string{"fr"}, "keys": []string{"order.pay", "checkout.total"},
	}}))
	r.want(t, http.StatusOK, "")
	r.decode(t, &pv)
	if pv.Select != "missing_or_outdated" || len(pv.Locales) != 1 || strings.Join(pv.Locales[0].Keys, ",") != "checkout.total,order.pay" ||
		pv.Locales[0].TMExact != 1 || pv.Locales[0].Refused["provider_consent"] != 1 || pv.Cost.Estimated != 0 {
		t.Fatalf("preview = %s", r.body)
	}
	var none struct {
		Items []aiJob `json:"items"`
	}
	s.do(call{method: "GET", path: base + "/ai-jobs", bearer: tp.token}).decode(t, &none)
	if len(none.Items) != 0 {
		t.Fatalf("the preview queued jobs: %+v", none.Items)
	}
	s.do(owner(call{method: "POST", path: p + "/ai-fill-previews", body: map[string]any{
		"locales": []string{"ja"},
	}})).want(t, http.StatusNotFound, "locale_not_found")
	r = s.do(owner(call{method: "POST", path: p + "/ai-fills", body: map[string]any{
		"locales": []string{"fr"}, "keys": []string{"order.pay", "checkout.total"},
	}, headers: map[string]string{"Idempotency-Key": "fill-fr-1"}}))
	r.want(t, http.StatusCreated, "")
	r.decode(t, &fill)
	if fill.JobsCreated != 2 || !strings.Contains(strings.Join(fill.Warnings, ","), "provider_consent_off") || fill.Select != "missing_or_outdated" {
		t.Fatalf("fill = %s", r.body)
	}
	jobs := waitForJobs(t, s, base, tp.token, fill.ID, 2)
	byKey := map[string]aiJob{}
	for _, j := range jobs {
		byKey[j.MessageKey] = j
	}
	if j := byKey["checkout.total"]; j.State != "failed" || j.FailureCode != "provider_consent" {
		t.Errorf("checkout.total job = %+v", j)
	}
	tm := byKey["order.pay"]
	if tm.State != "succeeded" || tm.SuggestionID == "" {
		t.Fatalf("order.pay job = %+v", tm)
	}
	var detailed aiJob
	s.do(call{method: "GET", path: base + "/ai-jobs/" + tm.ID, bearer: tp.token}).decode(t, &detailed)
	if len(detailed.Audit) == 0 || detailed.Audit[0].Tool != "tm_lookup" {
		t.Errorf("audit = %+v", detailed.Audit)
	}
	s.do(owner(call{method: "POST", path: base + "/ai-jobs/" + tm.ID + "/cancellation"})).want(t, http.StatusConflict, "job_not_cancellable")

	// The review queue holds the suggestion; accepting writes a revision
	// with its provenance.
	var queue struct {
		Items []struct {
			ID         string  `json:"id"`
			MessageKey string  `json:"message_key"`
			Score      float64 `json:"score"`
			Provenance struct {
				Origin    string   `json:"origin"`
				TMUnitIDs []string `json:"tm_unit_ids"`
			} `json:"provenance"`
			Explanation []struct {
				Factor string `json:"factor"`
			} `json:"explanation"`
			Source struct {
				MessageKey     string `json:"message_key"`
				Namespace      string `json:"namespace"`
				State          string `json:"state"`
				SourceRevision int    `json:"source_revision"`
				Text           string `json:"text"`
				Syntax         string `json:"syntax"`
				MF2            string `json:"mf2"`
			} `json:"source"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: p + "/ai-review-queue?locale=fr", bearer: tp.token}).decode(t, &queue)
	if len(queue.Items) != 1 || queue.Items[0].ID != tm.SuggestionID || queue.Items[0].Provenance.Origin != "translation_memory" ||
		len(queue.Items[0].Provenance.TMUnitIDs) != 1 || len(queue.Items[0].Explanation) == 0 {
		t.Fatalf("queue = %+v", queue)
	}
	if src := queue.Items[0].Source; src.MessageKey != "order.pay" || src.Namespace != "default" || src.State != "active" ||
		src.SourceRevision != 1 || src.Text != "Pay {amount, number}" || src.Syntax != "mf1" || src.MF2 != "Pay {$amount :number}" {
		t.Errorf("queue item source = %+v", src)
	}
	s.do(call{method: "POST", path: base + "/ai-suggestions/" + tm.SuggestionID + "/acceptance", bearer: tp.token, body: map[string]any{}}).
		want(t, http.StatusForbidden, "forbidden")
	r = s.do(owner(call{method: "POST", path: base + "/ai-suggestions/" + tm.SuggestionID + "/acceptance", body: map[string]any{}}))
	r.want(t, http.StatusOK, "")
	var accepted struct {
		Status              string `json:"status"`
		TranslationRevision int    `json:"translation_revision"`
	}
	r.decode(t, &accepted)
	if accepted.Status != "accepted" || accepted.TranslationRevision != 1 {
		t.Errorf("accepted = %s", r.body)
	}
	var tr struct {
		Text   string `json:"text"`
		State  string `json:"state"`
		Origin string `json:"origin"`
	}
	s.do(call{method: "GET", path: p + "/messages/order.pay/translations/fr", bearer: tp.token}).decode(t, &tr)
	if tr.Origin != "translation_memory" || tr.State != "approved" || !strings.Contains(tr.Text, "Payer") {
		t.Errorf("translation = %+v", tr)
	}
	s.do(owner(call{method: "POST", path: base + "/ai-suggestions/" + tm.SuggestionID + "/rejection", body: map[string]any{}})).
		want(t, http.StatusConflict, "suggestion_decided")

	// Insights: metrics, disclosures (none: nothing reached a provider),
	// budget, the eval baseline and Prometheus.
	var metrics struct {
		Locales []struct {
			Locale         string  `json:"locale"`
			Accepted       int     `json:"accepted"`
			AcceptanceRate float64 `json:"acceptance_rate"`
		} `json:"locales"`
	}
	s.do(call{method: "GET", path: p + "/ai-metrics", bearer: tp.token}).decode(t, &metrics)
	if len(metrics.Locales) != 1 || metrics.Locales[0].Locale != "fr" || metrics.Locales[0].AcceptanceRate != 1 {
		t.Errorf("metrics = %+v", metrics)
	}
	var disclosures struct {
		Items []any `json:"items"`
	}
	s.do(call{method: "GET", path: base + "/ai-disclosures", bearer: tp.token}).decode(t, &disclosures)
	if len(disclosures.Items) != 0 {
		t.Errorf("disclosures = %+v", disclosures)
	}
	var budget struct {
		SpentMicroUSD int64 `json:"spent_micro_usd"`
	}
	s.do(call{method: "GET", path: base + "/ai-budget", bearer: tp.token}).decode(t, &budget)
	if budget.SpentMicroUSD != 0 {
		t.Errorf("spent %d without a provider call", budget.SpentMicroUSD)
	}
	var baseline struct {
		Pairs map[string]struct {
			Cases int `json:"cases"`
		} `json:"pairs"`
	}
	s.do(call{method: "GET", path: base + "/ai-eval-baseline", bearer: tp.token}).decode(t, &baseline)
	if baseline.Pairs["all"].Cases < 50 || baseline.Pairs["en-pl"].Cases == 0 {
		t.Errorf("baseline = %+v", baseline)
	}
	resp, err := http.Get(s.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"glossa_intelligence_jobs_total", "glossa_intelligence_suggestion_decisions_total"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
}

// waitForJobs polls a fill's jobs until n of them are final.
func waitForJobs(t *testing.T, s *server, base, token, fill string, n int) []aiJob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var list struct {
			Items []aiJob `json:"items"`
		}
		s.do(call{method: "GET", path: base + "/ai-jobs?fill=" + fill, bearer: token}).decode(t, &list)
		final := 0
		for _, j := range list.Items {
			if j.State != "queued" && j.State != "running" {
				final++
			}
		}
		if final >= n {
			return list.Items
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs never finished: %+v", list.Items)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
