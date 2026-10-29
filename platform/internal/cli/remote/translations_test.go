package remote_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// The bulk listing takes at most 20 locales a request: more are asked
// for in chunks, each paged to the end.
func TestProjectTranslationsChunksLocalesAndPages(t *testing.T) {
	var (
		mu       sync.Mutex
		requests [][]string
	)
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/v1/tenants/t1/projects/p1/translations" || q.Get("message_state") != "active" ||
			q.Get("page_size") != "100" || !slices.Equal(q["state"], []string{"approved"}) {
			t.Errorf("request = %s", r.URL)
		}
		locales := q["locale"]
		mu.Lock()
		requests = append(requests, locales)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if q.Get("page_token") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":           []map[string]any{{"key": "a", "locale": locales[0], "message_id": "m1"}},
				"next_page_token": "n1",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"key": "b", "locale": locales[0], "message_id": "m2"}}})
	}))
	locales := make([]string, 25)
	for i := range locales {
		locales[i] = fmt.Sprintf("l%02d", i)
	}
	trs, err := c.ProjectTranslations(context.Background(), remote.Scope{Tenant: "t1", Project: "p1"}, locales,
		remote.TranslationFilter{MessageState: "active", States: []string{"approved"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(trs) != 4 || trs[0].Key != "a" || trs[1].Key != "b" || trs[2].Locale != "l20" {
		t.Errorf("translations %+v", trs)
	}
	if len(requests) != 4 || len(requests[0]) != 20 || len(requests[2]) != 5 {
		t.Errorf("requests %v: want two pages of 20 locales, then two of 5", requests)
	}
}

// The bounded read asks each chunk of locales for one page, never
// follows a page token, and says whether there was another page.
func TestFirstProjectTranslationsReadsOnePagePerChunk(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
	)
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("message_state") != "obsolete" || q.Get("page_size") != "7" || q.Get("page_token") != "" {
			t.Errorf("request = %s", r.URL)
		}
		mu.Lock()
		requests++
		first := requests == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		page := map[string]any{"items": []map[string]any{{"key": "old", "locale": q["locale"][0], "message_id": "m1"}}}
		if !first {
			page["next_page_token"] = "n1"
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	locales := make([]string, 25)
	for i := range locales {
		locales[i] = fmt.Sprintf("l%02d", i)
	}
	trs, more, err := c.FirstProjectTranslations(context.Background(), remote.Scope{Tenant: "t1", Project: "p1"}, locales,
		remote.TranslationFilter{MessageState: "obsolete"}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(trs) != 2 || !more || requests != 2 {
		t.Errorf("translations %+v, more %v, %d requests: want one page per chunk, and the second chunk's more", trs, more, requests)
	}
}

func TestProjectTranslationsWithoutLocalesAsksNothing(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request without locales")
	}))
	trs, err := c.ProjectTranslations(context.Background(), remote.Scope{Tenant: "t", Project: "p"}, nil, remote.TranslationFilter{})
	if err != nil || len(trs) != 0 {
		t.Errorf("%v %v", trs, err)
	}
}

func TestTranslationStats(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tenants/t/projects/p/translation-stats" {
			t.Errorf("request = %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":3,"locales":[{"code":"en","direction":"ltr","is_source":true,"translated":3,"missing":0,"outdated":0,
			"states":{"draft":0,"needs_review":0,"approved":3,"rejected":0}}]}`))
	}))
	st, err := c.TranslationStats(context.Background(), remote.Scope{Tenant: "t", Project: "p"})
	if err != nil || st.Messages != 3 || len(st.Locales) != 1 || st.Locales[0].States.Approved != 3 {
		t.Errorf("%+v %v", st, err)
	}
}
