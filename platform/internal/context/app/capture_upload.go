package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// errNoImages means the service was built without WithImages.
var errNoImages = errors.New("context: capture images need object storage (WithImages)")

// ImageParts yields a capture upload's image parts, in the order they
// arrive after the manifest.
type ImageParts interface {
	// Next returns the next part's name and body; the body is valid until
	// the next call. It returns io.EOF after the last part.
	Next() (name string, body io.Reader, err error)
}

// IngestCaptures is the input of IngestCaptures.
type IngestCaptures struct {
	Project uuid.UUID
	// Manifest is the glossa.captures/v1 manifest's bytes.
	Manifest []byte
	// Images are the upload's image parts, each named by the lowercase
	// hex SHA-256 of its bytes as the manifest references it.
	Images ImageParts
}

// CapturesIngested says what IngestCaptures stored.
type CapturesIngested struct {
	// Build is the capture build (source capture).
	Build domain.Build
	// Captures counts the build's captures.
	Captures int
	// ImagesStored counts the images written to object storage;
	// ImagesDeduplicated those whose pixels were stored already. Both
	// are 0 on a replay: its images aren't read.
	ImagesStored       int
	ImagesDeduplicated int
	// UnknownKeys are the region keys the catalog didn't know, in order
	// (stored with a null message ID, like usages).
	UnknownKeys []string
	// Findings counts the visual findings the upload's captures carried
	// that Quality stored (RFC 0005 §5.1). It is 0 on a replay, when the
	// upload carried none, when the project's policy switched the visual
	// layer off, and in a deployment that does not run Quality.
	Findings int
	// Replayed is true when the same manifest was uploaded before for
	// the application and commit: nothing changed and Build is the first
	// upload's.
	Replayed bool
}

// IngestCaptures stores a capture upload (RFC 0004 §3.2–§3.3): a build
// of source capture with the manifest's captures and their regions,
// region keys resolved to message IDs. Every image part is checked
// against its name and its manifest entry, re-encoded and stored
// content-addressed, once per project. The upload is idempotent by
// application, commit and manifest digest: a repeat returns the first
// build without reading its images. Rate-limited per tenant with the
// usage uploads (ErrRateLimited). Needs catalog.write.
func (s *Service) IngestCaptures(ctx context.Context, in IngestCaptures) (_ CapturesIngested, err error) {
	// One span per CI upload (RFC 0004 §11), like the usages upload.
	ctx, end := s.span(ctx, "context.ingest_captures",
		attribute.String("glossa.project_id", in.Project.String()),
		attribute.Int("glossa.manifest_bytes", len(in.Manifest)))
	defer end(&err)

	by, err := actorIn(ctx, authz.CatalogWrite, in.Project)
	if err != nil {
		return CapturesIngested{}, err
	}
	if s.objects == nil || s.images == nil {
		return CapturesIngested{}, errNoImages
	}
	if err := s.allowUpload(ctx); err != nil {
		return CapturesIngested{}, err
	}
	up, err := domain.ParseCaptures(in.Manifest)
	if err != nil {
		return CapturesIngested{}, err
	}
	b, err := s.captureBuild(ctx, in.Project, up, by)
	if err != nil {
		return CapturesIngested{}, err
	}
	if first, found, err := s.capturesUploaded(ctx, b); err != nil || found {
		s.recordCaptures(ctx, first, 0, 0)
		return first, err
	}
	stored, err := s.storedBytes(ctx)
	if err != nil {
		return CapturesIngested{}, err
	}
	rec, err := s.receiveImages(ctx, in.Project, up, in.Images, stored)
	var (
		captures []domain.Capture
		out      CapturesIngested
		findings int
	)
	if err == nil {
		var unknown []string
		if captures, unknown, err = s.newCaptures(ctx, b, up, rec.images); err == nil {
			// The findings go to Quality before the captures are stored,
			// so a failure to record them leaves nothing behind: the
			// upload is idempotent by its manifest, and a retry after a
			// half-done ingest would replay the build and could never
			// add the findings it lost.
			if findings, err = s.recordFindings(ctx, b, up, captures); err == nil {
				out, err = s.insertCaptures(ctx, b, captures, unknown)
			}
		}
	}
	if err != nil {
		s.discardImages(ctx, rec.written)
		return CapturesIngested{}, err
	}
	if !out.Replayed {
		out.ImagesStored, out.ImagesDeduplicated, out.Findings = rec.stored, rec.deduplicated, findings
	}
	s.recordCaptures(ctx, out, regionCount(captures), rec.bytes)
	s.recordStorage(ctx, rec.stored0+rec.bytes)
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("glossa.build_id", out.Build.ID.String()),
		attribute.Int("glossa.captures", out.Captures),
		attribute.Int("glossa.visual_findings", out.Findings),
		attribute.Int("glossa.regions", regionCount(captures)),
		attribute.Int("glossa.images_stored", out.ImagesStored),
		attribute.Int("glossa.images_deduplicated", out.ImagesDeduplicated),
		attribute.Bool("glossa.replayed", out.Replayed))
	return out, nil
}

