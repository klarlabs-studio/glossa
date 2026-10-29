//go:build integration

package main

import (
	"net/http"
	"slices"
	"testing"
)

// Assignments and approvals end to end over HTTP against Postgres (RFC
// 0006 §3, §8; the path §12.1 and §12.2 walk): an admin makes a vendor
// and a group, invites the vendor's translator with visibility
// `assigned` scoped to one project, and gives them units; the vendor's
// member sees exactly that work, translates it and completes it; then
// an approval of the text cannot be decided by a token or by its
// author, and two distinct reviewers grant it.
func TestAssignmentsAndApprovalsOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	as := func(who session, method, path string, body any, headers ...string) reply {
		c := call{method: method, path: path, cookie: who.cookie, csrf: who.csrf, body: body}
		if len(headers) == 2 {
			c.headers = map[string]string{headers[0]: headers[1]}
		}
		return s.do(c)
	}
	project := func(slug string, keys ...string) (string, string) {
		var p struct{ ID string }
		r := as(ada, "POST", base+"/projects", map[string]any{"slug": slug, "name": slug, "source_locale": "en",
			"settings": map[string]any{"default_syntax": "mf1", "review_required": false}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &p)
		path := base + "/projects/" + p.ID
		as(ada, "POST", path+"/locales", map[string]string{"code": "de"}).want(t, http.StatusCreated, "")
		var items []map[string]any
		for _, k := range keys {
			items = append(items, map[string]any{"key": k, "text": "Text of " + k})
		}
		as(ada, "POST", path+"/message-upserts", map[string]any{"items": items}).want(t, http.StatusOK, "")
		return p.ID, path
	}
	aID, a := project("shop", "pay", "cancel")
	_, b := project("blog", "elsewhere")

	// The vendor, its translator scoped to project A, and three `de`
	// reviewers in a group.
	var vendor struct{ ID string }
	as(ada, "POST", base+"/vendors", map[string]any{"name": "Lingua", "locales": []string{"de"}}).decode(t, &vendor)
	var group struct{ ID string }
	as(ada, "POST", base+"/groups", map[string]any{"name": "de reviewers"}).decode(t, &group)
	invite := func(email string, body map[string]any) (string, session) {
		body["email"] = email
		var m memberJSON
		r := as(ada, "POST", base+"/members", body)
		r.want(t, http.StatusCreated, "")
		r.decode(t, &m)
		return m.ID, s.signIn(email)
	}
	_, vera := invite("vera@lingua.example", map[string]any{"roles": []string{"translator"}, "locales": []string{"de"},
		"vendor_id": vendor.ID, "visibility": "assigned", "projects": []string{aID}})
	reviewers := map[string]session{}
	for _, name := range []string{"rita", "rolf", "ruth"} {
		id, sess := invite(name+"@example.com", map[string]any{"roles": []string{"reviewer"}, "locales": []string{"de"}})
		as(ada, "PUT", base+"/groups/"+group.ID+"/members/"+id, nil).want(t, http.StatusOK, "")
		reviewers[name] = sess
	}

	// Work for the vendor, and work for the group the vendor never sees.
	assign := func(key string, assignee map[string]any) string {
		var x struct{ ID string }
		r := as(ada, "POST", base+"/assignments", map[string]any{"project_id": aID, "assignee": assignee,
			"units": []map[string]string{{"message": key, "locale": "de"}}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &x)
		return x.ID
	}
	theirs := assign("pay", map[string]any{"vendor": "Lingua"})
	notTheirs := assign("cancel", map[string]any{"group": "de reviewers"})

	work := func(who session, query string) []string {
		var page struct{ Items []struct{ ID string } }
		r := as(who, "GET", base+"/assignments"+query, nil)
		r.want(t, http.StatusOK, "")
		r.decode(t, &page)
		var out []string
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		return out
	}
	for _, q := range []string{"", "?mine=true", "?project=" + aID + "&message=pay", "?project=" + aID + "&message=cancel&locale=de"} {
		want := []string{theirs}
		if q == "?project="+aID+"&message=cancel&locale=de" {
			want = nil
		}
		if got := work(vera, q); !slices.Equal(got, want) {
			t.Errorf("the vendor's member lists %v for %q, want %v", got, q, want)
		}
	}
	if got := work(ada, ""); len(got) != 2 {
		t.Errorf("the admin lists %v, want both", got)
	}
	if got := work(reviewers["rita"], ""); !slices.Equal(got, []string{notTheirs}) {
		t.Errorf("a reviewer in the group lists %v, want the group's %s", got, notTheirs)
	}
	as(vera, "GET", base+"/assignments/"+theirs, nil).want(t, http.StatusOK, "")
	as(vera, "GET", base+"/assignments/"+notTheirs, nil).want(t, http.StatusNotFound, "not_found")
	// Acting on it answers as reading it does: a 403 would say it exists.
	as(vera, "POST", base+"/assignments/"+notTheirs+"/completion", nil).want(t, http.StatusNotFound, "not_found")
	as(vera, "POST", base+"/assignments", map[string]any{"project_id": aID, "assignee": map[string]any{"vendor": "Lingua"},
		"units": []map[string]string{{"message": "cancel", "locale": "de"}}}).want(t, http.StatusForbidden, "forbidden")

	// Nothing else is theirs: not project B, not another unit.
	as(vera, "GET", b, nil).want(t, http.StatusNotFound, "not_found")
	as(vera, "GET", a+"/messages/cancel", nil).want(t, http.StatusNotFound, "not_found")
	as(vera, "PUT", a+"/messages/cancel/translations/de", map[string]string{"text": "Abbrechen"}).want(t, http.StatusNotFound, "not_found")
	as(vera, "GET", base+"/approvals", nil).want(t, http.StatusForbidden, "forbidden")
	as(vera, "GET", base+"/vendors", nil).want(t, http.StatusForbidden, "forbidden")

	// They translate the unit, take the work on and complete it.
	as(vera, "PUT", a+"/messages/pay/translations/de", map[string]string{"text": "Bezahlen"}).want(t, http.StatusCreated, "")
	as(vera, "POST", base+"/assignments/"+theirs+"/acceptance", nil).want(t, http.StatusOK, "")
	var done struct{ State string }
	as(vera, "POST", base+"/assignments/"+theirs+"/completion", nil).decode(t, &done)
	if done.State != "done" {
		t.Fatalf("after completing, the assignment is %q", done.State)
	}
	as(vera, "POST", base+"/assignments/"+theirs+"/completion", nil).want(t, http.StatusConflict, "assignment_state")
	// Completed within 30 days, the unit stays theirs to read.
	as(vera, "GET", a+"/messages/pay/translations/de", nil).want(t, http.StatusOK, "")

	// The approval: two of the group, four-eyes.
	var approval struct {
		ID        string
		State     string
		Decisions []struct{ Principal, Decision string }
	}
	r := as(ada, "POST", base+"/approvals", map[string]any{"project_id": aID, "message": "pay", "locale": "de", "n": 2,
		"from": map[string]any{"group": "de reviewers"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &approval)
	decide := func(c call) reply {
		c.method, c.path = "POST", base+"/approvals/"+approval.ID+"/decisions"
		c.body = map[string]any{"decision": "granted", "reason": "reads well"}
		return s.do(c)
	}
	var tok struct{ Secret string }
	as(ada, "POST", base+"/tokens", map[string]any{"name": "everything",
		"scopes": []string{"read", "write", "publish", "admin", "workflows"}}).decode(t, &tok)
	decide(call{bearer: tok.Secret}).want(t, http.StatusForbidden, "person_required")
	decide(call{cookie: vera.cookie, csrf: vera.csrf}).want(t, http.StatusForbidden, "forbidden")
	decide(call{cookie: ada.cookie, csrf: ada.csrf}).want(t, http.StatusForbidden, "not_eligible")

	// Rita rewrites the text, and so cannot approve it.
	rita := reviewers["rita"]
	cur := as(rita, "GET", a+"/messages/pay/translations/de", nil)
	as(rita, "PUT", a+"/messages/pay/translations/de", map[string]string{"text": "Jetzt bezahlen"}, "If-Match", cur.header.Get("ETag")).
		want(t, http.StatusOK, "")
	decide(call{cookie: rita.cookie, csrf: rita.csrf}).want(t, http.StatusForbidden, "own_text")

	rolf, ruth := reviewers["rolf"], reviewers["ruth"]
	decide(call{cookie: rolf.cookie, csrf: rolf.csrf}).want(t, http.StatusCreated, "")
	decide(call{cookie: rolf.cookie, csrf: rolf.csrf}).want(t, http.StatusCreated, "") // counts once
	as(rolf, "GET", base+"/approvals/"+approval.ID, nil).decode(t, &approval)
	if approval.State != "pending" || len(approval.Decisions) != 1 {
		t.Fatalf("after one reviewer the approval is %+v", approval)
	}
	decide(call{cookie: ruth.cookie, csrf: ruth.csrf}).decode(t, &approval)
	if approval.State != "granted" || len(approval.Decisions) != 2 || approval.Decisions[0].Principal == approval.Decisions[1].Principal {
		t.Fatalf("after two distinct reviewers the approval is %+v", approval)
	}
	var inbox struct{ Items []struct{ ID, State string } }
	as(ruth, "GET", base+"/approvals?project="+aID+"&message=pay&locale=de", nil).decode(t, &inbox)
	if len(inbox.Items) != 1 || inbox.Items[0].ID != approval.ID || inbox.Items[0].State != "granted" {
		t.Errorf("the inbox = %+v", inbox.Items)
	}
	decide(call{cookie: ada.cookie, csrf: ada.csrf}).want(t, http.StatusConflict, "approval_closed")
}
