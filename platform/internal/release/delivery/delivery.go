// Package delivery is the contract between the control plane (the
// Release context, which writes object storage) and the delivery plane
// (glossa-edge, which only reads it): the publishable delivery key
// format, the bucket layout, and the key index objects through which the
// edge resolves a key to its project without a database (RFC 0002 §3,
// runtimes/SPEC.md §2).
//
// Bucket layout (all under the configured prefix):
//
//	v1/keys/<sha256(key)>.json                              key index, present while the key is active
//	v1/projects/<project>/environments/<env>/manifest.json the manifest the environment serves
//	v1/projects/<project>/a/<sha256>.json                  artifact bytes, content-addressed, immutable
//
// Artifacts are addressed per project, not globally: a delivery key only
// ever reaches its own project's objects, and no one can probe whether
// another project published some text by guessing its hash.
package delivery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// KeyPrefix starts every publishable delivery key, so secret scanners
// and people can tell it from an API token (`glossa_api_`). It is public
// by design, but the prefix still says what leaked where.
const KeyPrefix = "glossa_pk_"

const keyRandomBytes = 24 // 192 bits, 32 base64url characters

var keyPattern = regexp.MustCompile(`^glossa_pk_[A-Za-z0-9_-]{32}$`)

// NewKey returns a fresh random delivery key.
func NewKey() (string, error) {
	b := make([]byte, keyRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("delivery: random key: %w", err)
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidKey reports whether s has the delivery key format. The edge
// checks it before touching storage.
func ValidKey(s string) bool { return keyPattern.MatchString(s) }

// Environment names follow the manifest schema's pattern. "a" is
// reserved: the edge's artifact path is /v1/{key}/a/{sha256}.json.
var environmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ArtifactSegment is the path segment of artifact URLs, never an
// environment name.
const ArtifactSegment = "a"

// ValidEnvironment reports whether name can be an environment.
func ValidEnvironment(name string) bool {
	return name != ArtifactSegment && environmentPattern.MatchString(name)
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidDigest reports whether s is a lowercase hex SHA-256.
func ValidDigest(s string) bool { return digestPattern.MatchString(s) }

// Digest returns the lowercase hex SHA-256 of b.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Size limits the edge enforces when reading storage.
const (
	MaxManifestBytes = 16 << 20
	MaxArtifactBytes = 64 << 20
	MaxKeyIndexBytes = 4 << 10
)

const root = "v1/"

// KeyIndexPath is where the index object of key lives. The name is the
// key's hash, so a bucket listing reveals no keys and a key never becomes
// part of a path.
func KeyIndexPath(key string) string { return root + "keys/" + Digest([]byte(key)) + ".json" }

// ManifestPath is where an environment's served manifest lives.
func ManifestPath(project, environment string) string {
	return root + "projects/" + project + "/environments/" + environment + "/manifest.json"
}

// ArtifactPath is where an artifact's bytes live.
func ArtifactPath(project, digest string) string {
	return root + "projects/" + project + "/a/" + digest + ".json"
}

// KeyIndexSchema versions the key index object. Its scope (environments,
// branches) was added within v1 (runtimes/testdata/schemas).
const KeyIndexSchema = "glossa.delivery-key/v1"

// KeyIndex is the object that maps an active key to its project and
// says what the key may read.
type KeyIndex struct {
	Schema  string `json:"schema"`
	Project string `json:"project"`
	KeyID   string `json:"key_id"`
	Scope
}

// ErrInvalidKeyIndex means a key index object can't be used.
var ErrInvalidKeyIndex = errors.New("delivery: invalid key index")

// EncodeKeyIndex encodes the index object for a key of project.
func EncodeKeyIndex(project, keyID string, scope Scope) []byte {
	if scope.Environments == nil {
		scope.Environments = []string{}
	}
	b, _ := json.Marshal(KeyIndex{Schema: KeyIndexSchema, Project: project, KeyID: keyID, Scope: scope})
	return b
}

// DecodeKeyIndex decodes and checks an index object. One written before
// scopes existed (no "environments" member) reads as the scope those
// keys migrated to: the default environments, no branches.
func DecodeKeyIndex(b []byte) (KeyIndex, error) {
	var raw struct {
		Schema       string    `json:"schema"`
		Project      string    `json:"project"`
		KeyID        string    `json:"key_id"`
		Environments *[]string `json:"environments"`
		Branches     bool      `json:"branches"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return KeyIndex{}, fmt.Errorf("%w: %v", ErrInvalidKeyIndex, err)
	}
	if raw.Schema != KeyIndexSchema || !projectPattern.MatchString(raw.Project) {
		return KeyIndex{}, fmt.Errorf("%w: schema %q, project %q", ErrInvalidKeyIndex, raw.Schema, raw.Project)
	}
	k := KeyIndex{Schema: raw.Schema, Project: raw.Project, KeyID: raw.KeyID, Scope: Scope{Branches: raw.Branches}}
	if raw.Environments == nil {
		k.Environments = slices.Clone(DefaultEnvironments)
		return k, nil
	}
	for _, e := range *raw.Environments {
		if !ValidEnvironment(e) {
			return KeyIndex{}, fmt.Errorf("%w: environment %q", ErrInvalidKeyIndex, e)
		}
	}
	k.Environments = *raw.Environments
	return k, nil
}

// Project IDs are UUIDs in their canonical form.
var projectPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
