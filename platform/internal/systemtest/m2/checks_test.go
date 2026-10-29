//go:build system

package m2_test

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	mf "go.klarlabs.de/glossa/messageformat"
	glossa "go.klarlabs.de/glossa/runtimes/go"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m2/fixture"
)

// maxReviewShare is the M2 exit bar: people review at most this share of
// what the platform filled (TM and AI suggestions) per locale.
const maxReviewShare = 0.15

// slipFactor is the explanation factor that must route each surviving
// slip to review.
var slipFactor = map[string]string{
	fixture.SlipForbiddenTerm: "term_forbidden",
	fixture.SlipFormality:     "formality",
	fixture.SlipPluralMissing: "missing_plural_categories",
	fixture.SlipTooLong:       "max_length",
}

// checkRouting holds every job and suggestion against the fixture: the
// routes (TM reuse, provider, refusal), the slips' outcomes, and the M2
// exit criteria on the review share.
func (s *scenario) checkRouting() {
	t := s.t
	for _, j := range s.jobs {
		r := s.results[j.Locale]
		r.jobs[j.State]++
		if j.FailureCode != "" {
			r.failures[j.FailureCode]++
		}
		m, tr := s.translation(j.MessageKey, j.Locale)
		switch {
		case s.f.Sensitive(m.Namespace):
			t.Errorf("a job was queued for sensitive %s %s", j.Locale, j.MessageKey)
		case tr.Slip == fixture.SlipPlaceholderPersistent:
			if j.State != "failed" || j.FailureCode != "invalid_output" {
				t.Errorf("%s %s (placeholder dropped in every draft) ended %s %s, want failed invalid_output", j.Locale, j.MessageKey, j.State, j.FailureCode)
			}
		case j.State != "succeeded":
			t.Errorf("%s %s ended %s %s: %s", j.Locale, j.MessageKey, j.State, j.FailureCode, j.LastError)
		}
	}
	for _, sg := range s.suggestions {
		s.checkSuggestion(sg)
	}
	for _, l := range s.f.FillLocales {
		r, e := s.results[l], s.f.Expect[l]
		if r.tm != e.TMReused || r.ai != e.AIDrafted-e.Slips[fixture.SlipPlaceholderPersistent] {
			t.Errorf("%s: %d TM and %d AI suggestions, want %d and %d", l, r.tm, r.ai, e.TMReused, e.AIDrafted-e.Slips[fixture.SlipPlaceholderPersistent])
		}
		if r.failures["invalid_output"] != e.Slips[fixture.SlipPlaceholderPersistent] {
			t.Errorf("%s: %d invalid_output, want %d", l, r.failures["invalid_output"], e.Slips[fixture.SlipPlaceholderPersistent])
		}
		for _, k := range fixture.SurvivingSlips {
			if r.slipsCaught[k] != e.Slips[k] {
				t.Errorf("%s: %d of %d %s slips routed review_required", l, r.slipsCaught[k], e.Slips[k], k)
			}
		}
		// The exit criteria. Review is required for the slips that reach
		// a suggestion, the drafts that needed a structural repair, and
		// the clean drafts the length heuristic flags when no strong TM
		// match supports them — nothing else — and all of that stays
		// below maxReviewShare of what the platform filled.
		bound := e.Surviving() + e.Slips[fixture.SlipPlaceholderRepaired] + e.LengthFlagged
		filled := r.tm + r.ai
		if r.review > bound {
			t.Errorf("%s: %d suggestions need review, more than the %d slips, repairs and length-flagged drafts", l, r.review, bound)
		}
		if share := float64(r.review) / float64(filled); share > maxReviewShare {
			t.Errorf("%s: %.1f %% of %d filled messages need review, above the M2 bar of %.0f %%", l, 100*share, filled, 100*maxReviewShare)
		}
		if r.termIDs == 0 {
			t.Errorf("%s: no suggestion names the terms it used", l)
		}
	}
}

