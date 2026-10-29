package config_test

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
)

func base(extra map[string]string) map[string]string {
	m := map[string]string{"DATABASE_URL": "postgres://app@db/glossa", "GLOSSA_AUTH_SECRET": testSecret}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestStorageAndReleaseDefaults(t *testing.T) {
	cfg, err := config.Load(env(base(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Driver != "dir" || cfg.Storage.Dir != "data/objects" {
		t.Errorf("storage = %+v", cfg.Storage)
	}
	if !cfg.Release.SigningKeys.IsZero() || cfg.Release.RetiredKeys != "" || cfg.Release.EdgePublicURL != "" {
		t.Errorf("release = %+v", cfg.Release)
	}
}

func TestEdgePublicURL(t *testing.T) {
	cfg, err := config.Load(env(base(map[string]string{"GLOSSA_EDGE_PUBLIC_URL": "https://edge.glossa.test/"})))
	if err != nil || cfg.Release.EdgePublicURL != "https://edge.glossa.test" {
		t.Errorf("edge URL = %q, %v", cfg.Release.EdgePublicURL, err)
	}
	_, err = config.Load(env(base(map[string]string{"GLOSSA_EDGE_PUBLIC_URL": "edge.glossa.test"})))
	if err == nil || !strings.Contains(err.Error(), "GLOSSA_EDGE_PUBLIC_URL: must be an absolute http(s) URL") {
		t.Errorf("relative edge URL: %v", err)
	}
}

func TestStorageS3(t *testing.T) {
	cfg, err := config.Load(env(base(map[string]string{
		"GLOSSA_STORAGE_DRIVER":       "s3",
		"GLOSSA_S3_ENDPOINT":          "fsn1.your-objectstorage.com",
		"GLOSSA_S3_BUCKET":            "glossa-releases",
		"GLOSSA_S3_PREFIX":            "prod/",
		"GLOSSA_S3_ACCESS_KEY_ID":     "AKIA",
		"GLOSSA_S3_SECRET_ACCESS_KEY": "very-secret",
		"GLOSSA_S3_PATH_STYLE":        "true",
		"GLOSSA_RELEASE_SIGNING_KEYS": "k_2026a=c2VjcmV0",
	})))
	if err != nil {
		t.Fatal(err)
	}
	s3 := cfg.Storage.S3
	if s3.Endpoint != "fsn1.your-objectstorage.com" || s3.Bucket != "glossa-releases" || s3.Region != "us-east-1" ||
		s3.Prefix != "prod/" || !s3.PathStyle || s3.Insecure || s3.Timeout != 10*time.Second || s3.SecretAccessKey.Reveal() != "very-secret" {
		t.Errorf("s3 = %+v", s3)
	}
	for _, s := range []string{cfg.String(), s3.SecretAccessKey.String(), cfg.Release.SigningKeys.String()} {
		if strings.Contains(s, "very-secret") || strings.Contains(s, "c2VjcmV0") {
			t.Errorf("secret leaked: %s", s)
		}
	}
}

func TestStorageValidation(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want []string
	}{
		"unknown driver": {map[string]string{"GLOSSA_STORAGE_DRIVER": "ftp"}, []string{`GLOSSA_STORAGE_DRIVER: must be dir or s3 (got "ftp")`}},
		"s3 without bucket": {
			map[string]string{"GLOSSA_STORAGE_DRIVER": "s3", "GLOSSA_S3_ENDPOINT": "https://s3.example.com"},
			[]string{"GLOSSA_S3_BUCKET: required when GLOSSA_STORAGE_DRIVER is s3", "GLOSSA_S3_ENDPOINT: must be host[:port] without a scheme"},
		},
		"malformed signing keys": {
			map[string]string{"GLOSSA_RELEASE_SIGNING_KEYS": "k1=abc,nokey", "GLOSSA_RELEASE_RETIRED_KEYS": "=x"},
			[]string{"GLOSSA_RELEASE_SIGNING_KEYS: entries are keyId=base64", "GLOSSA_RELEASE_RETIRED_KEYS: entries are keyId=base64"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := config.Load(env(base(tc.env)))
			if err == nil {
				t.Fatal("Load succeeded")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestKeyList(t *testing.T) {
	got := config.KeyList(" a=AAA , b=BBB= ")
	if len(got) != 2 || got[0] != [2]string{"a", "AAA"} || got[1] != [2]string{"b", "BBB="} {
		t.Errorf("KeyList = %v", got)
	}
	if config.KeyList("") != nil {
		t.Error("empty list")
	}
}

func TestLoadEdge(t *testing.T) {
	cfg, err := config.LoadEdge(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":8081" || cfg.OTel.ServiceName != "glossa-edge" || cfg.Storage.Driver != "dir" ||
		cfg.Cache.Bytes != 64<<20 || cfg.Cache.KeyTTL != 30*time.Second || cfg.Cache.ManifestTTL != 5*time.Second ||
		cfg.ShutdownTimeout != 25*time.Second {
		t.Errorf("edge = %+v", cfg)
	}
	if strings.Contains(cfg.String(), "DATABASE") {
		t.Error("the edge has database configuration")
	}
	_, err = config.LoadEdge(env(map[string]string{
		"GLOSSA_EDGE_CACHE_BYTES": "-1", "GLOSSA_EDGE_KEY_TTL": "never", "GLOSSA_STORAGE_DRIVER": "s3",
	}))
	for _, w := range []string{"GLOSSA_EDGE_CACHE_BYTES", "GLOSSA_EDGE_KEY_TTL", "GLOSSA_S3_ENDPOINT: required"} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("error %v lacks %s", err, w)
		}
	}
}
