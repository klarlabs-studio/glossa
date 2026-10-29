package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The fake server's release requests, approvals and staged rollouts
// (RFC 0006 §3.2, §5.1, §5.2) as platform/api/openapi.yaml describes
// them: a publish or promote into an environment with approvals is held
// (202); deciding is human-only (person_required for an API token),
// never the requester's (own_text); n distinct grants deploy; rollouts
// carry an ETag, PATCH needs If-Match, and ended ones refuse changes.

// Person credentials: device sign-in bearers (glossa_dev_…), each a
// person. The API token (testToken) is no one.
const (
	testPersonVera = "glossa_dev_VVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVV"
	testPersonOtto = "glossa_dev_OOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOOO"
)

func personOf(header string) string {
	switch header {
	case "Bearer " + testPersonVera:
		return "person:vera"
	case "Bearer " + testPersonOtto:
		return "person:otto"
	}
	return ""
}

// principalOf names the caller as the server records it.
func principalOf(r *http.Request) string {
	if p := personOf(r.Header.Get("Authorization")); p != "" {
		return p
	}
	return "token:1"
}

var fakeApprovalTime = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

type fakeApprovals struct {
	requests  []map[string]any
	approvals []map[string]any
	rollouts  []*fakeRollout
	// heldKeys maps a publish's Idempotency-Key to its request.
	heldKeys map[string]string
	// rolloutKeys maps a start's Idempotency-Key to its rollout.
	rolloutKeys map[string]string
	// notAsked leaves new requests without an approval, as before the
	// workflow asked (approval_not_requested).
	notAsked bool
	// policyUnmet makes a rollout start's gate refuse unless forced.
	policyUnmet bool
	// ifMatch records each rollout change's If-Match ("" when absent),
	// as "op ifmatch".
	ifMatch              []string
	reqSeq, apSeq, roSeq int
}

type fakeRollout struct {
	json    map[string]any
	version int
}

func (ro *fakeRollout) etag() string { return strconv.Quote(strconv.Itoa(ro.version)) }

func (ro *fakeRollout) end(status, end string) {
	ro.json["status"], ro.json["end"] = status, end
	ro.json["ended_by"], ro.json["ended_at"] = "person:vera", fakeApprovalTime.Add(time.Hour).Format(time.RFC3339)
	ro.version++
}

func (f *fakeServer) routeApprovals(mux *http.ServeMux, p string) {
	f.appr = &fakeApprovals{heldKeys: map[string]string{}, rolloutKeys: map[string]string{}}
	t := "/v1/tenants/ten_1"
	mux.HandleFunc("GET "+p+"/release-requests", f.rrList)
	mux.HandleFunc("GET "+p+"/release-requests/{id}", f.rrGet)
	mux.HandleFunc("POST "+p+"/release-requests/{id}/approvals", f.rrDecide)
	mux.HandleFunc("POST "+p+"/release-requests/{id}/withdrawal", f.rrWithdraw)
	mux.HandleFunc("GET "+t+"/approvals", f.apList)
	mux.HandleFunc("GET "+t+"/approvals/{id}", f.apGet)
	mux.HandleFunc("POST "+t+"/approvals/{id}/decisions", f.apDecide)
	mux.HandleFunc("GET "+p+"/environments/{env}/rollouts", f.roList)
	mux.HandleFunc("POST "+p+"/environments/{env}/rollouts", f.roStart)
	mux.HandleFunc("GET "+p+"/environments/{env}/rollouts/{id}", f.roGet)
	mux.HandleFunc("PATCH "+p+"/environments/{env}/rollouts/{id}", f.roChange("advance"))
	mux.HandleFunc("POST "+p+"/environments/{env}/rollouts/{id}/completion", f.roChange("complete"))
	mux.HandleFunc("POST "+p+"/environments/{env}/rollouts/{id}/abort", f.roChange("abort"))
}

// ── release requests ────────────────────────────────────────────────

// hold records a release request for rel into e and asks for its
// approval (unless notAsked). Called with f.mu held.
func (f *fakeServer) hold(r *http.Request, e *fakeEnv, rel *fakeRel, action, key string) map[string]any {
	a := f.appr
	// A newer publish or promote withdraws the pending one.
	for _, q := range a.requests {
		if q["environment"] == e.name && q["state"] == "pending" {
			q["state"], q["reason"], q["decided_by"] = "withdrawn", "superseded by a newer request", principalOf(r)
		}
	}
	a.reqSeq++
	q := map[string]any{"id": fmt.Sprintf("rr_%d", a.reqSeq), "environment": e.name, "release_id": rel.id, "action": action,
		"requester": principalOf(r), "approval": map[string]any{"n": e.approvals, "from": map[string]any{"role": "reviewer"},
			"distinct_from_requester": true},
		"gate": map[string]any{"met": true}, "forced": false, "state": "pending",
		"created_at": fakeApprovalTime.Add(time.Duration(a.reqSeq) * time.Minute).Format(time.RFC3339)}
	a.requests = append(a.requests, q)
	if key != "" {
		a.heldKeys[key] = q["id"].(string)
	}
	if !a.notAsked {
		f.newApproval("release_request", q["id"].(string), "", e.approvals)
	}
	return q
}

