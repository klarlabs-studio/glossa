package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"time"
)

// The fake server's Release endpoints: environments with policies,
// publishes that build one artifact per locale from the eligible
// translations, promotions, rollbacks, diffs, bundles and delivery keys,
// with the contract's problem codes.

type fakeEnv struct {
	name    string
	states  []string
	current string   // release ID
	served  []string // release IDs, oldest first
	version int
	// approvals is the environment's approval requirement (0: none):
	// that many distinct people of role reviewer (fakeapprovals_test.go).
	approvals int
}

type fakeRel struct {
	id, env, parent, note string
	version               int
	states                []string
	created               time.Time
	artifacts             map[string][]byte // locale → bytes
	messages              map[string]map[string]string
	newArtifacts          int
}

type fakeKey struct {
	id, name, key string
	environments  []string
	branches      bool
	revoked       *time.Time
}

type fakeReleases struct {
	envs      map[string]*fakeEnv
	releases  []*fakeRel
	keys      []*fakeKey
	idem      map[string]string // Idempotency-Key → release ID
	stored    map[string]bool   // artifact digests
	tamper    bool              // serve artifacts that don't match their hash
	releaseAt time.Time
}

func newFakeReleases() *fakeReleases {
	approved, all := []string{"approved"}, []string{"approved", "needs_review", "draft"}
	return &fakeReleases{
		envs: map[string]*fakeEnv{
			"development": {name: "development", states: all, version: 1},
			"preview":     {name: "preview", states: all, version: 1},
			"staging":     {name: "staging", states: approved, version: 1},
			"production":  {name: "production", states: approved, version: 1},
		},
		idem: map[string]string{}, stored: map[string]bool{},
		releaseAt: time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC),
	}
}

