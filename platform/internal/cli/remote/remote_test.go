package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/fortify/retry"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

const token = "glossa_api_0123456789012345678901234567890123456789abc"

func newClient(t *testing.T, h http.Handler) *remote.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := remote.New(srv.URL, token, remote.Options{UserAgent: "glossa-cli/test",
		Retry: retry.Config{MaxAttempts: 3, InitialDelay: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMessagesPagesAndAuthenticates(t *testing.T) {
	var pages atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("User-Agent") != "glossa-cli/test" {
			t.Errorf("headers = %v", r.Header)
		}
		if r.URL.Path != "/v1/tenants/t1/projects/p1/messages" || r.URL.Query().Get("missing_in") != "de" {
			t.Errorf("request = %s", r.URL)
		}
		pages.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page_token") == "" {
			_, _ = io.WriteString(w, `{"items":[{"key":"a"}],"next_page_token":"n1"}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"key":"b"}]}`)
	}))
	msgs, err := c.Messages(context.Background(), remote.Scope{Tenant: "t1", Project: "p1"}, remote.MessageFilter{MissingIn: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Key != "a" || msgs[1].Key != "b" || pages.Load() != 2 {
		t.Errorf("messages = %+v after %d pages", msgs, pages.Load())
	}
}

func TestProblemDetailsBecomeAPIErrors(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"urn:glossa:problem:unauthenticated","code":"unauthenticated","title":"Unauthenticated","status":401,"detail":"token expired"}`)
	}))
	_, err := c.Tenants(context.Background())
	var ae *remote.APIError
	if !errors.As(err, &ae) || ae.Status != 401 || ae.Code != "unauthenticated" || ae.Detail != "token expired" {
		t.Fatalf("err = %#v", err)
	}
	if !strings.Contains(ae.URL, "/v1/tenants") {
		t.Errorf("URL = %s", ae.URL)
	}
}

func TestRetriesIdempotentWritesButNotOthers(t *testing.T) {
	var calls atomic.Int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"key":"a"`) {
			t.Errorf("attempt %d lost the body: %s", n, body)
		}
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"key":"a","status":"created"}]}`)
	}))
	res, err := c.UpsertMessages(context.Background(), remote.Scope{Tenant: "t", Project: "p"},
		[]remote.MessageUpsertItem{{Key: "a", Text: "A"}})
	if err != nil || len(res) != 1 || res[0].Status != "created" || calls.Load() != 3 {
		t.Fatalf("res = %+v err = %v calls = %d", res, err, calls.Load())
	}

	calls.Store(0)
	c = newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	_, _, err = c.AddLocale(context.Background(), remote.Scope{Tenant: "t", Project: "p"}, "de")
	var ae *remote.APIError
	if !errors.As(err, &ae) || ae.Status != 503 || calls.Load() != 1 {
		t.Errorf("AddLocale err = %v after %d calls, want one 503", err, calls.Load())
	}
}

func TestUpsertBatchesAt500(t *testing.T) {
	var sizes []int
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Items []struct{ Key string } `json:"items"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sizes = append(sizes, len(req.Items))
		res := make([]map[string]string, len(req.Items))
		for i, it := range req.Items {
			res[i] = map[string]string{"key": it.Key, "status": "unchanged"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": res})
	}))
	items := make([]remote.MessageUpsertItem, 1001)
	for i := range items {
		items[i] = remote.MessageUpsertItem{Key: "k", Text: "t"}
	}
	res, err := c.UpsertMessages(context.Background(), remote.Scope{Tenant: "t", Project: "p"}, items)
	if err != nil || len(res) != 1001 || len(sizes) != 3 || sizes[0] != 500 || sizes[2] != 1 {
		t.Fatalf("results %d, batches %v, err %v", len(res), sizes, err)
	}
}

func TestResolveProjectBySlugOrID(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"id":"p1","slug":"shop","name":"Shop","source_locale":"de"}]}`)
	}))
	for _, ref := range []string{"shop", "p1"} {
		p, err := c.ResolveProject(context.Background(), "t", ref)
		if err != nil || p.Id != "p1" {
			t.Errorf("%s: %+v %v", ref, p, err)
		}
	}
	var nf *remote.ErrProjectNotFound
	if _, err := c.ResolveProject(context.Background(), "t", "nope"); !errors.As(err, &nf) {
		t.Errorf("unknown project err = %v", err)
	}
}

func TestNetworkFailureIsAnAPIErrorWithoutStatus(t *testing.T) {
	c, err := remote.New("http://127.0.0.1:1", token, remote.Options{Retry: retry.Config{MaxAttempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Tenants(context.Background())
	var ae *remote.APIError
	if !errors.As(err, &ae) || ae.Status != 0 {
		t.Fatalf("err = %v", err)
	}
}
