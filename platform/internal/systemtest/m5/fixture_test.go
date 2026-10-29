//go:build system

package m5_test

import (
	"fmt"
	"net/http"
	"strings"
)

// The fixture organisation. Every address is in a reserved domain.
const (
	ownerEmail     = "lena@acme.example"
	reviewer1Email = "ana@acme.example"
	reviewer2Email = "ben@acme.example"
	vendorEmail    = "vera@lingua.example"
	vendorPassword = "vendor-m5-correct-horse"
	vendorName     = "lingua-gmbh"
)

// The canaries: words that exist nowhere but in this fixture's message
// text, seeded into source text and translation text of both projects.
// §12.5 fails if any of them appears anywhere in the exported audit
// range, because audit entries carry identifiers and shapes, never text
// (RFC 0006 §6.1).
var canaries = []string{
	"Quokkafjordine",  // source text, project A
	"Velutinaquill",   // source text, project B
	"Brombeerzwirnt",  // German translations, project A
	"Nachtfalterzopf", // German translations, project B
	"Zwielichtmarmor", // the revised source of the shared message
}

// sharedKey is the message both projects hold, revised once in both so
// the same source revision reaches two workflows (§12.1).
const sharedKey = "shared.welcome"

// Message counts. Project B's first assignedUnits `de` units are the
// vendor's assignment; the rest are outside it.
const (
	messagesPerProject = 30
	assignedUnits      = 20
)

// fixture creates the organisation, its people, its two projects and
// their catalogs through the API — everything M2–M4 already proved —
// and then the M5 parts the criteria need (a vendor, a vendor member),
// recording a gap where the platform cannot yet make them.
func (s *scenario) fixture() {
	t := s.t
	var err error
	if s.owner, err = s.d.signIn(t, ownerEmail, s.log); err != nil {
		fatalf("the owner could not sign in through the test mailer: %v", err)
	}
	var org struct{ ID string }
	s.owner.do(http.MethodPost, "/v1/tenants", map[string]string{"slug": "acme", "name": "Acme"}, http.StatusCreated, &org)
	s.tenant = org.ID

	s.projectA = s.newProject("ledger", "Acme Ledger")
	s.projectB = s.newProject("portal", "Acme Portal")

	var token struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/tokens"),
		map[string]any{"name": "m5-owner", "scopes": []string{"read", "write", "publish"}}, http.StatusCreated, &token)
	s.ownerToken = s.d.newClient(t, "the owner's API token", s.log)
	s.ownerToken.bearer, s.ownerToken.actor = token.Secret, "token:"+token.ID

	// Two reviewers for `de`: four-eyes needs two people who are not the
	// author, and release approvals need two who are not the requester.
	s.reviewer1 = s.invite(reviewer1Email, map[string]any{"roles": []string{"reviewer"}, "locales": []string{"de"}})
	s.reviewer2 = s.invite(reviewer2Email, map[string]any{"roles": []string{"reviewer"}, "locales": []string{"de"}})

	s.seedCatalog(s.projectA, "a", canaries[0], canaries[2])
	s.seedCatalog(s.projectB, "b", canaries[1], canaries[3])

	s.vendorMember()
	s.addressable()
}

// addressable gives §12.2's sweep something to address on operations
// that read one resource: an application, a check policy version and a
// staging release in each project, and a style guide, a term concept,
// an AI provider (the fake on loopback) and an export job in the
// tenant — then, in each project, every resource sweepResources makes.
// All are M2–M4 features; one that cannot be made only leaves its
// operations without a verdict in the coverage table, which the report
// counts against §12.2 rather than hiding.
func (s *scenario) addressable() {
	sides := [2]sweepSide{{project: s.projectA, prefix: "a"}, {project: s.projectB, prefix: "b"}}
	for i := range sides {
		p := sides[i].project
		var app struct {
			ID string `json:"id"`
		}
		_, _ = s.owner.try(http.MethodPost, s.projectPathOf(p, "/applications"),
			map[string]string{"slug": "web", "name": "Web", "platform": "web"}, http.StatusCreated, &app)
		sides[i].application = app.ID
		_, _ = s.owner.try(http.MethodPost, s.projectPathOf(p, "/check-policy"), map[string]any{"policy": map[string]any{
			"schema": "glossa.check-policy/v1", "require_complete": "all", "fail_on": "error", "missing_translations": "error",
		}}, http.StatusOK, nil)
		r, _ := s.publish(s.owner, p, "staging", map[string]any{"note": "the fixture's first"}, http.StatusCreated)
		sides[i].release = r.ID
	}
	_, _ = s.owner.try(http.MethodPost, s.tenantPath("/style-guides"),
		map[string]any{"project_id": s.projectA, "locale": "de", "name": "Ledger tone"}, http.StatusCreated, nil)
	_, _ = s.owner.try(http.MethodPost, s.tenantPath("/term-concepts"), map[string]any{"project_id": s.projectA,
		"terms": []map[string]string{{"locale": "en", "text": "ledger"}, {"locale": "de", "text": "Hauptbuch"}}}, http.StatusCreated, nil)
	s.configureAI()
	_, _ = s.owner.try(http.MethodPost, s.tenantPath("/export-jobs"),
		map[string]any{"project_id": s.projectA, "format": "xliff"}, http.StatusAccepted, nil)
	// A group (RFC 0006 §4.3), so the group reads have one to address.
	_, _ = s.owner.try(http.MethodPost, s.tenantPath("/groups"), map[string]any{"name": "de reviewers"}, http.StatusCreated, nil)
	s.sweepResources(sides)
}

