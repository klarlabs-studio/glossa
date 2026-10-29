// Package app is the Context context's application layer (RFC 0004 §2–
// §3): ingesting glossa.usages/v1 documents and captures, with keys
// resolved to message IDs at ingest through Catalog's port; the current
// usages of a message on the default branch or a branch view; the
// active messages no current build uses; and retention.
package app

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Errors. Adapters translate storage and Catalog errors into these.
var (
	ErrNotFound            = errors.New("context: not found")
	ErrProjectNotFound     = errors.New("context: no such project")
	ErrApplicationNotFound = errors.New("context: no such application in the project")
	ErrMessageNotFound     = errors.New("context: no such message in the project")
	ErrBuildNotFound       = errors.New("context: no such build in the project")
	// ErrCaptureConflict means a build already holds a different
	// capture of the same route, viewport and locale.
	ErrCaptureConflict = errors.New("context: the build already holds another capture of this route, viewport and locale")
	// ErrRateLimited means the tenant uploaded too much too fast.
	ErrRateLimited = errors.New("context: too many uploads; slow down")
	// ErrInvalidQuery means a list's filter is malformed.
	ErrInvalidQuery    = errors.New("context: invalid query")
	ErrCaptureNotFound = errors.New("context: no such capture in the project")
	// ErrStorageUnavailable means object storage failed: the upload or
	// read can be retried.
	ErrStorageUnavailable = errors.New("context: image storage is unavailable; retry later")
	// ErrStorageQuotaExceeded means the tenant's capture images would
	// pass its storage quota (RFC 0004 §3.3). Unlike ErrRateLimited,
	// retrying alone doesn't help: the tenant has to let retention free
	// space, or the deployment has to raise the quota.
	ErrStorageQuotaExceeded = errors.New("context: the tenant's capture storage quota is exhausted")
)

// Limiter decides whether a tenant may upload now: Allow consumes one
// upload from key's budget.
type Limiter interface {
	Allow(ctx context.Context, key string) bool
}

// Metrics records Context in the deployment's metrics (RFC 0004 §11).
type Metrics interface {
	// BuildIngested counts an upload: its usages and unknown keys, or a
	// replay of an earlier one.
	BuildIngested(source domain.Source, usages, unknownKeys int, replayed bool)
	// Coverage is the share of a project's active messages with a
	// current usage (default branch).
	Coverage(tenant tenancy.ID, project uuid.UUID, active, used int)
	// CapturesIngested counts a capture upload's captures and regions,
	// the images it stored and those already stored (deduplicated), and
	// the bytes it added to the tenant's object storage.
	CapturesIngested(tenant tenancy.ID, captures, regions, imagesStored, imagesDeduplicated int, bytesStored int64)
	// StorageUsed is what the tenant's capture images occupy now, and
	// the quota they are bounded by (RFC 0004 §3.3). Unlike the ingested
	// bytes, it goes down again when retention deletes images.
	StorageUsed(tenant tenancy.ID, used, quota int64)
	// CaptureCoverage is the share of a project's active messages with a
	// visible region on a current capture (default branch).
	CaptureCoverage(tenant tenancy.ID, project uuid.UUID, active, captured int)
	// Purged counts one project's retention deletions by kind: the
	// builds, the captures that went with them, and the images object
	// storage no longer holds.
	Purged(p Purged)
}

// NoMetrics records nothing.
type NoMetrics struct{}

// BuildIngested implements Metrics.
func (NoMetrics) BuildIngested(domain.Source, int, int, bool) {}

// Coverage implements Metrics.
func (NoMetrics) Coverage(tenant tenancy.ID, project uuid.UUID, active, used int) {}

// CapturesIngested implements Metrics.
func (NoMetrics) CapturesIngested(tenancy.ID, int, int, int, int, int64) {}

// StorageUsed implements Metrics.
func (NoMetrics) StorageUsed(tenancy.ID, int64, int64) {}

// CaptureCoverage implements Metrics.
func (NoMetrics) CaptureCoverage(tenancy.ID, uuid.UUID, int, int) {}

// Purged implements Metrics.
func (NoMetrics) Purged(Purged) {}

