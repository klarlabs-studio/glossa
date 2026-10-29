package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

type receivedPart struct {
	name, contentType, body string
}

func TestUploadCapturesStreamsTheManifestThenTheImages(t *testing.T) {
	var (
		mu    sync.Mutex
		got   []receivedPart
		calls int
	)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/glossa/v1/tenants/t1/projects/p1/captures" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("no token: %v", r.Header)
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("not multipart: %v", err)
		}
		mu.Lock()
		got = nil
		calls++
		n := calls
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(p)
			got = append(got, receivedPart{p.FormName(), p.Header.Get("Content-Type"), string(b)})
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusCreated
		if n > 1 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"build": map[string]any{"id": "b1", "source": "capture"}, "captures": 2, "images_stored": 1,
			"images_deduplicated": 1, "unknown_keys": []string{"gone.key"},
		})
	}))
	t.Cleanup(api.Close)
	c, err := remote.New(api.URL+"/glossa", token, remote.Options{})
	if err != nil {
		t.Fatal(err)
	}
	images := func() map[string]io.Reader {
		return map[string]io.Reader{"bb": strings.NewReader("png-b"), "aa": strings.NewReader("png-a")}
	}
	s := remote.Scope{Tenant: "t1", Project: "p1"}
	out, err := c.UploadCaptures(context.Background(), s, []byte(`{"schema":"glossa.captures/v1"}`), images())
	if err != nil {
		t.Fatal(err)
	}
	if out.Replayed || out.Build.Id != "b1" || out.Captures != 2 || out.ImagesStored != 1 || out.ImagesDeduplicated != 1 ||
		len(out.UnknownKeys) != 1 {
		t.Errorf("upload = %+v", out)
	}
	want := []receivedPart{
		{"manifest", "application/json", `{"schema":"glossa.captures/v1"}`},
		{"aa", "image/png", "png-a"},
		{"bb", "image/png", "png-b"},
	}
	if len(got) != len(want) {
		t.Fatalf("parts = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("part %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	again, err := c.UploadCaptures(context.Background(), s, []byte(`{}`), images())
	if err != nil || !again.Replayed {
		t.Errorf("replay = %+v, %v", again, err)
	}
	j, _ := json.Marshal(again)
	if !strings.Contains(string(j), `"replayed":true`) || !strings.Contains(string(j), `"images_stored":1`) {
		t.Errorf("--json shape = %s", j)
	}
}

func TestUploadCapturesReportsProblemsAndBrokenImages(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"invalid_captures","detail":"no part holds image aa"}`)
	}))
	t.Cleanup(api.Close)
	c, err := remote.New(api.URL, token, remote.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := remote.Scope{Tenant: "t1", Project: "p1"}
	_, err = c.UploadCaptures(context.Background(), s, []byte(`{}`), nil)
	var apiErr *remote.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || apiErr.Code != "invalid_captures" {
		t.Errorf("err = %v", err)
	}
	boom := errors.New("disk gone")
	_, err = c.UploadCaptures(context.Background(), s, []byte(`{}`), map[string]io.Reader{"aa": failing{boom}})
	if !errors.As(err, &apiErr) || !errors.Is(err, boom) {
		t.Errorf("a failing image: err = %v", err)
	}
}

type failing struct{ err error }

func (f failing) Read([]byte) (int, error) { return 0, f.err }
