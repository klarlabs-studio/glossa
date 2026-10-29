package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
)

// fakeContext is the Context API's usage uploads, faithful to the
// contract's shapes: POST …/context-builds?source=… with a
// glossa.usages/v1 body answers 201 with the build, or 200 with
// Idempotent-Replayed for the same document again.
type fakeContext struct {
	uploads []fakeUpload
	builds  map[string]map[string]any // application|commit|source|digest → build
	// usages are the current usages GET …/usages serves, two per page.
	usages []map[string]any
	// usageBranches are the branch views asked for.
	usageBranches []string
	captures      []fakeCaptureUpload
	captureDigest map[string]string // manifest digest → build
	images        map[string]bool   // stored image digests
}

// fakeCaptureUpload is one POST …/captures as the fake read it.
type fakeCaptureUpload struct {
	parts []string // part names in order
	// raw is the manifest part verbatim, so a test can hold the CLI to
	// the published schema and to the server's own parser rather than to
	// the fake's partial reading of it.
	raw    []byte
	doc    fakeCapturesDoc
	images map[string][]byte
}

type fakeCapturesDoc struct {
	Schema      string `json:"schema"`
	Application string `json:"application"`
	Commit      string `json:"commit"`
	Branch      string `json:"branch"`
	Captures    []struct {
		Route  string `json:"route"`
		Locale string `json:"locale"`
		Image  struct {
			SHA256 string `json:"sha256"`
		} `json:"image"`
		// Findings is what the capture's probe pass measured. The fake
		// counts them and answers with the total, as the ingest does:
		// the number the command prints is the server's, so a manifest
		// the server read nothing out of cannot look like a success.
		Findings []map[string]any `json:"findings"`
	} `json:"captures"`
}

// findings is how many visual findings the upload carried.
func (d fakeCapturesDoc) findings() int {
	n := 0
	for _, c := range d.Captures {
		n += len(c.Findings)
	}
	return n
}

type fakeUpload struct {
	source string
	doc    fakeUsagesDoc
}

