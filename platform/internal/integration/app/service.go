package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Config tunes the jobs.
type Config struct {
	// MaxUploadBytes bounds an import's file (and what its reader
	// accepts).
	MaxUploadBytes int64
	// Retention is how long a job's file is kept.
	Retention time.Duration
	// UploadWindow is how long an import waits for its file.
	UploadWindow time.Duration
}

// Defaults.
const (
	DefaultMaxUploadBytes = 64 << 20
	DefaultRetention      = 7 * 24 * time.Hour
	DefaultUploadWindow   = 24 * time.Hour
)

func (c *Config) defaults() {
	if c.MaxUploadBytes <= 0 {
		c.MaxUploadBytes = DefaultMaxUploadBytes
	}
	if c.Retention <= 0 {
		c.Retention = DefaultRetention
	}
	if c.UploadWindow <= 0 {
		c.UploadWindow = DefaultUploadWindow
	}
}

// Deps are the Service's collaborators.
type Deps struct {
	Tx           Transactor
	Catalog      Catalog
	Localization Localization
	Knowledge    Knowledge
	Objects      Objects
	Config       Config
	Logger       *slog.Logger
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// Service implements Integration's use cases.
type Service struct {
	tx           Transactor
	catalog      Catalog
	localization Localization
	knowledge    Knowledge
	objects      Objects
	cfg          Config
	logger       *slog.Logger
	now          func() time.Time
}

// New returns the service.
func New(d Deps) *Service {
	d.Config.defaults()
	s := &Service{
		tx: d.Tx, catalog: d.Catalog, localization: d.Localization, knowledge: d.Knowledge, objects: d.Objects,
		cfg: d.Config, logger: d.Logger, now: d.Now,
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	return s
}

// Config returns the effective configuration.
func (s *Service) Config() Config { return s.cfg }

// Events, their aggregates, and the background principals.
const (
	EventImportCompleted = "integration.import.completed"
	EventExportCompleted = "integration.export.completed"
	aggregateImport      = "import"
	aggregateExport      = "export"
	principalWorker      = "integration.worker"
	principalDropProject = "integration.drop_project"
)

// JobEvent is the payload of integration.import.completed and
// integration.export.completed: the job's end, with its counts.
type JobEvent struct {
	JobID       string         `json:"job_id"`
	ProjectID   string         `json:"project_id,omitempty"`
	Kind        domain.Kind    `json:"kind"`
	Format      domain.Format  `json:"format"`
	Mode        domain.Mode    `json:"mode,omitempty"`
	State       domain.State   `json:"state"`
	FailureCode string         `json:"failure_code,omitempty"`
	ReusedJobID string         `json:"reused_job_id,omitempty"`
	Summary     domain.Summary `json:"summary"`
	By          string         `json:"by"`
}

// completedEvent announces j's end. by is who ended it: the person or
// token whose request did (a reused result, a cancel before the job
// ran), or the worker that ran it to its end. The payload's By stays
// the job's requester.
func completedEvent(j domain.Job, by outbox.Actor) outbox.Event {
	typ, agg := EventImportCompleted, aggregateImport
	if j.Direction == domain.Export {
		typ, agg = EventExportCompleted, aggregateExport
	}
	e := JobEvent{
		JobID: j.ID.String(), Kind: j.Kind, Format: j.Format, Mode: j.Mode, State: j.State,
		FailureCode: j.FailureCode, Summary: j.Summary, By: j.CreatedBy,
	}
	if j.ProjectID != nil {
		e.ProjectID = j.ProjectID.String()
	}
	if j.ReusedJobID != nil {
		e.ReusedJobID = j.ReusedJobID.String()
	}
	return outbox.Event{Type: typ, AggregateType: agg, AggregateID: j.ID.String(), Actor: by, Payload: e}
}

// actorOf returns the acting principal's actor.
func actorOf(ctx context.Context) (string, error) {
	p, err := authz.Authenticated(ctx)
	if err != nil {
		return "", err
	}
	return p.Actor.String(), nil
}

// newJobID derives a create's ID from its Idempotency-Key, or a new
// time-ordered ID when there is none.
func newJobID(ctx context.Context, operation, by, key string) (uuid.UUID, error) {
	if key == "" {
		return uuid.Must(uuid.NewV7()), nil
	}
	if err := idempotency.CheckKey(key); err != nil {
		return uuid.Nil, err
	}
	tenant, _ := tenancy.FromContext(ctx)
	return idempotency.ID(operation, tenant.String(), by, key), nil
}

// fileKey is where a job's object lives: under integration/, which
// glossa-edge never serves.
func fileKey(tenant uuid.UUID, job uuid.UUID, name string) string {
	return fmt.Sprintf("integration/v1/%s/jobs/%s/%s", tenant, job, name)
}

func tenantOf(ctx context.Context) uuid.UUID {
	t, _ := tenancy.FromContext(ctx)
	return t.UUID()
}

// withoutTenant drops the tenant a handler runs in, for a write to a
// queue that belongs to the system scope: the unit of work refuses a
// system transaction started from a tenant's context.
func withoutTenant(ctx context.Context) context.Context {
	return tenancy.ContextWithTenant(ctx, tenancy.ID{})
}

func invalidPageToken() error {
	return problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this list issued")
}

// requireActor checks that the caller is the job's requester: jobs are
// visible to anyone with integration.read, but only the requester
// uploads a job's file.
func requireActor(ctx context.Context, j domain.Job) error {
	by, err := actorOf(ctx)
	if err != nil {
		return err
	}
	if by != j.CreatedBy {
		return &authz.DeniedError{Permission: authz.IntegrationImport}
	}
	return nil
}
