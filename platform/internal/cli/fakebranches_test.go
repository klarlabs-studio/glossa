package cli

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// fakeBranches is the Branches API as the CLI uses it (RFC 0004 §4.1):
// a branch per name, its proposals, and the status report a push and a
// read both return.
type fakeBranches struct {
	byName map[string]*fakeBranch
	// conflicts are keys this server reports as proposed differently
	// elsewhere.
	conflicts map[string][]string
}

type fakeBranch struct {
	id, name, state string
	pr              *int
	headCommit      string
	previewURL      string
	newKeys         []string
	sourceProposals []string
	removed         []string
}

func newFakeBranches() *fakeBranches {
	return &fakeBranches{byName: map[string]*fakeBranch{}, conflicts: map[string][]string{}}
}

func (f *fakeServer) routeBranches(mux *http.ServeMux, p string) {
	mux.HandleFunc("GET "+p+"/branches", f.listBranches)
	mux.HandleFunc("POST "+p+"/branch-pushes", f.pushBranch)
	mux.HandleFunc("GET "+p+"/branches/{branch}", f.getBranch)
	mux.HandleFunc("POST "+p+"/branches/{branch}/closure", f.closeBranch)
	mux.HandleFunc("PUT "+p+"/branches/{branch}/preview", f.setBranchPreview)
}

func (f *fakeServer) branchJSON(b *fakeBranch) map[string]any {
	out := map[string]any{
		"id": b.id, "name": b.name, "state": b.state,
		"created_at": "2026-09-19T00:00:00Z", "updated_at": "2026-09-19T00:00:00Z",
	}
	if b.pr != nil {
		out["pr_number"] = *b.pr
	}
	if b.headCommit != "" {
		out["head_commit"] = b.headCommit
	}
	if b.previewURL != "" {
		out["preview_url"] = b.previewURL
	}
	return out
}

func (f *fakeServer) statusJSON(b *fakeBranch) map[string]any {
	conflicts := []map[string]any{}
	for _, k := range b.newKeys {
		if others, ok := f.branches.conflicts[k]; ok {
			conflicts = append(conflicts, map[string]any{"key": k, "branches": others})
		}
	}
	outdated := map[string]int{}
	if len(b.sourceProposals) > 0 {
		outdated["de"] = len(b.sourceProposals)
	}
	return map[string]any{
		"branch": f.branchJSON(b), "new_keys": b.newKeys, "source_proposals": b.sourceProposals,
		"removed": b.removed, "conflicts": conflicts, "outdated": outdated,
	}
}

func (f *fakeServer) branchByID(id string) *fakeBranch {
	for _, b := range f.branches.byName {
		if b.id == id {
			return b
		}
	}
	return nil
}

func (f *fakeServer) listBranches(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, state := r.URL.Query().Get("name"), r.URL.Query().Get("state")
	names := make([]string, 0, len(f.branches.byName))
	for n := range f.branches.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	items := []map[string]any{}
	for _, n := range names {
		b := f.branches.byName[n]
		if (name != "" && n != name) || (state != "" && b.state != state) {
			continue
		}
		items = append(items, f.branchJSON(b))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) pushBranch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Branch     string `json:"branch"`
		PRNumber   *int   `json:"pr_number"`
		HeadCommit string `json:"head_commit"`
		Complete   bool   `json:"complete"`
		Items      []struct {
			Key, Text string
		} `json:"items"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.branches.byName[body.Branch]
	if !ok {
		b = &fakeBranch{id: "brc_1", name: body.Branch, state: "open"}
		f.branches.byName[body.Branch] = b
	}
	if b.state == "merged" {
		problemResp(w, 409, "branch_merged", "the branch is merged; it takes no more pushes")
		return
	}
	b.state, b.pr, b.headCommit = "open", body.PRNumber, body.HeadCommit
	b.newKeys, b.sourceProposals, b.removed = nil, nil, nil
	pushed := map[string]bool{}
	results := []map[string]any{}
	for _, it := range body.Items {
		pushed[it.Key] = true
		c, err := mfcontent.Parse(mfcontent.MF1, it.Text, bcp47.MustParse(f.sourceLocale))
		if err != nil {
			results = append(results, map[string]any{"key": it.Key, "status": "failed",
				"error": map[string]any{"code": "invalid_message", "detail": err.Error()}})
			continue
		}
		m, live := f.messages[it.Key]
		status := "unchanged"
		switch {
		case !live:
			status = "new_key"
			b.newKeys = append(b.newKeys, it.Key)
			if _, conflicting := f.branches.conflicts[it.Key]; conflicting {
				status = "key_conflict"
			}
		case !m.content.SameModel(c):
			status = "source_proposal"
			b.sourceProposals = append(b.sourceProposals, it.Key)
		}
		results = append(results, map[string]any{"key": it.Key, "status": status})
	}
	if body.Complete {
		for key, m := range f.messages {
			if m.state == "active" && !pushed[key] {
				b.removed = append(b.removed, key)
			}
		}
		sort.Strings(b.removed)
	}
	out := f.statusJSON(b)
	out["items"] = results
	writeJSONResp(w, 200, out)
}

func (f *fakeServer) branchOfRequest(w http.ResponseWriter, r *http.Request) *fakeBranch {
	b := f.branchByID(r.PathValue("branch"))
	if b == nil {
		problemResp(w, 404, "not_found", "no such branch")
		return nil
	}
	return b
}

func (f *fakeServer) getBranch(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b := f.branchOfRequest(w, r); b != nil {
		writeJSONResp(w, 200, f.statusJSON(b))
	}
}

func (f *fakeServer) closeBranch(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.branchOfRequest(w, r)
	if b == nil {
		return
	}
	if b.state == "merged" {
		problemResp(w, 409, "branch_merged", "the branch is merged")
		return
	}
	b.state = "closed"
	writeJSONResp(w, 200, f.branchJSON(b))
}

func (f *fakeServer) setBranchPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.branchOfRequest(w, r)
	if b == nil {
		return
	}
	if body.URL != "" && !strings.HasPrefix(body.URL, "http") {
		problemResp(w, 400, "invalid_preview_url", "the preview URL must be absolute http(s)")
		return
	}
	b.previewURL = body.URL
	writeJSONResp(w, 200, f.branchJSON(b))
}