func (s *scenario) checkSuggestion(sg suggestion) {
	t := s.t
	r := s.results[sg.Locale]
	m, tr := s.translation(sg.MessageKey, sg.Locale)
	where := sg.Locale + " " + sg.MessageKey
	switch sg.Provenance.Origin {
	case "translation_memory":
		r.tm++
		if tr.Route != fixture.RouteTM || len(sg.Provenance.TMUnitIDs) != 1 || sg.Provenance.Provider != "" {
			t.Errorf("%s: reused from TM (%+v), the fixture routes it %s", where, sg.Provenance, tr.Route)
		}
	case "ai":
		r.ai++
		if tr.Route != fixture.RouteAI {
			t.Errorf("%s: drafted by the provider, the fixture routes it %s (tm_blocked %v)", where, tr.Route, tr.TMBlocked)
		}
		p := sg.Provenance
		if p.Provider != fakeProviderName || p.Model != translateModel || !strings.HasPrefix(p.PromptVersion, "translate/") || p.StyleVersion == "" {
			t.Errorf("%s: provenance %+v", where, p)
		}
		if p.Repairs > 0 {
			r.repaired++
		}
	default:
		t.Errorf("%s: origin %q", where, sg.Provenance.Origin)
	}
	if len(sg.Provenance.TermIDs) > 0 {
		r.termIDs++
	}
	if len(sg.Explanation) == 0 || sg.Status != "pending" || sg.Source == nil {
		t.Errorf("%s: explanation %v, status %s, source %v", where, sg.Explanation, sg.Status, sg.Source)
		return
	}
	if slices.Contains(fixture.SurvivingSlips, tr.Slip) {
		if sg.Action == "review_required" && sg.has(slipFactor[tr.Slip]) {
			r.slipsCaught[tr.Slip]++
		} else {
			t.Errorf("%s: %s slip routed %s at %.3f without %s: %v", where, tr.Slip, sg.Action, sg.Score, slipFactor[tr.Slip], sg.Explanation)
		}
	}
	if tr.Slip == fixture.SlipPlaceholderRepaired && (sg.Provenance.Repairs < 1 || canonical(sg.Message) != canonical(tr.MF2)) {
		t.Errorf("%s: the repaired draft is %q after %d repairs, want the reference %q", where, sg.Message, sg.Provenance.Repairs, tr.MF2)
	}
	switch sg.Action {
	case "approve_recommended":
		r.approve++
		s.checkRecommended(sg, m, tr)
	case "review_required":
		r.review++
		switch {
		case slices.Contains(fixture.SurvivingSlips, tr.Slip):
			r.reviewReasons["slip: "+tr.Slip]++
		case sg.has("repairs"):
			r.reviewReasons["structural repair"]++
		case sg.has("length_ratio") && tr.LengthFlagged:
			r.reviewReasons["length heuristic (clean draft)"]++
		default:
			t.Errorf("%s: routed review_required at %.3f for no reason the fixture explains: %v", where, sg.Score, sg.Explanation)
		}
	default:
		t.Errorf("%s: action %s (auto-approval is off)", where, sg.Action)
	}
}

// checkRecommended: nothing routed approve_recommended carries a defect.
// It is the fixture's clean reference, structurally compatible with the
// source for the target locale (every plural category included), within
// max_length and without forbidden terms.
func (s *scenario) checkRecommended(sg suggestion, m *fixture.Message, tr *fixture.Translation) {
	t := s.t
	where := sg.Locale + " " + sg.MessageKey
	if tr.Slip != "" && tr.Slip != fixture.SlipPlaceholderRepaired {
		t.Errorf("%s: the %s slip was routed approve_recommended", where, tr.Slip)
	}
	if canonical(sg.Message) != canonical(tr.MF2) {
		t.Errorf("%s: approve_recommended %q is not the reference %q", where, sg.Message, tr.MF2)
	}
	src, err := mf.ParseMF2(sg.Source.MF2)
	if err != nil {
		t.Errorf("%s: source: %v", where, err)
		return
	}
	msg, err := mf.ParseMF2(sg.Message)
	if err != nil {
		t.Errorf("%s: approve_recommended text is not MF2: %v", where, err)
		return
	}
	if fs := mf.CheckCompat(src, msg, sg.Locale); len(fs) > 0 {
		t.Errorf("%s: approve_recommended with structural findings %v", where, fs)
	}
	if m.MaxLength > 0 && visibleLength(msg) > m.MaxLength {
		t.Errorf("%s: approve_recommended over max_length %d", where, m.MaxLength)
	}
	for _, f := range sg.TermFindings {
		if f.Code == "term_forbidden" {
			t.Errorf("%s: approve_recommended with forbidden term %q", where, f.Term)
		}
	}
}

