package remote

import (
	"context"
	"net/http"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// The tenant's audit trail and its export jobs (RFC 0006 §6.2).

type (
	AuditEntry          = apiclient.AuditEntry
	AuditExportJob      = apiclient.AuditExportJob
	AuditExportJobInput = apiclient.AuditExportJobCreate
	AuditExportFile     = apiclient.AuditExportFile
)

const (
	AuditExportJobStateFailed    = apiclient.AuditExportJobStateFailed
	AuditExportJobStateSucceeded = apiclient.AuditExportJobStateSucceeded
)

// AuditFilter narrows AuditEntries; an empty field doesn't. Project is a
// project's id.
type AuditFilter struct {
	From, To                    *time.Time
	FirstSequence, LastSequence *int64
	Actor, Action, Project      string
	Source                      string
	Ascending                   bool
}

// AuditEntries lists entries, newest first (oldest first when
// Ascending), at most limit (0: all).
func (c *Client) AuditEntries(ctx context.Context, tenant string, f AuditFilter, limit int) ([]AuditEntry, error) {
	params := apiclient.ListAuditEntriesParams{From: f.From, To: f.To, FirstSequence: f.FirstSequence, LastSequence: f.LastSequence,
		Actor: optional(f.Actor), Action: optional(f.Action), Project: optional(f.Project)}
	if f.Source != "" {
		s := apiclient.AuditSource(f.Source)
		params.Source = &s
	}
	if f.Ascending {
		o := apiclient.ListAuditEntriesParamsOrder("asc")
		params.Order = &o
	}
	return limited(limit, func(size int, tok *string) ([]AuditEntry, *string, error) {
		p := params
		p.PageSize, p.PageToken = &size, tok
		r, err := c.api.ListAuditEntriesWithResponse(ctx, tenant, &p)
		if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/audit-entries")); err != nil {
			return nil, nil, err
		}
		return r.JSON200.Items, r.JSON200.NextPageToken, nil
	})
}

// CreateAuditExportJob queues an export; the Idempotency-Key makes a
// retried request create it once.
func (c *Client) CreateAuditExportJob(ctx context.Context, tenant string, body AuditExportJobInput, idempotencyKey string) (AuditExportJob, error) {
	r, err := c.api.CreateAuditExportJobWithResponse(idempotent(ctx), tenant,
		&apiclient.CreateAuditExportJobParams{IdempotencyKey: optional(idempotencyKey)}, body)
	if err := check(r, err, http.MethodPost, c.tenantPath(tenant, "/audit-export-jobs")); err != nil {
		return AuditExportJob{}, err
	}
	return *r.JSON201, nil
}

// AuditExportJob reads an export job.
func (c *Client) AuditExportJob(ctx context.Context, tenant, id string) (AuditExportJob, error) {
	r, err := c.api.GetAuditExportJobWithResponse(ctx, tenant, id)
	if err := check(r, err, http.MethodGet, c.tenantPath(tenant, "/audit-export-jobs/%s", id)); err != nil {
		return AuditExportJob{}, err
	}
	return *r.JSON200, nil
}
