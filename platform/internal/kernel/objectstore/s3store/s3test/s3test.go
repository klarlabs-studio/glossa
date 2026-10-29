//go:build integration || system

// Package s3test boots a disposable MinIO for integration tests, so the
// S3 adapter and glossa-edge are tested against a real S3 API.
package s3test

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store"
)

const (
	// MinIO withdrew its OSS distribution in September 2026: Docker Hub
	// stopped serving minio/minio and quay.io/minio/* refuses anonymous
	// pulls, so the release tags this harness used are gone. Chainguard's
	// drop-in build takes their place (same /usr/bin/minio entrypoint, same
	// MINIO_ROOT_USER/MINIO_ROOT_PASSWORD, same /minio/health/live the
	// testcontainers module waits on).
	//
	// Deliberately a floating tag, unlike the chart, which pins the digest:
	// Chainguard's free tier publishes :latest only and garbage-collects the
	// digests behind it, so a pin here would rot into exactly the unpullable
	// image this replaces. A test harness wants an image that exists; a
	// deployment wants one that cannot change underneath it.
	image    = "cgr.dev/chainguard/minio:latest"
	user     = "glossa-test"
	password = "glossa-test-secret"
)

// Env is a running MinIO.
type Env struct {
	// Endpoint is host:port (plain HTTP).
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string

	container *tcminio.MinioContainer
}

// Start boots MinIO.
func Start(ctx context.Context) (*Env, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ctr, err := tcminio.Run(ctx, image, tcminio.WithUsername(user), tcminio.WithPassword(password))
	if err != nil {
		return nil, fmt.Errorf("s3test: start minio: %w", err)
	}
	endpoint, err := ctr.ConnectionString(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)
		return nil, fmt.Errorf("s3test: endpoint: %w", err)
	}
	return &Env{Endpoint: endpoint, AccessKeyID: user, SecretAccessKey: password, container: ctr}, nil
}

// Config returns the adapter configuration for bucket.
func (e *Env) Config(bucket string) s3store.Config {
	return s3store.Config{
		Endpoint: e.Endpoint, Insecure: true, Region: "us-east-1", Bucket: bucket, PathStyle: true,
		AccessKeyID: e.AccessKeyID, SecretAccessKey: e.SecretAccessKey, Timeout: 10 * time.Second,
	}
}

// Store returns an adapter on bucket, creating the bucket.
//
// MinIO answers its port before it finishes starting, and the container
// module's wait strategy returns then — the first request can still come back
// "Server not initialized yet". So the bucket is created with a short retry,
// which a genuinely broken MinIO still fails.
func (e *Env) Store(ctx context.Context, bucket string) (*s3store.Store, error) {
	s, err := s3store.New(e.Config(bucket))
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = s.EnsureBucket(ctx)
		if err == nil || time.Now().After(deadline) || ctx.Err() != nil {
			return s, err
		}
		select {
		case <-ctx.Done():
			return s, err
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Close stops MinIO.
func (e *Env) Close() {
	if e.container != nil {
		_ = testcontainers.TerminateContainer(e.container)
	}
}