// visibleLength is the longest pattern's literal text, in characters.
func visibleLength(m mf.Message) int {
	longest := 0
	for _, p := range m.Patterns() {
		n := 0
		for _, el := range p {
			if tx, ok := el.(mf.Text); ok {
				n += len([]rune(string(tx)))
			}
		}
		longest = max(longest, n)
	}
	return longest
}

// checkQueue: the review queue holds every pending suggestion, riskiest
// first — scores never decrease, every review_required item comes
// before every approve_recommended one, and every surviving slip ranks
// above every clean draft.
func (s *scenario) checkQueue() {
	t := s.t
	if len(s.queue) != len(s.suggestions) {
		t.Fatalf("the queue has %d items, %d suggestions are pending", len(s.queue), len(s.suggestions))
	}
	lastSlip, firstClean, lastReview, firstApprove := -1, len(s.queue), -1, len(s.queue)
	for i, sg := range s.queue {
		if i > 0 && sg.Score < s.queue[i-1].Score {
			t.Errorf("queue item %d (%s %s, %.3f) scores below item %d (%.3f)", i, sg.Locale, sg.MessageKey, sg.Score, i-1, s.queue[i-1].Score)
		}
		_, tr := s.translation(sg.MessageKey, sg.Locale)
		switch {
		case slices.Contains(fixture.SurvivingSlips, tr.Slip):
			lastSlip = i
		case tr.Slip == "" && !tr.LengthFlagged:
			firstClean = min(firstClean, i)
		}
		if sg.Action == "review_required" {
			lastReview = i
		} else {
			firstApprove = min(firstApprove, i)
		}
	}
	if lastSlip >= firstClean {
		t.Errorf("a slip ranks at %d, below the first clean draft at %d", lastSlip, firstClean)
	}
	if lastReview >= firstApprove {
		t.Errorf("review_required item at %d after approve_recommended at %d", lastReview, firstApprove)
	}
}

// checkPrivacyAndSpend: the sensitive namespace never reached the
// provider (neither the fake nor the disclosures saw it), every call is
// disclosed and priced, and the fake saw only scripted, well-formed
// prompts.
func (s *scenario) checkPrivacyAndSpend() {
	t := s.t
	stats := s.provider.snapshot()
	if len(stats.Refused) > 0 || len(stats.Violations) > 0 || stats.Legal > 0 {
		t.Errorf("fake provider: refused %v, violations %v, legal %d", stats.Refused, stats.Violations, stats.Legal)
	}
	keyOf := map[string]string{}
	for _, j := range s.jobs {
		keyOf[j.MessageID] = j.MessageKey
	}
	var legal []string
	for _, m := range s.f.Messages {
		if s.f.Sensitive(m.Namespace) {
			legal = append(legal, m.Source.MF2)
		}
	}
	for _, d := range s.disclosures {
		key, ok := keyOf[d.MessageID]
		if !ok {
			t.Errorf("disclosure %s is for message %s, which no job of the fill translated", d.ID, d.MessageID)
			continue
		}
		if m, _ := s.f.Message(key); m != nil && s.f.Sensitive(m.Namespace) {
			t.Errorf("disclosure %s: %s reached %s", d.ID, key, d.Provider)
		}
		for _, sent := range d.Sent {
			for _, text := range legal {
				if strings.Contains(sent.Text, text) {
					t.Errorf("disclosure %s carried legal text %q", d.ID, text)
				}
			}
		}
		if d.Provider != fakeProviderName || len(d.Sent) == 0 {
			t.Errorf("disclosure %+v", d)
		}
	}
	calls := stats.Calls["translate"] + stats.Calls["repair"] + stats.Calls["assess"]
	if len(s.disclosures) != calls || s.budget.Calls != calls || s.budget.Spent <= 0 {
		t.Errorf("%d disclosures and %d priced calls for %d provider calls; spent %d µ$", len(s.disclosures), s.budget.Calls, calls, s.budget.Spent)
	}
	var cost int64
	for _, sg := range s.suggestions {
		cost += sg.CostMicroUSD
	}
	if cost <= 0 || cost > s.budget.Spent {
		t.Errorf("suggestions cost %d µ$, the budget recorded %d µ$", cost, s.budget.Spent)
	}
}