func (f *fakeServer) newApproval(subject, subjectID, locale string, required int) map[string]any {
	a := f.appr
	a.apSeq++
	ap := map[string]any{"id": fmt.Sprintf("apr_%d", a.apSeq), "project_id": "prj_1", "instance_id": fmt.Sprintf("wfi_%d", a.apSeq),
		"subject": subject, "subject_id": subjectID, "required": required,
		"eligible": map[string]any{"kind": "role", "role": "reviewer"}, "distinct_from_author": true,
		"state": "pending", "decisions": []any{}, "created_by": "workflow",
		"created_at": fakeApprovalTime.Add(time.Duration(a.apSeq) * time.Minute).Format(time.RFC3339)}
	if locale != "" {
		ap["locale"] = locale
	}
	a.approvals = append(a.approvals, ap)
	return ap
}

// translationApproval asks for key@locale's approval, as a workflow's
// request_approval action would.
func (f *fakeServer) translationApproval(key, locale string, required int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newApproval("translation", "msg_"+key, locale, required)["id"].(string)
}

func (f *fakeServer) heldByKey(key string) map[string]any {
	if id, ok := f.appr.heldKeys[key]; ok {
		return f.findRequest(id)
	}
	return nil
}

func (f *fakeServer) writeHeld(w http.ResponseWriter, q map[string]any) {
	w.Header().Set("Location", "/v1/tenants/ten_1/projects/prj_1/release-requests/"+q["id"].(string))
	writeJSONResp(w, 202, map[string]any{"id": q["release_id"], "release_request_id": q["id"], "release_request": q})
}

func (f *fakeServer) findRequest(id string) map[string]any {
	for _, q := range f.appr.requests {
		if q["id"] == id {
			return q
		}
	}
	return nil
}

func (f *fakeServer) rrList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	items := []map[string]any{}
	for i := len(f.appr.requests) - 1; i >= 0; i-- {
		x := f.appr.requests[i]
		if (q.Get("environment") != "" && x["environment"] != q.Get("environment")) || (q.Get("state") != "" && x["state"] != q.Get("state")) {
			continue
		}
		items = append(items, x)
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) rrGet(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q := f.findRequest(r.PathValue("id")); q != nil {
		writeJSONResp(w, 200, q)
		return
	}
	problemResp(w, 404, "not_found", "no such release request")
}

