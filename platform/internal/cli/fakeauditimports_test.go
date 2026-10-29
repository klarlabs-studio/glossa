package cli

import (
	"encoding/json"
	"io"
	"net/http"
)

// fakeAuditImports is the audit-imports route as RFC 0006 §7.2 shapes
// it: rows recorded once per v0_id across every call (the tenant's
// chain is one), 403 for a credential that is not an owner's, and every
// raw request body kept so a test can prove no text was sent.
type fakeAuditImports struct {
	recorded map[string]map[string]any
	order    []string
	bodies   [][]byte
	// refuse makes every import 403, as for any API token.
	refuse bool
	posts  int
}

func (f *fakeServer) routeAuditImports(mux *http.ServeMux, p string) {
	f.audit = &fakeAuditImports{recorded: map[string]map[string]any{}}
	mux.HandleFunc("POST "+p+"/audit-imports", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		a := f.audit
		a.posts++
		raw, _ := io.ReadAll(r.Body)
		a.bodies = append(a.bodies, raw)
		if a.refuse {
			problemResp(w, 403, "forbidden", "missing permission audit.import")
			return
		}
		var body struct {
			Restore       string           `json:"restore"`
			RestoreSHA256 string           `json:"restore_sha256"`
			Entries       []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.Restore == "" || body.RestoreSHA256 == "" {
			problemResp(w, 422, "invalid_request", "a restore and its digest are required")
			return
		}
		if len(body.Entries) > 1000 {
			problemResp(w, 422, "invalid_request", "at most 1000 entries")
			return
		}
		out := map[string]int{"recorded": 0, "existing": 0}
		seen := map[string]bool{}
		for _, e := range body.Entries {
			id, _ := e["v0_id"].(string)
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, ok := a.recorded[id]; ok {
				out["existing"]++
				continue
			}
			a.recorded[id] = e
			a.order = append(a.order, id)
			out["recorded"]++
		}
		writeJSONResp(w, 200, out)
	})
}
