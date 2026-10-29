//go:build integration

package main

import (
	"net/http"
	"testing"
	"time"
)

type tmMatch struct {
	Score            int    `json:"score"`
	Kind             string `json:"kind"`
	Target           string `json:"target"`
	TargetText       string `json:"target_text"`
	TargetSyntax     string `json:"target_syntax"`
	Fallback         bool   `json:"target_syntax_fallback"`
	VariablesAdapted bool   `json:"variables_adapted"`
	Unit             struct {
		ID             string `json:"id"`
		MessageKey     string `json:"message_key"`
		State          string `json:"state"`
		SourceNormal   string `json:"source_normalized"`
		RetiredReason  string `json:"retired_reason"`
		TranslationRev int    `json:"translation_revision"`
	} `json:"unit"`
}

// Knowledge end to end over HTTP: an approved translation reaches
// translation memory through the outbox, lookups and concordance find
// it, the termbase recognizes and checks terms, style guides merge, and
// a read token reads but can't write.
func TestKnowledgeOverHTTP(t *testing.T) {
	tp := newTranslatedProject(t) // fr checkout.pay "Payer {amount, number}" is approved
	s, ada := tp.s, tp.ada
	base := tp.path[:len(tp.path)-len("/projects/")-36]
	project := tp.path[len(tp.path)-36:]

	lookup := map[string]any{
		"source": "Pay {total, number}", "source_locale": "en", "target_locale": "fr", "project_id": project,
		"message_key": "checkout.pay", "namespace": "default",
	}
	var res struct {
		SourceNormalized string    `json:"source_normalized"`
		Matches          []tmMatch `json:"matches"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.do(call{method: "POST", path: base + "/tm-lookups", bearer: tp.token, body: lookup}).decode(t, &res)
		if len(res.Matches) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if res.SourceNormalized != "Pay {1}" || len(res.Matches) != 1 || res.Matches[0].Score != 101 ||
		res.Matches[0].Target != "Payer {$total :number}" || !res.Matches[0].VariablesAdapted {
		t.Fatalf("lookup = %+v", res)
	}
	// An MF1 query gets its targets in MF1 too, unless it asks for MF2.
	if m := res.Matches[0]; m.TargetText != "Payer {total, number}" || m.TargetSyntax != "mf1" || m.Fallback {
		t.Errorf("mf1 target = %+v", m)
	}
	lookup["target_syntax"] = "mf2"
	s.do(call{method: "POST", path: base + "/tm-lookups", bearer: tp.token, body: lookup}).decode(t, &res)
	if m := res.Matches[0]; m.TargetText != "Payer {$total :number}" || m.TargetSyntax != "mf2" {
		t.Errorf("mf2 target = %+v", m)
	}
	// Bad input is a problem, not a 500.
	s.do(call{method: "POST", path: base + "/tm-lookups", bearer: tp.token, body: map[string]any{
		"source": "{broken", "source_locale": "en", "target_locale": "fr",
	}}).want(t, http.StatusBadRequest, "invalid_message")
	if r := s.do(call{method: "POST", path: base + "/tm-lookups", bearer: tp.token, body: map[string]any{
		"source": "x", "source_locale": "en", "target_locale": "fr", "min_score": 40,
	}}); r.status != http.StatusBadRequest {
		t.Errorf("min_score 40 = %d %s", r.status, r.body)
	}

	var conc struct {
		Matches []struct {
			Similarity float64 `json:"similarity"`
			Unit       struct {
				ID string `json:"id"`
			} `json:"unit"`
		} `json:"matches"`
	}
	s.do(call{method: "GET", path: base + "/tm-concordance?q=payer&side=target&all_projects=true", bearer: tp.token}).decode(t, &conc)
	if len(conc.Matches) != 1 {
		t.Fatalf("concordance = %+v", conc)
	}
	unit := conc.Matches[0].Unit.ID
	s.do(call{method: "DELETE", path: base + "/tm-units/" + unit, bearer: tp.token}).want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "DELETE", path: base + "/tm-units/" + unit, cookie: ada.cookie, csrf: ada.csrf}).want(t, http.StatusNoContent, "")
	var u tmMatch
	s.do(call{method: "GET", path: base + "/tm-units/" + unit, bearer: tp.token}).decode(t, &u.Unit)
	if u.Unit.State != "retired" || u.Unit.RetiredReason != "deleted" {
		t.Errorf("retired unit = %+v", u.Unit)
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: base + "/tm-units?state=retired", bearer: tp.token}).decode(t, &list)
	if len(list.Items) != 1 {
		t.Errorf("retired units = %+v", list)
	}
	s.do(call{method: "GET", path: base + "/tm-units/not-an-id", bearer: tp.token}).want(t, http.StatusNotFound, "not_found")

	// Termbase.
	concept := map[string]any{"definition": "What you buy", "terms": []map[string]any{
		{"locale": "en", "text": "item"}, {"locale": "de", "text": "Artikel"}, {"locale": "de", "text": "Item", "status": "forbidden"},
	}}
	s.do(call{method: "POST", path: base + "/term-concepts", bearer: tp.token, body: concept}).want(t, http.StatusForbidden, "forbidden")
	r := s.do(call{method: "POST", path: base + "/term-concepts", cookie: ada.cookie, csrf: ada.csrf, body: concept,
		headers: map[string]string{"Idempotency-Key": "concept-1"}})
	r.want(t, http.StatusCreated, "")
	var created struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	r.decode(t, &created)
	again := s.do(call{method: "POST", path: base + "/term-concepts", cookie: ada.cookie, csrf: ada.csrf, body: concept,
		headers: map[string]string{"Idempotency-Key": "concept-1"}})
	if again.status != http.StatusCreated || again.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay = %d %v", again.status, again.header)
	}
	s.do(call{method: "POST", path: base + "/term-concepts", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"terms": []map[string]any{{"locale": "en", "text": "a"}, {"locale": "en", "text": "A"}},
	}}).want(t, http.StatusBadRequest, "duplicate_term")

	var rec struct {
		AnalyzedText string `json:"analyzed_text"`
		Hits         []struct {
			Text    string `json:"text"`
			Start   int    `json:"start"`
			End     int    `json:"end"`
			Targets []struct {
				Text string `json:"text"`
			} `json:"targets"`
		} `json:"hits"`
	}
	s.do(call{method: "POST", path: base + "/term-recognitions", bearer: tp.token, body: map[string]any{
		"text": "{count, plural, one {# item} other {# items}}", "syntax": "mf1", "locale": "en", "target_locale": "de",
	}}).decode(t, &rec)
	if rec.AnalyzedText != "￼ item\n￼ items" || len(rec.Hits) != 2 || rec.Hits[1].Text != "items" ||
		rec.Hits[1].Start != 9 || rec.Hits[1].End != 14 || len(rec.Hits[0].Targets) != 2 {
		t.Errorf("recognition = %+v", rec)
	}
	var check struct {
		Findings []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
			Side     string `json:"side"`
			Text     string `json:"text"`
		} `json:"findings"`
	}
	s.do(call{method: "POST", path: base + "/terminology-checks", bearer: tp.token, body: map[string]any{
		"source": "One item", "source_locale": "en", "target": "Ein Item", "target_locale": "de",
	}}).decode(t, &check)
	if len(check.Findings) != 2 || check.Findings[1].Code != "term_forbidden" || check.Findings[1].Severity != "error" {
		t.Errorf("check = %+v", check)
	}

	// The project check runs the same QA over every translation, by key.
	s.do(call{method: "POST", path: base + "/term-concepts", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"project_id": project, "terms": []map[string]any{
			{"locale": "en", "text": "pay"}, {"locale": "fr", "text": "régler"}, {"locale": "fr", "text": "payer", "status": "forbidden"},
		},
	}}).want(t, http.StatusCreated, "")
	var findings struct {
		Items []struct {
			MessageKey     string `json:"message_key"`
			Locale         string `json:"locale"`
			SourceRevision int    `json:"source_revision"`
			TargetText     string `json:"target_text"`
			Findings       []struct {
				Code string `json:"code"`
			} `json:"findings"`
		} `json:"items"`
		Checked map[string]int `json:"checked"`
	}
	s.do(call{method: "GET", path: tp.path + "/terminology-findings?locale=fr", bearer: tp.token}).decode(t, &findings)
	if len(findings.Items) != 1 || findings.Items[0].MessageKey != "checkout.pay" || findings.Checked["fr"] != 1 ||
		len(findings.Items[0].Findings) != 2 || findings.Items[0].TargetText != "Payer \uFFFC" ||
		// The revision the findings were computed against, which a waiver
		// on one of them is measured against (RFC 0005 §2.3).
		findings.Items[0].SourceRevision != 1 {
		t.Errorf("project check = %+v", findings)
	}
	s.do(call{method: "GET", path: tp.path + "/terminology-findings?locale=fr&state=published", bearer: tp.token}).
		want(t, http.StatusBadRequest, "invalid_state")
	r = s.do(call{method: "GET", path: base + "/term-concepts/" + created.ID, bearer: tp.token})
	s.do(call{method: "PUT", path: base + "/term-concepts/" + created.ID, cookie: ada.cookie, csrf: ada.csrf,
		body:    map[string]any{"terms": []map[string]any{{"locale": "en", "text": "item"}}},
		headers: map[string]string{"If-Match": `"9"`}}).want(t, http.StatusPreconditionFailed, "precondition_failed")
	s.do(call{method: "PUT", path: base + "/term-concepts/" + created.ID, cookie: ada.cookie, csrf: ada.csrf,
		body:    map[string]any{"terms": []map[string]any{{"locale": "en", "text": "item"}}},
		headers: map[string]string{"If-Match": r.header.Get("ETag")}}).want(t, http.StatusOK, "")
	var revs struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: base + "/term-concepts/" + created.ID + "/revisions", bearer: tp.token}).decode(t, &revs)
	if len(revs.Items) != 2 || revs.Items[0].Action != "updated" {
		t.Errorf("revisions = %+v", revs)
	}

	// Style guides.
	s.do(call{method: "POST", path: base + "/style-guides", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"locale": "de", "fields": map[string]any{"formality": map[string]any{"register": "formal", "pronoun": "Sie"}},
		"rules": []map[string]any{{"id": "no-exclamation", "title": "No exclamation marks", "bad": []string{"Gespeichert!"}}},
	}}).want(t, http.StatusCreated, "")
	s.do(call{method: "POST", path: base + "/style-guides", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"locale": "de",
	}}).want(t, http.StatusConflict, "style_guide_exists")
	s.do(call{method: "POST", path: base + "/style-guides", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"project_id": project, "locale": "de-AT", "fields": map[string]any{"formality": map[string]any{"pronoun": "du"}, "tone": []string{"warm"}},
	}}).want(t, http.StatusCreated, "")
	s.do(call{method: "POST", path: base + "/style-guides", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{
		"namespace": "legal",
	}}).want(t, http.StatusBadRequest, "namespace_needs_project")
	var eff struct {
		Fields struct {
			Formality struct {
				Register string `json:"register"`
				Pronoun  string `json:"pronoun"`
			} `json:"formality"`
			Tone []string `json:"tone"`
		} `json:"fields"`
		Rules   []struct{ ID string } `json:"rules"`
		Sources []struct {
			Version int `json:"version"`
		} `json:"sources"`
	}
	s.do(call{method: "GET", path: base + "/effective-style-guide?project=" + project + "&locale=de-AT", bearer: tp.token}).decode(t, &eff)
	if eff.Fields.Formality.Register != "formal" || eff.Fields.Formality.Pronoun != "du" || len(eff.Rules) != 1 ||
		len(eff.Sources) != 2 || len(eff.Fields.Tone) != 1 {
		t.Errorf("effective = %+v", eff)
	}
}
