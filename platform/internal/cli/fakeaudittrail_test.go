package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	audit "go.klarlabs.de/glossa/platform/internal/audit/domain"
)

// fakeAuditTrail is the audit API as RFC 0006 §6.2 shapes it, over a
// real hash chain: the export jobs it writes are signed by a real key,
// so `glossa audit export --public-key` verifies them for real.
type fakeAuditTrail struct {
	entries []audit.Entry
	key     audit.SigningKey

	jobs  map[string]*fakeAuditJob
	posts []string // Idempotency-Key of each job creation
	// queries are the raw query strings of the entry listings.
	queries []string
	// refuse answers 403; unavailable answers audit_export_unavailable;
	// notContiguous makes every time-range job fail range_not_contiguous;
	// tamper flips a byte of the entries before the job digests them (a
	// compromised store); corrupt flips one in transit.
	refuse, unavailable, notContiguous, tamper, corrupt bool
}

type fakeAuditJob struct {
	id            string
	state         string
	polls         int
	from, to      *time.Time
	first, last   int64
	manifest      []byte
	lines         []byte
	failCode      string
	requestedSeqs bool
}

// newAuditTrail appends n entries by two actors to a fresh chain.
func newAuditTrail(t *testing.T, n int) *fakeAuditTrail {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	key, err := audit.ParseSigningKey("audit-test", base64.StdEncoding.EncodeToString(seed))
	if err != nil {
		t.Fatal(err)
	}
	tr := &fakeAuditTrail{key: key, jobs: map[string]*fakeAuditJob{}}
	var head audit.Head
	for i := range n {
		d := audit.Draft{EventID: uuid.NewSHA1(auditTenant, []byte(fmt.Sprint(i))), Source: audit.SourceOutbox, Action: "localization.translation.revised",
			Actor: "person:0190a1b2-0000-7000-8000-000000000001", OccurredAt: time.Date(2026, 10, 1, 9, i, 0, 0, time.UTC),
			AggregateType: "translation", AggregateID: fmt.Sprintf("tr_%d", i), Locale: "de",
			Summary: json.RawMessage(fmt.Sprintf(`{"revision":%d,"state":"approved","text":"string(len=12)"}`, i+1))}
		if i%3 == 2 {
			d.Source, d.Action, d.Actor = audit.SourceDirect, "identity.person.signed_in", "person:0190a1b2-0000-7000-8000-000000000002"
			d.AggregateType, d.AggregateID, d.Locale = "person", "0190a1b2-0000-7000-8000-000000000002", ""
			d.Summary = json.RawMessage(`{"method":"passkey"}`)
		}
		if i%3 == 0 {
			d.Project = uuid.NullUUID{UUID: uuid.MustParse("0190a1b2-aaaa-7000-8000-000000000001"), Valid: true}
		}
		e, err := audit.Append(auditTenant, head, d)
		if err != nil {
			t.Fatal(err)
		}
		tr.entries = append(tr.entries, e)
		head = e.Head()
	}
	return tr
}

// auditTrail gives the server a chain of n entries.
func (f *fakeServer) auditTrail(t *testing.T, n int) *fakeAuditTrail {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trail = newAuditTrail(t, n)
	return f.trail
}

func (tr *fakeAuditTrail) publicPair() string { return pair(tr.key) }

func entryJSON(e audit.Entry) map[string]any {
	m := map[string]any{"sequence": e.Sequence, "event_id": e.EventID.String(), "source": string(e.Source), "action": e.Action,
		"actor": e.Actor, "occurred_at": e.OccurredAt.UTC().Format("2006-01-02T15:04:05.000000Z"),
		"aggregate_type": e.AggregateType, "aggregate_id": e.AggregateID, "summary": json.RawMessage(e.Summary),
		"prev_hash": hex.EncodeToString(e.PrevHash), "hash": hex.EncodeToString(e.Hash)}
	if e.Project.Valid {
		m["project_id"] = e.Project.UUID.String()
	}
	if e.Locale != "" {
		m["locale"] = e.Locale
	}
	return m
}

