package glossa

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Release manifest v1 (runtimes/SPEC.md §1.1).

const (
	manifestSchemaPrefix = "glossa.manifest/v"
	artifactSchemaPrefix = "glossa.artifact/v"
	supportedMajor       = 1
	defaultNamespace     = "default"
	fallbackDefaultKey   = "*"
)

var (
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
)

type manifest struct {
	Schema       string                            `json:"schema"`
	Project      string                            `json:"project"`
	Environment  string                            `json:"environment"`
	Release      ReleaseRef                        `json:"release"`
	SourceLocale string                            `json:"sourceLocale"`
	Locales      []localeEntry                     `json:"locales"`
	Fallback     map[string][]string               `json:"fallback"`
	Artifacts    map[string]map[string]artifactRef `json:"artifacts"`
	Signatures   []signature                       `json:"signatures"`
	// Rollout is the optional staged rollout (SPEC §1.4), parsed only when
	// rollout support is on.
	Rollout json.RawMessage `json:"rollout,omitempty"`
}

// ReleaseRef identifies a release.
type ReleaseRef struct {
	ID        string `json:"id"`
	Version   int64  `json:"version"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type localeEntry struct {
	Code      string    `json:"code"`
	Direction Direction `json:"direction"`
}

type artifactRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type signature struct {
	KeyID string `json:"keyId"`
	Alg   string `json:"alg"`
	Sig   string `json:"sig"`
}

// parseManifest decodes and validates a manifest. Unknown fields are
// ignored; a different schema major version or a malformed manifest is an
// errSchema.
func parseManifest(raw []byte) (*manifest, error) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", errSchema, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", errSchema, err)
	}
	return &m, nil
}

// schemaMajor returns the major version of a versioned schema name such as
// "glossa.manifest/v1.2", or -1 when name doesn't have the prefix.
func schemaMajor(name, prefix string) int {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return -1
	}
	major, _, _ := strings.Cut(rest, ".")
	n, err := strconv.Atoi(major)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

func (m *manifest) validate() error {
	if major := schemaMajor(m.Schema, manifestSchemaPrefix); major != supportedMajor {
		return fmt.Errorf("unsupported schema %q (this runtime reads %s%d)", m.Schema, manifestSchemaPrefix, supportedMajor)
	}
	if m.Release.ID == "" {
		return fmt.Errorf("release.id is required")
	}
	if err := m.validateLocales(); err != nil {
		return err
	}
	return m.validateArtifacts()
}

func (m *manifest) validateLocales() error {
	if len(m.Locales) == 0 {
		return fmt.Errorf("locales is empty")
	}
	seen := map[string]bool{}
	for _, l := range m.Locales {
		if c, ok := canonicalize(l.Code); !ok || c != l.Code {
			return fmt.Errorf("locale %q is not a canonical BCP 47 tag", l.Code)
		}
		if l.Direction != LTR && l.Direction != RTL {
			return fmt.Errorf("locale %q: direction %q", l.Code, l.Direction)
		}
		if seen[l.Code] {
			return fmt.Errorf("locale %q is listed twice", l.Code)
		}
		seen[l.Code] = true
	}
	if !seen[m.SourceLocale] {
		return fmt.Errorf("sourceLocale %q is not a listed locale", m.SourceLocale)
	}
	return nil
}

func (m *manifest) validateArtifacts() error {
	for _, l := range m.Locales {
		namespaces := m.Artifacts[l.Code]
		if _, ok := namespaces[defaultNamespace]; !ok {
			return fmt.Errorf("locale %q has no %q artifact", l.Code, defaultNamespace)
		}
		for ns, ref := range namespaces {
			if !namespacePattern.MatchString(ns) {
				return fmt.Errorf("locale %q: invalid namespace %q", l.Code, ns)
			}
			if !sha256Pattern.MatchString(ref.SHA256) || ref.Size < 0 {
				return fmt.Errorf("locale %q namespace %q: invalid artifact reference", l.Code, ns)
			}
		}
	}
	return nil
}

func (m *manifest) localeCodes() []string {
	out := make([]string, len(m.Locales))
	for i, l := range m.Locales {
		out[i] = l.Code
	}
	return out
}

func (m *manifest) hasLocale(code string) bool {
	for _, l := range m.Locales {
		if l.Code == code {
			return true
		}
	}
	return false
}

// direction returns the manifest's direction for a listed locale, and the
// script-derived direction otherwise.
func (m *manifest) direction(code string) Direction {
	for _, l := range m.Locales {
		if l.Code == code {
			return l.Direction
		}
	}
	return scriptDirection(code)
}

// negotiate picks the active locale for canonical requested tags (RFC 4647
// Lookup over the manifest's locales, else the source locale; SPEC §4.1).
func (m *manifest) negotiate(requested []string) string {
	if active, ok := lookup(requested, m.localeCodes()); ok {
		return active
	}
	return m.SourceLocale
}

// chain builds the fallback chain of an active locale (SPEC §4.2), without
// duplicates, stopping on cycles. Explicit fallback targets the manifest
// doesn't list stay in the chain; they resolve as missing.
func (m *manifest) chain(active string) []string {
	b := chainBuilder{m: m, seen: map[string]bool{}}
	b.add(active)
	if _, explicit := m.Fallback[active]; explicit {
		b.expand(active)
	} else {
		for _, t := range truncations(active) {
			if m.hasLocale(t) {
				b.add(t)
			}
		}
	}
	for _, l := range m.Fallback[fallbackDefaultKey] {
		b.visit(l)
	}
	b.add(m.SourceLocale)
	return b.out
}

type chainBuilder struct {
	m    *manifest
	seen map[string]bool
	out  []string
}

// add appends locale unless it was seen, and reports whether it was new.
func (b *chainBuilder) add(locale string) bool {
	if b.seen[locale] {
		return false
	}
	b.seen[locale] = true
	b.out = append(b.out, locale)
	return true
}

// visit adds locale and then, depth-first, its own explicit fallbacks.
func (b *chainBuilder) visit(locale string) {
	if b.add(locale) {
		b.expand(locale)
	}
}

func (b *chainBuilder) expand(locale string) {
	for _, next := range b.m.Fallback[locale] {
		b.visit(next)
	}
}
