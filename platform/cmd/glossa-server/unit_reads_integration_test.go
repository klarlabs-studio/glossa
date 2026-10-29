//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestVendorUnitReads holds the unit workspace's reads — TM matches and
// AI suggestions of one translation unit — to RFC 0006 §3.3: an assigned
// member reaches the units of their assignment and nothing else, and the
// memory tells them no key or id of what it matched.
func TestVendorUnitReads(t *testing.T) {
	f := newRestrictedFixture(t)
	s := f.s
	reads := []string{"tm-matches", "ai-suggestions"}

	// A second message with the same source, approved in German, is what
	// the memory remembers for the vendor's unit (pay in de).
	s.do(call{method: "POST", path: f.a + "/message-upserts", cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]any{"items": []map[string]any{{"key": "secret-sibling", "text": "Text of pay"}}}}).want(t, http.StatusOK, "")
	s.do(call{method: "PUT", path: f.a + "/messages/secret-sibling/translations/de", cookie: f.ada.cookie, csrf: f.ada.csrf,
		body: map[string]string{"text": "Zahlen sibling"}}).want(t, http.StatusCreated, "")

	type matches struct {
		SourceNormalized string      `json:"source_normalized"`
		Items            []tmMatchJS `json:"items"`
	}
	tm := f.a + "/messages/pay/translations/de/tm-matches"

	// The memory is derived through the outbox: wait for the owner to see
	// the sibling's unit, with the key the owner may know.
	var owner matches
	deadline := time.Now().Add(20 * time.Second)
	for {
		r := s.do(call{method: "GET", path: tm, cookie: f.ada.cookie})
		r.want(t, http.StatusOK, "")
		owner = matches{}
		r.decode(t, &owner)
		if hasKey(owner.Items, "secret-sibling") || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !hasKey(owner.Items, "secret-sibling") {
		t.Fatalf("the owner's matches = %+v, want the sibling's exact match with its key", owner.Items)
	}

	t.Run("the vendor reads the matches of their unit, as text and score", func(t *testing.T) {
		r := s.do(call{method: "GET", path: tm, cookie: f.vera.cookie})
		r.want(t, http.StatusOK, "")
		var got matches
		r.decode(t, &got)
		var sibling *tmMatchJS
		for i := range got.Items {
			if got.Items[i].TargetText == "Zahlen sibling" {
				sibling = &got.Items[i]
			}
		}
		if sibling == nil || sibling.Score < 100 || !sibling.ProjectScoped {
			t.Fatalf("the vendor's matches = %+v, want the sibling's exact match as text", got.Items)
		}
		body := string(r.body)
		for _, leak := range []string{"secret-sibling", "message_key", `"unit`, "translation_id", "message_id", "tm_unit"} {
			if strings.Contains(body, leak) {
				t.Errorf("the vendor's matches reveal %q: %s", leak, body)
			}
		}
	})
	t.Run("a unit outside the assignment is not found, in every read", func(t *testing.T) {
		for _, read := range reads {
			for _, unit := range []string{
				"/messages/pay/translations/fr/",     // another locale of an assigned message
				"/messages/cancel/translations/de/",  // another message
				"/messages/missing/translations/de/", // no such message
			} {
				s.do(call{method: "GET", path: f.a + unit + read, cookie: f.vera.cookie}).want(t, http.StatusNotFound, "not_found")
			}
			// Project B, where the vendor holds nothing.
			s.do(call{method: "GET", path: f.b + "/messages/elsewhere/translations/de/" + read, cookie: f.vera.cookie}).
				want(t, http.StatusNotFound, "not_found")
		}
	})
	t.Run("the vendor reads the suggestions of their unit", func(t *testing.T) {
		r := s.do(call{method: "GET", path: f.a + "/messages/pay/translations/de/ai-suggestions", cookie: f.vera.cookie})
		r.want(t, http.StatusOK, "")
		if !strings.Contains(string(r.body), `"items"`) {
			t.Errorf("suggestions body = %s, want an items list", r.body)
		}
	})
	t.Run("a token scoped to project A reads its units, and not project B's", func(t *testing.T) {
		s.do(call{method: "GET", path: tm, bearer: f.scopedToken}).want(t, http.StatusOK, "")
		s.do(call{method: "GET", path: f.b + "/messages/elsewhere/translations/de/tm-matches", bearer: f.scopedToken}).
			want(t, http.StatusNotFound, "not_found")
	})
}

type tmMatchJS struct {
	Score         int    `json:"score"`
	Target        string `json:"target"`
	TargetText    string `json:"target_text"`
	MessageKey    string `json:"message_key"`
	ProjectScoped bool   `json:"project_scoped"`
}

func hasKey(items []tmMatchJS, key string) bool {
	for _, m := range items {
		if m.MessageKey == key {
			return true
		}
	}
	return false
}