func (f *fakeServer) routeAuditTrail(mux *http.ServeMux) {
	t := "/v1/tenants/ten_1"
	mux.HandleFunc("GET "+t+"/audit-entries", f.auditEntries)
	mux.HandleFunc("POST "+t+"/audit-export-jobs", f.auditCreateJob)
	mux.HandleFunc("GET "+t+"/audit-export-jobs/{job}", f.auditGetJob)
	mux.HandleFunc("GET "+t+"/audit-export-jobs/{job}/file", func(w http.ResponseWriter, r *http.Request) { f.auditFile(w, r, false) })
	mux.HandleFunc("GET "+t+"/audit-export-jobs/{job}/manifest", func(w http.ResponseWriter, r *http.Request) { f.auditFile(w, r, true) })
}

func (f *fakeServer) auditEntries(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tr := f.trail
	if tr == nil || tr.refuse {
		problemResp(w, 403, "forbidden", "missing permission audit.read")
		return
	}
	tr.queries = append(tr.queries, r.URL.RawQuery)
	q := r.URL.Query()
	var match []audit.Entry
	for _, e := range tr.entries {
		switch {
		case q.Get("actor") != "" && e.Actor != q.Get("actor"):
		case q.Get("action") != "" && e.Action != q.Get("action"):
		case q.Get("source") != "" && string(e.Source) != q.Get("source"):
		case q.Get("project") != "" && (!e.Project.Valid || q.Get("project") != "prj_1"):
		case q.Get("from") != "" && e.OccurredAt.Before(mustTime(q.Get("from"))):
		case q.Get("to") != "" && !e.OccurredAt.Before(mustTime(q.Get("to"))):
		default:
			match = append(match, e)
		}
	}
	if q.Get("order") != "asc" {
		for i, j := 0, len(match)-1; i < j; i, j = i+1, j-1 {
			match[i], match[j] = match[j], match[i]
		}
	}
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size <= 0 || size > 100 {
		size = 100
	}
	off, _ := strconv.Atoi(q.Get("page_token"))
	end := min(off+size, len(match))
	items := []map[string]any{}
	for _, e := range match[min(off, end):end] {
		items = append(items, entryJSON(e))
	}
	out := map[string]any{"items": items}
	if end < len(match) {
		out["next_page_token"] = strconv.Itoa(end)
	}
	writeJSONResp(w, 200, out)
}

func mustTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		panic(err)
	}
	return t
}

