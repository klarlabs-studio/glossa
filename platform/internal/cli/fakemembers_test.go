package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// fakeMembers is a tenant's members and open invitations, as Identity
// answers them: an address is invited once (409 already_member), and a
// retried Idempotency-Key returns the first invitation.
type fakeMembers struct {
	items []map[string]any
	byKey map[string]map[string]any
	// refuse makes every invitation 403, as for a token without
	// members.manage.
	refuse bool
	posts  int
}

func (f *fakeServer) routeMembers(mux *http.ServeMux) {
	f.members = &fakeMembers{byKey: map[string]map[string]any{}}
	mux.HandleFunc("GET /v1/tenants/ten_1/members", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSONResp(w, 200, map[string]any{"items": f.members.items})
	})
	mux.HandleFunc("POST /v1/tenants/ten_1/members", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		m := f.members
		m.posts++
		if m.refuse {
			problemResp(w, 403, "forbidden", "missing permission members.manage")
			return
		}
		var body struct {
			Email   string   `json:"email"`
			Roles   []string `json:"roles"`
			Locales []string `json:"locales"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			problemResp(w, 400, "invalid_request", err.Error())
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if prior, ok := m.byKey[key]; ok && key != "" {
			w.Header().Set("Idempotent-Replayed", "true")
			writeJSONResp(w, 201, prior)
			return
		}
		for _, it := range m.items {
			if strings.EqualFold(it["email"].(string), body.Email) {
				problemResp(w, 409, "already_member", "that address is already a member or invited")
				return
			}
		}
		if body.Locales == nil {
			body.Locales = []string{}
		}
		now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
		it := map[string]any{"id": fmt.Sprintf("mem_%d", len(m.items)+1), "email": body.Email, "status": "invited",
			"roles": body.Roles, "locales": body.Locales, "projects": []string{}, "visibility": "all",
			"created_at": now, "updated_at": now}
		m.items = append(m.items, it)
		if key != "" {
			m.byKey[key] = it
		}
		writeJSONResp(w, 201, it)
	})
}

// member adds an existing member.
func (f *fakeServer) member(email string, roles ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	f.members.items = append(f.members.items, map[string]any{"id": fmt.Sprintf("mem_%d", len(f.members.items)+1),
		"email": email, "status": "active", "roles": roles, "locales": []string{}, "projects": []string{},
		"visibility": "all", "created_at": now, "updated_at": now})
}
