package glossa

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	fakeEdgeURL = "https://edge.test"
	fakeKey     = "pk_test"
	fakeEnv     = "production"
)

// fakeEdge is an http.RoundTripper that answers like glossa-edge
// (runtimes/SPEC.md §2) from in-memory state.
type fakeEdge struct {
	mu        sync.Mutex
	manifest  fakeResponse
	artifacts map[string][]byte
	requests  []fakeRequest
	failNext  int // answer this many requests with a transport error first
}

type fakeResponse struct {
	status int
	etag   string
	body   []byte
}

type fakeRequest struct {
	path, ifNoneMatch string
}

func newFakeEdge() *fakeEdge {
	return &fakeEdge{manifest: fakeResponse{status: http.StatusServiceUnavailable}, artifacts: map[string][]byte{}}
}

func (f *fakeEdge) serve(manifest fakeResponse, artifacts map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.manifest = manifest
	f.artifacts = artifacts
}

func (f *fakeEdge) recorded() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

func (f *fakeEdge) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, fakeRequest{path: r.URL.Path, ifNoneMatch: r.Header.Get("If-None-Match")})
	if f.failNext > 0 {
		f.failNext--
		return nil, io.ErrUnexpectedEOF
	}
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	prefix := "/v1/" + fakeKey + "/"
	switch {
	case r.URL.Path == prefix+fakeEnv+"/manifest.json":
		return f.manifestResponse(r), nil
	case strings.HasPrefix(r.URL.Path, prefix+"a/"):
		digest := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix+"a/"), ".json")
		if body, ok := f.artifacts[digest]; ok {
			return response(r, http.StatusOK, body, nil), nil
		}
	}
	return response(r, http.StatusNotFound, nil, nil), nil
}

func (f *fakeEdge) manifestResponse(r *http.Request) *http.Response {
	m := f.manifest
	if m.status == http.StatusOK && m.etag != "" && r.Header.Get("If-None-Match") == m.etag {
		return response(r, http.StatusNotModified, nil, nil)
	}
	header := http.Header{}
	if m.etag != "" {
		header.Set("ETag", m.etag)
	}
	return response(r, m.status, m.body, header)
}

func response(r *http.Request, status int, body []byte, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    r,
	}
}