// acceptRecommended accepts every approve_recommended suggestion as is,
// the way a reviewer clears the easy part of the queue.
func (s *scenario) acceptRecommended() {
	t := s.t
	var todo []suggestion
	for _, sg := range s.queue {
		if sg.Action == "approve_recommended" {
			todo = append(todo, sg)
		}
	}
	var mu sync.Mutex
	accepted := map[string]suggestion{}
	errs := parallel(todo, 8, func(sg suggestion) error {
		var out suggestion
		if _, err := s.owner.try(http.MethodPost, s.tenantPath("/ai-suggestions/"+sg.ID+"/acceptance"), map[string]any{}, http.StatusOK, &out); err != nil {
			return fmt.Errorf("accept %s %s: %w", sg.Locale, sg.MessageKey, err)
		}
		if out.Status != "accepted" || out.TranslationRevision == nil {
			return fmt.Errorf("accept %s %s: %s, revision %v", sg.Locale, sg.MessageKey, out.Status, out.TranslationRevision)
		}
		mu.Lock()
		accepted[sg.ID] = out
		mu.Unlock()
		return nil
	})
	for _, err := range errs {
		t.Error(err)
	}
	for _, sg := range accepted {
		s.results[sg.Locale].accepted++
	}
	var queue []suggestion
	queue = list[suggestion](s.owner, s.projectPath("/ai-review-queue"), nil)
	review := 0
	for _, l := range s.f.FillLocales {
		review += s.results[l].review
	}
	if len(queue) != review || slices.ContainsFunc(queue, func(sg suggestion) bool { return sg.Action != "review_required" }) {
		t.Errorf("after accepting, the queue has %d items, want the %d that need review", len(queue), review)
	}
	s.owner.do(http.MethodGet, s.projectPath("/ai-metrics"), nil, http.StatusOK, &s.metrics)
	for _, lm := range s.metrics.Locales {
		if r := s.results[lm.Locale]; r != nil && (lm.Accepted != r.accepted || lm.AcceptanceRate != 1) {
			t.Errorf("ai-metrics %s: %+v, %d accepted", lm.Locale, lm, r.accepted)
		}
	}
}

type metricsDoc struct {
	Locales []struct {
		Locale         string  `json:"locale"`
		Accepted       int     `json:"accepted"`
		Rejected       int     `json:"rejected"`
		AcceptanceRate float64 `json:"acceptance_rate"`
	} `json:"locales"`
}