func (s *Service) captureBuild(ctx context.Context, project uuid.UUID, up domain.CaptureUpload, by string) (domain.Build, error) {
	defaultBranch, err := s.catalog.DefaultBranch(ctx, project)
	if err != nil {
		return domain.Build{}, err
	}
	application, err := s.catalog.Application(ctx, project, up.Application)
	if err != nil {
		return domain.Build{}, err
	}
	return domain.NewBuild(project, application, up.Upload, domain.SourceCapture, up.Branch.String() == defaultBranch, by, s.now())
}

func (s *Service) recordCaptures(ctx context.Context, out CapturesIngested, regions int, bytes int64) {
	if out.Build.ID == uuid.Nil {
		return
	}
	s.metrics.BuildIngested(domain.SourceCapture, 0, 0, out.Replayed)
	if !out.Replayed {
		t, _ := tenancy.FromContext(ctx)
		s.metrics.CapturesIngested(t, out.Captures, regions, out.ImagesStored, out.ImagesDeduplicated, bytes)
	}
}

// recordStorage reports what the tenant's capture images occupy now,
// against the quota that bounds them (RFC 0004 §3.3, §11).
func (s *Service) recordStorage(ctx context.Context, used int64) {
	t, _ := tenancy.FromContext(ctx)
	s.metrics.StorageUsed(t, used, s.storageQuota)
}

func regionCount(cs []domain.Capture) int {
	n := 0
	for _, c := range cs {
		n += len(c.Regions)
	}
	return n
}

// capturesUploaded finds an earlier ingest of b's manifest.
func (s *Service) capturesUploaded(ctx context.Context, b domain.Build) (CapturesIngested, bool, error) {
	var out CapturesIngested
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		out, err = replayCaptures(ctx, st, b)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return CapturesIngested{}, false, nil
	}
	return out, err == nil, err
}

func replayCaptures(ctx context.Context, st Store, b domain.Build) (CapturesIngested, error) {
	first, err := st.BuildByUpload(ctx, b.ApplicationID, b.Commit, b.Source, b.Digest)
	if err != nil {
		return CapturesIngested{}, err
	}
	n, err := st.CountCaptures(ctx, first.ID)
	if err != nil {
		return CapturesIngested{}, err
	}
	unknown, err := st.UnknownRegionKeys(ctx, first.ID)
	if unknown == nil {
		unknown = []string{}
	}
	return CapturesIngested{Build: first, Captures: n, UnknownKeys: unknown, Replayed: true}, err
}

// received is what an upload's image parts stored.
type received struct {
	// images maps each uploaded digest to its stored image.
	images map[domain.Digest]domain.Image
	// seen are the stored images' digests.
	seen map[domain.Digest]bool
	// written are the keys this upload wrote: removed again if the
	// upload fails.
	written []string
	// stored counts the images written, deduplicated those whose pixels
	// were stored already; bytes is what was written.
	stored, deduplicated int
	bytes                int64
	// stored0 is what the tenant held before this upload: stored0 +
	// bytes is what it holds now, and what the quota bounds.
	stored0 int64
}

// storedBytes is what the tenant's capture images already occupy in
// object storage, the base the storage quota is measured from
// (RFC 0004 §3.3).
func (s *Service) storedBytes(ctx context.Context) (int64, error) {
	var n int64
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		n, err = st.StoredImageBytes(ctx)
		return err
	})
	return n, err
}

