package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// The fake server's import and export jobs (RFC 0003 §6): create →
// upload (imports) → a job that advances one state per read (queued →
// running → final) → paginated results and a download with its SHA-256
// as the ETag. Tests script what a job ends with.

type fakeIOJob struct {
	id, direction, format, mode, state string
	project                            *string
	options                            map[string]any
	fileName                           string
	upload                             []byte
	uploadType                         string
	reads                              int
	results                            []map[string]any
	reused                             string
	failureCode, failureMessage        string
	file                               []byte
	fileType                           string
	cancelRequested                    bool
	created                            time.Time
}

type fakeInterchange struct {
	seq  int
	jobs []*fakeIOJob
	// importResults scripts the results of the next imports (each a
	// result with seq, kind, key, status, …); nil: every file is one
	// created message.
	importResults []map[string]any
	// importFailure fails the next imports with this code (its results
	// still reported).
	importFailure string
	// exportFile is what exports write; exportType its content type.
	exportFile []byte
	exportType string
	// exportFailure fails the next exports with this code.
	exportFailure string
	// corruptDownload serves other bytes than the ETag names.
	corruptDownload bool
	// corruptUpload records another digest than the upload's.
	corruptUpload bool
	// readsToFinish is how many reads a job takes to finish (default 2).
	readsToFinish int
	// refuseOverwrite answers overwrite imports 403 (no integration.manage).
	refuseOverwrite bool
	// bodies are the create requests, in order.
	bodies []map[string]any
	// downloads counts file downloads.
	downloads int
}

func newFakeInterchange() *fakeInterchange {
	return &fakeInterchange{exportFile: []byte("{\n  \"checkout.pay\": \"Zahle {amount, number}\"\n}\n"), exportType: "application/json", readsToFinish: 2}
}

func (x *fakeInterchange) nextID() string {
	x.seq++
	return fmt.Sprintf("00000000-0000-4000-9000-%012d", x.seq)
}

