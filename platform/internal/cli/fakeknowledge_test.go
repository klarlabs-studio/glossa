package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// The fake server's Knowledge and Intelligence endpoints: translation
// memory, the termbase with a word-based terminology check, style
// guides, AI settings, fills whose jobs advance one state per read, and
// the review queue, with the contract's shapes and problem codes.

type fakeUnit struct {
	id, src, tgt, srcLocale, tgtLocale, key string
	project                                 *string
	retired                                 bool
	hits                                    int
}

type fakeTerm struct {
	id, locale, text, status string
}

type fakeConcept struct {
	id, definition, domain, note string
	project                      *string
	version                      int
	terms                        []fakeTerm
}

type fakeGuide struct {
	id, name                   string
	project, locale, namespace *string
	version                    int
	fields                     map[string]any
	rules                      []any
}

type fakeJob struct {
	id, fill, key, locale, state, failure string
	suggestion                            string
}

type fakeSuggestion struct {
	id, job, key, locale, text, status string
	score                              float64
	version                            int
	revision                           *int
}

type fakeKnowledge struct {
	seq         int
	units       []*fakeUnit
	concepts    []*fakeConcept
	guides      []*fakeGuide
	consent     bool
	budget      int64
	providers   []map[string]any
	nsTags      map[string][]string
	fills       map[string][]*fakeJob
	jobs        []*fakeJob
	suggestions []*fakeSuggestion
	// failKeys are keys whose jobs fail (invalid_output).
	failKeys map[string]bool
	// termChecks counts terminology-check requests, termPages
	// terminology-findings pages, previews fill previews.
	termChecks int
	termPages  int
	previews   int
	// fillBodies are the fill requests, in order.
	fillBodies []fakeFillBody
}

func newFakeKnowledge() *fakeKnowledge {
	return &fakeKnowledge{budget: 5_000_000, nsTags: map[string][]string{}, fills: map[string][]*fakeJob{}, failKeys: map[string]bool{}}
}

func (k *fakeKnowledge) nextID() string {
	k.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", k.seq)
}

func (f *fakeServer) routeKnowledge(mux *http.ServeMux) {
	t := "/v1/tenants/ten_1"
	p := t + "/projects/prj_1"
	mux.HandleFunc("POST "+t+"/tm-lookups", f.tmLookup)
	mux.HandleFunc("GET "+t+"/tm-concordance", f.tmConcordance)
	mux.HandleFunc("GET "+t+"/tm-units", f.tmUnits)
	mux.HandleFunc("GET "+t+"/tm-units/{id}", f.tmUnit)
	mux.HandleFunc("DELETE "+t+"/tm-units/{id}", f.tmRetire)
	mux.HandleFunc("GET "+t+"/term-concepts", f.listConcepts)
	mux.HandleFunc("POST "+t+"/term-concepts", f.createConcept)
	mux.HandleFunc("GET "+t+"/term-concepts/{id}", f.getConcept)
	mux.HandleFunc("PUT "+t+"/term-concepts/{id}", f.replaceConcept)
	mux.HandleFunc("POST "+t+"/terminology-checks", f.checkTerminology)
	mux.HandleFunc("GET "+p+"/terminology-findings", f.terminologyFindings)
	mux.HandleFunc("GET "+t+"/effective-style-guide", f.effectiveStyle)
	mux.HandleFunc("GET "+t+"/style-guides", f.listGuides)
	mux.HandleFunc("POST "+t+"/style-guides", f.createGuide)
	mux.HandleFunc("GET "+t+"/style-guides/{id}", f.getGuide)
	mux.HandleFunc("PUT "+t+"/style-guides/{id}", f.replaceGuide)
	mux.HandleFunc("GET "+t+"/ai-settings", f.aiSettings)
	mux.HandleFunc("GET "+t+"/ai-budget", f.aiBudget)
	mux.HandleFunc("GET "+t+"/ai-providers", f.aiProviders)
	mux.HandleFunc("GET "+p+"/ai-settings", f.projectAISettings)
	mux.HandleFunc("POST "+p+"/ai-fills", f.createFill)
	mux.HandleFunc("POST "+p+"/ai-fill-previews", f.previewFill)
	mux.HandleFunc("GET "+t+"/ai-fills/{id}", f.getFill)
	mux.HandleFunc("GET "+t+"/ai-jobs", f.listJobs)
	mux.HandleFunc("GET "+p+"/ai-review-queue", f.reviewQueue)
	mux.HandleFunc("GET "+t+"/ai-suggestions/{id}", f.getSuggestion)
	mux.HandleFunc("POST "+t+"/ai-suggestions/{id}/acceptance", f.acceptSuggestion)
	mux.HandleFunc("POST "+t+"/ai-suggestions/{id}/rejection", f.rejectSuggestion)
}

