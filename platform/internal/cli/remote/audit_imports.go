package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The import of v0.3's history (RFC 0006 §7.2): POST
// /v1/tenants/{tenant}/projects/{project}/audit-imports. The route is
// the wave-4 API slice's, built in parallel against the same use case
// (audit/app.V0HistoryImporter), so this call is written by hand
// against that shape until the generated client has it; it then moves
// onto apiclient like every other call here.

// V0HistoryRow is one v0.3 audit_log row as the route takes it: its
// digests, never its text (RFC 0006 §6.1).
type V0HistoryRow struct {
	V0ID         string    `json:"v0_id"`
	Action       string    `json:"action"`
	Actor        string    `json:"actor"`
	OccurredAt   time.Time `json:"occurred_at"`
	Key          string    `json:"key,omitempty"`
	Locale       string    `json:"locale,omitempty"`
	Unresolved   string    `json:"unresolved,omitempty"`
	BeforeSHA256 string    `json:"before_sha256,omitempty"`
	AfterSHA256  string    `json:"after_sha256,omitempty"`
}

// V0HistoryImport is one request's worth of rows and the restore they
// were read from.
type V0HistoryImport struct {
	Restore       string         `json:"restore"`
	RestoreSHA256 string         `json:"restore_sha256"`
	Entries       []V0HistoryRow `json:"entries"`
}

// V0HistoryReport is the route's answer: how many rows it recorded, and
// how many an earlier import already had.
type V0HistoryReport struct {
	Recorded int `json:"recorded"`
	Existing int `json:"existing"`
}

// MaxV0HistoryBatch is how many rows one request carries; the use case
// takes at most 1,000 in its one transaction.
const MaxV0HistoryBatch = 500

// ImportV0History sends one batch. It is idempotent on each row's v0_id
// (a row already recorded is counted as existing), so it is retried like
// a GET.
func (c *Client) ImportV0History(ctx context.Context, s Scope, in V0HistoryImport) (V0HistoryReport, error) {
	ref := c.path("/v1/tenants/%s/projects/%s/audit-imports", s.Tenant, s.Project)
	body, err := json.Marshal(in)
	if err != nil {
		return V0HistoryReport{}, err
	}
	req, err := http.NewRequestWithContext(idempotent(ctx), http.MethodPost, ref, bytes.NewReader(body))
	if err != nil {
		return V0HistoryReport{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := c.editor(ctx, req); err != nil {
		return V0HistoryReport{}, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return V0HistoryReport{}, &APIError{Method: http.MethodPost, URL: ref, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return V0HistoryReport{}, &APIError{Method: http.MethodPost, URL: ref, Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return V0HistoryReport{}, problem(resp.StatusCode, raw, http.MethodPost, ref)
	}
	var out V0HistoryReport
	if err := json.Unmarshal(raw, &out); err != nil {
		return V0HistoryReport{}, &APIError{Method: http.MethodPost, URL: ref, Status: resp.StatusCode, Code: "unexpected_response",
			Detail: fmt.Sprintf("the server answered without a report: %v", err)}
	}
	return out, nil
}
