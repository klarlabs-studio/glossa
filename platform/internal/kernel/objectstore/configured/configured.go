// Package configured opens the object store the configuration names,
// for both binaries: glossa-server writes it, glossa-edge reads it.
package configured

import (
	"fmt"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store"
)

// Open returns the configured store.
func Open(cfg config.Storage) (objectstore.StreamStore, error) {
	switch cfg.Driver {
	case "dir":
		return objectstore.NewDir(cfg.Dir)
	case "s3":
		return s3store.New(s3store.Config{
			Endpoint: cfg.S3.Endpoint, Insecure: cfg.S3.Insecure, Region: cfg.S3.Region, Bucket: cfg.S3.Bucket,
			Prefix: cfg.S3.Prefix, AccessKeyID: cfg.S3.AccessKeyID, SecretAccessKey: cfg.S3.SecretAccessKey.Reveal(),
			PathStyle: cfg.S3.PathStyle, Timeout: cfg.S3.Timeout,
		})
	}
	return nil, fmt.Errorf("objectstore: unknown driver %q", cfg.Driver)
}