const fakeTime = "2026-09-19T10:00:00Z"

func decodeBody(r *http.Request, v any) { _ = json.NewDecoder(r.Body).Decode(v) }

// ── translation memory ──────────────────────────────────────────────

func (f *fakeServer) addUnit(src, tgt, srcLocale, tgtLocale, key string) *fakeUnit {
	f.mu.Lock()
	defer f.mu.Unlock()
	project := "prj_1"
	u := &fakeUnit{id: f.kn.nextID(), src: src, tgt: tgt, srcLocale: srcLocale, tgtLocale: tgtLocale, key: key, project: &project}
	f.kn.units = append(f.kn.units, u)
	return u
}

func unitJSON(u *fakeUnit) map[string]any {
	out := map[string]any{"id": u.id, "source_locale": u.srcLocale, "target_locale": u.tgtLocale, "source": u.src, "target": u.tgt,
		"source_normalized": u.src, "signature": "", "target_model": map[string]any{}, "origin": "translation", "state": "active",
		"hit_count": u.hits, "created_at": fakeTime, "created_by": "token:1", "updated_at": fakeTime}
	if u.project != nil {
		out["project_id"] = *u.project
	}
	if u.key != "" {
		out["message_key"], out["namespace"] = u.key, "default"
	}
	if u.retired {
		out["state"], out["retired_at"], out["retired_reason"] = "retired", fakeTime, "deleted"
	}
	return out
}

func (f *fakeServer) tmLookup(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Source       string `json:"source"`
		SourceLocale string `json:"source_locale"`
		TargetLocale string `json:"target_locale"`
		Syntax       string `json:"syntax"`
		Limit        int    `json:"limit"`
	}
	decodeBody(r, &q)
	if q.TargetLocale == "fr-CA" {
		problemResp(w, 400, "invalid_locale", "fr-CA is not supported")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	matches := []map[string]any{}
	for _, u := range f.kn.units {
		if u.retired || u.srcLocale != q.SourceLocale || u.tgtLocale != q.TargetLocale {
			continue
		}
		switch {
		case u.src == q.Source:
			matches = append(matches, map[string]any{"score": 100, "kind": "exact", "target": u.tgt, "target_model": map[string]any{},
				"target_text": u.tgt, "target_syntax": orDefault(q.Syntax, "mf1"), "target_syntax_fallback": false,
				"variables_adapted": false, "unit": unitJSON(u)})
		case strings.Contains(strings.ToLower(u.src), strings.ToLower(q.Source)):
			matches = append(matches, map[string]any{"score": 75, "kind": "fuzzy", "target": u.tgt, "target_model": map[string]any{},
				"variables_adapted": false, "unit": unitJSON(u)})
		}
	}
	writeJSONResp(w, 200, map[string]any{"source_normalized": q.Source, "matches": matches})
}

func (f *fakeServer) tmConcordance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	matches := []map[string]any{}
	for _, u := range f.kn.units {
		text := u.src
		if q.Get("side") == "target" {
			text = u.tgt
		}
		if !u.retired && strings.Contains(strings.ToLower(text), strings.ToLower(q.Get("q"))) {
			matches = append(matches, map[string]any{"similarity": 0.5, "unit": unitJSON(u)})
		}
	}
	writeJSONResp(w, 200, map[string]any{"matches": matches})
}

func (f *fakeServer) tmUnits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, u := range f.kn.units {
		state := orDefault(q.Get("state"), "active")
		if (state == "active" && u.retired) || (state == "retired" && !u.retired) ||
			(q.Get("source_locale") != "" && q.Get("source_locale") != u.srcLocale) ||
			(q.Get("target_locale") != "" && q.Get("target_locale") != u.tgtLocale) {
			continue
		}
		items = append(items, unitJSON(u))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) findUnit(w http.ResponseWriter, r *http.Request) *fakeUnit {
	for _, u := range f.kn.units {
		if u.id == r.PathValue("id") {
			return u
		}
	}
	problemResp(w, 404, "not_found", "no such unit")
	return nil
}

func (f *fakeServer) tmUnit(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.findUnit(w, r); u != nil {
		writeJSONResp(w, 200, unitJSON(u))
	}
}