// MessageRef names a Catalog message.
type MessageRef struct {
	ID  uuid.UUID
	Key string
}

// Catalog is Catalog's application port, as Context uses it: projects,
// applications by slug, keys resolved to message IDs, the active
// messages, and (with the branch overlay, RFC 0004 §4.1) closed
// branches.
type Catalog interface {
	// Project answers ErrProjectNotFound for an unknown project.
	Project(ctx context.Context, project uuid.UUID) error
	// DefaultBranch is the project's default branch, a Catalog setting
	// (ErrProjectNotFound): ingest decides by it whether a build is of
	// the default branch, not by what the uploader says.
	DefaultBranch(ctx context.Context, project uuid.UUID) (string, error)
	// Application resolves an application's slug: ErrProjectNotFound or
	// ErrApplicationNotFound.
	Application(ctx context.Context, project uuid.UUID, slug string) (uuid.UUID, error)
	// MessageIDs resolves keys to message IDs; keys the catalog doesn't
	// know are absent from the map.
	MessageIDs(ctx context.Context, project uuid.UUID, keys []string) (map[string]uuid.UUID, error)
	// ActiveMessages lists the project's active messages in key order.
	ActiveMessages(ctx context.Context, project uuid.UUID) ([]MessageRef, error)
	// ClosedBranches reports when each of the project's closed branches
	// closed.
	ClosedBranches(ctx context.Context, project uuid.UUID) (map[domain.Branch]time.Time, error)
}

// CaptureFindings are one capture's visual findings, with the ID the
// ingest just minted for it and the scope the two-sighting rule counts
// in.
type CaptureFindings struct {
	Capture uuid.UUID
	// Previous is the capture the application showed last of this same
	// (route, viewport, locale), or uuid.Nil where it showed none. The
	// findings stored against it are what makes a finding here a second
	// sighting (RFC 0005 §5.2) — and a scope captured for the first time
	// promotes nothing, which is the safe direction.
	Previous uuid.UUID
	// Route and Viewport are the rest of the scope; Locale is the
	// capture's locale, which is also the locale of every finding on it
	// that names no other.
	Route    string
	Viewport domain.Viewport
	Locale   string
	Findings []domain.VisualFinding
}

// RecordFindings is a capture upload's visual findings, on their way to
// Quality.
type RecordFindings struct {
	Project uuid.UUID
	// Ref is the branch the upload was of, Commit the commit it
	// captured, and At when the build was recorded.
	Ref    string
	Commit string
	At     time.Time
	// Captures are the upload's captures that carry findings.
	Captures []CaptureFindings
}

// Findings is Quality's application port, as Context uses it: a capture
// upload carries the visual probe pass's findings (RFC 0005 §5.1) and
// Quality owns them.
//
// Context validates them — a page may only report what a page can see,
// at the one severity it may emit, about a region that is really on the
// capture — and completes what a browser cannot know: the message each
// key resolves to, and the capture each finding is on. Quality computes
// the fingerprint from that, grades them against the project's policy
// and stores them as ordinary findings. Nothing about them is stored
// with the capture.
type Findings interface {
	// RecordFindings stores the upload's findings and returns how many
	// were stored — fewer than were handed in when the project's policy
	// switched the visual layer, or some of its codes, off.
	RecordFindings(ctx context.Context, in RecordFindings) (int, error)
}

// ImageNormalizer validates and re-encodes uploaded capture images
// (RFC 0004 §3.3): nothing uploaded is stored as it came.
type ImageNormalizer interface {
	// Normalize reads one uploaded image and checks that it is a PNG of
	// at most 10 MB and 40 megapixels whose SHA-256 is want, then
	// re-encodes its pixels without metadata. It fails with
	// domain.ErrInvalidImage or domain.ErrImageTooLarge, or with the
	// reader's own error.
	Normalize(ctx context.Context, part io.Reader, want domain.Digest) (NormalizedImage, error)
}

