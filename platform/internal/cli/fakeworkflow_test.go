package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// fakeWorkflows is the Workflow API (RFC 0006 §2–§3) as the contract
// describes it: definitions with immutable versions saved under
// If-Match, a lint that refuses unknown primitives, bindings unique by
// selector, instances with their transition log, and assignments that
// only their assignee works.
type fakeWorkflows struct {
	defs     []*fakeDefinition
	bindings []map[string]any
	// instances and transitions are what the runner would have made.
	instances   []map[string]any
	transitions map[string][]map[string]any
	assignments []*fakeAssignment
	byKey       map[string]*fakeAssignment
	// refuseManage answers 403 to every save and bind, as for a token
	// without the workflows scope; refuseAssign 403 to a create, as for
	// any token (no scope grants assignments.manage).
	refuseManage, refuseAssign bool
	// noInstances answers 503 workflow_instances_unavailable.
	noInstances bool
	// mine is the assignee the caller is, for "my work".
	mine string
	// reportRows are the quality report's rows; lastReportQuery the raw
	// query the last report sent.
	reportRows      []map[string]any
	reportTruncated bool
	lastReportQuery string
	// lastListMine is the mine parameter the last list sent.
	lastListMine string
	saves        int
	seq          int
}

type fakeDefinition struct {
	id, name, subject, project string
	versions                   []map[string]any // documents
	deleted                    bool
}

type fakeAssignment struct {
	json map[string]any
}

// knownPrimitives is the fake's vocabulary.
var knownPrimitives = []string{"approvals_at_least", "review_state_in", "origin_in", "set_review_state", "request_approval", "assign"}

var fakeWorkflowTime = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)

func (f *fakeServer) routeWorkflows(mux *http.ServeMux) {
	f.wf = &fakeWorkflows{transitions: map[string][]map[string]any{}, byKey: map[string]*fakeAssignment{}, mine: "role:translator"}
	t := "/v1/tenants/ten_1"
	p := t + "/projects/prj_1"
	mux.HandleFunc("POST "+t+"/workflow-definition-lints", f.wfLint)
	mux.HandleFunc("GET "+t+"/workflow-definitions", f.wfListDefs)
	mux.HandleFunc("POST "+t+"/workflow-definitions", f.wfCreateDef)
	mux.HandleFunc("GET "+t+"/workflow-definitions/{id}", f.wfGetDef)
	mux.HandleFunc("GET "+t+"/workflow-definitions/{id}/versions", f.wfListVersions)
	mux.HandleFunc("POST "+t+"/workflow-definitions/{id}/versions", f.wfSaveVersion)
	mux.HandleFunc("GET "+t+"/workflow-definitions/{id}/versions/{n}", f.wfGetVersion)
	mux.HandleFunc("GET "+p+"/workflow-bindings", f.wfListBindings)
	mux.HandleFunc("POST "+p+"/workflow-bindings", f.wfBind)
	mux.HandleFunc("DELETE "+p+"/workflow-bindings/{id}", f.wfUnbind)
	mux.HandleFunc("GET "+p+"/workflow-instances", f.wfInstances)
	mux.HandleFunc("GET "+p+"/workflow-instances/{id}", f.wfInstance)
	mux.HandleFunc("GET "+p+"/workflow-instances/{id}/transitions", f.wfTransitions)
	mux.HandleFunc("POST "+p+"/workflow-instances/{id}/rebase", f.wfRebase)
	mux.HandleFunc("GET "+t+"/assignment-reports", f.asReport)
	mux.HandleFunc("GET "+t+"/assignments", f.asList)
	mux.HandleFunc("POST "+t+"/assignments", f.asCreate)
	mux.HandleFunc("GET "+t+"/assignments/{id}", f.asGet)
	mux.HandleFunc("POST "+t+"/assignments/{id}/acceptance", f.asWork("accept"))
	mux.HandleFunc("POST "+t+"/assignments/{id}/completion", f.asWork("complete"))
	mux.HandleFunc("POST "+t+"/assignments/{id}/decline", f.asWork("decline"))
}

// ── definitions ─────────────────────────────────────────────────────