func (f *fakeServer) tmRetire(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.findUnit(w, r); u != nil {
		u.retired = true
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── terminology ─────────────────────────────────────────────────────

func conceptJSONResp(c *fakeConcept) map[string]any {
	terms := []map[string]any{}
	for _, t := range c.terms {
		terms = append(terms, map[string]any{"id": t.id, "locale": t.locale, "text": t.text, "status": t.status, "case_sensitive": false})
	}
	out := map[string]any{"id": c.id, "definition": c.definition, "domain": c.domain, "note": c.note, "product_ref": "",
		"version": c.version, "terms": terms, "created_at": fakeTime, "created_by": "token:1", "updated_at": fakeTime, "updated_by": "token:1"}
	if c.project != nil {
		out["project_id"] = *c.project
	}
	return out
}

func (f *fakeServer) listConcepts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, c := range f.kn.concepts {
		match := q.Get("q") == ""
		for _, t := range c.terms {
			if q.Get("q") != "" && strings.Contains(strings.ToLower(t.text), strings.ToLower(q.Get("q"))) &&
				(q.Get("locale") == "" || q.Get("locale") == t.locale) {
				match = true
			}
		}
		if match && (q.Get("domain") == "" || q.Get("domain") == c.domain) {
			items = append(items, conceptJSONResp(c))
		}
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

type termInput struct {
	Locale string  `json:"locale"`
	Text   string  `json:"text"`
	Status *string `json:"status"`
}

type conceptBody struct {
	ProjectID  *string     `json:"project_id"`
	Definition *string     `json:"definition"`
	Domain     *string     `json:"domain"`
	Note       *string     `json:"note"`
	Terms      []termInput `json:"terms"`
}

// applyConcept sets c from body; terms that stay keep their IDs.
func (f *fakeServer) applyConcept(w http.ResponseWriter, c *fakeConcept, b conceptBody) bool {
	if len(b.Terms) == 0 {
		problemResp(w, 400, "invalid_concept", "a concept needs at least one term")
		return false
	}
	var terms []fakeTerm
	for _, in := range b.Terms {
		status := "preferred"
		if in.Status != nil {
			status = *in.Status
		}
		if !slices.Contains([]string{"preferred", "admitted", "deprecated", "forbidden"}, status) {
			problemResp(w, 400, "invalid_term_status", "no status "+status)
			return false
		}
		id := f.kn.nextID()
		for _, t := range c.terms {
			if t.locale == in.Locale && t.text == in.Text {
				id = t.id
			}
		}
		for _, t := range terms {
			if t.locale == in.Locale && strings.EqualFold(t.text, in.Text) {
				problemResp(w, 400, "duplicate_term", in.Text+" twice")
				return false
			}
		}
		terms = append(terms, fakeTerm{id: id, locale: in.Locale, text: in.Text, status: status})
	}
	next := fakeConcept{id: c.id, project: c.project, definition: derefStr(b.Definition), domain: derefStr(b.Domain),
		note: derefStr(b.Note), terms: terms, version: c.version}
	if fmt.Sprint(next.terms, next.definition, next.domain, next.note) != fmt.Sprint(c.terms, c.definition, c.domain, c.note) {
		next.version++
	}
	*c = next
	return true
}

func (f *fakeServer) createConcept(w http.ResponseWriter, r *http.Request) {
	var b conceptBody
	decodeBody(r, &b)
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &fakeConcept{id: f.kn.nextID(), project: b.ProjectID}
	if !f.applyConcept(w, c, b) {
		return
	}
	f.kn.concepts = append(f.kn.concepts, c)
	writeJSONResp(w, 201, conceptJSONResp(c))
}

func (f *fakeServer) findConcept(w http.ResponseWriter, r *http.Request) *fakeConcept {
	for _, c := range f.kn.concepts {
		if c.id == r.PathValue("id") {
			return c
		}
	}
	problemResp(w, 404, "not_found", "no such concept")
	return nil
}

func (f *fakeServer) getConcept(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.findConcept(w, r); c != nil {
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, c.version))
		writeJSONResp(w, 200, conceptJSONResp(c))
	}
}

func (f *fakeServer) replaceConcept(w http.ResponseWriter, r *http.Request) {
	var b conceptBody
	decodeBody(r, &b)
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.findConcept(w, r)
	if c == nil {
		return
	}
	if r.Header.Get("If-Match") != fmt.Sprintf(`"%d"`, c.version) {
		problemResp(w, 412, "precondition_failed", "the concept changed")
		return
	}
	if f.applyConcept(w, c, b) {
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, c.version))
		writeJSONResp(w, 200, conceptJSONResp(c))
	}
}

// words splits a message's visible text into lowercase words.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'à' && r <= 'ÿ') && r != 'ß'
	})
}