// checkProvenance: every revision the acceptances wrote is approved and
// carries the full provenance — origin, suggestion, job, score and
// explanation, and the provider, model, prompt and style versions (AI)
// or the TM unit (TM).
func (s *scenario) checkProvenance() {
	t := s.t
	var accepted []suggestion
	for _, sg := range s.queue {
		if sg.Action == "approve_recommended" {
			accepted = append(accepted, sg)
		}
	}
	errs := parallel(accepted, 8, func(sg suggestion) error {
		var revs struct {
			Items []struct {
				Origin       string         `json:"origin"`
				State        string         `json:"state"`
				OriginDetail map[string]any `json:"origin_detail"`
			} `json:"items"`
		}
		path := s.projectPath("/messages/" + url.PathEscape(sg.MessageKey) + "/translations/" + sg.Locale + "/revisions?page_size=1")
		if _, err := s.owner.try(http.MethodGet, path, nil, http.StatusOK, &revs); err != nil {
			return err
		}
		if len(revs.Items) == 0 {
			return fmt.Errorf("%s %s: no revision", sg.Locale, sg.MessageKey)
		}
		r := revs.Items[0]
		want := []string{"suggestion_id", "job_id", "score", "explanation", "action"}
		if r.Origin == "ai" {
			want = append(want, "provider", "model", "prompt_version", "style_version")
		} else {
			want = append(want, "tm_unit_ids")
		}
		var missing []string
		for _, k := range want {
			if v, ok := r.OriginDetail[k]; !ok || v == nil || v == "" {
				missing = append(missing, k)
			}
		}
		if r.Origin != sg.Provenance.Origin || r.State != "approved" || r.OriginDetail["suggestion_id"] != sg.ID || len(missing) > 0 {
			return fmt.Errorf("%s %s: revision origin %s, state %s, origin_detail %v lacks %v", sg.Locale, sg.MessageKey, r.Origin, r.State, r.OriginDetail, missing)
		}
		return nil
	})
	for _, err := range errs {
		t.Error(err)
	}
	var stats struct {
		Locales []struct {
			Code   string         `json:"code"`
			States map[string]int `json:"states"`
		} `json:"locales"`
	}
	s.owner.do(http.MethodGet, s.projectPath("/translation-stats"), nil, http.StatusOK, &stats)
	for _, ls := range stats.Locales {
		if r := s.results[ls.Code]; r != nil {
			r.approvedAfter = ls.States["approved"]
			if want := r.expect.Existing + r.accepted; r.approvedAfter != want {
				t.Errorf("%s: %d approved translations, want %d", ls.Code, r.approvedAfter, want)
			}
		}
	}
}

type releaseDoc struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Counts  struct {
		Messages int `json:"messages"`
		Locales  map[string]struct {
			Messages int `json:"messages"`
		} `json:"locales"`
	} `json:"counts"`
}

// publish publishes staging, which ships approved translations only.
func (s *scenario) publish() {
	t := s.t
	s.owner.do(http.MethodPost, s.projectPath("/releases"), map[string]string{"environment": "staging", "note": "M2: filled es, fr, ja"},
		http.StatusCreated, &s.release, "Idempotency-Key", "m2-staging")
	if s.release.Counts.Messages != len(s.f.Messages) {
		t.Errorf("release = %+v", s.release)
	}
	for _, l := range s.f.FillLocales {
		r := s.results[l]
		r.releaseMessages = s.release.Counts.Locales[l].Messages
		if r.releaseMessages != r.approvedAfter {
			t.Errorf("staging ships %d %s messages, %d are approved", r.releaseMessages, l, r.approvedAfter)
		}
	}
}

type renderSample struct {
	Locale, Key, Args, Got, Note string
}

