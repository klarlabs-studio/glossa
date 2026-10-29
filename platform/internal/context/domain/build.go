// Package domain is the Context context's model (RFC 0004 §2–§3): where
// every message appears. A Build is one upload of usages for one
// application at one commit; a Usage places a message (by key, resolved
// to its ID at ingest) at a file, line, component and route; a Capture
// is one screenshot of a route taken during a build, and its Regions
// are the boxes rendered messages occupy on it. Retention decides which
// builds are kept. Nothing here does I/O.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Source says which collector produced a build (RFC 0004 §2.1).
type Source string

// Sources.
const (
	// SourcePlugin is @klarlabs-studio/glossa/unplugin's .glossa/usages.json.
	SourcePlugin Source = "plugin"
	// SourceExtract is `glossa extract --upload`.
	SourceExtract Source = "extract"
	// SourceRuntime is usages the runtime reported in a capture or
	// editor session.
	SourceRuntime Source = "runtime"
	// SourceCapture is `glossa capture --upload`: captures and their
	// regions, usually without usages.
	SourceCapture Source = "capture"
)

// ParseSource validates s.
func ParseSource(s string) (Source, error) {
	switch Source(s) {
	case SourcePlugin, SourceExtract, SourceRuntime, SourceCapture:
		return Source(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidSource, s)
}

// Commit is a Git commit ID in lower case: an abbreviated or full SHA-1
// or SHA-256 object name.
type Commit string

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// ParseCommit validates and lower-cases s.
func ParseCommit(s string) (Commit, error) {
	c := strings.ToLower(s)
	if !commitPattern.MatchString(c) {
		return "", fmt.Errorf("%w: %q", ErrInvalidCommit, s)
	}
	return Commit(c), nil
}

func (c Commit) String() string { return string(c) }

// Branch is a Git branch name: the glossa.usages/v1 schema's short
// branch name (no '..', no component starting or ending with '.', none
// of ~^:?*[\{, spaces or control characters), which git-check-ref-format
// narrows further — a component ending in .lock, a leading '-' and the
// name '@' are refused too.
type Branch string

// MaxBranchLen bounds a branch name in bytes.
const MaxBranchLen = 255

// branchPattern is the schema's (usages.v1.schema.json), verbatim.
var branchPattern = regexp.MustCompile(`^[^/.\x00-\x20\x7f~^:?*\[\\{](?:[^/.\x00-\x20\x7f~^:?*\[\\{]|\.[^/.\x00-\x20\x7f~^:?*\[\\{])*` +
	`(?:/[^/.\x00-\x20\x7f~^:?*\[\\{](?:[^/.\x00-\x20\x7f~^:?*\[\\{]|\.[^/.\x00-\x20\x7f~^:?*\[\\{])*)*$`)

// ParseBranch validates s.
func ParseBranch(s string) (Branch, error) {
	if !validBranch(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidBranch, s)
	}
	return Branch(s), nil
}

func validBranch(s string) bool {
	if len(s) > MaxBranchLen || !branchPattern.MatchString(s) || s == "@" || strings.HasPrefix(s, "-") {
		return false
	}
	for component := range strings.SplitSeq(s, "/") {
		if strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}

func (b Branch) String() string { return string(b) }

// Digest is a SHA-256 digest in lower-case hex: of an uploaded usages
// document, or of a capture's image.
type Digest string

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseDigest validates s.
func ParseDigest(s string) (Digest, error) {
	if !digestPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidDigest, s)
	}
	return Digest(s), nil
}

// DigestOf returns the SHA-256 digest of b.
func DigestOf(b []byte) Digest {
	sum := sha256.Sum256(b)
	return Digest(hex.EncodeToString(sum[:]))
}

func (d Digest) String() string { return string(d) }

// Tool names the collector that wrote a document.
type Tool struct {
	Name    string
	Version string
}

// Tool limits, in characters: the schema's for the name (an npm package
// name); the schema doesn't bound the version, the server does.
const (
	MaxToolNameLen    = 214
	MaxToolVersionLen = 256
)

func (t Tool) validate() error {
	if !textWithin(t.Name, 1, MaxToolNameLen) || !toolNamePattern.MatchString(t.Name) {
		return fmt.Errorf("tool name must be a package name of at most %d characters", MaxToolNameLen)
	}
	if !textWithin(t.Version, 1, MaxToolVersionLen) || !semverPattern.MatchString(t.Version) {
		return fmt.Errorf("tool version must be a semantic version of at most %d characters", MaxToolVersionLen)
	}
	return nil
}

// Build is one upload for one application at one commit (RFC 0004
// §2.2). It is immutable: usages and captures are added with it or to
// it, never changed. Uploading the same document again for the same
// application, commit and source is a no-op: (ApplicationID, Commit,
// Source, Digest) identifies a build's upload.
type Build struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	ApplicationID uuid.UUID
	Commit        Commit
	Branch        Branch
	// OnDefaultBranch says the build is of the repository's default
	// branch: what the current usages of every view fall back to.
	OnDefaultBranch bool
	Source          Source
	Tool            Tool
	Digest          Digest
	UsageCount      int
	CreatedBy       string
	CreatedAt       time.Time
}

// NewBuild creates the build of an upload for an application of
// project.
func NewBuild(project, application uuid.UUID, up Upload, source Source, onDefault bool, by string, now time.Time) (Build, error) {
	if _, err := ParseSource(string(source)); err != nil {
		return Build{}, err
	}
	if len(up.Usages) > MaxUsagesPerBuild {
		return Build{}, ErrTooManyUsages
	}
	return Build{
		ID: uuid.Must(uuid.NewV7()), ProjectID: project, ApplicationID: application, Commit: up.Commit,
		Branch: up.Branch, OnDefaultBranch: onDefault, Source: source, Tool: up.Tool, Digest: up.Digest,
		UsageCount: len(up.Usages), CreatedBy: by, CreatedAt: now,
	}, nil
}

// textWithin reports whether s has min to max characters and no control
// characters.
func textWithin(s string, minLen, maxLen int) bool {
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return false
		}
		n++
	}
	return n >= minLen && n <= maxLen
}