func fakeLint(doc map[string]any) []map[string]any {
	findings := []map[string]any{}
	if doc["schema"] != "glossa.workflow/v1" {
		findings = append(findings, map[string]any{"rule": "envelope", "severity": "error", "path": "schema", "message": "schema must be glossa.workflow/v1"})
	}
	if _, ok := doc["chart"].(map[string]any); !ok {
		findings = append(findings, map[string]any{"rule": "statechart", "severity": "error", "path": "chart", "message": "no chart"})
	}
	for _, section := range []string{"guards", "actions"} {
		m, _ := doc[section].(map[string]any)
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			body, _ := m[n].(map[string]any)
			if use, _ := body["use"].(string); !slices.Contains(knownPrimitives, use) {
				findings = append(findings, map[string]any{"rule": "unknown-primitive", "severity": "error",
					"path": section + "." + n, "message": fmt.Sprintf("%q is not a primitive", use)})
			}
		}
	}
	if note, _ := doc["note"].(string); note != "" {
		findings = append(findings, map[string]any{"rule": "note", "severity": "info", "state": "current", "message": note})
	}
	return findings
}

func lintRefuses(findings []map[string]any) bool {
	for _, x := range findings {
		if x["severity"] != "info" {
			return true
		}
	}
	return false
}

func (f *fakeServer) wfLint(w http.ResponseWriter, r *http.Request) {
	var doc map[string]any
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		problemResp(w, 400, "invalid_request", err.Error())
		return
	}
	findings := fakeLint(doc)
	writeJSONResp(w, 200, map[string]any{"valid": !lintRefuses(findings), "findings": findings})
}

func (d *fakeDefinition) json() map[string]any {
	out := map[string]any{"id": d.id, "name": d.name, "subject": d.subject, "version": len(d.versions),
		"created_by": "token:1", "created_at": fakeWorkflowTime}
	if d.project != "" {
		out["project_id"] = d.project
	}
	return out
}

func (f *fakeServer) wfDef(id string) *fakeDefinition {
	for _, d := range f.wf.defs {
		if d.id == id {
			return d
		}
	}
	return nil
}

func (f *fakeServer) wfListDefs(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, d := range f.wf.defs {
		if d.deleted || (d.project != "" && d.project != project) {
			continue
		}
		items = append(items, d.json())
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) wfRefusesManage(w http.ResponseWriter) bool {
	if f.wf.refuseManage {
		problemResp(w, 403, "forbidden", "missing permission workflows.manage")
		return true
	}
	return false
}

func invalidWorkflowResp(w http.ResponseWriter, findings []map[string]any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(422)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:glossa:problem:invalid_workflow", "code": "invalid_workflow",
		"title": "invalid_workflow", "status": 422, "detail": "the workflow definition does not compile or lint; see findings",
		"findings": findings})
}

func (f *fakeServer) wfCreateDef(w http.ResponseWriter, r *http.Request) {
	var doc map[string]any
	_ = json.NewDecoder(r.Body).Decode(&doc)
	project := r.URL.Query().Get("project")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wfRefusesManage(w) {
		return
	}
	if findings := fakeLint(doc); lintRefuses(findings) {
		invalidWorkflowResp(w, findings)
		return
	}
	name, _ := doc["name"].(string)
	for _, d := range f.wf.defs {
		if !d.deleted && d.name == name && d.project == project {
			problemResp(w, 409, "workflow_definition_exists", "a live definition with this name exists in this scope; save a new version of it instead")
			return
		}
	}
	subject, _ := doc["subject"].(string)
	d := &fakeDefinition{id: fmt.Sprintf("wfd_%d", len(f.wf.defs)+1), name: name, subject: orDefault(subject, "translation"),
		project: project, versions: []map[string]any{doc}}
	f.wf.defs = append(f.wf.defs, d)
	f.wf.saves++
	out := d.json()
	out["findings"] = fakeLint(doc)
	w.Header().Set("ETag", `"1"`)
	writeJSONResp(w, 201, out)
}

func (f *fakeServer) wfGetDef(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.wfDef(r.PathValue("id"))
	if d == nil {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(len(d.versions))))
	writeJSONResp(w, 200, d.json())
}

func versionJSON(d *fakeDefinition, n int) map[string]any {
	return map[string]any{"definition_id": d.id, "version": n, "schema": "glossa.workflow/v1", "document": d.versions[n-1],
		"created_by": "token:1", "created_at": fakeWorkflowTime}
}

