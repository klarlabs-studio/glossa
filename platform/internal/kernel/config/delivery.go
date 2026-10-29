package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Storage configures object storage (release artifacts and manifests).
// glossa-server writes it; glossa-edge only reads it.
type Storage struct {
	// Driver is "dir" (a local directory, single-node development) or
	// "s3" (any S3-compatible service).
	Driver string
	Dir    string
	S3     S3
}

// S3 locates an S3-compatible bucket.
type S3 struct {
	// Endpoint is host[:port] without a scheme.
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey Secret
	// Insecure uses plain HTTP (a local MinIO only).
	Insecure  bool
	PathStyle bool
	Timeout   time.Duration
}

// Release configures publishing in glossa-server.
type Release struct {
	// SigningKeys are the active manifest signing keys,
	// "keyId=base64(ed25519 seed),…". Every manifest is signed with each.
	// Empty means one key derived from GLOSSA_AUTH_SECRET (development).
	SigningKeys Secret
	// RetiredKeys are public keys still published for verification,
	// "keyId=base64(ed25519 public key),…".
	RetiredKeys string
	// EdgePublicURL is glossa-edge's public base URL, what runtimes are
	// configured with (GET /v1/meta tells clients). Empty: not announced.
	EdgePublicURL string
}

// Cache sizes glossa-edge's in-process cache.
type Cache struct {
	// Bytes bounds the cached artifacts and manifests.
	Bytes int64
	// KeyTTL is how long a delivery key's resolution (or its absence) is
	// trusted: the longest a revoked key keeps working at this edge.
	KeyTTL time.Duration
	// ManifestTTL is how long a manifest is served before storage is
	// asked again: the longest a pointer move takes to reach this edge
	// (a CDN in front adds its max-age).
	ManifestTTL time.Duration
}

// Edge is glossa-edge's complete configuration. It has no database:
// the delivery plane reads object storage and nothing else.
type Edge struct {
	HTTP            HTTP
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	OTel            OTel
	Storage         Storage
	Cache           Cache
}

// String renders the configuration with secrets redacted.
func (e Edge) String() string {
	return fmt.Sprintf("http=%s log=%s shutdown=%s otel=%q storage=%s cache=%dB key_ttl=%s manifest_ttl=%s",
		e.HTTP.Addr, e.LogLevel, e.ShutdownTimeout, e.OTel.Endpoint, e.Storage.Driver,
		e.Cache.Bytes, e.Cache.KeyTTL, e.Cache.ManifestTTL)
}

// LoadEdge reads and validates glossa-edge's configuration.
func LoadEdge(lookup LookupFunc) (Edge, error) {
	r := reader{lookup: lookup}
	cfg := Edge{
		HTTP:            r.http(":8081"),
		LogLevel:        r.logLevel("GLOSSA_LOG_LEVEL"),
		ShutdownTimeout: r.duration("GLOSSA_SHUTDOWN_TIMEOUT", 25*time.Second),
		OTel: OTel{
			Endpoint:    r.str("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
			ServiceName: r.str("OTEL_SERVICE_NAME", "glossa-edge"),
		},
		Storage: r.storage(),
		Cache: Cache{
			Bytes:       int64(r.intRange("GLOSSA_EDGE_CACHE_BYTES", 64<<20, 0, 1<<34)),
			KeyTTL:      r.duration("GLOSSA_EDGE_KEY_TTL", 30*time.Second),
			ManifestTTL: r.duration("GLOSSA_EDGE_MANIFEST_TTL", 5*time.Second),
		},
	}
	if len(r.errs) > 0 {
		return Edge{}, fmt.Errorf("invalid configuration:\n  %w", errors.Join(r.errs...))
	}
	return cfg, nil
}

func (r *reader) http(defaultAddr string) HTTP {
	return HTTP{
		Addr:              r.str("GLOSSA_HTTP_ADDR", defaultAddr),
		ReadHeaderTimeout: r.duration("GLOSSA_HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       r.duration("GLOSSA_HTTP_READ_TIMEOUT", 30*time.Second),
		WriteTimeout:      r.duration("GLOSSA_HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       r.duration("GLOSSA_HTTP_IDLE_TIMEOUT", 120*time.Second),
		MaxBodyBytes:      int64(r.intRange("GLOSSA_HTTP_MAX_BODY_BYTES", 1<<20, 1, 1<<30)),
	}
}

func (r *reader) storage() Storage {
	s := Storage{
		Driver: r.str("GLOSSA_STORAGE_DRIVER", "dir"),
		Dir:    r.str("GLOSSA_STORAGE_DIR", "data/objects"),
		S3: S3{
			Endpoint:        r.str("GLOSSA_S3_ENDPOINT", ""),
			Region:          r.str("GLOSSA_S3_REGION", "us-east-1"),
			Bucket:          r.str("GLOSSA_S3_BUCKET", ""),
			Prefix:          r.str("GLOSSA_S3_PREFIX", ""),
			AccessKeyID:     r.str("GLOSSA_S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: Secret{r.str("GLOSSA_S3_SECRET_ACCESS_KEY", "")},
			Insecure:        r.boolean("GLOSSA_S3_INSECURE", false),
			PathStyle:       r.boolean("GLOSSA_S3_PATH_STYLE", false),
			Timeout:         r.duration("GLOSSA_S3_TIMEOUT", 10*time.Second),
		},
	}
	switch s.Driver {
	case "dir":
	case "s3":
		if s.S3.Endpoint == "" {
			r.fail("GLOSSA_S3_ENDPOINT", "required when GLOSSA_STORAGE_DRIVER is s3")
		} else if strings.Contains(s.S3.Endpoint, "://") {
			r.fail("GLOSSA_S3_ENDPOINT", "must be host[:port] without a scheme (got %q)", s.S3.Endpoint)
		}
		if s.S3.Bucket == "" {
			r.fail("GLOSSA_S3_BUCKET", "required when GLOSSA_STORAGE_DRIVER is s3")
		}
	default:
		r.fail("GLOSSA_STORAGE_DRIVER", "must be dir or s3 (got %q)", s.Driver)
	}
	return s
}

func (r *reader) release() Release {
	rel := Release{
		SigningKeys:   Secret{r.str("GLOSSA_RELEASE_SIGNING_KEYS", "")},
		RetiredKeys:   r.str("GLOSSA_RELEASE_RETIRED_KEYS", ""),
		EdgePublicURL: r.absoluteURL("GLOSSA_EDGE_PUBLIC_URL", ""),
	}
	r.keyList("GLOSSA_RELEASE_SIGNING_KEYS", rel.SigningKeys.Reveal())
	r.keyList("GLOSSA_RELEASE_RETIRED_KEYS", rel.RetiredKeys)
	return rel
}

func (r *reader) keyList(key, raw string) {
	if raw == "" {
		return
	}
	for _, entry := range strings.Split(raw, ",") {
		id, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok || id == "" || value == "" {
			r.fail(key, "entries are keyId=base64, separated by commas")
			return
		}
	}
}

// KeyList splits "id=value,id=value" into pairs. Load has validated the
// shape; the Release context validates the keys themselves.
func KeyList(raw string) [][2]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out [][2]string
	for _, entry := range strings.Split(raw, ",") {
		id, value, _ := strings.Cut(strings.TrimSpace(entry), "=")
		out = append(out, [2]string{strings.TrimSpace(id), strings.TrimSpace(value)})
	}
	return out
}