type fakeUsagesDoc struct {
	Schema      string `json:"schema"`
	Application string `json:"application"`
	Commit      string `json:"commit"`
	Branch      string `json:"branch"`
	Tool        struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"tool"`
	Usages []struct {
		Key string `json:"key"`
	} `json:"usages"`
}

func newFakeContext() *fakeContext {
	return &fakeContext{builds: map[string]map[string]any{}, captureDigest: map[string]string{}, images: map[string]bool{}}
}

func (f *fakeServer) routeContext(mux *http.ServeMux, p string) {
	mux.HandleFunc("POST "+p+"/context-builds", f.uploadUsages)
	mux.HandleFunc("GET "+p+"/usages", f.listUsages)
	mux.HandleFunc("GET "+p+"/applications", func(w http.ResponseWriter, _ *http.Request) {
		app := func(id, slug string) map[string]any {
			return map[string]any{"id": id, "slug": slug, "name": slug, "platform": "web", "created_at": "2026-09-19T00:00:00Z", "updated_at": "2026-09-19T00:00:00Z"}
		}
		writeJSONResp(w, 200, map[string]any{"items": []any{app("app_web", "web"), app("app_admin", "admin")}})
	})
	mux.HandleFunc("POST "+p+"/captures", f.uploadCaptures)
}

// usage is a current usage of key in application appID, as the API
// serves it; known keys name a message.
func usage(appID, key, file string, line int, route string, known bool) map[string]any {
	u := map[string]any{"key": key, "file": file, "line": line, "kind": "t", "build_id": "bld_1", "application_id": appID,
		"commit": testCommit, "branch": "main", "on_default_branch": true, "source": "plugin"}
	if route != "" {
		u["route"] = route
	}
	if known {
		u["message_id"] = "msg_" + key
	}
	return u
}

func (f *fakeServer) listUsages(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctx.usageBranches = append(f.ctx.usageBranches, r.URL.Query().Get("branch"))
	start := 0
	if tok := r.URL.Query().Get("page_token"); tok != "" {
		_, _ = fmt.Sscan(tok, &start)
	}
	end := min(start+2, len(f.ctx.usages))
	page := map[string]any{"items": append([]map[string]any{}, f.ctx.usages[start:end]...)}
	if end < len(f.ctx.usages) {
		page["next_page_token"] = fmt.Sprint(end)
	}
	writeJSONResp(w, 200, page)
}

// uploadCaptures is the Captures API's upload contract: multipart, the
// manifest part first, then one image/png part per image named by its
// SHA-256; 201 with the counts, 200 for the same manifest again.
func (f *fakeServer) uploadCaptures(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		problemResp(w, 400, "invalid_request", "not multipart/form-data")
		return
	}
	up := fakeCaptureUpload{images: map[string][]byte{}}
	var manifest []byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			problemResp(w, 400, "invalid_request", err.Error())
			return
		}
		body, _ := io.ReadAll(part)
		name, ct := part.FormName(), part.Header.Get("Content-Type")
		up.parts = append(up.parts, name)
		switch {
		case len(up.parts) == 1 && name == "manifest" && ct == "application/json":
			manifest = body
		case len(up.parts) > 1 && ct == "image/png":
			sum := sha256.Sum256(body)
			if hex.EncodeToString(sum[:]) != name {
				problemResp(w, 400, "invalid_image", "part "+name+" isn't named by its SHA-256")
				return
			}
			up.images[name] = body
		default:
			problemResp(w, 400, "invalid_captures", "unexpected part "+name+" ("+ct+")")
			return
		}
	}
	up.raw = manifest
	if err := json.Unmarshal(manifest, &up.doc); err != nil || up.doc.Schema != "glossa.captures/v1" {
		problemResp(w, 400, "invalid_captures", "invalid glossa.captures/v1 manifest")
		return
	}
	if up.doc.Application != "web" {
		problemResp(w, 400, "unknown_application", "the project has no application with that slug")
		return
	}
	for _, c := range up.doc.Captures {
		if _, ok := up.images[c.Image.SHA256]; !ok {
			problemResp(w, 400, "missing_image", "no part for image "+c.Image.SHA256)
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctx.captures = append(f.ctx.captures, up)
	sum := sha256.Sum256(manifest)
	digest := hex.EncodeToString(sum[:])
	answer := map[string]any{"captures": len(up.doc.Captures), "unknown_keys": []string{},
		"findings": up.doc.findings()}
	if b, ok := f.ctx.captureDigest[digest]; ok {
		// A replay stores nothing, findings included.
		answer["build"], answer["images_stored"], answer["images_deduplicated"] = map[string]any{"id": b}, 0, 0
		answer["findings"] = 0
		writeJSONResp(w, 200, answer)
		return
	}
	stored, dedup := 0, 0
	for d := range up.images {
		if f.ctx.images[d] {
			dedup++
		} else {
			f.ctx.images[d] = true
			stored++
		}
	}
	b := fmt.Sprintf("bld_c%d", len(f.ctx.captureDigest)+1)
	f.ctx.captureDigest[digest] = b
	answer["build"], answer["images_stored"], answer["images_deduplicated"] = map[string]any{"id": b}, stored, dedup
	writeJSONResp(w, 201, answer)
}

func (f *fakeServer) uploadUsages(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if !slices.Contains([]string{"plugin", "extract", "runtime", "capture"}, source) {
		problemResp(w, 400, "invalid_source", "source must be plugin, extract, runtime or capture")
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		problemResp(w, 400, "invalid_request", "the body is JSON")
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var doc fakeUsagesDoc
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Schema != "glossa.usages/v1" || len(doc.Commit) != 40 || doc.Usages == nil {
		problemResp(w, 400, "invalid_usages", "context: invalid glossa.usages/v1 document")
		return
	}
	if doc.Application != "web" {
		problemResp(w, 400, "unknown_application", "the project has no application with that slug")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctx.uploads = append(f.ctx.uploads, fakeUpload{source: source, doc: doc})
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	key := doc.Application + "|" + doc.Commit + "|" + source + "|" + digest
	if b, ok := f.ctx.builds[key]; ok {
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSONResp(w, 200, b)
		return
	}
	unknown := 0
	for _, u := range doc.Usages {
		if _, ok := f.messages[u.Key]; !ok {
			unknown++
		}
	}
	b := map[string]any{
		"id": fmt.Sprintf("bld_%d", len(f.ctx.builds)+1), "application_id": "app_web", "commit": doc.Commit, "branch": doc.Branch,
		"on_default_branch": doc.Branch == "main", "source": source, "tool": doc.Tool, "digest": digest,
		"usages": len(doc.Usages), "unknown_keys": unknown, "created_by": "token:ci", "created_at": "2026-09-19T12:00:00Z",
	}
	f.ctx.builds[key] = b
	writeJSONResp(w, 201, b)
}
