//go:build integration

package main

import (
	"net/http"
	"testing"
)

// selfReviewFixture is a project in tenant base with a "de" locale and
// one message "a", plus the authored translation's URL.
func selfReviewFixture(t *testing.T, s *server, owner session, base string) (project, tr string) {
	t.Helper()
	var proj struct {
		ID string `json:"id"`
	}
	s.do(call{method: "POST", path: base + "/projects", cookie: owner.cookie, csrf: owner.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &proj)
	project = base + "/projects/" + proj.ID
	s.do(call{method: "POST", path: project + "/locales", cookie: owner.cookie, csrf: owner.csrf,
		body: map[string]string{"code": "de"}}).want(t, http.StatusCreated, "")
	s.do(call{method: "POST", path: project + "/messages", cookie: owner.cookie, csrf: owner.csrf,
		body: map[string]string{"key": "a", "text": "Hello"}}).want(t, http.StatusCreated, "")
	return project, project + "/messages/a/translations/de"
}

type reviewHint struct {
	State                 string `json:"state"`
	ReviewByAuthorAllowed *bool  `json:"review_by_author_allowed"`
}

type revisionList struct {
	Items []struct {
		Kind       string `json:"kind"`
		State      string `json:"state"`
		SelfReview bool   `json:"self_review"`
	} `json:"items"`
}

func review(s *server, who session, tr, state string) reply {
	return s.do(call{method: "POST", path: tr + "/reviews", cookie: who.cookie, csrf: who.csrf,
		body: map[string]string{"state": state}, headers: map[string]string{"If-Match": `"1"`}})
}

// An individual tenant has one member: the author approves their own
// text, and the history says so (RFC 0006 §15 Q6, amended 2026-10-08).
func TestSoloAuthorApprovesOwnTextOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var me struct {
		Person struct {
			IndividualTenantID string `json:"individual_tenant_id"`
		} `json:"person"`
	}
	s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie}).decode(t, &me)
	_, tr := selfReviewFixture(t, s, ada, "/v1/tenants/"+me.Person.IndividualTenantID)

	var put reviewHint
	r := s.do(call{method: "PUT", path: tr, cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"text": "Hallo"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &put)
	if put.State != "needs_review" || put.ReviewByAuthorAllowed == nil || !*put.ReviewByAuthorAllowed {
		t.Fatalf("a solo author may review: %s", r.body)
	}
	review(s, ada, tr, "approved").want(t, http.StatusOK, "")

	var revs revisionList
	s.do(call{method: "GET", path: tr + "/revisions", cookie: ada.cookie}).decode(t, &revs)
	if len(revs.Items) != 2 || revs.Items[0].Kind != "review" || !revs.Items[0].SelfReview || revs.Items[1].SelfReview {
		t.Errorf("history = %+v", revs.Items)
	}
}

// With a second reviewer for the locale, the author is refused and a
// write asking for approval lands as needs_review (#73, #90).
func TestAuthorRefusedWhenAnotherReviewerExistsOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	_, tr := selfReviewFixture(t, s, ada, base)
	s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "rita@example.com", "roles": []string{"reviewer"}, "locales": []string{"de"}}}).
		want(t, http.StatusCreated, "")
	rita := s.signIn("rita@example.com")

	var put reviewHint
	r := s.do(call{method: "PUT", path: tr, cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"text": "Hallo", "state": "approved"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &put)
	if put.State != "needs_review" || put.ReviewByAuthorAllowed == nil || *put.ReviewByAuthorAllowed {
		t.Fatalf("save & approve with another reviewer: %s", r.body)
	}
	review(s, ada, tr, "approved").want(t, http.StatusForbidden, "own_text")
	review(s, rita, tr, "approved").want(t, http.StatusOK, "")

	var revs revisionList
	s.do(call{method: "GET", path: tr + "/revisions", cookie: ada.cookie}).decode(t, &revs)
	if revs.Items[0].SelfReview {
		t.Errorf("a second reviewer's decision is no self-review: %+v", revs.Items)
	}
}

// Another member who cannot review this locale is not a second pair of
// eyes: a translator, or a reviewer for another locale, leaves the
// author free to decide.
func TestAuthorFreeWhenOthersCannotReviewTheLocaleOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	_, tr := selfReviewFixture(t, s, ada, base)
	for email, m := range map[string]map[string]any{
		"tess@example.com": {"roles": []string{"translator"}, "locales": []string{"de"}},
		"fran@example.com": {"roles": []string{"reviewer"}, "locales": []string{"fr"}},
	} {
		m["email"] = email
		s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf, body: m}).
			want(t, http.StatusCreated, "")
	}
	s.do(call{method: "PUT", path: tr, cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"text": "Hallo"}}).
		want(t, http.StatusCreated, "")
	review(s, ada, tr, "approved").want(t, http.StatusOK, "")
	var revs revisionList
	s.do(call{method: "GET", path: tr + "/revisions", cookie: ada.cookie}).decode(t, &revs)
	if !revs.Items[0].SelfReview {
		t.Errorf("history = %+v", revs.Items)
	}
}
