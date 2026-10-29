package cli

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The Quality context, faked: stored findings, the waivers that accept
// them, the check policy as a resource and the seven numbers.
//
// The fake stores domain.Finding values and renders them by marshalling
// them, because glossa.finding/v1 is one shape (RFC 0005 §14 decision
// 1) and a fake that spelled it a second way would let the CLI's reader
// drift from the server's writer without a test noticing.
//
// Waivers are applied here, on read, the way the server applies them:
// by fingerprint. That is what lets a test prove the property the
// command depends on — a waiver `glossa waive` created matches the
// finding server-side — instead of assuming it.

type fakeWaiver struct {
	id, fingerprint, reason, scope, ref string
	sourceRevision                      int
	expiresAt, revokedAt                *time.Time
	createdAt                           time.Time
}

func (w *fakeWaiver) active(now time.Time) bool {
	return w.revokedAt == nil && (w.expiresAt == nil || w.expiresAt.After(now))
}

type fakeQuality struct {
	seq int
	// run is the stored check run; nil is a project nothing has checked.
	run      map[string]any
	findings []domain.Finding
	waivers  []*fakeWaiver
	// policyVersion is the version the next save gets.
	policyVersion int
	// saved are the documents that were written, in order, so a test can
	// see what a CI job sent.
	saved []map[string]any
	// recorded are the check runs that were filed, as raw bodies, so a
	// test can see exactly what `glossa check` sent — including what it
	// did not send.
	recorded []map[string]any
	// refuse makes the next record fail, so a test can prove that
	// failing to file a report never changes what the check concluded.
	refuse bool
	// impact is what a save or a preview answers; nil is an empty one.
	impact map[string]any
	// summary is what quality-summary answers. A test sets it whole,
	// because the point of most of these tests is which numbers are
	// *missing* from it.
	summary map[string]any
}

func newFakeQuality() *fakeQuality { return &fakeQuality{} }

func (f *fakeQuality) nextID(prefix string) string {
	f.seq++
	return prefix + "_" + strconv.Itoa(f.seq)
}

// nextWaiverID mints what the server mints. A waiver's id is a UUID
// there, and `glossa check` parses it to apply the waiver with
// domain.Waivers — so a fake handing out `wv_1` would let the CLI's
// reading of a waiver id drift from the server's writing of one without
// a test noticing.
func (f *fakeQuality) nextWaiverID() string {
	f.seq++
	return uuid.NewSHA1(uuid.Nil, []byte("waiver/"+strconv.Itoa(f.seq))).String()
}

func (f *fakeServer) routeQuality(mux *http.ServeMux, p string) {
	mux.HandleFunc("GET "+p+"/findings", f.listFindings)
	mux.HandleFunc("POST "+p+"/check-runs", f.createCheckRun)
	mux.HandleFunc("GET "+p+"/waivers", f.listWaivers)
	mux.HandleFunc("POST "+p+"/waivers", f.createWaiver)
	mux.HandleFunc("DELETE "+p+"/waivers/{waiver}", f.revokeWaiver)
	mux.HandleFunc("POST "+p+"/check-policy", f.saveCheckPolicy)
	mux.HandleFunc("GET "+p+"/check-policy/export", f.exportCheckPolicy)
	mux.HandleFunc("POST "+p+"/check-policy/import", f.importCheckPolicy)
	mux.HandleFunc("GET "+p+"/quality-summary", f.qualitySummary)
}

// ── recording a run ─────────────────────────────────────────────────