// mentions reports whether text has a word starting with term (the
// server tolerates short inflections).
// termSpan is where term occurs in text, in bytes. mentions() found it
// by word prefix, so the span is the term's own length from there.
func termSpan(text, term string) (int, int) {
	i := strings.Index(strings.ToLower(text), strings.ToLower(term))
	if i < 0 {
		return 0, min(len(term), len(text))
	}
	return i, i + len(term)
}

func mentions(text, term string) bool {
	for _, w := range words(text) {
		if strings.HasPrefix(w, strings.ToLower(term)) && len(w)-len(term) <= 2 {
			return true
		}
	}
	return false
}

func (f *fakeServer) checkTerminology(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Source       string `json:"source"`
		Target       string `json:"target"`
		SourceLocale string `json:"source_locale"`
		TargetLocale string `json:"target_locale"`
	}
	decodeBody(r, &q)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kn.termChecks++
	writeJSONResp(w, 200, map[string]any{"findings": f.termFindings(q.Source, q.SourceLocale, q.Target, q.TargetLocale),
		"source_text": q.Source, "target_text": q.Target})
}

// terminologyFindings checks the project's translations in the locales
// a page (page_size translations, default 100) at a time, by key and
// locale; the page token is the index to go on from.
func (f *fakeServer) terminologyFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kn.termPages++
	states := q["state"]
	if len(states) == 0 {
		states = []string{"draft", "needs_review", "approved"}
	}
	type pair struct{ key, locale string }
	var pairs []pair
	keys := make([]string, 0, len(f.messages))
	for k := range f.messages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	locales := slices.Clone(q["locale"])
	sort.Strings(locales)
	for _, k := range keys {
		if f.messages[k].state != "active" {
			continue
		}
		for _, l := range locales {
			if t := f.translations[l][k]; t != nil && slices.Contains(states, t.state) {
				pairs = append(pairs, pair{k, l})
			}
		}
	}
	size, from := 100, 0
	fmt.Sscan(q.Get("page_size"), &size)
	fmt.Sscan(q.Get("page_token"), &from)
	to := min(from+size, len(pairs))
	checked := map[string]int{}
	for _, l := range q["locale"] {
		checked[l] = 0
	}
	items := []map[string]any{}
	for _, pr := range pairs[from:to] {
		checked[pr.locale]++
		src, tgt := f.messages[pr.key].content.Text, f.translations[pr.locale][pr.key].content.Text
		if fs := f.termFindings(src, f.sourceLocale, tgt, pr.locale); len(fs) > 0 {
			items = append(items, map[string]any{"message_id": "msg_" + pr.key, "message_key": pr.key, "namespace": f.messages[pr.key].ns(),
				"locale": pr.locale, "state": f.translations[pr.locale][pr.key].state, "source_revision": f.messages[pr.key].revision, "source_text": src, "target_text": tgt, "findings": fs})
		}
	}
	page := map[string]any{"items": items, "checked": checked}
	if to < len(pairs) {
		page["next_page_token"] = fmt.Sprint(to)
	}
	writeJSONResp(w, 200, page)
}

// termFindings is the fake's word-based terminology QA of one
// translation.
func (f *fakeServer) termFindings(source, sourceLocale, target, targetLocale string) []map[string]any {
	q := struct{ Source, Target, SourceLocale, TargetLocale string }{source, target, sourceLocale, targetLocale}
	findings := []map[string]any{}
	for _, c := range f.kn.concepts {
		var suggestions []string
		recognized, used := false, false
		for _, t := range c.terms {
			if t.locale == q.SourceLocale && (t.status == "preferred" || t.status == "admitted") && mentions(q.Source, t.text) {
				recognized = true
			}
			if t.locale != q.TargetLocale {
				continue
			}
			switch t.status {
			case "preferred", "admitted":
				suggestions = append(suggestions, t.text)
				used = used || mentions(q.Target, t.text)
			}
		}
		for _, t := range c.terms {
			if t.locale == q.TargetLocale && (t.status == "forbidden" || t.status == "deprecated") && mentions(q.Target, t.text) {
				sev := "error"
				if t.status == "deprecated" {
					sev = "warning"
				}
				// The offsets are where the term actually is, as the real
				// termbase reports them: a span that pointed at the start
				// of every string would make anything reading one — the
				// overlay's underline, `glossa check --fix` — right only
				// by accident.
				start, end := termSpan(q.Target, t.text)
				findings = append(findings, map[string]any{"code": "term_forbidden", "severity": sev, "side": "target", "text": q.Target[start:end],
					"start": start, "end": end, "suggestions": suggestions, "concept_id": c.id, "term_id": t.id,
					"message": fmt.Sprintf("%q is %s", t.text, t.status)})
			}
		}
		if recognized && !used && len(suggestions) > 0 {
			findings = append(findings, map[string]any{"code": "term_missing", "severity": "warning", "side": "source", "text": c.terms[0].text,
				"start": 0, "end": 1, "suggestions": suggestions, "concept_id": c.id, "term_id": c.terms[0].id,
				"message": "the translation uses none of the concept's terms"})
		}
	}
	return findings
}