func (f *fakeServer) routeInterchange(mux *http.ServeMux) {
	t := "/v1/tenants/ten_1"
	mux.HandleFunc("POST "+t+"/import-jobs", f.createIOJob("import"))
	mux.HandleFunc("GET "+t+"/import-jobs", f.listIOJobs("import"))
	mux.HandleFunc("GET "+t+"/import-jobs/{id}", f.getIOJob("import"))
	mux.HandleFunc("PUT "+t+"/import-jobs/{id}/file", f.uploadIOFile)
	mux.HandleFunc("POST "+t+"/import-jobs/{id}/cancellation", f.cancelIOJob("import"))
	mux.HandleFunc("GET "+t+"/import-jobs/{id}/results", f.ioResults)
	mux.HandleFunc("POST "+t+"/export-jobs", f.createIOJob("export"))
	mux.HandleFunc("GET "+t+"/export-jobs", f.listIOJobs("export"))
	mux.HandleFunc("GET "+t+"/export-jobs/{id}", f.getIOJob("export"))
	mux.HandleFunc("POST "+t+"/export-jobs/{id}/cancellation", f.cancelIOJob("export"))
	mux.HandleFunc("GET "+t+"/export-jobs/{id}/file", f.downloadIOFile)
	// The workspace's memory and termbase: tenant-wide jobs of one format.
	for route, format := range map[string]string{"tm": "tmx", "termbase": "tbx"} {
		mux.HandleFunc("POST "+t+"/"+route+"-import-jobs", f.createWorkspaceJob("import", format))
		mux.HandleFunc("GET "+t+"/"+route+"-import-jobs", f.listWorkspaceJobs("import", format))
		mux.HandleFunc("POST "+t+"/"+route+"-export-jobs", f.createWorkspaceJob("export", format))
		mux.HandleFunc("GET "+t+"/"+route+"-export-jobs", f.listWorkspaceJobs("export", format))
	}
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var ioKinds = map[string]string{"xliff": "catalog", "json": "catalog", "po": "catalog", "tmx": "tm", "tbx": "termbase"}

func (x *fakeInterchange) jobJSON(j *fakeIOJob) map[string]any {
	out := map[string]any{"id": j.id, "kind": ioKinds[j.format], "format": j.format, "state": j.state, "file_name": j.fileName,
		"options": j.options, "attempts": 1, "cancel_requested": j.cancelRequested, "created_by": "token:1",
		"created_at": j.created.Format(time.RFC3339Nano), "updated_at": j.created.Format(time.RFC3339Nano),
		"expires_at": j.created.Add(7 * 24 * time.Hour).Format(time.RFC3339)}
	if j.project != nil {
		out["project_id"] = *j.project
	}
	if j.failureCode != "" {
		out["failure_code"], out["failure_message"] = j.failureCode, j.failureMessage
	}
	final := j.state == "succeeded" || j.state == "failed" || j.state == "cancelled"
	if final {
		out["finished_at"] = j.created.Add(time.Second).Format(time.RFC3339)
	}
	if j.direction == "import" {
		out["mode"] = j.mode
		counts := map[string]int{"created": 0, "updated": 0, "unchanged": 0, "conflict": 0, "invalid": 0}
		byKind := map[string]map[string]int{}
		if j.state != "awaiting_upload" && j.state != "queued" {
			for _, r := range j.results {
				st, kind := r["status"].(string), r["kind"].(string)
				counts[st]++
				if byKind[kind] == nil {
					byKind[kind] = map[string]int{"created": 0, "updated": 0, "unchanged": 0, "conflict": 0, "invalid": 0}
				}
				byKind[kind][st]++
			}
		}
		summary := map[string]any{"by_kind": byKind}
		for k, v := range counts {
			summary[k] = v
		}
		out["summary"] = summary
		total, processed := len(j.results), 0
		switch j.state {
		case "running":
			processed = total / 2
		case "succeeded", "failed":
			processed = total
		}
		out["total_items"], out["processed_items"] = total, processed
		if j.state == "awaiting_upload" {
			out["upload_url"] = "/v1/tenants/ten_1/import-jobs/" + j.id + "/file"
		}
		if j.upload != nil {
			digest := sha(j.upload)
			if x.corruptUpload {
				digest = sha([]byte("something else"))
			}
			out["file"] = map[string]any{"size": len(j.upload), "sha256": digest, "content_type": j.uploadType}
		}
		if j.reused != "" {
			out["reused_job_id"] = j.reused
		}
		return out
	}
	written := 0
	if j.state == "succeeded" {
		written = 3
		out["file"] = map[string]any{"size": len(j.file), "sha256": sha(j.file), "content_type": j.fileType}
		out["download_url"] = "/v1/tenants/ten_1/export-jobs/" + j.id + "/file"
	}
	out["written"] = written
	return out
}

func (f *fakeServer) createIOJob(direction string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		x := f.io
		x.bodies = append(x.bodies, body)
		format, _ := body["format"].(string)
		if _, ok := ioKinds[format]; !ok || direction == "export" && format == "po" {
			problemResp(w, 400, "invalid_format", "unsupported format "+format)
			return
		}
		opts, _ := body["options"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		if _, ok := opts["plural_variable"]; ok && format != "po" {
			problemResp(w, 400, "invalid_options", "plural_variable doesn't apply to "+format+" imports")
			return
		}
		mode, _ := body["mode"].(string)
		if direction == "import" && mode == "" {
			mode = "merge"
		}
		if mode == "overwrite" && x.refuseOverwrite {
			problemResp(w, 403, "forbidden", "overwrite needs integration.manage")
			return
		}
		j := &fakeIOJob{id: x.nextID(), direction: direction, format: format, mode: mode, options: opts, created: time.Now().UTC().Add(time.Duration(x.seq) * time.Millisecond)}
		if p, ok := body["project_id"].(string); ok {
			j.project = &p
		}
		if direction == "import" {
			j.state = "awaiting_upload"
			j.fileName, _ = body["file_name"].(string)
		} else {
			j.state = "queued"
			j.file, j.fileType = x.exportFile, x.exportType
			j.fileName = "shop." + format
			if x.exportType == "application/zip" {
				j.fileName += ".zip"
			}
			j.failureCode = x.exportFailure
		}
		x.jobs = append(x.jobs, j)
		writeJSONResp(w, 201, x.jobJSON(j))
	}
}

func (x *fakeInterchange) find(direction, id string) *fakeIOJob {
	for _, j := range x.jobs {
		if j.id == id && j.direction == direction {
			return j
		}
	}
	return nil
}

func (f *fakeServer) uploadIOFile(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		problemResp(w, 400, "upload_interrupted", err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.io
	j := x.find("import", r.PathValue("id"))
	switch {
	case j == nil:
		problemResp(w, 404, "not_found", "no such job")
		return
	case j.state != "awaiting_upload":
		problemResp(w, 409, "upload_not_expected", "the job has its file")
		return
	case len(body) == 0:
		problemResp(w, 400, "empty_file", "the file is empty")
		return
	case r.ContentLength != int64(len(body)):
		problemResp(w, 400, "upload_interrupted", "short body")
		return
	}
	j.upload, j.uploadType, j.state = body, r.Header.Get("Content-Type"), "queued"
	j.results = x.importResults
	if j.results == nil {
		j.results = []map[string]any{{"seq": 1, "kind": "message", "key": "checkout.pay", "status": "created"}}
	}
	j.failureCode = x.importFailure
	if j.failureCode != "" {
		j.failureMessage = "line 1: the file is malformed"
	}
	// An applied import of the same file and options reuses the
	// earlier result (dry runs always run).
	for _, e := range x.jobs {
		if e != j && e.direction == "import" && e.state == "succeeded" && e.mode == j.mode && j.mode != "dry_run" &&
			e.format == j.format && sha(e.upload) == sha(j.upload) {
			j.state, j.reused, j.results = "succeeded", e.id, e.results
		}
	}
	writeJSONResp(w, 200, x.jobJSON(j))
}

// advance moves a job one state on per read.
func (x *fakeInterchange) advance(j *fakeIOJob) {
	if j.state != "queued" && j.state != "running" {
		return
	}
	j.reads++
	switch {
	case j.cancelRequested:
		j.state = "cancelled"
	case j.reads >= x.readsToFinish:
		j.state = "succeeded"
		if j.failureCode != "" {
			j.state = "failed"
		}
	default:
		j.state = "running"
	}
}

func (f *fakeServer) getIOJob(direction string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		j := f.io.find(direction, r.PathValue("id"))
		if j == nil {
			problemResp(w, 404, "not_found", "no such job")
			return
		}
		f.io.advance(j)
		writeJSONResp(w, 200, f.io.jobJSON(j))
	}
}

func (f *fakeServer) listIOJobs(direction string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		defer f.mu.Unlock()
		var items []map[string]any
		for i := len(f.io.jobs) - 1; i >= 0; i-- {
			j := f.io.jobs[i]
			if j.direction != direction || q.Get("state") != "" && j.state != q.Get("state") ||
				q.Get("project") != "" && (j.project == nil || *j.project != q.Get("project")) {
				continue
			}
			items = append(items, f.io.jobJSON(j))
		}
		if items == nil {
			items = []map[string]any{}
		}
		writeJSONResp(w, 200, map[string]any{"items": items})
	}
}