// createCheckRun keeps the body whole and answers the run the server
// would have stored. It grades nothing, because what the CLI has to get
// right here is what it *sends*: the real server computes every
// fingerprint and every severity itself, so a fake that re-graded would
// only be testing the fake.
func (f *fakeServer) createCheckRun(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	decodeBody(r, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.qa.refuse {
		problemResp(w, 400, "too_many_findings", "more findings than one run may hold")
		return
	}
	f.qa.recorded = append(f.qa.recorded, body)
	layers, _ := body["layers"].([]any)
	if layers == nil {
		layers = []any{}
	}
	writeJSONResp(w, 201, map[string]any{
		"id": f.qa.nextID("run"), "ref": body["ref"], "trigger": "cli", "policy_version": 0,
		"layers": layers, "counts": map[string]any{"errors": 0, "warnings": 0, "waived": 0},
		"conclusion": "success", "created_by": "ci",
		"started_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// ── findings ────────────────────────────────────────────────────────

// rendered applies today's waivers, the way the server does: by
// fingerprint, and never by hiding anything. A waived finding keeps its
// place and gets severity `waived` (RFC 0005 §2.3).
func (f *fakeServer) rendered() []domain.Finding {
	now := time.Now()
	out := make([]domain.Finding, 0, len(f.qa.findings))
	for _, fd := range f.qa.findings {
		for _, w := range f.qa.waivers {
			if w.fingerprint == fd.Fingerprint && w.active(now) {
				fd = fd.Waive(w.id)
				break
			}
		}
		out = append(out, fd)
	}
	return out
}

func (f *fakeServer) listFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.rendered()
	counts := map[string]int{"errors": 0, "warnings": 0, "waived": 0}
	for _, fd := range all {
		switch fd.Severity {
		case domain.Error:
			counts["errors"]++
		case domain.Warning:
			counts["warnings"]++
		case domain.Waived:
			counts["waived"]++
		}
	}
	items := []json.RawMessage{}
	for _, fd := range all {
		if !matchesFindingQuery(fd, q) {
			continue
		}
		body, _ := json.Marshal(fd)
		items = append(items, body)
	}
	out := map[string]any{"items": items, "counts": counts}
	if f.qa.run != nil {
		out["run"] = f.qa.run
	}
	writeJSONResp(w, 200, out)
}

func matchesFindingQuery(fd domain.Finding, q map[string][]string) bool {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	for k, want := range map[string]string{
		"layer": string(fd.Layer), "severity": string(fd.Severity), "code": fd.Code,
		"locale": fd.Locus.Locale, "namespace": fd.Locus.Namespace, "message": fd.Locus.Key,
	} {
		if v := get(k); v != "" && v != want {
			return false
		}
	}
	switch get("waived") {
	case "true":
		return fd.Severity == domain.Waived
	case "false":
		return fd.Severity != domain.Waived
	}
	return true
}

// ── waivers ─────────────────────────────────────────────────────────

func (f *fakeServer) waiverJSON(w *fakeWaiver) map[string]any {
	out := map[string]any{
		"id": w.id, "fingerprint": w.fingerprint, "reason": w.reason, "scope": w.scope,
		"source_revision": w.sourceRevision, "active": w.active(time.Now()),
		"created_by": "token:1", "created_at": w.createdAt.UTC().Format(time.RFC3339),
	}
	if w.ref != "" {
		out["ref"] = w.ref
	}
	if w.expiresAt != nil {
		out["expires_at"] = w.expiresAt.UTC().Format(time.RFC3339)
	}
	if w.revokedAt != nil {
		out["revoked_at"] = w.revokedAt.UTC().Format(time.RFC3339)
	}
	// `accepts` is read from the most recent stored finding carrying the
	// fingerprint — and is absent where none does any more.
	for _, fd := range f.qa.findings {
		if fd.Fingerprint != w.fingerprint {
			continue
		}
		accepts := map[string]any{
			"layer": string(fd.Layer), "code": fd.Code, "key": fd.Locus.Key, "message": fd.Message,
		}
		if fd.Locus.Locale != "" {
			accepts["locale"] = fd.Locus.Locale
		}
		if fd.Locus.Namespace != "" {
			accepts["namespace"] = fd.Locus.Namespace
		}
		out["accepts"] = accepts
		break
	}
	return out
}

func (f *fakeServer) listWaivers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, wv := range f.qa.waivers {
		if fp := q.Get("fingerprint"); fp != "" && fp != wv.fingerprint {
			continue
		}
		if a := q.Get("active"); a != "" && a != boolText(wv.active(time.Now())) {
			continue
		}
		items = append(items, f.waiverJSON(wv))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (f *fakeServer) createWaiver(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fingerprint    string     `json:"fingerprint"`
		Reason         string     `json:"reason"`
		Scope          string     `json:"scope"`
		Ref            string     `json:"ref"`
		SourceRevision *int       `json:"source_revision"`
		ExpiresAt      *time.Time `json:"expires_at"`
	}
	decodeBody(r, &body)
	// The reason is the mechanism (RFC 0005 §2.3): a blank one is a 400,
	// here as on the real server.
	if strings.TrimSpace(body.Reason) == "" {
		problemResp(w, 400, "invalid_waiver", "reason is required")
		return
	}
	if !strings.HasPrefix(body.Fingerprint, domain.FingerprintPrefix) {
		problemResp(w, 400, "invalid_fingerprint", "not a fingerprint")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	scope := body.Scope
	if scope == "" {
		scope = string(domain.WaiverProject)
	}
	for _, wv := range f.qa.waivers {
		if wv.fingerprint == body.Fingerprint && wv.scope == scope && wv.ref == body.Ref && wv.active(time.Now()) {
			writeJSONResp(w, 200, f.waiverJSON(wv))
			return
		}
	}
	// The revision the waiver is made against is the one the most recent
	// stored finding carries, unless the caller named one.
	revision := 0
	if body.SourceRevision != nil {
		revision = *body.SourceRevision
	} else {
		for _, fd := range f.qa.findings {
			if fd.Fingerprint == body.Fingerprint && fd.SourceRevision != nil {
				revision = *fd.SourceRevision
				break
			}
		}
	}
	wv := &fakeWaiver{
		id: f.qa.nextWaiverID(), fingerprint: body.Fingerprint, reason: body.Reason, scope: scope,
		ref: body.Ref, sourceRevision: revision, expiresAt: body.ExpiresAt, createdAt: time.Now().UTC(),
	}
	f.qa.waivers = append(f.qa.waivers, wv)
	w.Header().Set("Location", "/v1/tenants/ten_1/projects/prj_1/waivers/"+wv.id)
	writeJSONResp(w, 201, f.waiverJSON(wv))
}

func (f *fakeServer) revokeWaiver(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("waiver")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, wv := range f.qa.waivers {
		if wv.id != id {
			continue
		}
		now := time.Now().UTC()
		wv.revokedAt = &now
		w.WriteHeader(204)
		return
	}
	problemResp(w, 404, "waiver_not_found", "no such waiver")
}

// ── the policy as a resource ────────────────────────────────────────

func (f *fakeServer) impactJSON() map[string]any {
	if f.qa.impact != nil {
		return f.qa.impact
	}
	return map[string]any{
		"findings": 0, "runs": 0, "raised": 0, "lowered": 0, "silenced": 0,
		"newly_failing": 0, "no_longer_failing": 0, "open_pull_requests": 0, "rules": []any{},
	}
}

// savePolicy stores a document and answers what the save did, or —
// dry_run — what it would do, storing nothing.
func (f *fakeServer) savePolicy(w http.ResponseWriter, doc map[string]any, dryRun bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qa.saved = append(f.qa.saved, doc)
	version := f.qa.policyVersion
	if !dryRun {
		version++
		f.qa.policyVersion = version
		f.policyDoc = map[string]any{
			"version": version, "document": doc, "created_by": "token:1",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		}
	}
	state := f.policyDoc
	if state == nil {
		state = map[string]any{"version": version, "document": doc}
	}
	writeJSONResp(w, 200, map[string]any{"dry_run": dryRun, "policy": state, "impact": f.impactJSON()})
}

func (f *fakeServer) saveCheckPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Policy    map[string]any `json:"policy"`
		GraceDays *int           `json:"grace_days"`
		DryRun    bool           `json:"dry_run"`
	}
	decodeBody(r, &body)
	f.savePolicy(w, body.Policy, body.DryRun)
}

func (f *fakeServer) importCheckPolicy(w http.ResponseWriter, r *http.Request) {
	var doc map[string]any
	decodeBody(r, &doc)
	f.savePolicy(w, doc, r.URL.Query().Get("dry_run") == "true")
}

func (f *fakeServer) exportCheckPolicy(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.policyDoc == nil {
		problemResp(w, 404, "not_found", "no check policy")
		return
	}
	writeJSONResp(w, 200, f.policyDoc["document"])
}

// ── the seven numbers ───────────────────────────────────────────────

func (f *fakeServer) qualitySummary(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.qa.summary == nil {
		problemResp(w, 404, "not_found", "no summary")
		return
	}
	writeJSONResp(w, 200, f.qa.summary)
}

// baseSummary is a summary with the fields every one has and no numbers
// at all: a test adds the ones it wants measured and names the rest in
// `unmeasured`, which is the distinction these tests are about.
func baseSummary() map[string]any {
	now := time.Now().UTC().Format(time.RFC3339)
	return map[string]any{
		"schema": "glossa.quality-summary/v1", "project_id": "prj_1", "environment": "production",
		"since": now, "computed_at": now, "expires_at": now, "cached": false,
		"project": map[string]any{}, "locales": []any{}, "unmeasured": []any{},
	}
}
