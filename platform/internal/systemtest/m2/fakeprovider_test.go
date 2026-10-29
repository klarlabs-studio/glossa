//go:build system

package m2_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m2/fixture"
)

// The fake provider: an OpenAI-compatible /chat/completions endpoint on
// loopback that answers only from the fixture's script. Drafts come from
// the reference translations, except for the labelled slips, whose
// scripted drafts carry the mistake (and, for repairs, the fix). The
// self-assessment answers what the fixture says about the draft — a
// fluent, on-style 0.93 by default; the formality slips are the ones
// the "reviewer model" notices. Anything unscripted is refused with a
// 400, so an unexpected request fails its job instead of being answered.
const (
	fakeProviderName = "fake-llm"
	fakeAPIKey       = "sk-m2-fake"
	translateModel   = "m2-translate"
	assessModel      = "m2-assess"
)

var (
	translateHeader = regexp.MustCompile(`(?m)^Translate this message from (\S+) to (\S+)\.$`)
	keyLine         = regexp.MustCompile(`(?m)^- key: (\S+)$`)
	assessHeader    = regexp.MustCompile(`(?m)^Review this (\S+) → (\S+) translation\.$`)
)

type providerStats struct {
	Calls map[string]int // by task: translate, repair, assess
	// ByLocale counts draft calls (translate and repair) per locale.
	ByLocale map[string]int
	// Refused lists requests the script doesn't cover.
	Refused []string
	// Legal counts requests that carried sensitive (legal) text.
	Legal int
	// Violations are prompts that broke the contract the test expects:
	// a repair turn without the platform's findings, a formal locale's
	// prompt without its form of address.
	Violations []string
	// Glossary, TM and Style count draft prompts carrying each section.
	Glossary, TM, Style map[string]int
	// Keys are the message keys drafted, per locale.
	Keys map[string]map[string]int
}

type fakeProvider struct {
	srv         *httptest.Server
	mu          sync.Mutex
	scripts     map[string][]fixture.Draft // locale|key
	assessments map[string]fixture.Assessment
	legalText   []string
	pronoun     map[string]string
	stats       providerStats
}