// ── style guides ────────────────────────────────────────────────────

func guideJSON(g *fakeGuide) map[string]any {
	out := map[string]any{"id": g.id, "name": g.name, "version": g.version, "fields": g.fields, "rules": g.rules,
		"created_at": fakeTime, "created_by": "token:1", "updated_at": fakeTime, "updated_by": "token:1"}
	for k, v := range map[string]*string{"project_id": g.project, "locale": g.locale, "namespace": g.namespace} {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func eqPtr(a *string, b string) bool { return derefStr(a) == b }

func (f *fakeServer) listGuides(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, g := range f.kn.guides {
		if (q.Get("tenant_only") == "true" && g.project != nil) || (q.Get("project") != "" && !eqPtr(g.project, q.Get("project"))) ||
			(q.Get("locale") != "" && !eqPtr(g.locale, q.Get("locale"))) {
			continue
		}
		items = append(items, guideJSON(g))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

type guideBody struct {
	Name      *string        `json:"name"`
	ProjectID *string        `json:"project_id"`
	Locale    *string        `json:"locale"`
	Namespace *string        `json:"namespace"`
	Fields    map[string]any `json:"fields"`
	Rules     []any          `json:"rules"`
}

func (f *fakeServer) createGuide(w http.ResponseWriter, r *http.Request) {
	var b guideBody
	decodeBody(r, &b)
	f.mu.Lock()
	defer f.mu.Unlock()
	if b.Namespace != nil && b.ProjectID == nil {
		problemResp(w, 400, "namespace_needs_project", "a namespace guide needs a project")
		return
	}
	g := &fakeGuide{id: f.kn.nextID(), name: derefStr(b.Name), project: b.ProjectID, locale: b.Locale, namespace: b.Namespace,
		version: 1, fields: b.Fields, rules: b.Rules}
	if g.fields == nil {
		g.fields = map[string]any{}
	}
	if g.rules == nil {
		g.rules = []any{}
	}
	f.kn.guides = append(f.kn.guides, g)
	writeJSONResp(w, 201, guideJSON(g))
}

func (f *fakeServer) findGuide(w http.ResponseWriter, r *http.Request) *fakeGuide {
	for _, g := range f.kn.guides {
		if g.id == r.PathValue("id") {
			return g
		}
	}
	problemResp(w, 404, "not_found", "no such guide")
	return nil
}

func (f *fakeServer) getGuide(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g := f.findGuide(w, r); g != nil {
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, g.version))
		writeJSONResp(w, 200, guideJSON(g))
	}
}

func (f *fakeServer) replaceGuide(w http.ResponseWriter, r *http.Request) {
	var b guideBody
	decodeBody(r, &b)
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.findGuide(w, r)
	if g == nil {
		return
	}
	if r.Header.Get("If-Match") != fmt.Sprintf(`"%d"`, g.version) {
		problemResp(w, 412, "precondition_failed", "the guide changed")
		return
	}
	before, _ := json.Marshal([]any{g.name, g.fields, g.rules})
	if b.Name != nil {
		g.name = *b.Name
	}
	if b.Fields != nil {
		g.fields = b.Fields
	}
	if b.Rules != nil {
		g.rules = b.Rules
	}
	if after, _ := json.Marshal([]any{g.name, g.fields, g.rules}); string(after) != string(before) {
		g.version++
	}
	writeJSONResp(w, 200, guideJSON(g))
}

// effectiveStyle merges the applicable guides' top-level fields,
// broadest first.
func (f *fakeServer) effectiveStyle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	var applicable []*fakeGuide
	for _, g := range f.kn.guides {
		if (g.project != nil && !eqPtr(g.project, q.Get("project"))) || (g.locale != nil && !strings.HasPrefix(q.Get("locale"), *g.locale)) ||
			(g.namespace != nil && !eqPtr(g.namespace, q.Get("namespace"))) {
			continue
		}
		applicable = append(applicable, g)
	}
	rank := func(g *fakeGuide) int {
		n := 0
		for _, p := range []*string{g.project, g.locale, g.namespace} {
			if p != nil {
				n++
			}
		}
		return n
	}
	sort.SliceStable(applicable, func(i, j int) bool { return rank(applicable[i]) < rank(applicable[j]) })
	fields, rules, sources := map[string]any{}, []any{}, []map[string]any{}
	for _, g := range applicable {
		for k, v := range g.fields {
			fields[k] = v
		}
		rules = append(rules, g.rules...)
		src := map[string]any{"style_guide_id": g.id, "version": g.version}
		for k, v := range map[string]*string{"project_id": g.project, "locale": g.locale, "namespace": g.namespace} {
			if v != nil {
				src[k] = *v
			}
		}
		sources = append(sources, src)
	}
	writeJSONResp(w, 200, map[string]any{"fields": fields, "rules": rules, "sources": sources})
}

// ── AI settings ─────────────────────────────────────────────────────

func (f *fakeServer) aiSettings(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSONResp(w, 200, map[string]any{"provider_consent": f.kn.consent, "max_concurrent_jobs": 4,
		"monthly_budget_micro_usd": f.kn.budget, "version": 1})
}

func (f *fakeServer) aiBudget(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSONResp(w, 200, map[string]any{"monthly_budget_micro_usd": f.kn.budget, "spent_micro_usd": 1_250_000,
		"remaining_micro_usd": f.kn.budget - 1_250_000, "calls": 3, "month_start": "2026-09-01T00:00:00Z",
		"by_provider": []map[string]any{{"provider": "anthropic", "model": "claude-sonnet-5", "calls": 3, "cost_micro_usd": 1_250_000,
			"input_tokens": 3000, "output_tokens": 90}}})
}

func (f *fakeServer) aiProviders(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	items = append(items, f.kn.providers...)
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) addProvider() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kn.providers = append(f.kn.providers, map[string]any{"id": f.kn.nextID(), "name": "anthropic", "kind": "anthropic",
		"enabled": true, "api_key_set": true, "models": []string{}, "version": 1,
		"created_at": fakeTime, "created_by": "user:ada", "updated_at": fakeTime, "updated_by": "user:ada"})
}