// render loads the staging release from glossa-edge with the Go runtime,
// verifies its signature and renders AI-filled messages — a plural
// among them — exactly as the kernel formats the reference; a slip that
// is still in review is not shipped.
func (s *scenario) render() {
	t := s.t
	var key struct {
		Key string `json:"key"`
	}
	// A key reads production only unless it says otherwise (RFC 0004
	// §4.3), and this one reads the staging release.
	s.owner.do(http.MethodPost, s.projectPath("/delivery-keys"), map[string]any{
		"name": "m2-web", "scope": map[string]any{"environments": []string{"staging"}, "branches": false},
	}, http.StatusCreated, &key)
	var signing struct {
		Keys []struct {
			KeyID     string `json:"key_id"`
			PublicKey string `json:"public_key"`
		} `json:"keys"`
	}
	s.owner.do(http.MethodGet, s.projectPath("/release-signing-keys"), nil, http.StatusOK, &signing)
	var pubs []glossa.PublicKey
	for _, k := range signing.Keys {
		pk, err := glossa.ParsePublicKey(k.KeyID, k.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		pubs = append(pubs, pk)
	}
	c, err := glossa.New(glossa.Config{
		EdgeURL: s.d.edgeURL, DeliveryKey: key.Key, Environment: "staging", PublicKeys: pubs,
		DisableCache: true, RefreshInterval: -1, DisableBidiIsolation: true,
		Retry: glossa.RetryPolicy{MaxAttempts: 1}, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("runtime refresh: %v", err)
	}
	if rel, ok := c.Release(); !ok || rel.ID != s.release.ID {
		t.Fatalf("runtime loaded %+v, want release %s", rel, s.release.ID)
	}
	accepted := map[string]bool{}
	for _, sg := range s.queue {
		if sg.Action == "approve_recommended" {
			accepted[sg.Locale+"|"+sg.MessageKey] = true
		}
	}
	for _, l := range s.f.FillLocales {
		s.renderAccepted(c, l, accepted, "count", map[string]any{"count": 1})
		s.renderAccepted(c, l, accepted, "count", map[string]any{"count": 3})
		if l != "ja" {
			s.renderAccepted(c, l, accepted, "count", map[string]any{"count": 1_000_000})
		}
		s.renderAccepted(c, l, accepted, "activity", map[string]any{"name": "Ada"})
		s.renderAccepted(c, l, accepted, "permission", map[string]any{"role": "admin"})
		s.renderHeld(c, l)
	}
}

func (s *scenario) renderAccepted(c *glossa.Client, l string, accepted map[string]bool, pattern string, args map[string]any) {
	t := s.t
	for _, m := range s.f.Messages {
		tr := m.Translations[l]
		if m.Pattern != pattern || tr == nil || tr.Route != fixture.RouteAI || !accepted[l+"|"+m.Key] {
			continue
		}
		ref, err := mf.ParseMF2(tr.MF2)
		if err != nil {
			t.Fatal(err)
		}
		want, err := mf.Format(ref, l, args, mf.WithBidiIsolation(false))
		if err != nil {
			t.Fatal(err)
		}
		got := c.For(l).T(m.Key, glossa.Args(args))
		ex := c.For(l).Explain(m.Key)
		if got != want || ex.ResolvedFrom == nil || *ex.ResolvedFrom != l {
			t.Errorf("runtime %s %s %v = %q (from %v), want %q", l, m.Key, args, got, ex.ResolvedFrom, want)
		}
		s.samples = append(s.samples, renderSample{Locale: l, Key: m.Key, Args: fmt.Sprint(args), Got: got, Note: "AI, accepted"})
		return
	}
	t.Errorf("no accepted %s %s message to render", l, pattern)
}

// renderHeld: a surviving slip is not in the release; the runtime falls
// back and never shows the slip.
func (s *scenario) renderHeld(c *glossa.Client, l string) {
	for _, m := range s.f.Messages {
		tr := m.Translations[l]
		if tr == nil || tr.Slip != fixture.SlipForbiddenTerm || m.Pattern == "count" || m.Pattern == "selected" || m.Pattern == "activity" || m.Pattern == "permission" {
			continue
		}
		got := c.For(l).T(m.Key, nil)
		ex := c.For(l).Explain(m.Key)
		slip, _ := mf.ParseMF2(tr.Drafts[0].MF2)
		slipText, _ := mf.Format(slip, l, nil, mf.WithBidiIsolation(false))
		if got == slipText || (ex.ResolvedFrom != nil && *ex.ResolvedFrom == l) {
			s.t.Errorf("runtime %s %s = %q from %v: the slip in review shipped", l, m.Key, got, ex.ResolvedFrom)
		}
		from := "inline"
		if ex.ResolvedFrom != nil {
			from = *ex.ResolvedFrom
		}
		s.samples = append(s.samples, renderSample{Locale: l, Key: m.Key, Got: got, Note: "forbidden-term slip in review: falls back to " + from})
		return
	}
}

func pct(n, d int) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f %%", 100*float64(n)/float64(d))
}

func usd(micros int64) string { return fmt.Sprintf("$%.4f", float64(micros)/1e6) }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
