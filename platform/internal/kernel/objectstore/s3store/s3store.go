// Package s3store is the objectstore adapter for S3-compatible object
// storage: MinIO in development and on k3s, Hetzner Object Storage in
// production (RFC 0002 §6).
//
// It uses minio-go (pinned in go.mod) rather than the AWS SDK v2: one
// module instead of a dozen, written for S3-compatible servers, and
// without the SDK's default request checksums that non-AWS providers
// reject. minio-go retries transient failures itself (MaxRetries); a
// per-operation timeout and a fortify circuit breaker around every call
// make an unavailable bucket fail fast instead of piling up requests
// (RFC 0002 §11).
package s3store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.klarlabs.de/fortify/circuitbreaker"

	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
)

// Config locates the bucket.
type Config struct {
	// Endpoint is host[:port] without a scheme ("s3.eu-central-1.amazonaws.com",
	// "fsn1.your-objectstorage.com", "minio:9000").
	Endpoint string
	// Insecure uses plain HTTP (a local MinIO only).
	Insecure bool
	Region   string
	Bucket   string
	// Prefix is prepended to every key ("glossa/"), so one bucket can
	// hold several deployments. Empty means the bucket root.
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle forces path-style requests (MinIO); otherwise the
	// endpoint decides.
	PathStyle bool
	// Timeout bounds one operation, retries included (default 10s).
	Timeout time.Duration
	// MaxRetries is minio-go's retry budget per operation (default 3).
	MaxRetries int
}

// Store implements objectstore.Store on a bucket.
type Store struct {
	client  *minio.Client
	bucket  string
	prefix  string
	timeout time.Duration
	breaker circuitbreaker.CircuitBreaker[any]
}

var _ objectstore.Store = (*Store)(nil)

// New connects lazily: no request is made until the first operation.
func New(cfg Config) (*Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("s3store: endpoint and bucket are required")
	}
	if strings.Contains(cfg.Endpoint, "://") {
		return nil, fmt.Errorf("s3store: endpoint %q must be host[:port] without a scheme", cfg.Endpoint)
	}
	if cfg.Prefix != "" {
		cfg.Prefix = strings.Trim(cfg.Prefix, "/")
		if err := objectstore.CheckKey(cfg.Prefix); err != nil {
			return nil, fmt.Errorf("s3store: prefix: %w", err)
		}
		cfg.Prefix += "/"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	lookup := minio.BucketLookupAuto
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       !cfg.Insecure,
		Region:       cfg.Region,
		BucketLookup: lookup,
		MaxRetries:   cfg.MaxRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("s3store: %w", err)
	}
	return &Store{
		client: client, bucket: cfg.Bucket, prefix: cfg.Prefix, timeout: cfg.Timeout,
		breaker: circuitbreaker.New[any](circuitbreaker.Config{
			ReadyToTrip:  circuitbreaker.TripOnConsecutiveFailures(5),
			Timeout:      15 * time.Second,
			IsSuccessful: func(err error) bool { return err == nil || !unavailable(err) },
		}),
	}, nil
}

// unavailable reports whether err says the storage is failing, as
// opposed to a definite answer (missing object, too large, bad key).
func unavailable(err error) bool {
	return !errors.Is(err, objectstore.ErrNotFound) && !errors.Is(err, objectstore.ErrTooLarge) &&
		!errors.Is(err, objectstore.ErrInvalidKey) && !errors.Is(err, context.Canceled)
}

func (s *Store) object(key string) (string, error) {
	if err := objectstore.CheckKey(key); err != nil {
		return "", err
	}
	return s.prefix + key, nil
}

func (s *Store) do(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	return s.breaker.Execute(ctx, func(ctx context.Context) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()
		return fn(ctx)
	})
}

func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.Code == "NotFound" || resp.StatusCode == http.StatusNotFound
}

