package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
)

// Schemas of the artifacts a release writes (runtimes/SPEC.md §1, §8).
const (
	ManifestSchema = "glossa.manifest/v1"
	ArtifactSchema = "glossa.artifact/v1"
	// DefaultNamespace exists for every listed locale, even if empty.
	DefaultNamespace = "default"
)

// The artifact schema's limits (runtimes/testdata/schemas).
var (
	namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	messageIDPattern = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)*$`)
)

// Locale is a manifest locale.
type Locale struct {
	Code      string `json:"code"`
	Direction string `json:"direction"`
}

// SourceMessage is an active message as a release takes it from the
// catalog: its identity, the key runtimes use, its namespace and the
// canonical MF2 data model of its current source.
type SourceMessage struct {
	ID        uuid.UUID
	Key       string
	Namespace string
	Model     json.RawMessage
	// Proposed marks a message that exists only on branches (RFC 0004
	// §4.1). Build leaves it out; BuildBranch ships it.
	Proposed bool
	// Proposal is a branch's proposed source for the message, if the
	// snapshot carries one. Build ships Model, never the proposal;
	// BuildBranch ships the proposal in the source locale.
	Proposal json.RawMessage
}

// Overlay is what one branch adds to the main catalog (RFC 0004 §4.1):
// the messages it proposes and its proposed source for live messages.
type Overlay struct {
	// Proposed are the branch's new keys, each with the source the
	// branch pushed.
	Proposed []SourceMessage
	// Changes maps a live message's ID to the source the branch proposes
	// for it.
	Changes map[uuid.UUID]json.RawMessage
}

// WithOverlay returns a copy of s carrying o: o's proposed messages
// (marked Proposed) and, on the live messages it changes, their
// proposals. s itself is unchanged. Only BuildBranch ships the overlay.
func (s Snapshot) WithOverlay(o Overlay) Snapshot {
	out := s
	out.Messages = make([]SourceMessage, 0, len(s.Messages)+len(o.Proposed))
	have := make(map[uuid.UUID]bool, len(s.Messages))
	for _, m := range s.Messages {
		if p, ok := o.Changes[m.ID]; ok && !m.Proposed {
			m.Proposal = p
		}
		have[m.ID] = true
		out.Messages = append(out.Messages, m)
	}
	for _, m := range o.Proposed {
		if have[m.ID] {
			continue
		}
		m.Proposed, m.Proposal = true, nil
		out.Messages = append(out.Messages, m)
	}
	return out
}

// Translation is an eligible translation as a release takes it from
// Localization.
type Translation struct {
	Model    json.RawMessage
	Outdated bool
}

// Snapshot is everything a release is built from: Catalog's releasable
// source joined with Localization's releasable translations.
type Snapshot struct {
	SourceLocale string
	// Locales are the project's locales, the source locale among them.
	Locales  []Locale
	Fallback map[string][]string
	Messages []SourceMessage
	// Translations maps locale → message ID → translation, already
	// limited to the review states of the policy.
	Translations map[string]map[uuid.UUID]Translation
}

// ArtifactRef names an artifact by the SHA-256 of its exact bytes.
type ArtifactRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Content is what every environment's manifest of a release carries:
// the manifest without schema, project, environment, release and
// signatures. It is stored with the release, so any environment's
// manifest can be written again without rebuilding anything.
type Content struct {
	SourceLocale string                            `json:"sourceLocale"`
	Locales      []Locale                          `json:"locales"`
	Fallback     map[string][]string               `json:"fallback"`
	Artifacts    map[string]map[string]ArtifactRef `json:"artifacts"`
}

// Digest is the SHA-256 of the content's canonical JSON: the release's
// manifest digest. Two releases with equal digests serve exactly the
// same text.
func (c Content) Digest() (string, error) {
	b, err := jcs.Marshal(c)
	if err != nil {
		return "", err
	}
	return delivery.Digest(b), nil
}

// Artifact is one namespace of one locale, serialized.
type Artifact struct {
	Locale    string
	Namespace string
	Body      []byte
	Ref       ArtifactRef
}

// LocaleStats counts what a release ships for one locale.
type LocaleStats struct {
	Messages int `json:"messages"`
	Outdated int `json:"outdated"`
}

// Stats summarize a release.
type Stats struct {
	// Messages is the number of source messages (the source locale's).
	Messages  int                    `json:"messages"`
	Locales   map[string]LocaleStats `json:"locales"`
	Artifacts int                    `json:"artifacts"`
	Bytes     int64                  `json:"bytes"`
	// NewArtifacts is how many artifacts the publish had to upload;
	// the rest were already in storage from earlier releases.
	NewArtifacts int `json:"new_artifacts"`
}

// Built is a release's content and artifacts, ready to store.
type Built struct {
	Content   Content
	Artifacts []Artifact
	Stats     Stats
}

// Build turns a snapshot into artifacts under policy (runtimes/SPEC.md
// §1): one artifact per locale and namespace, holding only the messages
// translated for that locale, the source locale holding every message.
// Outdated translations are dropped unless the policy includes them.
//
// The output is deterministic: equal snapshots give byte-identical
// artifacts and content, whatever order their parts came in. Artifact
// bytes are the RFC 8785 canonical JSON of
// {"schema","locale","namespace","messages"}, where each message is its
// MF2 data model (runtimes/SPEC.md §1.2).
//
// Build excludes the branch overlay — proposed messages and source
// proposals (RFC 0004 §4.2) — so no text that exists only on a branch
// reaches a release: that is what keeps release eligibility intact.
// Only a branch environment's build (BuildBranch) adds its overlay.
//
// A snapshot artifacts can't carry fails with a *NotReleasableError
// listing every problem found, not only the first.
func Build(s Snapshot, p Policy) (Built, error) { return build(s, p, false) }

// BuildBranch is a branch environment's build: Build, plus the overlay
// the snapshot carries (Snapshot.WithOverlay). Proposed messages ship
// like live ones, with their translations; a source proposal replaces
// the message's source in the source locale only, because translations
// stay against the live source (RFC 0004 §4.1).
func BuildBranch(s Snapshot, p Policy) (Built, error) { return build(s, p, true) }

func build(s Snapshot, p Policy, overlay bool) (Built, error) {
	var ps problems
	locales := orderLocales(s, &ps)
	if err := ps.err(); err != nil {
		return Built{}, err
	}
	byLocale, stats := groupMessages(s, locales, p, overlay, &ps)
	if err := ps.err(); err != nil {
		return Built{}, err
	}
	b := Built{
		Content: Content{
			SourceLocale: s.SourceLocale, Locales: locales, Fallback: cloneFallback(s.Fallback),
			Artifacts: map[string]map[string]ArtifactRef{},
		},
		Stats: stats,
	}
	for _, l := range locales {
		namespaces := byLocale[l.Code]
		b.Content.Artifacts[l.Code] = map[string]ArtifactRef{}
		for _, ns := range slices.Sorted(maps.Keys(namespaces)) {
			body, err := encodeArtifact(l.Code, ns, namespaces[ns])
			if err != nil {
				return Built{}, err
			}
			ref := ArtifactRef{SHA256: delivery.Digest(body), Size: int64(len(body))}
			b.Content.Artifacts[l.Code][ns] = ref
			b.Artifacts = append(b.Artifacts, Artifact{Locale: l.Code, Namespace: ns, Body: body, Ref: ref})
			b.Stats.Artifacts++
			b.Stats.Bytes += ref.Size
		}
	}
	return b, nil
}

// orderLocales lists the source locale first, then the rest by code.
func orderLocales(s Snapshot, ps *problems) []Locale {
	var source *Locale
	seen := map[string]bool{}
	var rest []Locale
	for _, l := range s.Locales {
		if seen[l.Code] {
			ps.add("", l.Code, "locale %s is listed twice", l.Code)
			continue
		}
		seen[l.Code] = true
		if l.Direction != "ltr" && l.Direction != "rtl" {
			ps.add("", l.Code, "locale %s has direction %q", l.Code, l.Direction)
		}
		if l.Code == s.SourceLocale {
			source = &l
			continue
		}
		rest = append(rest, l)
	}
	if source == nil {
		ps.add("", s.SourceLocale, "the source locale %q is not among the locales", s.SourceLocale)
		return nil
	}
	slices.SortFunc(rest, func(a, b Locale) int { return compareStrings(a.Code, b.Code) })
	return append([]Locale{*source}, rest...)
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// groupMessages sorts messages into locale → namespace → key → model.
// A message artifacts can't carry is a problem and left out. overlay
// ships the branch overlay the snapshot carries.
func groupMessages(s Snapshot, locales []Locale, p Policy, overlay bool, ps *problems) (map[string]map[string]map[string]json.RawMessage, Stats) {
	out := map[string]map[string]map[string]json.RawMessage{}
	stats := Stats{Locales: map[string]LocaleStats{}}
	for _, l := range locales {
		out[l.Code] = map[string]map[string]json.RawMessage{DefaultNamespace: {}}
	}
	keys := map[string]bool{}
	for _, m := range s.Messages {
		if m.Proposed && !overlay {
			continue
		}
		if !overlay {
			m.Proposal = nil
		}
		stats.Messages++
		if !checkMessage(m, keys, ps) {
			continue
		}
		for _, l := range locales {
			model, outdated, ok := pick(s, l.Code, m, p)
			if !ok {
				continue
			}
			if !isObject(model) {
				ps.add(m.Key, l.Code, "the model of %s in %s is not a JSON object", m.Key, l.Code)
				continue
			}
			ns := out[l.Code][m.Namespace]
			if ns == nil {
				ns = map[string]json.RawMessage{}
				out[l.Code][m.Namespace] = ns
			}
			ns[m.Key] = model
			ls := stats.Locales[l.Code]
			ls.Messages++
			if outdated {
				ls.Outdated++
			}
			stats.Locales[l.Code] = ls
		}
	}
	for _, l := range locales {
		if _, ok := stats.Locales[l.Code]; !ok {
			stats.Locales[l.Code] = LocaleStats{}
		}
	}
	return out, stats
}

// pick returns the model locale ships for m, if any.
func pick(s Snapshot, locale string, m SourceMessage, p Policy) (json.RawMessage, bool, bool) {
	if locale == s.SourceLocale {
		if m.Proposal != nil {
			return m.Proposal, false, true
		}
		return m.Model, false, true
	}
	t, ok := s.Translations[locale][m.ID]
	if !ok || (t.Outdated && !p.IncludeOutdated) {
		return nil, false, false
	}
	return t.Model, t.Outdated, true
}

// checkMessage reports whether m's key and namespace fit an artifact.
func checkMessage(m SourceMessage, keys map[string]bool, ps *problems) bool {
	if !messageIDPattern.MatchString(m.Key) {
		ps.add(m.Key, "", "message key %q is not a valid message ID", m.Key)
		return false
	}
	if keys[m.Key] {
		ps.add(m.Key, "", "message key %q appears twice", m.Key)
		return false
	}
	keys[m.Key] = true
	if !namespacePattern.MatchString(m.Namespace) {
		ps.add(m.Key, "", "namespace %q of %s is longer than the 63 characters artifacts allow", m.Namespace, m.Key)
		return false
	}
	return true
}

type artifactDoc struct {
	Schema    string                     `json:"schema"`
	Locale    string                     `json:"locale"`
	Namespace string                     `json:"namespace"`
	Messages  map[string]json.RawMessage `json:"messages"`
}

func encodeArtifact(locale, namespace string, messages map[string]json.RawMessage) ([]byte, error) {
	b, err := jcs.Marshal(artifactDoc{Schema: ArtifactSchema, Locale: locale, Namespace: namespace, Messages: messages})
	if err != nil {
		return nil, fmt.Errorf("%w: artifact %s/%s: %v", ErrNotReleasable, locale, namespace, err)
	}
	return b, nil
}

func isObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{' && json.Valid(t)
}

func cloneFallback(f map[string][]string) map[string][]string {
	out := make(map[string][]string, len(f))
	for k, v := range f {
		out[k] = slices.Clone(v)
	}
	return out
}

// ArtifactMessages returns an artifact's messages as canonical JSON by
// message ID (for diffs). Artifacts are canonical, so equal messages have
// equal bytes.
func ArtifactMessages(body []byte) (LocaleMessages, error) {
	var doc artifactDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("release: artifact: %w", err)
	}
	if doc.Schema != ArtifactSchema {
		return nil, fmt.Errorf("release: artifact schema %q", doc.Schema)
	}
	return LocaleMessages(doc.Messages), nil
}
