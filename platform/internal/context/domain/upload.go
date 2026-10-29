package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// UsagesSchema is the document type every collector writes.
const UsagesSchema = "glossa.usages/v1"

// Limits of a build (RFC 0004 §10).
const (
	// MaxUploadBytes bounds a usages document.
	MaxUploadBytes = 20 << 20
	// MaxUsagesPerBuild bounds the usages of one build.
	MaxUsagesPerBuild = 100_000
)

// Usage limits, in characters (the glossa.usages/v1 schema's).
const (
	MaxKeyLen       = 200
	MaxFileLen      = 1024
	MaxComponentLen = 512
	MaxRouteLen     = 1024
)

// Kinds of usage: the call shapes collectors recognize
// (runtimes/testdata/usages/README.md, "What is a usage").
const (
	// KindT is t()/$t() and Go's T calls.
	KindT = "t"
	// KindComponent is <GlossaText id> (Vue) and <T id> (React).
	KindComponent = "component"
	// KindElement is <glossa-text|rich|plural|select key|message>.
	KindElement = "element"
	// KindAccessor is a typed accessor from `glossa generate`.
	KindAccessor = "accessor"
	// KindTemplate is {{t}}, {{td}} and {{th}} in Go templates.
	KindTemplate = "template"
)

// UsagesDocument is a glossa.usages/v1 document as collectors write it
// (RFC 0004 §2.2; the JSON Schema is
// runtimes/testdata/schemas/usages.v1.schema.json). Field names are the
// published contract. Pointers tell an absent member from an empty one.
type UsagesDocument struct {
	Schema      string          `json:"schema"`
	Application string          `json:"application"`
	Commit      string          `json:"commit"`
	Branch      string          `json:"branch"`
	Tool        DocumentTool    `json:"tool"`
	Usages      []DocumentUsage `json:"usages"`
}

// DocumentTool is the document's tool.
type DocumentTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// DocumentUsage is one entry of the document's usages. Component and
// route are optional.
type DocumentUsage struct {
	Key       string  `json:"key"`
	File      string  `json:"file"`
	Line      int     `json:"line"`
	Column    int     `json:"column"`
	Component *string `json:"component,omitempty"`
	Route     *string `json:"route,omitempty"`
	Kind      string  `json:"kind"`
}

// Usage is a message's key at a location in a build (RFC 0004 §2.2).
// The key is resolved to the message's ID at ingest, so a later rename
// doesn't touch the usage.
type Usage struct {
	Key  string
	File string
	// Line and Column are 1-based; Column counts Unicode code points.
	Line   int
	Column int
	// Component is the SFC, .astro file or enclosing component or
	// function that makes the call; Route the route pattern where the
	// collector knows it. Either may be empty.
	Component string
	Route     string
	// Kind is the call shape the collector recognized: t, component,
	// element, accessor or template.
	Kind string
	// MessageID is the message the key named at ingest; nil for a key
	// the catalog didn't know (an unknown key).
	MessageID *uuid.UUID
}