func (f *fakeServer) projectAISettings(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSONResp(w, 200, map[string]any{"auto_translate_locales": []string{"de"}, "namespace_tags": f.kn.nsTags,
		"review": map[string]any{"auto_approve": false, "auto_approve_min": 0.9, "recommend_min": 0.75}, "version": 0})
}

// ── AI fills, jobs and suggestions ──────────────────────────────────

type fakeFillBody struct {
	Locales         []string  `json:"locales"`
	Keys            *[]string `json:"keys"`
	KeyPrefix       *string   `json:"key_prefix"`
	IncludeOutdated bool      `json:"include_outdated"`
	Select          string    `json:"select"`
}

// selection is a fill's messages per locale, in key order: those whose
// translation is in the selected state (missing by default; listed keys
// missing or outdated), sensitive namespaces counted apart. ok is false
// (a 404 was written) for a locale the project lacks.
func (f *fakeServer) selection(w http.ResponseWriter, b fakeFillBody) (keys map[string][]string, sensitive map[string]int, ok bool) {
	sel := b.Select
	switch {
	case sel != "":
	case b.IncludeOutdated || b.Keys != nil:
		sel = "missing_or_outdated"
	default:
		sel = "missing"
	}
	keys, sensitive = map[string][]string{}, map[string]int{}
	for _, l := range b.Locales {
		if !f.hasLocale(l) {
			problemResp(w, 404, "locale_not_found", "no locale "+l)
			return nil, nil, false
		}
		all := make([]string, 0, len(f.messages))
		for k := range f.messages {
			all = append(all, k)
		}
		sort.Strings(all)
		keys[l] = []string{}
		for _, k := range all {
			m := f.messages[k]
			t := f.translations[l][k]
			outdated := t != nil && t.sourceRevision < m.revision
			switch {
			case b.Keys != nil && !slices.Contains(*b.Keys, k),
				b.KeyPrefix != nil && !strings.HasPrefix(k, *b.KeyPrefix),
				t != nil && !outdated,
				t == nil && sel == "outdated",
				outdated && sel == "missing":
				continue
			}
			if slices.Contains(f.kn.nsTags["default"], "sensitive") {
				sensitive[l]++
				continue
			}
			keys[l] = append(keys[l], k)
		}
	}
	return keys, sensitive, true
}