// createWorkspaceJob is a tenant-wide TMX or TBX job: the route names
// the format, and there is no project.
func (f *fakeServer) createWorkspaceJob(direction, format string) http.HandlerFunc {
	create := f.createIOJob(direction)
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		if _, ok := body["project_id"]; ok {
			problemResp(w, 400, "invalid_request", "project_id isn't a field of a workspace job")
			return
		}
		body["format"] = format
		b, _ := json.Marshal(body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		create(w, r)
	}
}

// listWorkspaceJobs lists the tenant-wide jobs of a format.
func (f *fakeServer) listWorkspaceJobs(direction, format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		defer f.mu.Unlock()
		items := []map[string]any{}
		for i := len(f.io.jobs) - 1; i >= 0; i-- {
			j := f.io.jobs[i]
			if j.direction == direction && j.format == format && j.project == nil && (q.Get("state") == "" || j.state == q.Get("state")) {
				items = append(items, f.io.jobJSON(j))
			}
		}
		writeJSONResp(w, 200, map[string]any{"items": items})
	}
}

func (f *fakeServer) cancelIOJob(direction string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		j := f.io.find(direction, r.PathValue("id"))
		switch {
		case j == nil:
			problemResp(w, 404, "not_found", "no such job")
			return
		case j.state == "succeeded" || j.state == "failed":
			problemResp(w, 409, "job_not_cancellable", "the job has finished")
			return
		case j.state == "running":
			j.cancelRequested = true
		default:
			j.state = "cancelled"
		}
		writeJSONResp(w, 200, f.io.jobJSON(j))
	}
}

// ioResults pages through a job's results (page_size, default 100).
func (f *fakeServer) ioResults(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.io.find("import", r.PathValue("id"))
	if j == nil {
		problemResp(w, 404, "not_found", "no such job")
		return
	}
	var matching []map[string]any
	for _, res := range j.results {
		if st := q.Get("status"); st != "" && res["status"] != st {
			continue
		}
		matching = append(matching, res)
	}
	sort.SliceStable(matching, func(a, b int) bool { return matching[a]["seq"].(int) < matching[b]["seq"].(int) })
	size, from := 100, 0
	if s := q.Get("page_size"); s != "" {
		size, _ = strconv.Atoi(s)
	}
	if s := q.Get("page_token"); s != "" {
		from, _ = strconv.Atoi(s)
	}
	to := min(from+size, len(matching))
	page := map[string]any{"items": append([]map[string]any{}, matching[from:to]...)}
	if to < len(matching) {
		page["next_page_token"] = strconv.Itoa(to)
	}
	writeJSONResp(w, 200, page)
}

func (f *fakeServer) downloadIOFile(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.io.find("export", r.PathValue("id"))
	switch {
	case j == nil:
		problemResp(w, 404, "not_found", "no such job")
		return
	case j.state != "succeeded":
		problemResp(w, 409, "export_not_ready", "the export hasn't finished")
		return
	}
	f.io.downloads++
	body := j.file
	if f.io.corruptDownload {
		body = append(append([]byte{}, body...), "tampered"...)
	}
	w.Header().Set("Content-Type", j.fileType)
	w.Header().Set("ETag", strconv.Quote(sha(j.file)))
	w.Header().Set("Content-Disposition", `attachment; filename="`+j.fileName+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