// NormalizedImage is a re-encoded image, held (on disk, not in memory)
// until it is stored.
type NormalizedImage interface {
	// Image is the re-encoded PNG's digest and its size in pixels.
	Image() domain.Image
	// Size is the re-encoded PNG's size in bytes.
	Size() int64
	// Open reads the re-encoded PNG.
	Open() (io.ReadCloser, error)
	// Close discards it.
	Close() error
}

// Objects is object storage as Context uses it: capture images,
// content-addressed by domain.ImageKey (objectstore.StreamStore).
type Objects interface {
	Exists(ctx context.Context, key string) (bool, error)
	PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error)
	// Open answers objectstore.ErrNotFound for a missing object.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// Transactor runs units of work scoped to the tenant on ctx.
type Transactor interface {
	InTenant(ctx context.Context, fn func(context.Context, Store) error) error
}

// ProjectRef is a tenant's project, as the retention sweep finds it.
type ProjectRef struct {
	Tenant  tenancy.ID
	Project uuid.UUID
}

// Sweeper lists, across tenants (system scope), the projects holding
// builds: what the daily retention run visits.
type Sweeper interface {
	ProjectsWithBuilds(ctx context.Context) ([]ProjectRef, error)
}

// BuildRecord is a stored build with the usages of unknown keys it
// holds.
type BuildRecord struct {
	domain.Build
	UnknownKeys int
}

// BuildCursor is where a page of builds (newest first) continues.
type BuildCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// UsageCursor is where a page of usages (by build and position)
// continues; the zero cursor is the start.
type UsageCursor struct {
	Build    uuid.UUID
	Position int
}

// UsageFilter narrows the usages in the current builds; empty members
// don't filter.
type UsageFilter struct {
	Route     string
	Component string
	File      string
	// Unknown keeps only the usages of keys the catalog didn't know at
	// ingest (RFC 0004 §2.2). The Glossa PR check reports them with
	// their file:line, and a branch with thousands of usages should not
	// have to be paged through to find the handful that are unknown.
	Unknown bool
}

// UsageView is a usage with the build it belongs to.
type UsageView struct {
	domain.Usage
	// Position is the usage's index in its build's document.
	Position        int
	BuildID         uuid.UUID
	ApplicationID   uuid.UUID
	Commit          domain.Commit
	Branch          domain.Branch
	OnDefaultBranch bool
	Source          domain.Source
}

// CaptureView is a capture with the build it belongs to.
type CaptureView struct {
	domain.Capture
	ApplicationID   uuid.UUID
	Commit          domain.Commit
	Branch          domain.Branch
	OnDefaultBranch bool
}