func (f *fakeServer) auditCreateJob(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tr := f.trail
	switch {
	case tr == nil || tr.refuse:
		problemResp(w, 403, "forbidden", "missing permission audit.export")
		return
	case tr.unavailable:
		problemResp(w, 503, "audit_export_unavailable", "this deployment has no audit key")
		return
	}
	tr.posts = append(tr.posts, r.Header.Get("Idempotency-Key"))
	var body struct {
		From, To      *time.Time
		FirstSequence *int64 `json:"first_sequence"`
		LastSequence  *int64 `json:"last_sequence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problemResp(w, 400, "invalid_range", err.Error())
		return
	}
	j := &fakeAuditJob{id: uuid.NewSHA1(auditTenant, []byte(fmt.Sprint("job", len(tr.jobs)+1))).String(), state: "queued", from: body.From, to: body.To}
	head := int64(len(tr.entries))
	switch {
	case body.FirstSequence != nil:
		j.requestedSeqs = true
		j.first, j.last = *body.FirstSequence, head
		if body.LastSequence != nil {
			j.last = *body.LastSequence
		}
		if j.first > head || j.last > head {
			problemResp(w, 422, "sequence_out_of_range", "past the chain's head")
			return
		}
	case body.From != nil && body.To != nil:
		if body.To.Sub(*body.From) > 31*24*time.Hour {
			problemResp(w, 422, "range_too_long", "at most 31 days")
			return
		}
		for _, e := range tr.entries {
			if !e.OccurredAt.Before(*body.From) && e.OccurredAt.Before(*body.To) {
				if j.first == 0 {
					j.first = e.Sequence
				}
				j.last = e.Sequence
			}
		}
	default:
		problemResp(w, 400, "invalid_range", "a time range or a sequence range")
		return
	}
	if !j.requestedSeqs && tr.notContiguous {
		j.failCode = "range_not_contiguous"
	}
	tr.jobs[j.id] = j
	w.Header().Set("Location", "/v1/tenants/ten_1/audit-export-jobs/"+j.id)
	writeJSONResp(w, 201, tr.jobDoc(j))
}

// jobDoc advances the job one step per read: queued, running, done.
func (tr *fakeAuditTrail) jobDoc(j *fakeAuditJob) map[string]any {
	doc := map[string]any{"id": j.id, "state": j.state, "attempts": 0, "created_by": "person:x",
		"created_at": "2026-10-04T10:00:00Z", "updated_at": "2026-10-04T10:00:00Z", "expires_at": "2026-10-11T10:00:00Z"}
	if j.from != nil {
		doc["from"], doc["to"] = j.from, j.to
	}
	if j.state == "failed" {
		doc["failure_code"], doc["failure_message"] = j.failCode, "the entries in that range are not one segment"
	}
	if j.state == "succeeded" {
		doc["first_sequence"], doc["last_sequence"] = j.first, j.last
		doc["entry_count"], doc["key_id"] = j.last-j.first+1, tr.key.ID
		sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
		doc["entries"] = map[string]any{"path": "entries.jsonl", "sha256": sum(j.lines), "bytes": len(j.lines),
			"download_url": "/v1/tenants/ten_1/audit-export-jobs/" + j.id + "/file"}
		doc["manifest"] = map[string]any{"path": "manifest.json", "sha256": sum(j.manifest), "bytes": len(j.manifest),
			"download_url": "/v1/tenants/ten_1/audit-export-jobs/" + j.id + "/manifest"}
	}
	return doc
}

func (f *fakeServer) auditGetJob(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tr := f.trail
	var j *fakeAuditJob
	if tr != nil {
		j = tr.jobs[r.PathValue("job")]
	}
	if j == nil {
		problemResp(w, 404, "not_found", "no such job")
		return
	}
	j.polls++
	switch {
	case j.polls == 1:
		j.state = "running"
	case j.failCode != "":
		j.state = "failed"
	case j.polls >= 2 && j.state != "succeeded":
		tr.finish(j)
	}
	writeJSONResp(w, 200, tr.jobDoc(j))
}

// finish writes the job's two files with the real exporter.
func (tr *fakeAuditTrail) finish(j *fakeAuditJob) {
	var from audit.Head
	if j.first > 1 {
		from = tr.entries[j.first-2].Head()
	}
	opts := audit.ExportOptions{CreatedAt: time.Now()}
	if j.from != nil {
		opts.From, opts.To = *j.from, *j.to
	}
	m, lines, err := audit.Export(auditTenant, from, tr.entries[j.first-1:j.last], tr.key, opts)
	if err != nil {
		panic(err)
	}
	if tr.tamper {
		lines[len(lines)/2] ^= 0x01
	}
	j.manifest, j.lines, j.state = m, lines, "succeeded"
}

func (f *fakeServer) auditFile(w http.ResponseWriter, r *http.Request, manifest bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.trail.jobs[r.PathValue("job")]
	if j == nil || j.state != "succeeded" {
		problemResp(w, 409, "export_not_ready", "the job has not succeeded")
		return
	}
	body := j.lines
	if manifest {
		body = j.manifest
	}
	h := sha256.Sum256(body)
	w.Header().Set("ETag", `"`+hex.EncodeToString(h[:])+`"`)
	if f.trail.corrupt && !manifest {
		// What arrives differs from what the ETag and the job digested.
		body = append([]byte(nil), body...)
		body[0] ^= 0x01
	}
	_, _ = w.Write(body)
}