func (f *fakeServer) rrWithdraw(w http.ResponseWriter, r *http.Request) {
	var body struct{ Reason string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.findRequest(r.PathValue("id"))
	switch {
	case q == nil:
		problemResp(w, 404, "not_found", "no such release request")
	case q["state"] != "pending":
		problemResp(w, 409, "release_request_closed", fmt.Sprintf("the request is %s", q["state"]))
	default:
		q["state"], q["decided_by"], q["decided_at"] = "withdrawn", principalOf(r), fakeApprovalTime.Format(time.RFC3339)
		if body.Reason != "" {
			q["reason"] = body.Reason
		}
		writeJSONResp(w, 200, q)
	}
}

func (f *fakeServer) rrDecide(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.findRequest(r.PathValue("id"))
	if q == nil {
		problemResp(w, 404, "not_found", "no such release request")
		return
	}
	var ap map[string]any
	for _, x := range f.appr.approvals {
		if x["subject_id"] == q["id"] {
			ap = x
		}
	}
	if q["state"] != "pending" {
		problemResp(w, 409, "release_request_closed", fmt.Sprintf("the request is %s", q["state"]))
		return
	}
	if ap == nil {
		problemResp(w, 409, "approval_not_requested", "the workflow has not asked for the approval yet; retry shortly")
		return
	}
	f.decideOn(w, r, ap, q)
}

// decideOn records a decision on ap; q is its release request, if any.
func (f *fakeServer) decideOn(w http.ResponseWriter, r *http.Request, ap, q map[string]any) {
	var body struct{ Decision, Reason string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	who := principalOf(r)
	switch {
	case !strings.HasPrefix(who, "person:"):
		problemResp(w, 403, "person_required", "an approval is a person's decision; no token scope grants approvals.decide")
		return
	case ap["state"] != "pending":
		problemResp(w, 409, "approval_closed", fmt.Sprintf("the approval is %s", ap["state"]))
		return
	case q != nil && q["requester"] == who:
		problemResp(w, 403, "own_text", "the requester never approves their own request")
		return
	case len(body.Reason) > 2000:
		problemResp(w, 422, "invalid_approval", "a reason is at most 2,000 characters")
		return
	}
	d := map[string]any{"principal": who, "decision": body.Decision, "at": fakeApprovalTime.Add(time.Hour).Format(time.RFC3339)}
	if body.Reason != "" {
		d["reason"] = body.Reason
	}
	decisions := ap["decisions"].([]any)
	granted := map[string]bool{}
	for _, x := range decisions {
		if m := x.(map[string]any); m["decision"] == "granted" {
			granted[m["principal"].(string)] = true
		}
	}
	if body.Decision == "granted" && granted[who] {
		writeJSONResp(w, 201, ap) // one person counts once; nothing recorded
		return
	}
	ap["decisions"] = append(decisions, d)
	switch {
	case body.Decision == "denied":
		ap["state"], ap["closed_at"] = "denied", d["at"]
		if q != nil {
			q["state"], q["decided_by"], q["decided_at"] = "denied", who, d["at"]
			if body.Reason != "" {
				q["reason"] = body.Reason
			}
		}
	case len(granted)+1 >= ap["required"].(int):
		ap["state"], ap["closed_at"] = "granted", d["at"]
		if q != nil {
			// The workflow deploys as the last approver.
			q["state"], q["decided_by"], q["decided_at"] = "deployed", who, d["at"]
			e := f.rel.envs[q["environment"].(string)]
			e.current, e.served = q["release_id"].(string), append(e.served, q["release_id"].(string))
		}
	}
	writeJSONResp(w, 201, ap)
}

// ── approvals ───────────────────────────────────────────────────────

func (f *fakeServer) findApproval(id string) map[string]any {
	for _, x := range f.appr.approvals {
		if x["id"] == id {
			return x
		}
	}
	return nil
}

func (f *fakeServer) apList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	items := []map[string]any{}
	for _, x := range f.appr.approvals {
		if (q.Get("project") != "" && x["project_id"] != q.Get("project")) ||
			(q.Get("subject") != "" && x["subject"] != q.Get("subject")) ||
			(q.Get("state") != "" && x["state"] != q.Get("state")) ||
			(q.Get("locale") != "" && x["locale"] != q.Get("locale")) ||
			(q.Get("message") != "" && x["subject_id"] != "msg_"+q.Get("message")) {
			continue
		}
		items = append(items, x)
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) apGet(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if x := f.findApproval(r.PathValue("id")); x != nil {
		writeJSONResp(w, 200, x)
		return
	}
	problemResp(w, 404, "not_found", "no such approval")
}

func (f *fakeServer) apDecide(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.findApproval(r.PathValue("id"))
	if x == nil {
		problemResp(w, 404, "not_found", "no such approval")
		return
	}
	var q map[string]any
	if x["subject"] == "release_request" {
		q = f.findRequest(x["subject_id"].(string))
	}
	f.decideOn(w, r, x, q)
}

// ── rollouts ────────────────────────────────────────────────────────

func (f *fakeServer) activeRolloutOf(env string) *fakeRollout {
	if f.appr == nil {
		return nil
	}
	for _, ro := range f.appr.rollouts {
		if ro.json["environment"] == env && ro.json["status"] == "active" {
			return ro
		}
	}
	return nil
}

// refuseUnderRollout answers rollout_active for a publish or promote
// into an environment with an active rollout.
func (f *fakeServer) refuseUnderRollout(w http.ResponseWriter, e *fakeEnv) bool {
	if ro := f.activeRolloutOf(e.name); ro != nil {
		problemResp(w, 409, "rollout_active", fmt.Sprintf("rollout %s is active in %s", ro.json["id"], e.name))
		return true
	}
	return false
}

func (f *fakeServer) writeRollout(w http.ResponseWriter, status int, ro *fakeRollout) {
	w.Header().Set("ETag", ro.etag())
	writeJSONResp(w, status, ro.json)
}

func (f *fakeServer) roList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.env(w, r) == nil {
		return
	}
	items := []map[string]any{}
	for i := len(f.appr.rollouts) - 1; i >= 0; i-- {
		if ro := f.appr.rollouts[i]; ro.json["environment"] == r.PathValue("env") {
			items = append(items, ro.json)
		}
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) roStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReleaseID   string  `json:"release_id"`
		Percent     int     `json:"percent"`
		MaxDuration *int    `json:"max_duration_seconds"`
		Force       bool    `json:"force"`
		ForceReason *string `json:"force_reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.env(w, r)
	if e == nil {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if id, ok := f.appr.rolloutKeys[key]; ok && key != "" {
		w.Header().Set("Idempotent-Replayed", "true")
		f.writeRollout(w, 201, f.findRollout(e.name, id))
		return
	}
	maxDur := 14 * 24 * 3600
	if body.MaxDuration != nil {
		maxDur = *body.MaxDuration
	}
	switch {
	case body.Percent < 0 || body.Percent > 100:
		problemResp(w, 400, "invalid_percent", "percent is 0–100")
		return
	case maxDur < 3600 || maxDur > 90*24*3600:
		problemResp(w, 400, "invalid_max_duration", "max_duration_seconds is one hour to 90 days")
		return
	case body.Force && body.ForceReason == nil:
		problemResp(w, 400, "force_reason_required", "force needs a force_reason")
		return
	case !body.Force && body.ForceReason != nil:
		problemResp(w, 400, "invalid_force_reason", "a force_reason without force")
		return
	}
	rel := f.release(w, body.ReleaseID)
	if rel == nil {
		return
	}
	switch {
	case e.approvals > 0:
		problemResp(w, 409, "rollout_needs_approval", e.name+" requires approvals; a release request can't carry a rollout yet")
		return
	case f.activeRolloutOf(e.name) != nil:
		problemResp(w, 409, "rollout_active", "a rollout is active in "+e.name)
		return
	case e.current == "":
		problemResp(w, 409, "rollout_no_stable", e.name+" serves no release")
		return
	case e.current == rel.id:
		problemResp(w, 409, "rollout_candidate_served", e.name+" already serves it")
		return
	case f.appr.policyUnmet && !body.Force:
		problemResp(w, 409, "policy_not_met", "de is 2 messages short of complete")
		return
	}
	f.appr.roSeq++
	started := fakeApprovalTime.Add(time.Duration(f.appr.roSeq) * time.Minute)
	ro := &fakeRollout{version: 1, json: map[string]any{"id": fmt.Sprintf("ro_%d", f.appr.roSeq), "environment": e.name,
		"release_id": rel.id, "stable_release_id": e.current, "percent": body.Percent, "status": "active",
		"max_duration_seconds": maxDur, "expires_at": started.Add(time.Duration(maxDur) * time.Second).Format(time.RFC3339),
		"forced": body.Force, "started_by": principalOf(r), "started_at": started.Format(time.RFC3339),
		"updated_at": started.Format(time.RFC3339)}}
	if body.Force {
		ro.json["force_reason"] = *body.ForceReason
	}
	f.appr.rollouts = append(f.appr.rollouts, ro)
	if key != "" {
		f.appr.rolloutKeys[key] = ro.json["id"].(string)
	}
	w.Header().Set("Location", r.URL.Path+"/"+ro.json["id"].(string))
	f.writeRollout(w, 201, ro)
}

func (f *fakeServer) findRollout(env, id string) *fakeRollout {
	for _, ro := range f.appr.rollouts {
		if ro.json["id"] == id && ro.json["environment"] == env {
			return ro
		}
	}
	return nil
}

func (f *fakeServer) roGet(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ro := f.findRollout(r.PathValue("env"), r.PathValue("id")); ro != nil {
		f.writeRollout(w, 200, ro)
		return
	}
	problemResp(w, 404, "not_found", "no such rollout")
}

func (f *fakeServer) roChange(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Percent *int `json:"percent"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		ifMatch := r.Header.Get("If-Match")
		f.appr.ifMatch = append(f.appr.ifMatch, op+" "+ifMatch)
		ro := f.findRollout(r.PathValue("env"), r.PathValue("id"))
		switch {
		case ro == nil:
			problemResp(w, 404, "not_found", "no such rollout")
			return
		case op == "advance" && ifMatch == "":
			problemResp(w, 428, "precondition_required", "If-Match is required")
			return
		case ifMatch != "" && ifMatch != ro.etag():
			problemResp(w, 412, "precondition_failed", fmt.Sprintf("the rollout is at version %d", ro.version))
			return
		case ro.json["status"] != "active":
			problemResp(w, 409, "rollout_ended", fmt.Sprintf("the rollout ended (%s)", ro.json["end"]))
			return
		}
		switch op {
		case "advance":
			if body.Percent == nil || *body.Percent < 0 || *body.Percent > 100 {
				problemResp(w, 400, "invalid_percent", "percent is 0–100")
				return
			}
			ro.json["percent"] = *body.Percent
			ro.version++
		case "complete":
			ro.end("completed", "completed")
			e := f.rel.envs[ro.json["environment"].(string)]
			e.current, e.served = ro.json["release_id"].(string), append(e.served, ro.json["release_id"].(string))
		default:
			ro.end("aborted", "aborted")
		}
		f.writeRollout(w, 200, ro)
	}
}