func newFakeProvider(t *testing.T, f *fixture.Fixture) *fakeProvider {
	t.Helper()
	p := &fakeProvider{
		scripts: map[string][]fixture.Draft{}, assessments: map[string]fixture.Assessment{}, pronoun: map[string]string{},
		stats: providerStats{Calls: map[string]int{}, ByLocale: map[string]int{}, Glossary: map[string]int{}, TM: map[string]int{},
			Style: map[string]int{}, Keys: map[string]map[string]int{}},
	}
	for _, m := range f.Messages {
		if f.Sensitive(m.Namespace) {
			// The literal runs of the legal source, as any prompt would
			// carry them.
			for _, part := range strings.FieldsFunc(m.Source.MF2, func(r rune) bool { return r == '{' || r == '}' }) {
				if len(part) >= 20 {
					p.legalText = append(p.legalText, part)
				}
			}
			continue
		}
		for _, l := range f.FillLocales {
			if t := m.Translations[l]; t != nil && t.Route == fixture.RouteAI {
				p.scripts[l+"|"+m.Key] = t.ProviderDrafts()
			}
		}
	}
	for _, g := range f.StyleGuides {
		if g.Locale != "" && g.Fields.Formality != nil {
			p.pronoun[g.Locale] = g.Fields.Formality.Pronoun
		}
	}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeProvider) baseURL() string { return p.srv.URL + "/v1" }

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	ResponseFormat struct {
		JSONSchema struct {
			Name string `json:"name"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

func (p *fakeProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+fakeAPIKey {
		p.refuse(w, fmt.Sprintf("%s %s", r.Method, r.URL.Path))
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		p.refuse(w, "undecodable request: "+err.Error())
		return
	}
	var users []string
	assistants := 0
	all := ""
	for _, m := range req.Messages {
		all += m.Content
		switch m.Role {
		case "user":
			users = append(users, m.Content)
		case "assistant":
			assistants++
		}
	}
	p.mu.Lock()
	for _, text := range p.legalText {
		if strings.Contains(all, text) {
			p.stats.Legal++
		}
	}
	p.mu.Unlock()
	if len(users) == 0 {
		p.refuse(w, "no user message")
		return
	}
	switch req.ResponseFormat.JSONSchema.Name {
	case "translation":
		p.draft(w, req, users, assistants)
	case "assessment":
		p.assess(w, req, users[0])
	default:
		p.refuse(w, "unknown response format "+req.ResponseFormat.JSONSchema.Name)
	}
}

func (p *fakeProvider) draft(w http.ResponseWriter, req chatRequest, users []string, assistants int) {
	first := users[0]
	h, k := translateHeader.FindStringSubmatch(first), keyLine.FindStringSubmatch(first)
	if h == nil || k == nil {
		p.refuse(w, "translate prompt without locale or key")
		return
	}
	locale, key := h[2], k[1]
	p.mu.Lock()
	if strings.HasPrefix(key, "legal.") {
		p.stats.Legal++
	}
	drafts, ok := p.scripts[locale+"|"+key]
	attempt := assistants + 1
	task := "translate"
	if attempt > 1 {
		task = "repair"
		if !strings.HasPrefix(users[len(users)-1], "Your translation cannot be used yet") {
			p.stats.Violations = append(p.stats.Violations, locale+" "+key+": a repair turn without the platform's findings")
		}
	}
	if ok {
		p.stats.Calls[task]++
		p.stats.ByLocale[locale]++
		if p.stats.Keys[locale] == nil {
			p.stats.Keys[locale] = map[string]int{}
		}
		p.stats.Keys[locale][key]++
		if attempt == 1 {
			if strings.Contains(first, "<glossary>") {
				p.stats.Glossary[locale]++
			}
			if strings.Contains(first, "<translation_memory>") {
				p.stats.TM[locale]++
			}
			if pr := p.pronoun[locale]; pr != "" {
				if strings.Contains(first, `form of address: formal ("`+pr+`")`) {
					p.stats.Style[locale]++
				} else {
					p.stats.Violations = append(p.stats.Violations, locale+" "+key+": the prompt lacks the style guide's form of address")
				}
			}
		}
	}
	var d fixture.Draft
	if ok {
		d = drafts[min(attempt, len(drafts))-1]
		a := fixture.DefaultAssessment
		if d.Assessment != nil {
			a = *d.Assessment
		}
		p.assessments[locale+"|"+canonical(d.MF2)] = a
	}
	p.mu.Unlock()
	if !ok {
		p.refuse(w, "no script for "+locale+" "+key)
		return
	}
	// Usage is a function of the task and the answer only, so the spend
	// the report shows is the same on every run.
	p.answer(w, req.Model, map[string]string{"message": d.MF2, "notes": ""}, 900+300*(attempt-1))
}

func (p *fakeProvider) assess(w http.ResponseWriter, req chatRequest, prompt string) {
	h := assessHeader.FindStringSubmatch(prompt)
	start, end := strings.Index(prompt, "<translation>\n"), strings.Index(prompt, "\n</translation>")
	if h == nil || start < 0 || end < start {
		p.refuse(w, "assess prompt without locale or translation")
		return
	}
	locale := h[2]
	translation := prompt[start+len("<translation>\n") : end]
	p.mu.Lock()
	a, ok := p.assessments[locale+"|"+canonical(translation)]
	if ok {
		p.stats.Calls["assess"]++
	}
	p.mu.Unlock()
	if !ok {
		p.refuse(w, "no script for the assessment of "+locale+" "+translation)
		return
	}
	p.answer(w, req.Model, a, 600)
}

func (p *fakeProvider) answer(w http.ResponseWriter, model string, answer any, promptTokens int) {
	content, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-m2", "object": "chat.completion", "model": model,
		"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": string(content)}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": promptTokens, "completion_tokens": len(content)/4 + 1},
	})
}

func (p *fakeProvider) refuse(w http.ResponseWriter, why string) {
	p.mu.Lock()
	p.stats.Refused = append(p.stats.Refused, why)
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "invalid_request_error", "message": "unscripted request: " + why}})
}

func (p *fakeProvider) snapshot() providerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// canonical is the kernel's canonical MF2 of s (s itself when it doesn't
// parse, so a broken draft still matches its own assessment).
func canonical(s string) string {
	m, err := mf.ParseMF2(s)
	if err != nil {
		return s
	}
	out, err := mf.Stringify(m)
	if err != nil {
		return s
	}
	return out
}