func (f *fakeServer) wfListVersions(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.wfDef(r.PathValue("id"))
	if d == nil {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	items := []map[string]any{}
	for n := len(d.versions); n >= 1; n-- {
		items = append(items, versionJSON(d, n))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) wfGetVersion(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.wfDef(r.PathValue("id"))
	n, _ := strconv.Atoi(r.PathValue("n"))
	if d == nil || n < 1 || n > len(d.versions) {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	writeJSONResp(w, 200, versionJSON(d, n))
}

func (f *fakeServer) wfSaveVersion(w http.ResponseWriter, r *http.Request) {
	var doc map[string]any
	_ = json.NewDecoder(r.Body).Decode(&doc)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wfRefusesManage(w) {
		return
	}
	d := f.wfDef(r.PathValue("id"))
	if d == nil {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	match := r.Header.Get("If-Match")
	switch {
	case match == "":
		problemResp(w, 428, "precondition_required", "If-Match is required")
		return
	case match != strconv.Quote(strconv.Itoa(len(d.versions))):
		problemResp(w, 412, "precondition_failed", "the definition has a newer version; read it and retry with its ETag")
		return
	}
	if findings := fakeLint(doc); lintRefuses(findings) {
		invalidWorkflowResp(w, findings)
		return
	}
	d.versions = append(d.versions, doc)
	f.wf.saves++
	out := d.json()
	out["findings"] = fakeLint(doc)
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(len(d.versions))))
	writeJSONResp(w, 201, out)
}

// ── bindings ────────────────────────────────────────────────────────

func (f *fakeServer) wfListBindings(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSONResp(w, 200, map[string]any{"items": append([]map[string]any{}, f.wf.bindings...)})
}

func (f *fakeServer) wfBind(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DefinitionID string   `json:"definition_id"`
		Locales      []string `json:"locales"`
		Namespace    string   `json:"namespace"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wfRefusesManage(w) {
		return
	}
	d := f.wfDef(body.DefinitionID)
	if d == nil || d.deleted {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	if d.project != "" && d.project != "prj_1" {
		problemResp(w, 422, "workflow_definition_out_of_scope", "another project's definition")
		return
	}
	if body.Locales == nil {
		body.Locales = []string{}
	}
	slices.Sort(body.Locales)
	if strings.Contains(body.Namespace, " ") {
		problemResp(w, 422, "invalid_workflow_binding", fmt.Sprintf("namespace %q cannot be one", body.Namespace))
		return
	}
	for _, b := range f.wf.bindings {
		if slices.Equal(b["locales"].([]string), body.Locales) && b["namespace"] == body.Namespace {
			problemResp(w, 409, "workflow_binding_exists", "a binding with this selector exists")
			return
		}
	}
	f.wf.seq++
	b := map[string]any{"id": fmt.Sprintf("wfb_%d", f.wf.seq), "project_id": "prj_1", "definition_id": d.id, "subject": d.subject,
		"locales": body.Locales, "namespace": body.Namespace, "position": f.wf.seq, "created_by": "token:1", "created_at": fakeWorkflowTime}
	f.wf.bindings = append(f.wf.bindings, b)
	writeJSONResp(w, 201, b)
}

func (f *fakeServer) wfUnbind(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wfRefusesManage(w) {
		return
	}
	for i, b := range f.wf.bindings {
		if b["id"] == r.PathValue("id") {
			f.wf.bindings = slices.Delete(f.wf.bindings, i, i+1)
			w.WriteHeader(204)
			return
		}
	}
	problemResp(w, 404, "not_found", "no such workflow resource")
}

// ── instances ───────────────────────────────────────────────────────

// instance adds an instance with its log, as the runner would have.
func (f *fakeServer) instance(id, definition, state, status, locale string, transitions ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wf.instances = append(f.wf.instances, map[string]any{"id": id, "project_id": "prj_1", "definition_id": definition,
		"definition_version": 1, "subject": "translation", "subject_id": "msg_checkout.pay", "locale": locale, "state": state,
		"status": status, "created_at": fakeWorkflowTime, "updated_at": fakeWorkflowTime})
	f.wf.transitions[id] = transitions
}

func (f *fakeServer) wfInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wf.noInstances {
		problemResp(w, 503, "workflow_instances_unavailable", "this server runs no instance store")
		return
	}
	if s := q.Get("status"); s != "" && s != "active" && s != "finished" {
		problemResp(w, 400, "invalid_query", "status")
		return
	}
	items := []map[string]any{}
	for _, it := range f.wf.instances {
		if (q.Get("status") != "" && it["status"] != q.Get("status")) ||
			(q.Get("definition") != "" && it["definition_id"] != q.Get("definition")) ||
			(q.Get("locale") != "" && it["locale"] != q.Get("locale")) ||
			(q.Get("message") != "" && it["subject_id"] != "msg_"+q.Get("message")) {
			continue
		}
		items = append(items, it)
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) wfInstanceByID(id string) map[string]any {
	for _, it := range f.wf.instances {
		if it["id"] == id {
			return it
		}
	}
	return nil
}

func (f *fakeServer) wfInstance(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it := f.wfInstanceByID(r.PathValue("id")); it != nil {
		writeJSONResp(w, 200, it)
		return
	}
	problemResp(w, 404, "not_found", "no such workflow resource")
}

// wfRebase moves an instance to a newer version as the contract says:
// If-Match on the version it runs on, the state kept by name.
func (f *fakeServer) wfRebase(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wfRefusesManage(w) {
		return
	}
	it := f.wfInstanceByID(r.PathValue("id"))
	if it == nil {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	from := it["definition_version"].(int)
	if r.Header.Get("If-Match") != strconv.Quote(strconv.Itoa(from)) {
		problemResp(w, 412, "precondition_failed", "the instance runs on another version now")
		return
	}
	if it["status"] != "active" {
		problemResp(w, 409, "workflow_instance_finished", "the instance has finished; there is nothing left to rebase")
		return
	}
	var body struct {
		Version *int `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	d := f.wfDef(it["definition_id"].(string))
	to := len(d.versions)
	if body.Version != nil {
		to = *body.Version
	}
	if to <= from || to > len(d.versions) {
		problemResp(w, 422, "invalid_workflow_rebase", fmt.Sprintf("version %d is not a newer version of the definition", to))
		return
	}
	states, _ := d.versions[to-1]["chart"].(map[string]any)["states"].(map[string]any)
	state, _ := states[it["state"].(string)].(map[string]any)
	switch {
	case state == nil:
		problemResp(w, 422, "workflow_rebase_state_missing", fmt.Sprintf("version %d has no state %q", to, it["state"]))
		return
	case state["type"] == "final":
		problemResp(w, 422, "workflow_rebase_state_final", fmt.Sprintf("%q is final in version %d", it["state"], to))
		return
	}
	it["definition_version"] = to
	id := it["id"].(string)
	f.wf.transitions[id] = append(f.wf.transitions[id], map[string]any{"seq": len(f.wf.transitions[id]) + 1,
		"from": it["state"], "event": "rebase", "to": it["state"], "outcome": "applied", "guards": []any{},
		"actions": []any{map[string]any{"name": "rebase", "outcome": "done", "detail": fmt.Sprintf("version %d → %d", from, to)}},
		"actor":   "person:me", "at": fakeWorkflowTime})
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(to)))
	writeJSONResp(w, 200, it)
}