// createFill queues a job per selected message in each locale,
// skipping sensitive namespaces.
func (f *fakeServer) createFill(w http.ResponseWriter, r *http.Request) {
	var b fakeFillBody
	decodeBody(r, &b)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kn.fillBodies = append(f.kn.fillBodies, b)
	keys, sensitive, ok := f.selection(w, b)
	if !ok {
		return
	}
	id := f.kn.nextID()
	skipped := map[string]int{}
	created := 0
	for _, l := range b.Locales {
		skipped["sensitive"] += sensitive[l]
		for _, k := range keys[l] {
			j := &fakeJob{id: f.kn.nextID(), fill: id, key: k, locale: l, state: "queued"}
			f.kn.jobs = append(f.kn.jobs, j)
			f.kn.fills[id] = append(f.kn.fills[id], j)
			created++
		}
	}
	if skipped["sensitive"] == 0 {
		delete(skipped, "sensitive")
	}
	w.Header().Set("Location", "/v1/tenants/ten_1/ai-fills/"+id)
	writeJSONResp(w, 201, f.fillJSON(id, b.Locales, created, skipped))
}

// previewFill answers what createFill would do, queueing nothing: an
// exact TM unit covers a message; the rest is refused (consent,
// provider, budget) or costs 1000 µ$ (at most 50000).
func (f *fakeServer) previewFill(w http.ResponseWriter, r *http.Request) {
	var b fakeFillBody
	decodeBody(r, &b)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kn.previews++
	keys, sensitive, ok := f.selection(w, b)
	if !ok {
		return
	}
	sel := "missing"
	if b.Select != "" {
		sel = b.Select
	}
	var estimated, most int64
	locales := []map[string]any{}
	for _, l := range b.Locales {
		refused := map[string]int{}
		if sensitive[l] > 0 {
			refused["sensitive"] = sensitive[l]
		}
		tm, provider := 0, 0
		var le, lm int64
		for _, k := range keys[l] {
			switch {
			case slices.ContainsFunc(f.kn.units, func(u *fakeUnit) bool {
				return !u.retired && u.tgtLocale == l && u.src == f.messages[k].content.Text
			}):
				tm++
			case !f.kn.consent:
				refused["provider_consent"]++
			case len(f.kn.providers) == 0:
				refused["no_route"]++
			case f.kn.budget == 0:
				refused["budget_exceeded"]++
			default:
				provider++
				le, lm = le+1000, lm+50000
			}
		}
		estimated, most = estimated+le, most+lm
		locales = append(locales, map[string]any{"locale": l, "keys": keys[l], "existing": 0, "tm_exact": tm, "provider": provider,
			"refused": refused, "skipped": map[string]int{}, "cost": map[string]any{"estimated_micro_usd": le, "max_micro_usd": lm, "unpriced": false}})
	}
	writeJSONResp(w, 200, map[string]any{"project_id": "prj_1", "select": sel, "locales": locales, "warnings": f.warnings(),
		"cost": map[string]any{"estimated_micro_usd": estimated, "max_micro_usd": most, "unpriced": false}})
}

func (f *fakeServer) warnings() []string {
	ws := []string{}
	if !f.kn.consent {
		ws = append(ws, "provider_consent_off")
	}
	if f.kn.budget == 0 {
		ws = append(ws, "no_budget")
	}
	if len(f.kn.providers) == 0 {
		ws = append(ws, "no_provider")
	}
	return ws
}

func (f *fakeServer) fillJSON(id string, locales []string, created int, skipped map[string]int) map[string]any {
	states := map[string]int{}
	for _, j := range f.kn.fills[id] {
		states[j.state]++
	}
	return map[string]any{"id": id, "project_id": "prj_1", "trigger": "fill", "locales": locales, "jobs_created": created,
		"jobs_existing": 0, "skipped": skipped, "job_states": states, "warnings": f.warnings(), "requested_by": "token:1", "created_at": fakeTime}
}

// advance moves each job of a fill one state on: queued → running →
// succeeded (with a suggestion) or failed.
func (f *fakeServer) advance(jobs []*fakeJob) {
	for _, j := range jobs {
		switch j.state {
		case "queued":
			j.state = "running"
		case "running":
			switch {
			case !f.kn.consent:
				j.state, j.failure = "failed", "provider_consent"
			case f.kn.failKeys[j.key]:
				j.state, j.failure = "failed", "invalid_output"
			default:
				j.state = "succeeded"
				s := &fakeSuggestion{id: f.kn.nextID(), job: j.id, key: j.key, locale: j.locale, status: "pending", version: 1,
					text: "AI " + j.key, score: 0.5 + float64(len(f.kn.suggestions))/10}
				j.suggestion = s.id
				f.kn.suggestions = append(f.kn.suggestions, s)
			}
		}
	}
}

func (f *fakeServer) getFill(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	jobs, ok := f.kn.fills[r.PathValue("id")]
	if !ok {
		problemResp(w, 404, "not_found", "no such fill")
		return
	}
	f.advance(jobs)
	locales := []string{}
	for _, j := range jobs {
		if !slices.Contains(locales, j.locale) {
			locales = append(locales, j.locale)
		}
	}
	writeJSONResp(w, 200, f.fillJSON(r.PathValue("id"), locales, len(jobs), map[string]int{}))
}