// configureAI points the tenant at the fake provider, routes translate
// and assess to it, prices its models and gives consent and a budget —
// M2's setup, so an AI fill makes a job and a suggestion. No review
// task is routed, so a linguistic job never reaches a model.
func (s *scenario) configureAI() {
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodPost, "/ai-providers", map[string]any{
			"name": fakeProviderName, "kind": "openai_compatible", "base_url": s.d.provider.baseURL(),
			"api_key": fakeAPIKey, "models": []string{translateModel, assessModel},
		}, http.StatusCreated},
		{http.MethodPut, "/ai-routing-policy", map[string]any{"rules": []map[string]any{
			{"task": "translate", "routes": []map[string]any{{"provider": fakeProviderName, "model": translateModel, "max_tokens": 512}}},
			{"task": "assess", "routes": []map[string]any{{"provider": fakeProviderName, "model": assessModel, "max_tokens": 256}}},
		}}, http.StatusOK},
		{http.MethodPut, "/ai-prices", map[string]any{"overrides": map[string]any{
			fakeProviderName + "/" + translateModel: map[string]float64{"input_per_mtok": 3, "output_per_mtok": 15},
			fakeProviderName + "/" + assessModel:    map[string]float64{"input_per_mtok": 1, "output_per_mtok": 5},
		}}, http.StatusOK},
		{http.MethodPut, "/ai-settings", map[string]any{
			"provider_consent": true, "monthly_budget_micro_usd": 10_000_000, "max_concurrent_jobs": 4,
		}, http.StatusOK},
	} {
		if _, err := s.owner.try(c.method, s.tenantPath(c.path), c.body, c.want, nil); err != nil {
			s.note("12.2", "The fixture could not configure AI (%s %s): %v", c.method, c.path, err)
		}
	}
}

func (s *scenario) newProject(slug, name string) string {
	var p struct{ ID string }
	s.owner.do(http.MethodPost, s.tenantPath("/projects"),
		map[string]any{"slug": slug, "name": name, "source_locale": "en"}, http.StatusCreated, &p)
	s.owner.do(http.MethodPost, s.projectPathOf(p.ID, "/locales"), map[string]string{"code": "de"}, http.StatusCreated, nil)
	return p.ID
}

// invite opens an invitation and has the person accept it by signing in
// through a mailed link — which only works because mail works.
func (s *scenario) invite(email string, body map[string]any) *client {
	body["email"] = email
	s.owner.do(http.MethodPost, s.tenantPath("/members"), body, http.StatusCreated, nil)
	c, err := s.d.signIn(s.t, email, s.log)
	if err != nil {
		fatalf("%s could not accept the invitation: %v", email, err)
	}
	return c
}