// receiveImages reads every image part: each names an image of the
// manifest, once, and is the PNG its entry describes. Each is stored as
// soon as it is re-encoded, so an upload holds one image on disk at a
// time, however many it carries. stored is what the tenant already
// holds: the upload is refused as soon as its new pixels would take the
// tenant past its quota, and the caller discards what it wrote.
func (s *Service) receiveImages(ctx context.Context, project uuid.UUID, up domain.CaptureUpload, parts ImageParts, stored int64) (received, error) {
	want := map[domain.Digest]domain.Image{}
	for _, img := range up.Parts() {
		want[img.Digest] = img
	}
	rec := received{images: map[domain.Digest]domain.Image{}, seen: map[domain.Digest]bool{}, stored0: stored}
	for {
		name, body, err := parts.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rec, err
		}
		d, err := domain.ParseDigest(name)
		entry, referenced := want[d]
		_, twice := rec.images[d]
		switch {
		case err != nil || !referenced:
			return rec, fmt.Errorf("%w: part %q is no image of the manifest", domain.ErrInvalidCaptures, name)
		case twice:
			return rec, fmt.Errorf("%w: image %s is uploaded twice", domain.ErrInvalidCaptures, d)
		}
		if err := s.receiveImage(ctx, project, entry, body, &rec); err != nil {
			return rec, err
		}
	}
	for _, img := range up.Parts() {
		if _, ok := rec.images[img.Digest]; !ok {
			return rec, fmt.Errorf("%w: no part holds image %s", domain.ErrInvalidCaptures, img.Digest)
		}
	}
	return rec, nil
}

// receiveImage re-encodes one part and stores it content-addressed,
// unless the project (or this upload) stored the same pixels already.
func (s *Service) receiveImage(ctx context.Context, project uuid.UUID, entry domain.Image, body io.Reader, rec *received) error {
	n, err := s.images.Normalize(ctx, body, entry.Digest)
	if err != nil {
		return err
	}
	defer func() { _ = n.Close() }()
	img := n.Image()
	if img.Width != entry.Width || img.Height != entry.Height {
		return fmt.Errorf("%w: image %s is %d×%d pixels; the manifest says %d×%d", domain.ErrInvalidImage, entry.Digest,
			img.Width, img.Height, entry.Width, entry.Height)
	}
	rec.images[entry.Digest] = img
	stored := rec.seen[img.Digest]
	rec.seen[img.Digest] = true
	t, _ := tenancy.FromContext(ctx)
	key := domain.ImageKey(t, project, img.Digest)
	if !stored {
		if stored, err = s.objects.Exists(ctx, key); err != nil {
			return fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
		}
	}
	if stored {
		rec.deduplicated++
		return nil
	}
	// Only new pixels cost quota: the project pays for an image once,
	// however many captures show it.
	if over := rec.stored0 + rec.bytes + n.Size() - s.storageQuota; over > 0 {
		return fmt.Errorf("%w: image %s would take the tenant %d bytes past its %d-byte capture storage quota; "+
			"retention frees space as builds age out", ErrStorageQuotaExceeded, img.Digest, over, s.storageQuota)
	}
	r, err := n.Open()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	size, err := s.objects.PutStream(ctx, key, r, "image/png")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	rec.written = append(rec.written, key)
	rec.stored++
	rec.bytes += size
	return nil
}

// discardImages removes the images a failed upload wrote. Best effort:
// what can't be removed stays unreferenced, and a retry of the same
// pixels reuses it.
func (s *Service) discardImages(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := s.objects.Delete(context.WithoutCancel(ctx), key); err != nil {
			s.logger.WarnContext(ctx, "context: image of a failed upload not removed", slog.String("key", key),
				slog.Any("error", err))
		}
	}
}

