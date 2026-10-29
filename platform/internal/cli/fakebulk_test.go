package cli

import (
	"net/http"
	"slices"
	"strconv"
)

// The fake server's bulk reads: the project-wide translation listing
// (paged by bulkPageSize so the CLI must follow next_page_token) and the
// per-locale stats.

// bulkPageSize is the fake's page size for the translation listing,
// small so tests page.
const bulkPageSize = 2

func (f *fakeServer) listProjectTranslations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	locales := q["locale"]
	if len(locales) == 0 || len(locales) > 20 {
		problemResp(w, 400, "too_many_locales", "1 to 20 locales")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.messages))
	for k := range f.messages {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var items []map[string]any
	for _, k := range keys {
		m := f.messages[k]
		if ms := q.Get("message_state"); ms != "" && m.state != ms {
			continue
		}
		for _, l := range locales {
			t := f.translations[l][k]
			if t == nil || (len(q["state"]) > 0 && !slices.Contains(q["state"], t.state)) {
				continue
			}
			item := f.translationJSON(k, l, t)
			item["key"], item["namespace"], item["message_state"] = k, "default", m.state
			items = append(items, item)
		}
	}
	offset, _ := strconv.Atoi(q.Get("page_token"))
	end := min(offset+bulkPageSize, len(items))
	page := map[string]any{"items": append([]map[string]any{}, items[offset:end]...)}
	if end < len(items) {
		page["next_page_token"] = strconv.Itoa(end)
	}
	writeJSONResp(w, 200, page)
}

func (f *fakeServer) translationStats(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	active := 0
	for _, m := range f.messages {
		if m.state == "active" {
			active++
		}
	}
	var locales []map[string]any
	for _, l := range f.locales {
		st := map[string]int{"draft": 0, "needs_review": 0, "approved": 0, "rejected": 0}
		translated, outdated := 0, 0
		if l == f.sourceLocale {
			translated, st["approved"] = active, active
		}
		for k, t := range f.translations[l] {
			m := f.messages[k]
			if m == nil || m.state != "active" || l == f.sourceLocale {
				continue
			}
			st[t.state]++
			if t.state == "rejected" {
				continue
			}
			translated++
			if t.sourceRevision < m.revision {
				outdated++
			}
		}
		loc := f.localeJSON(l)
		loc["translated"], loc["missing"], loc["outdated"], loc["states"] = translated, active-translated, outdated, st
		locales = append(locales, loc)
	}
	writeJSONResp(w, 200, map[string]any{"messages": active, "locales": locales})
}