// seedCatalog pushes messagesPerProject messages and the shared one,
// with approved German translations, and canaries in some of each.
func (s *scenario) seedCatalog(project, prefix, sourceCanary, translationCanary string) {
	var items []map[string]any
	for i := 1; i <= messagesPerProject; i++ {
		text := fmt.Sprintf("Entry %d of the %s screen", i, prefix)
		if i%5 == 0 {
			text = fmt.Sprintf("Entry %d mentions %s", i, sourceCanary)
		}
		items = append(items, map[string]any{"key": unitKey(prefix, i), "text": text, "syntax": "mf2",
			"description": "fixture message " + unitKey(prefix, i)})
	}
	items = append(items, map[string]any{"key": sharedKey, "text": "Welcome back, {$name}!", "syntax": "mf2"})
	s.owner.do(http.MethodPost, s.projectPathOf(project, "/message-upserts"), map[string]any{"items": items}, http.StatusOK, nil)

	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it["key"].(string))
	}
	errs := parallel(keys, 8, func(key string) error {
		text := "Eintrag für " + key
		if strings.HasSuffix(key, "0") || strings.HasSuffix(key, "5") {
			text = "Eintrag mit " + translationCanary
		}
		if key == sharedKey {
			text = "Willkommen zurück, {$name}!"
		}
		return s.owner.putTranslation(s.projectPathOf(project, "/messages/"+key+"/translations/de"),
			map[string]any{"text": text, "syntax": "mf2", "state": "approved", "origin": "human"})
	})
	if len(errs) > 0 {
		fatalf("seeding %s's German: %d failed (first: %v)", prefix, len(errs), errs[0])
	}
	if prefix == "b" {
		for i := 1; i <= messagesPerProject; i++ {
			if i <= assignedUnits {
				s.assigned = append(s.assigned, unitKey(prefix, i))
			} else {
				s.unassigned = append(s.unassigned, unitKey(prefix, i))
			}
		}
	}
}

func unitKey(prefix string, i int) string { return fmt.Sprintf("%s.unit.%02d", prefix, i) }

// vendorMember makes the vendor of §3.3 and its translator: a member
// with the vendor, visibility `assigned` and project B's scope, each of
// which the API must give back. A platform that refuses the invitation
// gets the same person as an ordinary `de` translator, so §12.2's sweep
// still runs and shows what such a member can read. That fallback is a
// gap of §12.2, never a pass.
func (s *scenario) vendorMember() {
	var vendor struct {
		ID string `json:"id"`
	}
	if _, err := s.owner.try(http.MethodPost, s.vendorsPath(), map[string]any{
		"name": vendorName, "contact": "jobs@lingua.example", "locales": []string{"de"},
	}, http.StatusCreated, &vendor); err != nil {
		s.gap("12.2", "creating the vendor — %v", missing("POST "+s.vendorsPath(), err))
	} else {
		s.vendorID = vendor.ID
	}
	invite := map[string]any{
		"email": vendorEmail, "roles": []string{"translator"}, "locales": []string{"de"},
		"visibility": visibilityAssigned, "projects": []string{s.projectB},
	}
	if s.vendorID != "" {
		invite["vendor_id"] = s.vendorID
	}
	var member struct {
		ID         string   `json:"id"`
		Visibility string   `json:"visibility"`
		Vendor     string   `json:"vendor_id"`
		Projects   []string `json:"projects"`
	}
	_, err := s.owner.try(http.MethodPost, s.tenantPath("/members"), invite, http.StatusCreated, &member)
	switch {
	case err != nil:
		s.gap("12.2", "inviting the vendor's translator with `visibility: assigned` and project B's scope — refused: %v", err)
		delete(invite, "visibility")
		delete(invite, "projects")
		delete(invite, "vendor_id")
		s.owner.do(http.MethodPost, s.tenantPath("/members"), invite, http.StatusCreated, &member)
	case member.Visibility != visibilityAssigned:
		s.gap("12.2", "the vendor's translator was invited, but the member the API returned has visibility %q, "+
			"not %q: the field was dropped, so the platform restricts nothing", member.Visibility, visibilityAssigned)
	case s.vendorID != "" && member.Vendor != s.vendorID:
		s.gap("12.2", "the vendor's translator was invited, but the member the API returned works for vendor %q, not %s",
			member.Vendor, s.vendorID)
	case len(member.Projects) != 1 || member.Projects[0] != s.projectB:
		s.gap("12.2", "the vendor's translator was invited, but the member the API returned is scoped to projects %v, "+
			"not project B alone: the scope was dropped", member.Projects)
	default:
		s.vendorAsVendor = true
	}
	s.vendorInvite = member.ID
	c, err := s.d.register(s.t, vendorEmail, vendorPassword, s.log)
	if err != nil {
		fatalf("the vendor's translator could not register and accept the invitation through the test mailer: %v", err)
	}
	s.vendor = c
}
