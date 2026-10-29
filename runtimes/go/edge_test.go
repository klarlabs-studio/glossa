package glossa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"go.klarlabs.de/fortify/ferrors"
)

func testEdge(t *testing.T, f *fakeEdge, policy RetryPolicy) *edge {
	t.Helper()
	e, err := newEdge(fakeEdgeURL, fakeKey, fakeEnv, &http.Client{Transport: f}, policy.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.close)
	return e
}

var fastRetry = RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}

func TestEdgeManifest(t *testing.T) {
	f := newFakeEdge()
	f.serve(fakeResponse{status: 200, etag: `"m1"`, body: []byte(`{"x":1}`)}, nil)
	e := testEdge(t, f, fastRetry)

	res, err := e.manifest(context.Background(), "")
	if err != nil || res.notModified || res.etag != `"m1"` || string(res.body) != `{"x":1}` {
		t.Fatalf("manifest = %+v, %v", res, err)
	}
	res, err = e.manifest(context.Background(), `"m1"`)
	if err != nil || !res.notModified {
		t.Fatalf("revalidation = %+v, %v; want not modified", res, err)
	}
	reqs := f.recorded()
	if reqs[1].ifNoneMatch != `"m1"` || reqs[1].path != "/v1/pk_test/production/manifest.json" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestEdgeRetriesTransientFailures(t *testing.T) {
	f := newFakeEdge()
	f.serve(fakeResponse{status: 200, etag: `"m1"`, body: []byte(`{}`)}, nil)
	f.failNext = 2
	e := testEdge(t, f, fastRetry)
	if _, err := e.manifest(context.Background(), ""); err != nil {
		t.Fatalf("two transport errors then success: %v", err)
	}
	if n := len(f.recorded()); n != 3 {
		t.Fatalf("%d requests, want 3", n)
	}
}

func TestEdgeGivesUpAfterMaxAttempts(t *testing.T) {
	f := newFakeEdge() // answers 503
	e := testEdge(t, f, fastRetry)
	_, err := e.manifest(context.Background(), "")
	if !errors.Is(err, errNetwork) {
		t.Fatalf("err = %v, want errNetwork", err)
	}
	if n := len(f.recorded()); n != 3 {
		t.Fatalf("%d requests, want 3", n)
	}
}

func TestEdgeDoesNotRetryNotFound(t *testing.T) {
	f := newFakeEdge()
	f.serve(fakeResponse{status: http.StatusNotFound}, nil)
	e := testEdge(t, f, fastRetry)
	if _, err := e.manifest(context.Background(), ""); !errors.Is(err, errNetwork) {
		t.Fatalf("err = %v, want errNetwork", err)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("%d requests, want 1 (404 is final)", n)
	}
}

func TestEdgeCircuitOpensOnRepeatedFailure(t *testing.T) {
	f := newFakeEdge()
	e := testEdge(t, f, RetryPolicy{MaxAttempts: 1})
	for range breakerFailures {
		_, _ = e.manifest(context.Background(), "")
	}
	before := len(f.recorded())
	if _, err := e.manifest(context.Background(), ""); !errors.Is(err, errNetwork) {
		t.Fatalf("err = %v, want errNetwork", err)
	}
	if after := len(f.recorded()); after != before {
		t.Fatalf("an open circuit must not reach the edge (%d → %d requests)", before, after)
	}
}

func TestEdgeArtifact(t *testing.T) {
	ref := refFor(deArtifact)
	f := newFakeEdge()
	f.serve(fakeResponse{status: 503}, map[string][]byte{ref.SHA256: []byte(deArtifact)})
	e := testEdge(t, f, fastRetry)
	body, err := e.artifact(context.Background(), ref)
	if err != nil || string(body) != deArtifact {
		t.Fatalf("artifact = %q, %v", body, err)
	}
	if p := f.recorded()[0].path; p != "/v1/pk_test/a/"+ref.SHA256+".json" {
		t.Fatalf("path = %s", p)
	}
}

func TestEdgeArtifactLargerThanDeclared(t *testing.T) {
	ref := refFor(deArtifact)
	f := newFakeEdge()
	f.serve(fakeResponse{status: 503}, map[string][]byte{ref.SHA256: []byte(deArtifact + "padding")})
	e := testEdge(t, f, fastRetry)
	if _, err := e.artifact(context.Background(), ref); !errors.Is(err, errIntegrity) {
		t.Fatalf("err = %v, want errIntegrity", err)
	}
}

func TestEdgeHonoursCancellation(t *testing.T) {
	f := newFakeEdge()
	e := testEdge(t, f, RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := e.manifest(ctx, ""); err == nil {
		t.Fatal("want an error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("backoff ignored the context")
	}
}

func TestIsRetryable(t *testing.T) {
	cases := map[error]bool{
		io.ErrUnexpectedEOF:                         true,
		&httpStatusError{status: 503}:               true,
		&httpStatusError{status: 429}:               true,
		&httpStatusError{status: 404}:               false,
		&tooLargeError{}:                            false,
		context.Canceled:                            false,
		context.DeadlineExceeded:                    false,
		fmt.Errorf("x: %w", ferrors.ErrCircuitOpen): false,
	}
	for err, want := range cases {
		if got := isRetryable(err); got != want {
			t.Errorf("isRetryable(%v) = %v, want %v", err, got, want)
		}
	}
}

func TestNewEdgeValidatesURL(t *testing.T) {
	for _, u := range []string{"ftp://x", "not a url", "https://"} {
		if _, err := newEdge(u, "k", "e", http.DefaultClient, fastRetry); err == nil {
			t.Errorf("newEdge(%q) succeeded", u)
		}
	}
}