// newCaptures builds the captures of b: each on its stored image, with
// its region keys resolved to message IDs. It returns the keys the
// catalog didn't know, in order.
func (s *Service) newCaptures(ctx context.Context, b domain.Build, up domain.CaptureUpload, images map[domain.Digest]domain.Image) ([]domain.Capture, []string, error) {
	ids, err := s.catalog.MessageIDs(ctx, b.ProjectID, up.Keys())
	if err != nil {
		return nil, nil, err
	}
	out := make([]domain.Capture, len(up.Captures))
	unknown := map[string]bool{}
	for i, in := range up.Captures {
		in.Image = images[in.Image.Digest]
		in.Regions = slices.Clone(in.Regions)
		// The upload's findings are resolved in place: they stay on the
		// manifest rather than on the capture, because a finding is
		// Quality's and a capture is Context's, and recordFindings reads
		// them back beside the capture this loop mints.
		domain.ResolveFindings(up.Captures[i].Findings, ids)
		c, err := domain.NewCapture(b.ProjectID, b.ID, in, b.CreatedBy, b.CreatedAt)
		if err != nil {
			return nil, nil, err
		}
		domain.ResolveRegions(c.Regions, ids)
		for _, r := range c.Regions {
			if r.MessageID == nil {
				unknown[r.Key] = true
			}
		}
		out[i] = c
	}
	keys := make([]string, 0, len(unknown))
	for k := range unknown {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return out, keys, nil
}

// recordFindings hands the upload's visual findings to Quality, each
// beside the capture it is on (RFC 0005 §5.1, §13 wave 4).
//
// captures is parallel to up.Captures, so the capture the ingest just
// minted is the capture a manifest entry's findings are about — which
// is the whole reason this runs here and not in the browser: `r_3`
// means nothing until something knows which capture it is `r_3` of.
func (s *Service) recordFindings(
	ctx context.Context, b domain.Build, up domain.CaptureUpload, captures []domain.Capture,
) (int, error) {
	if s.findings == nil || up.FindingCount() == 0 {
		return 0, nil
	}
	in := RecordFindings{Project: b.ProjectID, Ref: b.Branch.String(), Commit: b.Commit.String(), At: b.CreatedAt}
	for i, c := range captures {
		if fs := up.Captures[i].Findings; len(fs) > 0 {
			in.Captures = append(in.Captures, CaptureFindings{
				Capture: c.ID, Route: c.Route, Viewport: c.Viewport, Locale: c.Locale.String(), Findings: fs,
			})
		}
	}
	if err := s.previousCaptures(ctx, b, in.Captures); err != nil {
		return 0, err
	}
	return s.findings.RecordFindings(ctx, in)
}

// previousCaptures fills in each scope's previous capture: the one this
// application showed last of the same route, viewport and locale. It is
// the state the two-sighting rule needs, and the server has it because
// it keeps every capture's findings — where `glossa capture --check`
// has to keep a file in the workspace and a CI runner that throws the
// workspace away promotes nothing.
//
// A scope nobody has captured before leaves Previous at uuid.Nil, and
// its findings are first sightings. So does a lookup that fails: this
// runs before anything is stored, and losing a promotion is the safe
// direction where losing the upload is not.
func (s *Service) previousCaptures(ctx context.Context, b domain.Build, captures []CaptureFindings) error {
	if len(captures) == 0 {
		return nil
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		for i, c := range captures {
			locale, err := bcp47.Parse(c.Locale)
			if err != nil {
				continue
			}
			previous, err := st.PreviousCaptureOfScope(ctx, b.ProjectID, b.ApplicationID, c.Route, c.Viewport, locale)
			switch {
			case errors.Is(err, ErrNotFound):
				continue
			case err != nil:
				return err
			}
			captures[i].Previous = previous
		}
		return nil
	})
}

// insertCaptures stores b with its captures in one unit of work and
// publishes context.build.ingested and a context.capture.ingested per
// capture. A concurrent upload of the same manifest that won makes this
// one its replay.
func (s *Service) insertCaptures(ctx context.Context, b domain.Build, captures []domain.Capture, unknown []string) (CapturesIngested, error) {
	var out CapturesIngested
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		inserted, err := st.InsertBuild(ctx, b)
		if err != nil {
			return err
		}
		if !inserted {
			out, err = replayCaptures(ctx, st, b)
			return err
		}
		for _, c := range captures {
			ok, err := st.InsertCapture(ctx, c)
			if err != nil {
				return err
			}
			if !ok { // the manifest holds each shot once; a new build none
				return ErrCaptureConflict
			}
			if err := st.Publish(ctx, outbox.Event{
				Type: domain.EventCaptureIngested, AggregateType: domain.AggregateCapture, AggregateID: c.ID.String(),
				Actor:   outbox.Actor(c.CreatedBy),
				Payload: domain.CaptureIngestedOf(c, b), OccurredAt: c.CreatedAt,
			}); err != nil {
				return err
			}
		}
		out = CapturesIngested{Build: b, Captures: len(captures), UnknownKeys: unknown}
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventBuildIngested, AggregateType: domain.AggregateBuild, AggregateID: b.ID.String(),
			Actor:   outbox.Actor(b.CreatedBy),
			Payload: domain.BuildIngestedOf(b, 0), OccurredAt: b.CreatedAt,
		})
	})
	return out, err
}