func (f *fakeServer) wfTransitions(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wfInstanceByID(r.PathValue("id")) == nil {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	items := f.wf.transitions[r.PathValue("id")]
	if items == nil {
		items = []map[string]any{}
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

// ── assignments ─────────────────────────────────────────────────────

// assignment adds an assignment given to assignee ("role:translator",
// "vendor:ven_1", …) in state.
func (f *fakeServer) assignment(id, assignee, state string, units ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wf.assignments = append(f.wf.assignments, &fakeAssignment{json: fakeAssignmentJSON(id, assignee, state, units)})
}

func fakeAssignmentJSON(id, assignee, state string, units []string) map[string]any {
	kind, ref, _ := strings.Cut(assignee, ":")
	who := map[string]any{"kind": kind}
	if kind == "role" {
		who["role"] = ref
	} else {
		who["id"] = ref
	}
	us := []map[string]any{}
	for _, u := range units {
		key, locale, _ := strings.Cut(u, "@")
		us = append(us, map[string]any{"message_id": "msg_" + key, "locale": locale})
	}
	return map[string]any{"id": id, "project_id": "prj_1", "units": us, "assignee": who, "permission": "translations.write",
		"state": state, "created_by": "person:ada", "created_at": fakeWorkflowTime, "updated_at": fakeWorkflowTime}
}

func (a *fakeAssignment) assignee() string {
	who := a.json["assignee"].(map[string]any)
	if who["kind"] == "role" {
		return "role:" + who["role"].(string)
	}
	return who["kind"].(string) + ":" + who["id"].(string)
}

// asReport serves the quality report: the rows in wf.reportRows, cut to
// the vendor asked for, and the query it saw in wf.lastReportQuery. An
// `assigned` caller (refuseAssign) is refused, as the service does.
func (f *fakeServer) asReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wf.lastReportQuery = r.URL.RawQuery
	if f.wf.refuseAssign {
		problemResp(w, 403, "forbidden", "missing permission assignments.read")
		return
	}
	if v := q.Get("vendor"); v != "" && !strings.HasPrefix(v, "ven_") {
		problemResp(w, 400, "invalid_query", "vendor is not a vendor id")
		return
	}
	rows := []map[string]any{}
	for _, row := range f.wf.reportRows {
		if v := q.Get("vendor"); v == "" || row["assignee"] == "vendor:"+v {
			rows = append(rows, row)
		}
	}
	doc := map[string]any{"generated_at": fakeWorkflowTime, "rows": rows, "truncated": f.wf.reportTruncated}
	if s := q.Get("since"); s != "" {
		doc["since"] = s
	}
	writeJSONResp(w, 200, doc)
}

func (f *fakeServer) asList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wf.lastListMine = q.Get("mine")
	if q.Get("message") != "" && q.Get("project") == "" {
		problemResp(w, 400, "invalid_query", "message needs project")
		return
	}
	items := []map[string]any{}
	for _, a := range f.wf.assignments {
		if (q.Get("mine") == "true" || f.wf.refuseAssign) && a.assignee() != f.wf.mine {
			continue
		}
		if s := q.Get("state"); s != "" && a.json["state"] != s {
			continue
		}
		items = append(items, a.json)
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) asByID(id string) *fakeAssignment {
	for _, a := range f.wf.assignments {
		if a.json["id"] == id {
			return a
		}
	}
	return nil
}

func (f *fakeServer) asGet(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.asByID(r.PathValue("id"))
	if a == nil || (f.wf.refuseAssign && a.assignee() != f.wf.mine) {
		problemResp(w, 404, "not_found", "no such workflow resource")
		return
	}
	writeJSONResp(w, 200, a.json)
}

func (f *fakeServer) asCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
		Units     []struct {
			Message string `json:"message"`
			Locale  string `json:"locale"`
		} `json:"units"`
		Assignee   map[string]string `json:"assignee"`
		Permission string            `json:"permission"`
		DueAt      string            `json:"due_at"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wf.refuseAssign {
		problemResp(w, 403, "forbidden", "missing permission assignments.manage")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if prior, ok := f.wf.byKey[key]; ok && key != "" {
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSONResp(w, 201, prior.json)
		return
	}
	if len(body.Assignee) != 1 {
		problemResp(w, 422, "invalid_assignment", "name exactly one member, role, group or vendor")
		return
	}
	var assignee string
	for k, v := range body.Assignee {
		if v == "nobody" {
			problemResp(w, 422, "unknown_party", fmt.Sprintf("no %s %q", k, v))
			return
		}
		assignee = k + ":" + v
	}
	var units []string
	for _, u := range body.Units {
		if f.messages[u.Message] == nil {
			problemResp(w, 422, "invalid_assignment", fmt.Sprintf("the project has no message %q", u.Message))
			return
		}
		units = append(units, u.Message+"@"+u.Locale)
	}
	id := fmt.Sprintf("asg_%d", len(f.wf.assignments)+1)
	a := &fakeAssignment{json: fakeAssignmentJSON(id, assignee, "open", units)}
	if body.Permission != "" {
		a.json["permission"] = body.Permission
	}
	if body.DueAt != "" {
		a.json["due_at"] = body.DueAt
	}
	f.wf.assignments = append(f.wf.assignments, a)
	if key != "" {
		f.wf.byKey[key] = a
	}
	w.Header().Set("ETag", `"1"`)
	writeJSONResp(w, 201, a.json)
}

func (f *fakeServer) asWork(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		a := f.asByID(r.PathValue("id"))
		if a == nil || a.assignee() != f.wf.mine {
			problemResp(w, 404, "not_found", "no such assignment among yours")
			return
		}
		state := a.json["state"]
		next := map[string]string{"accept": "accepted", "complete": "done", "decline": "declined"}[action]
		live := state == "open" || state == "accepted"
		if (action == "accept" && state != "open") || !live {
			problemResp(w, 409, "assignment_state", fmt.Sprintf("assignment is %s", state))
			return
		}
		a.json["state"] = next
		a.json["updated_at"] = fakeWorkflowTime
		if action != "accept" {
			a.json["closed_by"], a.json["closed_at"] = "person:vera", fakeWorkflowTime
		}
		if body.Reason != "" {
			a.json["reason"] = body.Reason
		}
		writeJSONResp(w, 200, a.json)
	}
}