// Get implements objectstore.Reader.
func (s *Store) Get(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	name, err := s.object(key)
	if err != nil {
		return nil, err
	}
	v, err := s.do(ctx, func(ctx context.Context) (any, error) {
		obj, err := s.client.GetObject(ctx, s.bucket, name, minio.GetObjectOptions{})
		if err != nil {
			return nil, s.fail("get", key, err)
		}
		defer func() { _ = obj.Close() }()
		b, err := io.ReadAll(io.LimitReader(obj, maxBytes+1))
		if err != nil {
			return nil, s.fail("get", key, err)
		}
		if int64(len(b)) > maxBytes {
			return nil, objectstore.ErrTooLarge
		}
		return b, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// Exists implements objectstore.Reader.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	name, err := s.object(key)
	if err != nil {
		return false, err
	}
	v, err := s.do(ctx, func(ctx context.Context) (any, error) {
		_, err := s.client.StatObject(ctx, s.bucket, name, minio.StatObjectOptions{})
		if err != nil && isNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, s.fail("stat", key, err)
		}
		return true, nil
	})
	if err != nil {
		return false, err
	}
	return v.(bool), nil
}

// Put implements objectstore.Writer. S3 replaces objects atomically.
func (s *Store) Put(ctx context.Context, key string, body []byte, contentType string) error {
	name, err := s.object(key)
	if err != nil {
		return err
	}
	_, err = s.do(ctx, func(ctx context.Context) (any, error) {
		_, err := s.client.PutObject(ctx, s.bucket, name, bytes.NewReader(body), int64(len(body)),
			minio.PutObjectOptions{ContentType: contentType, DisableMultipart: true, SendContentMd5: true})
		if err != nil {
			return nil, s.fail("put", key, err)
		}
		return nil, nil
	})
	return err
}

var _ objectstore.StreamStore = (*Store)(nil)

// streamPartSize is the multipart part (and buffer) size of a stream of
// unknown length: 10 000 parts reach 160 GiB.
const streamPartSize = 16 << 20

// PutStream implements objectstore.Streamer: a multipart upload of
// unknown length, aborted (nothing stored) when r fails. It is bounded
// by ctx rather than the per-operation timeout, which is sized for
// small objects.
func (s *Store) PutStream(ctx context.Context, key string, r io.Reader, contentType string) (int64, error) {
	name, err := s.object(key)
	if err != nil {
		return 0, err
	}
	var (
		n       int64
		readErr error
	)
	_, err = s.breaker.Execute(ctx, func(ctx context.Context) (any, error) {
		tr := &trackingReader{r: r}
		info, err := s.client.PutObject(ctx, s.bucket, name, tr, -1,
			minio.PutObjectOptions{ContentType: contentType, PartSize: streamPartSize})
		if tr.err != nil {
			readErr = tr.err // the caller's reader, not the storage, failed
			return nil, nil
		}
		if err != nil {
			return nil, s.fail("put", key, err)
		}
		n = info.Size
		return nil, nil
	})
	if readErr != nil {
		return 0, readErr
	}
	return n, err
}

// trackingReader remembers the first error of r other than io.EOF.
type trackingReader struct {
	r   io.Reader
	err error
}

func (t *trackingReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && t.err == nil {
		t.err = err
	}
	return n, err
}

// Open implements objectstore.Streamer. The object is read lazily under
// ctx; a missing one is reported up front.
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	name, err := s.object(key)
	if err != nil {
		return nil, err
	}
	_, err = s.do(ctx, func(ctx context.Context) (any, error) {
		if _, err := s.client.StatObject(ctx, s.bucket, name, minio.StatObjectOptions{}); err != nil {
			return nil, s.fail("stat", key, err)
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, s.fail("get", key, err)
	}
	return obj, nil
}

// Delete implements objectstore.Writer.
func (s *Store) Delete(ctx context.Context, key string) error {
	name, err := s.object(key)
	if err != nil {
		return err
	}
	_, err = s.do(ctx, func(ctx context.Context) (any, error) {
		if err := s.client.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
			return nil, s.fail("delete", key, err)
		}
		return nil, nil
	})
	return err
}

// Ping checks that the bucket is reachable and exists.
func (s *Store) Ping(ctx context.Context) error {
	_, err := s.do(ctx, func(ctx context.Context) (any, error) {
		ok, err := s.client.BucketExists(ctx, s.bucket)
		if err != nil {
			return nil, fmt.Errorf("s3store: bucket %s: %w", s.bucket, err)
		}
		if !ok {
			return nil, fmt.Errorf("s3store: bucket %s does not exist", s.bucket)
		}
		return nil, nil
	})
	return err
}

// EnsureBucket creates the bucket if it is missing (development and
// tests; production buckets are provisioned with their policies).
func (s *Store) EnsureBucket(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("s3store: bucket %s: %w", s.bucket, err)
	}
	if ok {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("s3store: create bucket %s: %w", s.bucket, err)
	}
	return nil
}

func (s *Store) fail(op, key string, err error) error {
	if isNotFound(err) {
		return objectstore.ErrNotFound
	}
	return fmt.Errorf("s3store: %s %s: %w", op, key, err)
}