func (f *fakeServer) routeReleases(mux *http.ServeMux, p string) {
	mux.HandleFunc("GET "+p+"/environments", f.listEnvironments)
	mux.HandleFunc("GET "+p+"/environments/{env}", f.getEnvironment)
	mux.HandleFunc("POST "+p+"/environments/{env}/promotions", f.promote)
	mux.HandleFunc("POST "+p+"/environments/{env}/rollbacks", f.rollback)
	mux.HandleFunc("POST "+p+"/environments/{env}/release-previews", f.previewRelease)
	mux.HandleFunc("GET "+p+"/releases", f.listReleases)
	mux.HandleFunc("POST "+p+"/releases", f.publish)
	mux.HandleFunc("GET "+p+"/releases/{id}", f.getRelease)
	mux.HandleFunc("GET "+p+"/releases/{id}/diff", f.diff)
	mux.HandleFunc("GET "+p+"/releases/{id}/manifest", f.manifest)
	mux.HandleFunc("GET "+p+"/releases/{id}/artifacts/{digest}", f.artifact)
	mux.HandleFunc("GET "+p+"/delivery-keys", f.listKeys)
	mux.HandleFunc("POST "+p+"/delivery-keys", f.createKey)
	mux.HandleFunc("PUT "+p+"/delivery-keys/{id}/scope", f.setKeyScope)
	mux.HandleFunc("DELETE "+p+"/delivery-keys/{id}", f.revokeKey)
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (f *fakeServer) envJSON(e *fakeEnv) map[string]any {
	out := map[string]any{"name": e.name, "policy": map[string]any{"states": e.states, "include_outdated": false},
		"created_at": "2026-09-19T00:00:00Z", "updated_at": "2026-09-19T00:00:00Z"}
	if e.current != "" {
		out["current_release_id"] = e.current
	}
	return out
}

func (f *fakeServer) relJSON(r *fakeRel) map[string]any {
	locales, counts := []map[string]any{}, map[string]any{}
	total := 0
	for _, l := range f.locales {
		if r.artifacts[l] == nil {
			continue
		}
		locales = append(locales, map[string]any{"code": l, "direction": "ltr"})
		counts[l] = map[string]any{"messages": len(r.messages[l]), "outdated": 0}
		total += len(r.artifacts[l])
	}
	out := map[string]any{"id": r.id, "version": r.version, "environment": r.env, "author": "token:1",
		"created_at": r.created.Format(time.RFC3339), "source_locale": f.sourceLocale, "locales": locales,
		"manifest_digest": fmt.Sprintf("%064d", r.version), "policy": map[string]any{"states": r.states, "include_outdated": false},
		"counts": map[string]any{"messages": len(r.messages[f.sourceLocale]), "artifacts": len(r.artifacts),
			"new_artifacts": r.newArtifacts, "bytes": total, "locales": counts}}
	if r.parent != "" {
		out["parent_id"] = r.parent
	}
	if r.note != "" {
		out["note"] = r.note
	}
	return out
}

func (f *fakeServer) findRelease(id string) *fakeRel {
	for _, r := range f.rel.releases {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (f *fakeServer) env(w http.ResponseWriter, r *http.Request) *fakeEnv {
	e := f.rel.envs[r.PathValue("env")]
	if e == nil {
		problemResp(w, 404, "not_found", "no such environment")
	}
	return e
}

func (f *fakeServer) listEnvironments(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.rel.envs))
	for n := range f.rel.envs {
		names = append(names, n)
	}
	sort.Strings(names)
	items := []map[string]any{}
	for _, n := range names {
		items = append(items, f.envJSON(f.rel.envs[n]))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) getEnvironment(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.env(w, r); e != nil {
		writeJSONResp(w, 200, f.envJSON(e))
	}
}

func (f *fakeServer) publish(w http.ResponseWriter, r *http.Request) {
	var body struct{ Environment, Note string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Header.Get("Idempotency-Key")
	if id, ok := f.rel.idem[key]; ok && key != "" {
		w.Header().Set("Idempotent-Replayed", "true")
		if q := f.heldByKey(key); q != nil {
			f.writeHeld(w, q)
			return
		}
		writeJSONResp(w, 201, f.relJSON(f.findRelease(id)))
		return
	}
	e := f.rel.envs[body.Environment]
	if e == nil {
		problemResp(w, 400, "invalid_environment", fmt.Sprintf("no environment %q", body.Environment))
		return
	}
	if f.refuseUnderRollout(w, e) {
		return
	}
	if len(f.messages) == 0 {
		problemResp(w, 422, "not_releasable", "the project has no active messages")
		return
	}
	rel := f.build(e)
	rel.id, rel.env, rel.parent, rel.note = fmt.Sprintf("rel_%d", len(f.rel.releases)+1), e.name, e.current, body.Note
	rel.version, rel.created = len(f.rel.releases)+1, f.rel.releaseAt.Add(time.Duration(len(f.rel.releases))*time.Hour)
	for _, b := range rel.artifacts {
		f.rel.stored[digest(b)] = true
	}
	f.rel.releases = append(f.rel.releases, rel)
	if key != "" {
		f.rel.idem[key] = rel.id
	}
	if e.approvals > 0 {
		f.writeHeld(w, f.hold(r, e, rel, "publish", key))
		return
	}
	e.current, e.served = rel.id, append(e.served, rel.id)
	writeJSONResp(w, 201, f.relJSON(rel))
}

// build is what publishing to e would ship, stored nowhere.
func (f *fakeServer) build(e *fakeEnv) *fakeRel {
	rel := &fakeRel{states: e.states, artifacts: map[string][]byte{}, messages: map[string]map[string]string{}}
	for _, l := range f.locales {
		msgs := map[string]string{}
		for k, m := range f.messages {
			if l == f.sourceLocale {
				msgs[k] = m.content.Text
			} else if t := f.translations[l][k]; t != nil && slices.Contains(e.states, t.state) {
				msgs[k] = t.content.Text
			}
		}
		b, _ := json.Marshal(map[string]any{"schema": "glossa.artifact/v1", "locale": l, "namespace": "default", "messages": msgs})
		rel.artifacts[l], rel.messages[l] = b, msgs
		if !f.rel.stored[digest(b)] {
			rel.newArtifacts++
		}
	}
	return rel
}

func (f *fakeServer) previewRelease(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.env(w, r)
	if e == nil {
		return
	}
	out := map[string]any{"environment": e.name, "policy": map[string]any{"states": e.states, "include_outdated": false},
		"problems": []any{}, "releasable": true}
	var base *fakeRel
	if e.current != "" {
		base = f.findRelease(e.current)
		out["base_release_id"] = base.id
	}
	if len(f.messages) == 0 {
		out["releasable"] = false
		out["problems"] = []any{map[string]any{"code": "not_releasable", "detail": "the project has no active messages"}}
		writeJSONResp(w, 200, out)
		return
	}
	head := f.build(e)
	rel := f.relJSON(head)
	for _, k := range []string{"source_locale", "locales", "counts", "manifest_digest"} {
		out[k] = rel[k]
	}
	out["manifest_digest"] = digest([]byte(fmt.Sprint(head.messages)))
	out["changes"] = f.localeDiffs(base, head)
	writeJSONResp(w, 200, out)
}

func (f *fakeServer) listReleases(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for i := len(f.rel.releases) - 1; i >= 0; i-- {
		items = append(items, f.relJSON(f.rel.releases[i]))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) release(w http.ResponseWriter, id string) *fakeRel {
	rel := f.findRelease(id)
	if rel == nil {
		problemResp(w, 404, "release_not_found", "the project has no such release")
	}
	return rel
}

func (f *fakeServer) getRelease(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rel := f.release(w, r.PathValue("id")); rel != nil {
		writeJSONResp(w, 200, f.relJSON(rel))
	}
}

func (f *fakeServer) diff(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	head := f.release(w, r.PathValue("id"))
	if head == nil {
		return
	}
	baseID := head.parent
	if b := r.URL.Query().Get("base"); b != "" {
		baseID = b
	}
	var base *fakeRel
	if baseID != "" {
		if base = f.release(w, baseID); base == nil {
			return
		}
	}
	out := map[string]any{"release_id": head.id}
	if base != nil {
		out["base_release_id"] = base.id
	}
	out["locales"] = f.localeDiffs(base, head)
	writeJSONResp(w, 200, out)
}

// localeDiffs compares what head ships with base (nil: nothing).
func (f *fakeServer) localeDiffs(base, head *fakeRel) []map[string]any {
	var locales []map[string]any
	for _, l := range f.locales {
		var before map[string]string
		if base != nil {
			before = base.messages[l]
		}
		added, changed, removed := []string{}, []string{}, []string{}
		for k, v := range head.messages[l] {
			if old, ok := before[k]; !ok {
				added = append(added, k)
			} else if old != v {
				changed = append(changed, k)
			}
		}
		for k := range before {
			if _, ok := head.messages[l][k]; !ok {
				removed = append(removed, k)
			}
		}
		sort.Strings(added)
		sort.Strings(changed)
		sort.Strings(removed)
		locales = append(locales, map[string]any{"locale": l, "added": added, "changed": changed, "removed": removed})
	}
	return locales
}

func (f *fakeServer) manifestBytes(rel *fakeRel, env string) []byte {
	arts := map[string]any{}
	for l, b := range rel.artifacts {
		arts[l] = map[string]any{"default": map[string]any{"sha256": digest(b), "size": len(b)}}
	}
	b, _ := json.Marshal(map[string]any{"schema": "glossa.manifest/v1", "project": "prj_1", "environment": env,
		"release":      map[string]any{"id": rel.id, "version": rel.version, "createdAt": rel.created.Format(time.RFC3339)},
		"sourceLocale": f.sourceLocale, "locales": f.locales, "fallback": map[string]any{"*": []string{f.sourceLocale}},
		"artifacts": arts, "signatures": []any{}})
	return b
}

func (f *fakeServer) manifest(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel := f.release(w, r.PathValue("id"))
	if rel == nil {
		return
	}
	env := r.URL.Query().Get("environment")
	if env == "" {
		problemResp(w, 400, "bad_request", "environment is required")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(f.manifestBytes(rel, env))
}

func (f *fakeServer) artifact(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel := f.release(w, r.PathValue("id"))
	if rel == nil {
		return
	}
	for _, b := range rel.artifacts {
		if digest(b) == r.PathValue("digest") {
			if f.rel.tamper {
				b = append(slices.Clone(b), ' ')
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
	}
	problemResp(w, 404, "not_found", "no such artifact")
}

func (f *fakeServer) promote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReleaseID string `json:"release_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.env(w, r)
	if e == nil {
		return
	}
	rel := f.release(w, body.ReleaseID)
	if rel == nil {
		return
	}
	for _, s := range rel.states {
		if !slices.Contains(e.states, s) {
			problemResp(w, 409, "release_ineligible", fmt.Sprintf("release %d ships %s translations; %s ships only %v", rel.version, s, e.name, e.states))
			return
		}
	}
	if f.refuseUnderRollout(w, e) {
		return
	}
	if e.approvals > 0 {
		f.writeHeld(w, f.hold(r, e, rel, "promote", ""))
		return
	}
	if e.current != rel.id {
		e.current, e.served = rel.id, append(e.served, rel.id)
	}
	writeJSONResp(w, 200, f.envJSON(e))
}

func (f *fakeServer) rollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReleaseID *string `json:"release_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.env(w, r)
	if e == nil {
		return
	}
	cur := f.findRelease(e.current)
	var target *fakeRel
	if body.ReleaseID != nil {
		if target = f.release(w, *body.ReleaseID); target == nil {
			return
		}
		if !slices.Contains(e.served, target.id) {
			problemResp(w, 409, "not_in_history", fmt.Sprintf("%s never served release %d", e.name, target.version))
			return
		}
	} else {
		for _, id := range e.served {
			if r := f.findRelease(id); cur != nil && r.version < cur.version && (target == nil || r.version > target.version) {
				target = r
			}
		}
		if target == nil {
			problemResp(w, 409, "no_rollback_target", e.name+" served no release before the current one")
			return
		}
	}
	e.current, e.served = target.id, append(e.served, target.id)
	// A rollback is never refused: it aborts the rollout (RFC 0006 §5.2).
	if ro := f.activeRolloutOf(e.name); ro != nil {
		ro.end("aborted", "rolled_back")
	}
	writeJSONResp(w, 200, f.envJSON(e))
}

func keyJSON(k *fakeKey) map[string]any {
	envs := k.environments
	if envs == nil {
		envs = []string{}
	}
	out := map[string]any{"id": k.id, "name": k.name, "key": k.key, "created_by": "token:1", "created_at": "2026-09-19T00:00:00Z",
		"scope": map[string]any{"environments": envs, "branches": k.branches}}
	if k.revoked != nil {
		out["revoked_at"] = k.revoked.Format(time.RFC3339)
	}
	return out
}

func (f *fakeServer) listKeys(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	for _, k := range f.rel.keys {
		items = append(items, keyJSON(k))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) createKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string
		Scope *struct {
			Environments []string
			Branches     bool
		}
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		problemResp(w, 400, "invalid_key_name", "a key needs a name")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.rel.keys) + 1
	k := &fakeKey{id: fmt.Sprintf("key_%d", n), name: body.Name, key: fmt.Sprintf("glossa_pk_%040d", n),
		environments: []string{"production"}}
	if body.Scope != nil {
		k.environments, k.branches = body.Scope.Environments, body.Scope.Branches
	}
	f.rel.keys = append(f.rel.keys, k)
	writeJSONResp(w, 201, keyJSON(k))
}

// setKeyScope is PUT …/delivery-keys/{id}/scope.
func (f *fakeServer) setKeyScope(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Environments []string
		Branches     bool
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.rel.keys {
		if k.id != r.PathValue("id") {
			continue
		}
		if k.revoked != nil {
			problemResp(w, 409, "key_revoked", "the key is revoked")
			return
		}
		if len(body.Environments) == 0 && !body.Branches {
			problemResp(w, 400, "invalid_key_scope", "a scope allows 1-50 environments, or branches")
			return
		}
		k.environments, k.branches = body.Environments, body.Branches
		writeJSONResp(w, 200, keyJSON(k))
		return
	}
	problemResp(w, 404, "not_found", "no such key")
}

func (f *fakeServer) revokeKey(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.rel.keys {
		if k.id != r.PathValue("id") {
			continue
		}
		if k.revoked != nil {
			problemResp(w, 409, "key_revoked", "the key is already revoked")
			return
		}
		at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
		k.revoked = &at
		w.WriteHeader(http.StatusNoContent)
		return
	}
	problemResp(w, 404, "not_found", "no such delivery key")
}
