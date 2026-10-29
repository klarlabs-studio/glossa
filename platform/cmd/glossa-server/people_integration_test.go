//go:build integration

package main

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
)

type memberJSON struct {
	ID         string   `json:"id"`
	Roles      []string `json:"roles"`
	Projects   []string `json:"projects"`
	VendorID   string   `json:"vendor_id"`
	Visibility string   `json:"visibility"`
}

// Groups, vendors and invitations that carry a restriction (RFC 0006
// §3.3, §4.1, §4.3) through HTTP against Postgres: every operation's
// success, its refusals (401, 403, 404, 409, 412, 428, 400) and who may
// do what.
func TestGroupsVendorsAndRestrictedInvitationsOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	project := func(slug string) string {
		var p struct{ ID string }
		r := s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"slug": slug, "name": slug, "source_locale": "en"}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &p)
		return p.ID
	}
	a, b := project("shop"), project("blog")
	as := func(method, path string, body any, headers ...string) reply {
		c := call{method: method, path: path, cookie: ada.cookie, csrf: ada.csrf, body: body}
		if len(headers) == 2 {
			c.headers = map[string]string{headers[0]: headers[1]}
		}
		return s.do(c)
	}

	s.do(call{method: "GET", path: base + "/vendors"}).want(t, http.StatusUnauthorized, "unauthenticated")
	s.do(call{method: "GET", path: base + "/groups"}).want(t, http.StatusUnauthorized, "unauthenticated")

	// ── vendors ──
	var vendor struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Contact string   `json:"contact"`
		Locales []string `json:"locales"`
	}
	r := as("POST", base+"/vendors", map[string]any{"name": "Lingua GmbH", "contact": "jobs@lingua.example", "locales": []string{"de", "fr"}},
		"Idempotency-Key", "vendor-1")
	r.want(t, http.StatusCreated, "")
	r.decode(t, &vendor)
	if vendor.Name != "Lingua GmbH" || vendor.Contact != "jobs@lingua.example" || !slices.Equal(vendor.Locales, []string{"de", "fr"}) ||
		r.header.Get("ETag") != `"1"` || r.header.Get("Location") != base+"/vendors/"+vendor.ID {
		t.Fatalf("created vendor = %s (%v)", r.body, r.header)
	}
	replay := as("POST", base+"/vendors", map[string]any{"name": "Lingua GmbH"}, "Idempotency-Key", "vendor-1")
	replay.want(t, http.StatusCreated, "")
	if replay.header.Get("Idempotent-Replayed") != "true" {
		t.Error("a retried vendor create was not a replay")
	}
	as("POST", base+"/vendors", map[string]any{"name": ""}).want(t, http.StatusBadRequest, "invalid_vendor_name")
	var vendors struct{ Items []struct{ ID string } }
	as("GET", base+"/vendors", nil).decode(t, &vendors)
	if len(vendors.Items) != 1 || vendors.Items[0].ID != vendor.ID {
		t.Errorf("vendors = %+v", vendors.Items)
	}
	as("GET", base+"/vendors/"+vendor.ID, nil).want(t, http.StatusOK, "")
	as("GET", base+"/vendors/"+uuid.NewString(), nil).want(t, http.StatusNotFound, "not_found")
	as("PATCH", base+"/vendors/"+vendor.ID, map[string]any{"name": "Lingua AG"}).want(t, http.StatusPreconditionRequired, "precondition_required")
	as("PATCH", base+"/vendors/"+vendor.ID, map[string]any{"name": "Lingua AG"}, "If-Match", `"7"`).want(t, http.StatusPreconditionFailed, "precondition_failed")
	r = as("PATCH", base+"/vendors/"+vendor.ID, map[string]any{"name": "Lingua AG"}, "If-Match", `"1"`)
	r.want(t, http.StatusOK, "")
	r.decode(t, &vendor)
	if vendor.Name != "Lingua AG" || vendor.Contact != "jobs@lingua.example" || r.header.Get("ETag") != `"2"` {
		t.Errorf("after the PATCH the vendor is %s (ETag %s); the contact must be kept", r.body, r.header.Get("ETag"))
	}

	// ── an invitation carries the restriction ──
	invite := func(body map[string]any) reply { return as("POST", base+"/members", body) }
	var vera memberJSON
	r = invite(map[string]any{"email": "vera@lingua.example", "roles": []string{"translator"}, "locales": []string{"de"},
		"vendor_id": vendor.ID, "visibility": "assigned", "projects": []string{a}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &vera)
	if vera.Visibility != "assigned" || vera.VendorID != vendor.ID || !slices.Equal(vera.Projects, []string{a}) {
		t.Fatalf("the invited vendor member is %s; visibility, vendor and projects must be stored", r.body)
	}
	var stored memberJSON
	as("GET", base+"/members/"+vera.ID, nil).decode(t, &stored)
	if stored.Visibility != "assigned" || stored.VendorID != vendor.ID || !slices.Equal(stored.Projects, []string{a}) {
		t.Errorf("read back, the vendor member is %+v", stored)
	}
	invite(map[string]any{"email": "x@lingua.example", "roles": []string{"translator"}, "vendor_id": vendor.ID}).
		want(t, http.StatusBadRequest, "vendor_member_visibility")
	invite(map[string]any{"email": "x@lingua.example", "roles": []string{"reviewer"}, "visibility": "assigned"}).
		want(t, http.StatusBadRequest, "assigned_visibility_role")
	invite(map[string]any{"email": "x@lingua.example", "roles": []string{"owner"}, "projects": []string{a}}).
		want(t, http.StatusBadRequest, "owner_project_scoped")
	invite(map[string]any{"email": "x@lingua.example", "roles": []string{"translator"}, "projects": []string{"shop"}}).
		want(t, http.StatusBadRequest, "invalid_project_scope")
	invite(map[string]any{"email": "x@lingua.example", "roles": []string{"translator"}, "visibility": "assigned",
		"vendor_id": uuid.NewString()}).want(t, http.StatusNotFound, "not_found")
	var plain memberJSON
	invite(map[string]any{"email": "tom@example.com", "roles": []string{"translator"}}).decode(t, &plain)
	if plain.Visibility != "all" || len(plain.Projects) != 0 || plain.VendorID != "" {
		t.Errorf("an unrestricted invitation reads %+v", plain)
	}

	as("DELETE", base+"/vendors/"+vendor.ID, nil).want(t, http.StatusConflict, "vendor_has_members")

	// A member's restriction changes on its own, never with their access.
	as("PATCH", base+"/members/"+vera.ID, map[string]any{"roles": []string{"translator"}, "projects": []string{b}}, "If-Match", `"1"`).
		want(t, http.StatusBadRequest, "access_and_restriction")
	r = as("PATCH", base+"/members/"+vera.ID, map[string]any{"projects": []string{b}}, "If-Match", `"1"`)
	r.want(t, http.StatusOK, "")
	r.decode(t, &stored)
	if !slices.Equal(stored.Projects, []string{b}) || stored.Visibility != "assigned" || stored.VendorID != vendor.ID {
		t.Errorf("after changing projects the member is %+v", stored)
	}

	// ── groups ──
	var group struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Members []string `json:"members"`
	}
	r = as("POST", base+"/groups", map[string]any{"name": "de reviewers"})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &group)
	as("POST", base+"/groups", map[string]any{"name": ""}).want(t, http.StatusBadRequest, "invalid_group_name")
	gp := base + "/groups/" + group.ID
	r = as("PUT", gp+"/members/"+plain.ID, nil)
	r.want(t, http.StatusOK, "")
	r.decode(t, &group)
	if !slices.Equal(group.Members, []string{plain.ID}) {
		t.Errorf("group members = %v", group.Members)
	}
	as("PUT", gp+"/members/"+plain.ID, nil).want(t, http.StatusOK, "") // idempotent
	as("PUT", gp+"/members/"+uuid.NewString(), nil).want(t, http.StatusNotFound, "not_found")
	etag := as("GET", gp, nil).header.Get("ETag")
	r = as("PATCH", gp, map[string]any{"name": "German reviewers"}, "If-Match", etag)
	r.want(t, http.StatusOK, "")
	r.decode(t, &group)
	if group.Name != "German reviewers" {
		t.Errorf("renamed group = %+v", group)
	}
	as("PATCH", gp, map[string]any{"name": "x"}, "If-Match", etag).want(t, http.StatusPreconditionFailed, "precondition_failed")
	var groups struct{ Items []struct{ ID string } }
	as("GET", base+"/groups", nil).decode(t, &groups)
	if len(groups.Items) != 1 {
		t.Errorf("groups = %+v", groups.Items)
	}
	as("DELETE", gp+"/members/"+plain.ID, nil).want(t, http.StatusNoContent, "")
	as("DELETE", gp+"/members/"+plain.ID, nil).want(t, http.StatusNotFound, "not_in_group")

	// ── who may ──
	tom := s.signIn("tom@example.com")
	s.do(call{method: "GET", path: base + "/groups", cookie: tom.cookie}).want(t, http.StatusOK, "")
	s.do(call{method: "POST", path: base + "/groups", cookie: tom.cookie, csrf: tom.csrf, body: map[string]any{"name": "mine"}}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "POST", path: base + "/vendors", cookie: tom.cookie, csrf: tom.csrf, body: map[string]any{"name": "mine"}}).
		want(t, http.StatusForbidden, "forbidden")
	veraSession := s.signIn("vera@lingua.example")
	for _, p := range []string{"/groups", "/vendors", "/vendors/" + vendor.ID, "/members"} {
		s.do(call{method: "GET", path: base + p, cookie: veraSession.cookie}).want(t, http.StatusForbidden, "forbidden")
	}
	// An admin token manages members, not vendors.
	var tok struct{ Secret string }
	as("POST", base+"/tokens", map[string]any{"name": "admin", "scopes": []string{"admin"}}).decode(t, &tok)
	s.do(call{method: "POST", path: base + "/vendors", bearer: tok.Secret, body: map[string]any{"name": "Other"}}).
		want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "POST", path: base + "/members", bearer: tok.Secret,
		body: map[string]any{"email": "y@lingua.example", "roles": []string{"translator"}, "visibility": "assigned", "vendor_id": vendor.ID}}).
		want(t, http.StatusForbidden, "forbidden")

	as("DELETE", gp, nil).want(t, http.StatusNoContent, "")
	as("GET", gp, nil).want(t, http.StatusNotFound, "not_found")
	as("DELETE", base+"/members/"+vera.ID, nil).want(t, http.StatusNoContent, "")
	as("DELETE", base+"/vendors/"+vendor.ID, nil).want(t, http.StatusNoContent, "")
	as("GET", base+"/vendors/"+vendor.ID, nil).want(t, http.StatusNotFound, "not_found")
}