// The schema's patterns (usages.v1.schema.json), verbatim.
var (
	slugPattern       = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	fullCommitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	toolNamePattern   = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
	semverPattern     = regexp.MustCompile(`^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	keyPattern        = regexp.MustCompile(`^[a-z0-9_-]+(?:\.[a-z0-9_-]+)*$`)
	filePattern       = regexp.MustCompile(`^(?:[^/.:\x00-\x1f\x7f\\][^/:\x00-\x1f\x7f\\]*|\.[^/.:\x00-\x1f\x7f\\][^/:\x00-\x1f\x7f\\]*|\.\.[^/:\x00-\x1f\x7f\\]+)` +
		`(?:/(?:[^/.:\x00-\x1f\x7f\\][^/:\x00-\x1f\x7f\\]*|\.[^/.:\x00-\x1f\x7f\\][^/:\x00-\x1f\x7f\\]*|\.\.[^/:\x00-\x1f\x7f\\]+))*$`)
	componentPattern = regexp.MustCompile(`^\S+$`)
	routePattern     = regexp.MustCompile(`^/[^\s?#]*$`)
)

func validKind(k string) bool {
	switch k {
	case KindT, KindComponent, KindElement, KindAccessor, KindTemplate:
		return true
	}
	return false
}

// noSpace reports whether s has no Unicode white space: the schema's \S
// in ECMA-262 (and Python) terms, where Go's is ASCII only.
func noSpace(s string) bool { return !strings.ContainsFunc(s, unicode.IsSpace) }

func (u Usage) validate(hasComponent, hasRoute bool) error {
	switch {
	case !textWithin(u.Key, 1, MaxKeyLen) || !keyPattern.MatchString(u.Key):
		return fmt.Errorf("key must be a message key of at most %d characters", MaxKeyLen)
	case !textWithin(u.File, 1, MaxFileLen) || !filePattern.MatchString(u.File):
		return fmt.Errorf("file must be a relative POSIX path of at most %d characters, without '.' or '..' segments", MaxFileLen)
	case u.Line < 1:
		return errors.New("line must be at least 1")
	case u.Column < 1:
		return errors.New("column must be at least 1")
	case hasComponent && (!textWithin(u.Component, 1, MaxComponentLen) || !componentPattern.MatchString(u.Component) || !noSpace(u.Component)):
		return fmt.Errorf("component must be 1–%d characters without spaces", MaxComponentLen)
	case hasRoute && (!textWithin(u.Route, 1, MaxRouteLen) || !routePattern.MatchString(u.Route) || !noSpace(u.Route)):
		return fmt.Errorf("route must be a route pattern (/checkout/[step]) of at most %d characters", MaxRouteLen)
	case !validKind(u.Kind):
		return errors.New("kind must be t, component, element, accessor or template")
	}
	return nil
}

// Upload is a validated usages document.
type Upload struct {
	// Application is the application's slug in the project.
	Application string
	Commit      Commit
	Branch      Branch
	Tool        Tool
	Usages      []Usage
	// Digest is the SHA-256 of the document's bytes.
	Digest Digest
}

// ParseUpload reads and validates a glossa.usages/v1 document by its
// schema's rules. Unknown members are ignored, so a collector can add an
// optional one within v1 without breaking older servers; everything
// else the schema refuses is ErrInvalidUpload.
func ParseUpload(raw []byte) (Upload, error) {
	if len(raw) > MaxUploadBytes {
		return Upload{}, ErrUploadTooLarge
	}
	var doc UsagesDocument
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return Upload{}, fmt.Errorf("%w: %v", ErrInvalidUpload, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Upload{}, fmt.Errorf("%w: data after the document", ErrInvalidUpload)
	}
	up, err := doc.validate()
	if err != nil {
		return Upload{}, err
	}
	up.Digest = DigestOf(raw)
	return up, nil
}

// parseHeader validates what every collector's document starts with:
// its schema, application, commit, branch and tool.
func parseHeader(schema, want, application, commit, branch string, tool DocumentTool) (Upload, error) {
	if schema != want {
		return Upload{}, fmt.Errorf("schema must be %q", want)
	}
	if !slugPattern.MatchString(application) {
		return Upload{}, errors.New("application must be an application's slug")
	}
	if !fullCommitPattern.MatchString(commit) {
		return Upload{}, errors.New("commit must be a full commit ID in lowercase hex (40 or 64 digits)")
	}
	b, err := ParseBranch(branch)
	if err != nil {
		return Upload{}, err
	}
	t := Tool{Name: tool.Name, Version: tool.Version}
	if err := t.validate(); err != nil {
		return Upload{}, err
	}
	return Upload{Application: application, Commit: Commit(commit), Branch: b, Tool: t}, nil
}

func (doc UsagesDocument) validate() (Upload, error) {
	up, err := parseHeader(doc.Schema, UsagesSchema, doc.Application, doc.Commit, doc.Branch, doc.Tool)
	if err != nil {
		return Upload{}, fmt.Errorf("%w: %w", ErrInvalidUpload, err)
	}
	if doc.Usages == nil {
		return Upload{}, fmt.Errorf("%w: usages must be a list", ErrInvalidUpload)
	}
	if len(doc.Usages) > MaxUsagesPerBuild {
		return Upload{}, ErrTooManyUsages
	}
	usages := make([]Usage, len(doc.Usages))
	for i, d := range doc.Usages {
		u := Usage{Key: d.Key, File: d.File, Line: d.Line, Column: d.Column, Kind: d.Kind}
		if d.Component != nil {
			u.Component = *d.Component
		}
		if d.Route != nil {
			u.Route = *d.Route
		}
		if err := u.validate(d.Component != nil, d.Route != nil); err != nil {
			return Upload{}, fmt.Errorf("%w: usages[%d]: %v", ErrInvalidUpload, i, err)
		}
		usages[i] = u
	}
	up.Usages = usages
	return up, nil
}

// UsageKeys returns the distinct keys of usages.
func UsageKeys(usages []Usage) []string {
	return distinct(len(usages), func(i int) string { return usages[i].Key })
}

// RegionKeys returns the distinct keys of regions.
func RegionKeys(regions []Region) []string {
	return distinct(len(regions), func(i int) string { return regions[i].Key })
}

func distinct(n int, key func(int) string) []string {
	seen := make(map[string]bool, n)
	var out []string
	for i := range n {
		if k := key(i); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// ResolveUsages sets each usage's message ID from ids (key → ID) and
// returns how many usages name a key ids lacks.
func ResolveUsages(usages []Usage, ids map[string]uuid.UUID) (unknown int) {
	for i := range usages {
		usages[i].MessageID = lookup(ids, usages[i].Key)
		if usages[i].MessageID == nil {
			unknown++
		}
	}
	return unknown
}

// ResolveRegions sets each region's message ID from ids and returns how
// many regions name a key ids lacks.
func ResolveRegions(regions []Region, ids map[string]uuid.UUID) (unknown int) {
	for i := range regions {
		regions[i].MessageID = lookup(ids, regions[i].Key)
		if regions[i].MessageID == nil {
			unknown++
		}
	}
	return unknown
}

func lookup(ids map[string]uuid.UUID, key string) *uuid.UUID {
	id, ok := ids[key]
	if !ok {
		return nil
	}
	return &id
}