func (f *fakeServer) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, j := range f.kn.jobs {
		if (q.Get("fill") != "" && j.fill != q.Get("fill")) || (q.Get("state") != "" && j.state != q.Get("state")) {
			continue
		}
		item := map[string]any{"id": j.id, "fill_id": j.fill, "message_id": "msg_" + j.key, "message_key": j.key, "namespace": "default",
			"locale": j.locale, "state": j.state, "project_id": "prj_1", "source_revision": 1, "knowledge_fingerprint": "kf", "trigger": "fill",
			"attempts": 1, "max_attempts": 3, "available_at": fakeTime, "created_at": fakeTime, "created_by": "token:1", "updated_at": fakeTime}
		if j.failure != "" {
			item["failure_code"], item["last_error"] = j.failure, j.failure+": the job failed"
		}
		items = append(items, item)
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func suggestionJSONResp(s *fakeSuggestion) map[string]any {
	out := map[string]any{"id": s.id, "job_id": s.job, "project_id": "prj_1", "message_id": "msg_" + s.key, "message_key": s.key,
		"namespace": "default", "locale": s.locale, "message": s.text, "model": map[string]any{}, "source_revision": 1,
		"status": s.status, "score": s.score, "action": "review_required", "risk_tags": []string{"marketing"},
		"explanation": []map[string]any{{"factor": "tm_match", "value": 0, "contribution": -0.2, "reason": "no translation-memory match"}},
		"findings":    []any{}, "term_findings": []any{}, "calls": []any{}, "cost_micro_usd": 1200,
		"usage":      map[string]any{"input_tokens": 900, "output_tokens": 20},
		"provenance": map[string]any{"origin": "ai", "provider": "anthropic", "model": "claude-sonnet-5", "repairs": 0},
		"created_at": fakeTime, "version": s.version}
	if s.revision != nil {
		out["translation_revision"] = *s.revision
	}
	return out
}

func (f *fakeServer) reviewQueue(w http.ResponseWriter, r *http.Request) {
	locales := r.URL.Query()["locale"]
	f.mu.Lock()
	defer f.mu.Unlock()
	var pending []*fakeSuggestion
	for _, s := range f.kn.suggestions {
		if s.status == "pending" && (len(locales) == 0 || slices.Contains(locales, s.locale)) {
			pending = append(pending, s)
		}
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].score < pending[j].score })
	items := []map[string]any{}
	for _, s := range pending {
		items = append(items, suggestionJSONResp(s))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) findSuggestion(w http.ResponseWriter, r *http.Request) *fakeSuggestion {
	for _, s := range f.kn.suggestions {
		if s.id == r.PathValue("id") {
			return s
		}
	}
	problemResp(w, 404, "not_found", "no such suggestion")
	return nil
}

func (f *fakeServer) getSuggestion(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.findSuggestion(w, r); s != nil {
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, s.version))
		writeJSONResp(w, 200, suggestionJSONResp(s))
	}
}

// decide checks the precondition and that the suggestion is pending.
func (f *fakeServer) decide(w http.ResponseWriter, r *http.Request) *fakeSuggestion {
	s := f.findSuggestion(w, r)
	if s == nil {
		return nil
	}
	if m := r.Header.Get("If-Match"); m != "" && m != fmt.Sprintf(`"%d"`, s.version) {
		problemResp(w, 412, "precondition_failed", "the suggestion changed")
		return nil
	}
	if s.status != "pending" {
		problemResp(w, 409, "suggestion_decided", "the suggestion was already "+s.status)
		return nil
	}
	return s
}

func (f *fakeServer) acceptSuggestion(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Text   *string `json:"text"`
		Syntax *string `json:"syntax"`
	}
	decodeBody(r, &b)
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.decide(w, r)
	if s == nil {
		return
	}
	if b.Text != nil {
		if strings.Contains(*b.Text, "{") && !strings.Contains(*b.Text, "}") {
			problemResp(w, 400, "invalid_message", "unclosed placeholder")
			return
		}
		s.text = *b.Text + " [" + derefStr(b.Syntax) + "]"
	}
	rev := 1
	s.status, s.revision, s.version = "accepted", &rev, s.version+1
	writeJSONResp(w, 200, suggestionJSONResp(s))
}

func (f *fakeServer) rejectSuggestion(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.decide(w, r); s != nil {
		s.status, s.version = "rejected", s.version+1
		writeJSONResp(w, 200, suggestionJSONResp(s))
	}
}
