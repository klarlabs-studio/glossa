package remote

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
)

// CaptureUpload is what a capture upload stored (RFC 0004 §3.2): the
// capture build, its captures, the images stored or already stored,
// and the region keys the catalog didn't know.
type CaptureUpload = apiclient.CaptureUpload

// UploadedCaptures is the answer to a capture upload.
type UploadedCaptures struct {
	CaptureUpload
	// Replayed is true when the same manifest was uploaded before: the
	// build is the first upload's and no image was read.
	Replayed bool `json:"replayed"`
}

// UploadCaptures posts a glossa.captures/v1 manifest with its images to
// the project's captures (POST …/captures, multipart/form-data). images
// maps each image's lowercase hex SHA-256, as the manifest's
// captures[].image.sha256 names it, to its PNG bytes; every image the
// manifest references needs exactly one entry. The body is streamed —
// the manifest, then the images by digest — so no image is held in
// memory, and it goes through the transfer client (no overall timeout;
// ctx bounds it). It isn't retried: the readers can't be read twice.
// The server refuses a body over 200 MB (payload_too_large).
func (c *Client) UploadCaptures(ctx context.Context, s Scope, manifest []byte, images map[string]io.Reader) (UploadedCaptures, error) {
	target := c.path("/v1/tenants/%s/projects/%s/captures", s.Tenant, s.Project)
	pr, pw := io.Pipe()
	defer func() { _ = pr.Close() }()
	mw := multipart.NewWriter(pw)
	go func() { _ = pw.CloseWithError(writeCaptures(mw, manifest, images)) }()

	req, err := apiclient.NewCreateCapturesRequestWithBody(c.server+"/", s.Tenant, s.Project, mw.FormDataContentType(), pr)
	if err != nil {
		return UploadedCaptures{}, err
	}
	req = req.WithContext(ctx)
	if err := c.editor(ctx, req); err != nil {
		return UploadedCaptures{}, err
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return UploadedCaptures{}, &APIError{Method: http.MethodPost, URL: target, Err: err}
	}
	r, err := apiclient.ParseCreateCapturesResponse(resp)
	if err := check(r, err, http.MethodPost, target); err != nil {
		return UploadedCaptures{}, err
	}
	switch {
	case r.JSON201 != nil:
		return UploadedCaptures{CaptureUpload: *r.JSON201}, nil
	case r.JSON200 != nil:
		return UploadedCaptures{CaptureUpload: *r.JSON200, Replayed: true}, nil
	}
	return UploadedCaptures{}, &APIError{Method: http.MethodPost, URL: target, Status: r.StatusCode(), Code: "unexpected_response",
		Detail: "the server answered without an upload"}
}

// writeCaptures writes the multipart body: the manifest part, then one
// image/png part per image, named by its digest, in digest order.
func writeCaptures(mw *multipart.Writer, manifest []byte, images map[string]io.Reader) error {
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="manifest"; filename="captures.json"`)
	h.Set("Content-Type", "application/json")
	w, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := w.Write(manifest); err != nil {
		return err
	}
	digests := make([]string, 0, len(images))
	for d := range images {
		digests = append(digests, d)
	}
	slices.Sort(digests)
	for _, d := range digests {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", multipartDisposition(d))
		h.Set("Content-Type", "image/png")
		w, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, images[d]); err != nil {
			return err
		}
	}
	return mw.Close()
}

// multipartDisposition names a part; a digest needs no quoting, but a
// caller's odd name must not break the header.
func multipartDisposition(name string) string {
	q := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '"' || r == '\\' || r == '\r' || r == '\n' {
			continue
		}
		q = append(q, r)
	}
	n := string(q)
	return `form-data; name="` + n + `"; filename="` + n + `.png"`
}
