//go:build integration

package main

import (
	"context"
	"net/http"
	"testing"
)

// Studio lists the person's passkeys from the server instead of
// guessing per browser, and removes them one at a time.
func TestPasskeysOfMeOverHTTP(t *testing.T) {
	s := startServer(t)
	ada, bob := s.signIn("ada@example.com"), s.signIn("bob@example.com")
	var me struct {
		Person struct {
			ID string `json:"id"`
		} `json:"person"`
	}
	s.do(call{method: "GET", path: "/v1/me", cookie: ada.cookie}).decode(t, &me)
	// Registration needs a real authenticator; seed what it would store.
	for i, name := range []string{"MacBook Touch ID", "YubiKey"} {
		if _, err := s.db.Super.Exec(context.Background(), `INSERT INTO identity_passkeys
			(credential_id, person_id, public_key, name, created_at, last_used_at)
			VALUES ($1, $2, '\x01', $3, now() + make_interval(secs => $4), CASE WHEN $4 = 1 THEN now() END)`,
			[]byte{0xca, 0xfe, byte(i)}, me.Person.ID, name, i); err != nil {
			t.Fatal(err)
		}
	}

	var list struct {
		Items []struct {
			ID         string  `json:"id"`
			Name       string  `json:"name"`
			CreatedAt  string  `json:"created_at"`
			LastUsedAt *string `json:"last_used_at"`
		} `json:"items"`
		NextPageToken *string `json:"next_page_token"`
	}
	r := s.do(call{method: "GET", path: "/v1/me/passkeys?page_size=1", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].Name != "MacBook Touch ID" || list.Items[0].ID != "yv4A" ||
		list.Items[0].LastUsedAt != nil || list.NextPageToken == nil {
		t.Fatalf("first page = %s", r.body)
	}
	r = s.do(call{method: "GET", path: "/v1/me/passkeys?page_size=1&page_token=" + *list.NextPageToken, cookie: ada.cookie})
	list.NextPageToken = nil
	r.decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].Name != "YubiKey" || list.Items[0].LastUsedAt == nil || list.NextPageToken != nil {
		t.Fatalf("second page = %s", r.body)
	}
	s.do(call{method: "GET", path: "/v1/me/passkeys?page_token=v1.bm9wZQ", cookie: ada.cookie}).
		want(t, http.StatusBadRequest, "invalid_page_token")
	s.do(call{method: "GET", path: "/v1/me/passkeys"}).want(t, http.StatusUnauthorized, "unauthenticated")

	// Bob sees none of them and can't remove them.
	list.Items = nil
	s.do(call{method: "GET", path: "/v1/me/passkeys", cookie: bob.cookie}).decode(t, &list)
	if len(list.Items) != 0 {
		t.Errorf("bob sees %+v", list.Items)
	}
	s.do(call{method: "DELETE", path: "/v1/me/passkeys/yv4A", cookie: bob.cookie, csrf: bob.csrf}).
		want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "DELETE", path: "/v1/me/passkeys/yv4A", cookie: ada.cookie}).want(t, http.StatusForbidden, "csrf_invalid")
	s.do(call{method: "DELETE", path: "/v1/me/passkeys/yv4A", cookie: ada.cookie, csrf: ada.csrf}).want(t, http.StatusNoContent, "")
	s.do(call{method: "DELETE", path: "/v1/me/passkeys/yv4A", cookie: ada.cookie, csrf: ada.csrf}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: "/v1/me/passkeys", cookie: ada.cookie}).decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].Name != "YubiKey" {
		t.Errorf("after removal = %+v", list.Items)
	}
}
