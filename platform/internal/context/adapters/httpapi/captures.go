package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
)

// manifestPart is the name of a capture upload's first part.
const manifestPart = "manifest"

// imageCacheControl lets a browser keep a capture's image: it is
// content-addressed and never changes (RFC 0004 §3.3).
const imageCacheControl = "private, max-age=31536000, immutable"

// CaptureUploadPath reports whether a request is a capture upload
// (POST /v1/tenants/{tenant}/projects/{project}/captures), whose body
// may reach domain.MaxCaptureUploadBytes: the composition root lifts the
// body limit there.
func CaptureUploadPath(method, path string) bool {
	s := strings.Split(path, "/")
	return method == http.MethodPost && len(s) == 7 && s[0] == "" && s[1] == "v1" && s[2] == "tenants" && s[3] != "" &&
		s[4] == "projects" && s[5] != "" && s[6] == "captures"
}

// CreateCaptures ingests a capture upload: the manifest part, then the
// image parts, streamed one at a time into the service (which spools
// each to disk while it checks it). Nothing is held in memory but the
// manifest.
func (a *API) CreateCaptures(ctx context.Context, req apiv1.CreateCapturesRequestObject) (apiv1.CreateCapturesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	manifest, err := readManifest(req.Body)
	if err != nil {
		return nil, mapError(err)
	}
	out, err := a.svc.IngestCaptures(ctx, app.IngestCaptures{Project: project, Manifest: manifest, Images: &imageParts{r: req.Body}})
	if err != nil {
		return nil, mapError(err)
	}
	body := apiv1.CaptureUpload{
		Build: toBuild(app.BuildRecord{Build: out.Build}), Captures: out.Captures, ImagesStored: out.ImagesStored,
		ImagesDeduplicated: out.ImagesDeduplicated, UnknownKeys: out.UnknownKeys, Findings: out.Findings,
	}
	if body.UnknownKeys == nil {
		body.UnknownKeys = []string{}
	}
	if out.Replayed {
		return apiv1.CreateCaptures200JSONResponse{
			Body: body, Headers: apiv1.CreateCaptures200ResponseHeaders{IdempotentReplayed: apiconv.Ptr("true")},
		}, nil
	}
	return apiv1.CreateCaptures201JSONResponse(body), nil
}

// readManifest reads the first part, which must be the manifest, up to
// its limit.
func readManifest(r *multipart.Reader) ([]byte, error) {
	p, err := r.NextPart()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the body has no parts", domain.ErrInvalidCaptures)
	}
	if err != nil {
		return nil, bodyError(err)
	}
	if p.FormName() != manifestPart {
		return nil, fmt.Errorf("%w: the first part must be the %q", domain.ErrInvalidCaptures, manifestPart)
	}
	b, err := io.ReadAll(io.LimitReader(partReader{p}, domain.MaxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > domain.MaxManifestBytes {
		return nil, domain.ErrManifestTooLarge
	}
	return b, nil
}

// imageParts implements app.ImageParts over the rest of the body.
type imageParts struct{ r *multipart.Reader }

func (p *imageParts) Next() (string, io.Reader, error) {
	part, err := p.r.NextPart()
	if err != nil {
		return "", nil, bodyError(err)
	}
	return part.FormName(), partReader{part}, nil
}

// partReader reports a body that broke off inside a part as an invalid
// upload, not as a server failure.
type partReader struct{ r io.Reader }

func (p partReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if err != nil && !errors.Is(err, io.EOF) {
		err = bodyError(err)
	}
	return n, err
}

// bodyError classifies a failure to read the multipart body: io.EOF
// after the last part, the body limit (413), or a malformed or
// interrupted body (invalid_captures).
func bodyError(err error) error {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF):
		return io.EOF
	case errors.As(err, &tooLarge):
		return err
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	return fmt.Errorf("%w: the multipart body is malformed or broke off: %v", domain.ErrInvalidCaptures, err)
}

// captureID parses a capture ID; a malformed one is simply not found.
func captureID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrCaptureNotFound)
	}
	return id, nil
}

// GetCaptureImage streams a capture's image. Its ETag is the stored
// PNG's digest, so a matching If-None-Match answers 304 without reading
// storage.
func (a *API) GetCaptureImage(ctx context.Context, req apiv1.GetCaptureImageRequestObject) (apiv1.GetCaptureImageResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	capture, err := captureID(req.Capture)
	if err != nil {
		return nil, err
	}
	img, err := a.svc.CaptureImage(ctx, project, capture)
	if err != nil {
		return nil, mapError(err)
	}
	etag := `"` + img.Digest.String() + `"`
	if inm := req.Params.IfNoneMatch; inm != nil && etagMatches(*inm, etag) {
		return apiv1.GetCaptureImage304Response{Headers: apiv1.GetCaptureImage304ResponseHeaders{
			CacheControl: apiconv.Ptr(imageCacheControl), ETag: &etag,
		}}, nil
	}
	body, err := img.Open()
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetCaptureImage200ImagepngResponse{Body: body, Headers: apiv1.GetCaptureImage200ResponseHeaders{
		CacheControl: apiconv.Ptr(imageCacheControl), ETag: &etag,
	}}, nil
}

// etagMatches is If-None-Match's weak comparison (RFC 9110 §13.1.2).
func etagMatches(header, etag string) bool {
	for v := range strings.SplitSeq(header, ",") {
		v = strings.TrimPrefix(strings.TrimSpace(v), "W/")
		if v == "*" || v == etag {
			return true
		}
	}
	return false
}

// ImagePath is the API path of a capture's image.
func ImagePath(tenant, project string, capture uuid.UUID) string {
	return "/v1/tenants/" + tenant + "/projects/" + project + "/captures/" + capture.String() + "/image"
}

func toMessageCapture(tenant, project string, c app.CaptureView) apiv1.MessageCapture {
	out := apiv1.MessageCapture{
		Id: c.ID.String(), BuildId: c.BuildID.String(), ApplicationId: c.ApplicationID.String(), Commit: c.Commit.String(),
		Branch: c.Branch.String(), OnDefaultBranch: c.OnDefaultBranch, Route: c.Route, Locale: c.Locale.String(),
		Viewport: apiv1.CaptureViewport{Width: c.Viewport.Width, Height: c.Viewport.Height},
		Image: apiv1.CaptureImage{
			Digest: c.Image.Digest.String(), Width: c.Image.Width, Height: c.Image.Height, Url: ImagePath(tenant, project, c.ID),
		},
		Regions: make([]apiv1.CaptureRegion, len(c.Regions)), CreatedAt: c.CreatedAt,
	}
	for i, r := range c.Regions {
		out.Regions[i] = apiv1.CaptureRegion{
			Kind: apiv1.CaptureRegionKind(r.Kind), Visible: r.Visible,
			Box: apiv1.CaptureBox{X: r.Box.X, Y: r.Box.Y, Width: r.Box.Width, Height: r.Box.Height},
		}
	}
	return out
}

func (a *API) ListMessageCaptures(ctx context.Context, req apiv1.ListMessageCapturesRequestObject) (apiv1.ListMessageCapturesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	got, err := a.svc.CapturesOfKey(ctx, project, req.Message, app.UsageQuery{
		Branch: deref(req.Params.Branch), Limit: deref(req.Params.Limit),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListMessageCaptures200JSONResponse{
		MessageId: got.MessageID.String(), Key: req.Message, Truncated: got.Truncated,
		Captures: make([]apiv1.MessageCapture, len(got.Captures)),
	}
	for i, c := range got.Captures {
		out.Captures[i] = toMessageCapture(req.Tenant, req.Project, c)
	}
	return out, nil
}