// Store is Context's persistence in tenant scope.
type Store interface {
	// InsertBuild stores b; inserted is false if a build of the same
	// upload (application, commit, source, digest) exists.
	InsertBuild(ctx context.Context, b domain.Build) (inserted bool, err error)
	// BuildByUpload returns the build of an upload (ErrNotFound).
	BuildByUpload(ctx context.Context, application uuid.UUID, commit domain.Commit, source domain.Source, digest domain.Digest) (domain.Build, error)
	Build(ctx context.Context, id uuid.UUID) (domain.Build, error)
	Builds(ctx context.Context, ids []uuid.UUID) ([]domain.Build, error)
	// BuildSummaries returns every build of the project.
	BuildSummaries(ctx context.Context, project uuid.UUID) ([]domain.BuildSummary, error)
	// InsertUsages stores a build's usages, by position.
	InsertUsages(ctx context.Context, build uuid.UUID, usages []domain.Usage) error
	CountUnknownKeys(ctx context.Context, build uuid.UUID) (int, error)
	// MessageUsages returns up to limit usages of message in builds.
	MessageUsages(ctx context.Context, message uuid.UUID, builds []uuid.UUID, limit int) ([]UsageView, error)
	// UsedMessages returns the messages with a usage in builds.
	UsedMessages(ctx context.Context, builds []uuid.UUID) ([]uuid.UUID, error)
	// ListBuilds returns up to limit of the project's builds (of one
	// application when application isn't nil), newest first, after the
	// cursor when it isn't nil.
	ListBuilds(ctx context.Context, project uuid.UUID, application *uuid.UUID, after *BuildCursor, limit int) ([]BuildRecord, error)
	// ListUsages returns up to limit usages in builds matching f, by
	// build and position after the cursor.
	ListUsages(ctx context.Context, builds []uuid.UUID, f UsageFilter, after UsageCursor, limit int) ([]UsageView, error)
	// CoLocatedMessages returns up to limit messages that share a route
	// or a capture with message in builds, most shared first.
	CoLocatedMessages(ctx context.Context, message uuid.UUID, builds []uuid.UUID, limit int) ([]uuid.UUID, error)

	// LockBuildCaptures serializes adding captures to a build until the
	// transaction ends.
	LockBuildCaptures(ctx context.Context, build uuid.UUID) error
	CountCaptures(ctx context.Context, build uuid.UUID) (int, error)
	// InsertCapture stores c with its regions; inserted is false if the
	// build holds a capture of the same route, viewport and locale.
	InsertCapture(ctx context.Context, c domain.Capture) (inserted bool, err error)
	// CaptureByShot returns a build's capture of a route, viewport and
	// locale, with its regions (ErrNotFound).
	CaptureByShot(ctx context.Context, build uuid.UUID, route string, v domain.Viewport, locale bcp47.Tag) (domain.Capture, error)
	// PreviousCaptureOfScope is the capture the application showed last
	// of one (route, viewport, locale), whatever branch it came from
	// (ErrNotFound). It is what the visual layer's two-sighting rule
	// counts against (RFC 0005 §5.2), and the ingest asks it before its
	// own captures are stored, so the answer is the capture immediately
	// before this one.
	PreviousCaptureOfScope(
		ctx context.Context, project, application uuid.UUID, route string, v domain.Viewport, locale bcp47.Tag,
	) (uuid.UUID, error)
	// Capture returns a capture without its regions (ErrNotFound).
	Capture(ctx context.Context, id uuid.UUID) (domain.Capture, error)
	// CaptureShows reports whether the capture has a region of any of
	// messages.
	CaptureShows(ctx context.Context, capture uuid.UUID, messages []uuid.UUID) (bool, error)
	// UnknownRegionKeys returns the distinct keys of a build's regions
	// the catalog didn't know at ingest, in order.
	UnknownRegionKeys(ctx context.Context, build uuid.UUID) ([]string, error)
	// MessageCaptures returns up to limit captures in builds that show
	// message, each with only that message's regions.
	MessageCaptures(ctx context.Context, message uuid.UUID, builds []uuid.UUID, limit int) ([]CaptureView, error)
	// CapturedMessages returns the messages with a visible region on the
	// builds' captures.
	CapturedMessages(ctx context.Context, builds []uuid.UUID) ([]uuid.UUID, error)

	// CountCapturesOfBuilds counts the captures the builds hold: what a
	// purge deletes with them.
	CountCapturesOfBuilds(ctx context.Context, builds []uuid.UUID) (int, error)
	// StoredImageBytes is what the tenant's capture images occupy in
	// object storage: the sum over the distinct images its captures
	// reference, since the same pixels are stored once per project. It
	// is what the per-tenant storage quota is measured against
	// (RFC 0004 §3.3).
	StoredImageBytes(ctx context.Context) (int64, error)
	// BuildImages returns the images the builds' captures reference;
	// ReferencedImages which of digests the project's captures still do;
	// ProjectImages every image the project's captures reference.
	BuildImages(ctx context.Context, builds []uuid.UUID) ([]domain.Digest, error)
	ReferencedImages(ctx context.Context, project uuid.UUID, digests []domain.Digest) ([]domain.Digest, error)
	ProjectImages(ctx context.Context, project uuid.UUID) ([]domain.Digest, error)
	// DeleteBuilds deletes builds with their usages, captures and
	// regions.
	DeleteBuilds(ctx context.Context, ids []uuid.UUID) (int, error)
	// DeleteProjectData erases everything Context holds for a project;
	// DeleteApplicationData for one of its applications.
	DeleteProjectData(ctx context.Context, project uuid.UUID) error
	DeleteApplicationData(ctx context.Context, project, application uuid.UUID) error

	Publish(ctx context.Context, e outbox.Event) error
}
