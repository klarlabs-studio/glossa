//go:build integration

package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// contextLoop is M3's usage upload against the real server (RFC 0004
// §6.3): the bundler plugin's document through `glossa context push`,
// then `glossa extract --upload`, both answered by the Context API; the
// usages are then readable where a translator looks for them.
func contextLoop(t *testing.T, r runner, s *server, owner session, p string) {
	s.do(owner.call("POST", p+"/applications", map[string]any{"slug": "web", "name": "Web", "platform": "web"}), http.StatusCreated, nil)
	commit := strings.Repeat("c0ffee12", 5)
	doc := `{"schema":"glossa.usages/v1","application":"web","commit":"` + commit + `","branch":"main",
	  "tool":{"name":"@klarlabs-studio/glossa","version":"0.1.0"},
	  "usages":[
	    {"key":"checkout.pay","file":"src/Checkout.vue","line":12,"column":7,"component":"Checkout","route":"/checkout","kind":"t"},
	    {"key":"gone.key","file":"src/Checkout.vue","line":20,"column":7,"component":"Checkout","route":"/checkout","kind":"t"}]}`
	if err := os.MkdirAll(filepath.Join(r.dir, ".glossa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, ".glossa", "usages.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var pushed struct {
		Source   string `json:"source"`
		Replayed bool   `json:"replayed"`
		Build    struct {
			ID              string `json:"id"`
			Usages          int    `json:"usages"`
			UnknownKeys     int    `json:"unknown_keys"`
			OnDefaultBranch bool   `json:"on_default_branch"`
		} `json:"build"`
	}
	r.run(cli.ExitOK, &pushed, "context", "push", ".glossa/usages.json")
	if pushed.Source != "plugin" || pushed.Replayed || pushed.Build.Usages != 2 || pushed.Build.UnknownKeys != 1 || !pushed.Build.OnDefaultBranch {
		t.Fatalf("context push = %+v", pushed)
	}
	first := pushed.Build.ID
	r.run(cli.ExitOK, &pushed, "context", "push", ".glossa/usages.json")
	if !pushed.Replayed || pushed.Build.ID != first {
		t.Errorf("second push = %+v", pushed)
	}

	// extract finds nothing here (the generated accessors are skipped),
	// and still records an extract build of the commit.
	r.run(cli.ExitOK, nil, "extract", "--upload", "--application", "web", "--commit", commit, "--branch", "main")

	var usages struct {
		Usages []struct {
			File, Component, Route string
		} `json:"usages"`
	}
	s.do(owner.call("GET", p+"/messages/checkout.pay/usages", nil), http.StatusOK, &usages)
	if len(usages.Usages) != 1 || usages.Usages[0].File != "src/Checkout.vue" || usages.Usages[0].Route != "/checkout" {
		t.Errorf("usages of checkout.pay = %+v", usages)
	}
	var builds struct {
		Items []struct{ Source string } `json:"items"`
	}
	s.do(owner.call("GET", p+"/context-builds", nil), http.StatusOK, &builds)
	if len(builds.Items) != 2 || builds.Items[0].Source != "extract" || builds.Items[1].Source != "plugin" {
		t.Errorf("builds = %+v", builds)
	}
	captureLoop(t, r, s, owner, p, commit)
}

// captureLoop is `glossa capture --upload`'s call against the real
// server on MinIO (RFC 0004 §3.2–§3.3): the manifest and its image over
// multipart, stored re-encoded and read back through the API.
func captureLoop(t *testing.T, r runner, s *server, owner session, p, commit string) {
	ids := strings.Split(p, "/") // /v1/tenants/{tenant}/projects/{project}
	scope := remote.Scope{Tenant: ids[3], Project: ids[5]}
	c, err := remote.New(s.base, r.env["GLOSSA_TOKEN"], remote.Options{})
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for x := range 40 {
		img.Set(x, x%30, color.NRGBA{R: 200, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	digest := hex.EncodeToString(sum[:])
	manifest := `{"schema":"glossa.captures/v1","application":"web","commit":"` + commit + `","branch":"main",
	  "tool":{"name":"glossa","version":"0.9.0"},
	  "captures":[{"route":"/checkout","url":"http://localhost:4173/checkout","locale":"en",
	    "viewport":{"width":40,"height":30},"image":{"sha256":"` + digest + `","width":40,"height":30},
	    "renders":[{"index":0,"key":"checkout.pay","locale":"en"}],
	    "regions":[{"index":0,"kind":"text","box":{"x":2,"y":3,"width":20.5,"height":8},"visible":true}]}]}`
	upload := func() remote.UploadedCaptures {
		t.Helper()
		out, err := c.UploadCaptures(context.Background(), scope, []byte(manifest), map[string]io.Reader{digest: bytes.NewReader(buf.Bytes())})
		if err != nil {
			t.Fatalf("upload captures: %v", err)
		}
		return out
	}
	first := upload()
	if first.Replayed || first.Captures != 1 || first.ImagesStored != 1 || first.Build.Source != "capture" {
		t.Fatalf("capture upload = %+v", first)
	}
	if again := upload(); !again.Replayed || again.Build.Id != first.Build.Id {
		t.Errorf("second upload = %+v", again)
	}
	var captures struct {
		Captures []struct {
			Image struct {
				URL    string `json:"url"`
				Digest string `json:"digest"`
			} `json:"image"`
			Regions []struct {
				Box struct{ Width int } `json:"box"`
			} `json:"regions"`
		} `json:"captures"`
	}
	s.do(owner.call("GET", p+"/messages/checkout.pay/captures", nil), http.StatusOK, &captures)
	if len(captures.Captures) != 1 || len(captures.Captures[0].Regions) != 1 || captures.Captures[0].Regions[0].Box.Width != 21 {
		t.Fatalf("captures of checkout.pay = %+v", captures)
	}
	h := s.do(owner.call("GET", captures.Captures[0].Image.URL, nil), http.StatusOK, nil)
	if h.Get("Content-Type") != "image/png" || h.Get("ETag") != `"`+captures.Captures[0].Image.Digest+`"` {
		t.Errorf("image headers = %v", h)
	}
}
