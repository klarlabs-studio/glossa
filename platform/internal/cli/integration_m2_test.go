//go:build integration

package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli"
)

// session is a signed-in Studio user (the project's owner): what the
// CLI's API token may not do, such as approving a translation or
// turning on provider consent, goes through it.
type session struct{ cookie, csrf string }

func (u session) call(method, path string, body any) call {
	return call{method: method, path: path, body: body, cookie: u.cookie, csrf: u.csrf}
}

// fakeProvider is an OpenAI-compatible endpoint with scripted answers —
// never a real provider. It answers the translate prompt with a fixed
// MF2 translation and the self-assessment with a confident score.
func fakeProvider(t *testing.T, translation string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-fake" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		calls.Add(1)
		var req struct {
			Model          string `json:"model"`
			ResponseFormat struct {
				JSONSchema struct {
					Schema struct {
						Properties map[string]any `json:"properties"`
					} `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answer := map[string]any{"message": translation, "notes": ""}
		if _, ok := req.ResponseFormat.JSONSchema.Schema.Properties["score"]; ok {
			answer = map[string]any{"score": 0.93, "formality_ok": true, "issues": []string{}}
		}
		content, _ := json.Marshal(answer)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   req.Model,
			"choices": []map[string]any{{"message": map[string]any{"content": string(content)}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 400, "completion_tokens": 20},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// knowledgeLoop is M2 through the CLI: add a term → terms check finds a
// forbidden term → a reviewer approves a translation → tm search finds
// it → translate --dry-run shows the consent refusal → the owner turns
// consent on → translate --wait with the fake provider → review list
// shows the suggestion → review accept.
func knowledgeLoop(t *testing.T, r runner, s *server, owner session, base, projectID string) {
	p := base + "/projects/" + projectID

	// The termbase: "loaf" is forbidden for de "Brot"; en cart.items says
	// "# loaf".
	var added struct {
		Action  string `json:"action"`
		Concept struct {
			ID    string                                  `json:"id"`
			Terms []struct{ Locale, Text, Status string } `json:"terms"`
		} `json:"concept"`
	}
	r.run(cli.ExitOK, &added, "terms", "add", "Brot", "--preferred", "en=bread", "--forbidden", "en=loaf", "--domain", "bakery")
	if added.Action != "created" || len(added.Concept.Terms) != 3 || added.Concept.Terms[0].Locale != "de" {
		t.Fatalf("terms add = %+v", added)
	}
	var shown struct {
		Concept struct{ ID string } `json:"concept"`
	}
	r.run(cli.ExitOK, &shown, "terms", "show", "loaf", "--locale", "en")
	if shown.Concept.ID != added.Concept.ID {
		t.Errorf("terms show = %+v", shown)
	}
	var check struct {
		Passed   bool `json:"passed"`
		Errors   int  `json:"errors"`
		Findings []struct {
			Code, Severity, Locale, Key, Text string
		} `json:"findings"`
	}
	r.run(cli.ExitCheckFailed, &check, "terms", "check", "--locale", "en")
	forbidden := false
	for _, f := range check.Findings {
		if f.Code == "term_forbidden" && f.Severity == "error" && f.Key == "cart.items" && strings.EqualFold(f.Text, "loaf") {
			forbidden = true
		}
	}
	if check.Passed || !forbidden {
		t.Fatalf("terms check = %+v", check)
	}
	r.run(cli.ExitCheckFailed, nil, "check", "--terminology")

	// A reviewer approves en checkout.pay; the translation memory derives
	// a unit from it (asynchronously, through the reviewed event). Targets
	// come back as MF2 and in the query's syntax.
	var tr struct{ State string }
	h := s.do(owner.call("GET", p+"/messages/checkout.pay/translations/en", nil), http.StatusOK, &tr)
	approve := owner.call("POST", p+"/messages/checkout.pay/translations/en/reviews", map[string]string{"state": "approved"})
	approve.headers = map[string]string{"If-Match": h.Get("ETag")}
	s.do(approve, http.StatusOK, nil)
	var search struct {
		Matches []struct {
			Score        int    `json:"score"`
			Kind         string `json:"kind"`
			Target       string `json:"target"`
			TargetText   string `json:"target_text"`
			TargetSyntax string `json:"target_syntax"`
			Unit         struct {
				ID         string `json:"id"`
				MessageKey string `json:"message_key"`
			} `json:"unit"`
		} `json:"matches"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		r.run(cli.ExitOK, &search, "tm", "search", "Bezahle {amount, number}", "--to", "en")
		if len(search.Matches) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(search.Matches) == 0 || search.Matches[0].Score < 100 || search.Matches[0].Target != "Pay {$amount :number}" ||
		search.Matches[0].TargetText != "Pay {amount, number}" || search.Matches[0].TargetSyntax != "mf1" ||
		search.Matches[0].Unit.MessageKey != "checkout.pay" {
		t.Fatalf("tm search = %+v", search)
	}
	var units struct {
		Units []struct{ ID string } `json:"units"`
	}
	r.run(cli.ExitOK, &units, "tm", "units", "--locale-pair", "de:en")
	if len(units.Units) != 1 || units.Units[0].ID != search.Matches[0].Unit.ID {
		t.Errorf("tm units = %+v", units)
	}

	// A new message to translate; consent is off, so the dry run refuses.
	catalog := filepath.Join(r.dir, "locales", "de.json")
	if err := os.WriteFile(catalog, []byte(`{
  "cart": {"items": "{count, plural, one {# Brot} other {# Brote}}"},
  "checkout.pay": "Bezahle {amount, number}",
  "home.title": "Willkommen",
  "home.greeting": "Hallo und willkommen"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r.run(cli.ExitOK, nil, "push")
	type translateDoc struct {
		Plan []struct {
			Locale   string         `json:"locale"`
			Keys     []string       `json:"keys"`
			Provider int            `json:"provider"`
			Refused  map[string]int `json:"refused"`
		} `json:"plan"`
		Refusals []struct{ Code string } `json:"refusals"`
		Fills    []struct {
			ID          string `json:"id"`
			JobsCreated int    `json:"jobs_created"`
		} `json:"fills"`
		Wait *struct {
			JobStates map[string]int `json:"job_states"`
			Failed    []struct {
				Key, FailureCode, Error string
			} `json:"failed"`
		} `json:"wait"`
	}
	// The push updated Localization's view of the catalog in its own
	// transaction: the server's preview sees home.greeting at once.
	var dry translateDoc
	r.run(cli.ExitCheckFailed, &dry, "translate", "--locale", "en", "--dry-run")
	codes := []string{}
	for _, rf := range dry.Refusals {
		codes = append(codes, rf.Code)
	}
	if len(dry.Plan) != 1 || strings.Join(dry.Plan[0].Keys, ",") != "home.greeting" || !strings.Contains(strings.Join(codes, ","), "provider_consent_off") ||
		dry.Plan[0].Refused["provider_consent"] != 1 {
		t.Fatalf("translate --dry-run = %+v", dry)
	}

	// The owner configures a provider (the fake, on loopback) and turns
	// consent on with a budget.
	provider, providerCalls := fakeProvider(t, "Hello and welcome")
	s.do(owner.call("POST", base+"/ai-providers", map[string]any{
		"name": "anthropic", "kind": "openai_compatible", "base_url": provider.URL + "/v1", "api_key": "sk-fake",
	}), http.StatusCreated, nil)
	s.do(owner.call("PUT", base+"/ai-settings", map[string]any{"provider_consent": true, "monthly_budget_micro_usd": 5_000_000}), http.StatusOK, nil)
	var status struct {
		Consent struct {
			Enabled bool `json:"enabled"`
		} `json:"consent"`
		Providers []struct {
			Name      string `json:"name"`
			APIKeySet bool   `json:"api_key_set"`
		} `json:"providers"`
	}
	out := r.run(cli.ExitOK, &status, "ai", "status")
	if !status.Consent.Enabled || len(status.Providers) != 1 || !status.Providers[0].APIKeySet || strings.Contains(out, "sk-fake") {
		t.Fatalf("ai status = %s", out)
	}
	dry = translateDoc{}
	r.run(cli.ExitOK, &dry, "translate", "--locale", "en", "--dry-run")
	if len(dry.Plan) != 1 || dry.Plan[0].Provider != 1 || len(dry.Refusals) != 0 {
		t.Fatalf("translate --dry-run with consent = %+v", dry)
	}

	var fill translateDoc
	r.run(cli.ExitOK, &fill, "translate", "--locale", "en", "--wait", "--poll-interval", "100ms", "--timeout", "60s")
	if len(fill.Fills) != 1 || fill.Fills[0].JobsCreated != 1 || fill.Wait == nil || fill.Wait.JobStates["succeeded"] != 1 || len(fill.Wait.Failed) != 0 {
		t.Fatalf("translate --wait = %+v", fill)
	}
	if providerCalls.Load() == 0 {
		t.Error("the fake provider was never called")
	}

	var queue struct {
		Suggestions []struct {
			ID     string  `json:"id"`
			Key    string  `json:"key"`
			Locale string  `json:"locale"`
			Text   string  `json:"text"`
			Origin string  `json:"origin"`
			Score  float64 `json:"score"`
		} `json:"suggestions"`
	}
	r.run(cli.ExitOK, &queue, "review", "list", "--locale", "en")
	if len(queue.Suggestions) != 1 || queue.Suggestions[0].Key != "home.greeting" || queue.Suggestions[0].Origin != "ai" ||
		!strings.Contains(queue.Suggestions[0].Text, "Hello and welcome") {
		t.Fatalf("review list = %+v", queue)
	}
	var decision struct {
		Decision   string `json:"decision"`
		Suggestion struct {
			Status              string `json:"status"`
			TranslationRevision *int   `json:"translation_revision"`
		} `json:"suggestion"`
	}
	r.run(cli.ExitOK, &decision, "review", "accept", "home.greeting", "--locale", "en")
	if decision.Decision != "accepted" || decision.Suggestion.Status != "accepted" || decision.Suggestion.TranslationRevision == nil {
		t.Fatalf("review accept = %+v", decision)
	}
	r.run(cli.ExitOK, &queue, "review", "list", "--locale", "en")
	if len(queue.Suggestions) != 0 {
		t.Errorf("queue after accept = %+v", queue)
	}
	var written struct {
		Text, Origin string
	}
	s.do(owner.call("GET", p+"/messages/home.greeting/translations/en", nil), http.StatusOK, &written)
	if written.Origin != "ai" || !strings.Contains(written.Text, "Hello and welcome") {
		t.Errorf("accepted translation = %+v", written)
	}
}
