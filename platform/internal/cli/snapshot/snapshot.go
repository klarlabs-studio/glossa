// Package snapshot is the CLI's read model of a project: its locales,
// active messages (canonical model and arguments) and translations, read
// either from glossa-server or from the local catalogs. check, status,
// diff, pull and generate all work on a Snapshot, so they behave the same
// online and offline.
package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/cli/catalog"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// Origins of a snapshot.
const (
	FromServerOrigin = "server"
	FromLocalOrigin  = "local"
)

// Invalid is text that doesn't parse, with the kernel's error code.
type Invalid struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Message is a source message.
type Message struct {
	// ID is the catalog message's ID, which a snapshot read from the
	// server always has. It is what a finding's identity is hashed over
	// (domain.Fingerprint), so the terminal and the server fingerprint
	// the same finding the same way. It is empty for a snapshot read
	// from the local catalogs, which know keys and not IDs.
	ID          string
	Key         string
	Namespace   string
	Description string
	// MaxLength bounds the rendered translation in characters; 0 means
	// none. The length layer computes `max-length-exceeded` from it, so
	// a check run against the server finds the finding itself rather
	// than waiting for the warning the server stored.
	MaxLength int
	Text      string
	Syntax    string
	Revision  int
	Model     *mf.Message
	Arguments []mf.Argument
	Invalid   *Invalid
	// File is the local catalog it came from ("" on the server).
	File string
}

// Translation is a message's text in one locale.
type Translation struct {
	Key            string
	Locale         string
	Text           string
	Syntax         string
	Model          *mf.Message
	State          string // review state; "" for local files
	Origin         string
	SourceRevision int
	Outdated       bool
	// Warnings are the findings the server stored with the translation.
	Warnings []mf.Finding
	Invalid  *Invalid
	File     string
}

// Locale is one of the project's locales.
type Locale struct {
	Code      string
	Direction string
	IsSource  bool
	File      string
}

// Snapshot is a project at one moment.
type Snapshot struct {
	Origin       string
	SourceLocale string
	// Locales has the source locale first, then the rest by code.
	Locales []Locale
	// Messages are the active messages by key.
	Messages []Message
	// Translations maps locale → key → translation.
	Translations map[string]map[string]Translation
	// Fallback is the server's fallback graph (nil offline).
	Fallback map[string][]string
	// Orphans are the translations of messages the catalog has
	// obsoleted, read only when Options.Orphans asks for them, and never
	// part of Translations: `glossa pull` writes Translations back into
	// the catalog files, and a translation of a message that no longer
	// exists does not belong in one. The completeness layer reports each
	// as `unknown-key`. MoreOrphans says the bounded read stopped before
	// the listing ran out (layers.MaxOrphans).
	Orphans     []Orphan
	MoreOrphans bool

	index map[string]int
}

// Orphan is a translation whose message the catalog has obsoleted: its
// identity, and nothing to grade.
type Orphan struct {
	// MessageID is the obsolete message's catalog ID, which is what the
	// finding's fingerprint is hashed over — the one the server computes.
	MessageID string
	Key       string
	Namespace string
	Locale    string
	// Revision is the translation's ID.
	Revision string
}

// Message returns the source message with key.
func (s *Snapshot) Message(key string) (*Message, bool) {
	if s.index == nil {
		s.index = make(map[string]int, len(s.Messages))
		for i, m := range s.Messages {
			s.index[m.Key] = i
		}
	}
	i, ok := s.index[key]
	if !ok {
		return nil, false
	}
	return &s.Messages[i], true
}

// TargetLocales are the locales other than the source.
func (s *Snapshot) TargetLocales() []Locale {
	var out []Locale
	for _, l := range s.Locales {
		if !l.IsSource {
			out = append(out, l)
		}
	}
	return out
}

// Parse parses text in syntax for locale with the MessageFormat kernel.
func Parse(syntax, text, locale string) (*mf.Message, []mf.Argument, *Invalid) {
	tag, err := bcp47.Parse(locale)
	if err != nil {
		return nil, nil, &Invalid{Code: string(mf.CodeInvalidLocale), Detail: err.Error()}
	}
	c, err := mfcontent.Parse(mfcontent.Syntax(syntax), text, tag)
	if err != nil {
		var inv *mfcontent.InvalidError
		if errors.As(err, &inv) {
			return nil, nil, &Invalid{Code: string(inv.Code), Detail: inv.Message}
		}
		return nil, nil, &Invalid{Code: "invalid-message", Detail: err.Error()}
	}
	model := c.Model
	return &model, c.Arguments, nil
}

// ModelJSON is a model's canonical JSON (sorted keys), for comparisons.
func ModelJSON(m *mf.Message) string {
	if m == nil {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// ── local ───────────────────────────────────────────────────────────

// SourceCatalogMissingError means the source locale's catalog file
// doesn't exist.
type SourceCatalogMissingError struct{ Path string }

func (e *SourceCatalogMissingError) Error() string {
	return "no source catalog at " + e.Path
}

// FromLocal reads the catalogs glossa.yaml points at.
func FromLocal(cfg *config.Config) (*Snapshot, error) {
	syntax := cfg.SyntaxOrDefault()
	src := cfg.CatalogPath(cfg.SourceLocale)
	c, err := catalog.Load(src)
	if catalog.IsNotExist(err) {
		return nil, &SourceCatalogMissingError{Path: src}
	}
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Origin: FromLocalOrigin, SourceLocale: cfg.SourceLocale, Translations: map[string]map[string]Translation{}}
	s.Locales = append(s.Locales, Locale{Code: cfg.SourceLocale, Direction: direction(cfg.SourceLocale), IsSource: true, File: src})
	for _, key := range c.Keys() {
		text := c.Entries[key]
		model, args, invalid := Parse(syntax, text, cfg.SourceLocale)
		s.Messages = append(s.Messages, Message{Key: key, Namespace: "default", Text: text, Syntax: syntax,
			Model: model, Arguments: args, Invalid: invalid, File: src})
	}
	found, err := catalog.Discover(cfg.Resolve(cfg.Catalogs.Path))
	if err != nil {
		return nil, err
	}
	var targets []Locale
	for code, file := range found {
		tag, err := bcp47.Parse(code)
		if err != nil || tag.String() == cfg.SourceLocale {
			continue
		}
		targets = append(targets, Locale{Code: tag.String(), Direction: string(tag.Direction()), File: file})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Code < targets[j].Code })
	for _, l := range targets {
		trs, err := loadTranslations(l, syntax)
		if err != nil {
			return nil, err
		}
		s.Locales = append(s.Locales, l)
		s.Translations[l.Code] = trs
	}
	return s, nil
}

func loadTranslations(l Locale, syntax string) (map[string]Translation, error) {
	c, err := catalog.Load(l.File)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Translation, len(c.Entries))
	for key, text := range c.Entries {
		model, _, invalid := Parse(syntax, text, l.Code)
		out[key] = Translation{Key: key, Locale: l.Code, Text: text, Syntax: syntax, Model: model, Invalid: invalid, File: l.File}
	}
	return out, nil
}

func direction(locale string) string {
	tag, err := bcp47.Parse(locale)
	if err != nil {
		return "ltr"
	}
	return string(tag.Direction())
}

// ── server ──────────────────────────────────────────────────────────

// Reader is what FromServer needs from glossa-server.
type Reader interface {
	Locales(ctx context.Context, s remote.Scope) ([]remote.ProjectLocale, error)
	Messages(ctx context.Context, s remote.Scope, f remote.MessageFilter) ([]remote.Message, error)
	ProjectTranslations(ctx context.Context, s remote.Scope, locales []string, f remote.TranslationFilter) ([]remote.ProjectTranslation, error)
	FallbackGraph(ctx context.Context, s remote.Scope) (map[string][]string, error)
	// FirstProjectTranslations reads the first page of size of the
	// translation listing per chunk of locales, and says whether any
	// chunk had another.
	FirstProjectTranslations(ctx context.Context, s remote.Scope, locales []string, f remote.TranslationFilter, size int) ([]remote.ProjectTranslation, bool, error)
}

// Options narrow what FromServer reads.
type Options struct {
	// SkipTranslations reads only locales and messages.
	SkipTranslations bool
	// Locales limits the translations read to these target locales
	// (empty: every locale).
	Locales []string
	// Orphans also reads the translations of obsolete messages, which
	// is what `glossa check` reports as `unknown-key` — bounded to one
	// page of layers.MaxOrphans per listing, so a project that has
	// obsoleted thousands of messages does not make every check read
	// thousands of dead translations.
	Orphans bool
}

// usableStates are the review states a check grades: every one but
// rejected, which is not a translation. The server's own snapshot reads
// the same three (quality/adapters/snapshot).
var usableStates = []string{"draft", "needs_review", "approved"}

// FromServer reads the project's active messages, locales and
// translations. Translations come from the bulk listing (every target
// locale, active messages, a page per request) and are joined to
// Catalog's messages by message ID, so a key Localization hasn't caught
// up with yet (a rename a moment ago) still lands on the right message.
func FromServer(ctx context.Context, r Reader, scope remote.Scope, sourceLocale string, opts Options) (*Snapshot, error) {
	locales, err := r.Locales(ctx, scope)
	if err != nil {
		return nil, err
	}
	remote.SortLocales(locales)
	msgs, err := r.Messages(ctx, scope, remote.MessageFilter{State: "active"})
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Origin: FromServerOrigin, SourceLocale: sourceLocale, Translations: map[string]map[string]Translation{}}
	for _, l := range locales {
		s.Locales = append(s.Locales, Locale{Code: l.Code, Direction: string(l.Direction), IsSource: l.IsSource})
		if !l.IsSource {
			s.Translations[l.Code] = map[string]Translation{}
		}
	}
	byID := make(map[string]string, len(msgs))
	for _, m := range msgs {
		s.Messages = append(s.Messages, fromRemoteMessage(m))
		byID[m.Id] = m.Key
	}
	sort.Slice(s.Messages, func(i, j int) bool { return s.Messages[i].Key < s.Messages[j].Key })
	if s.Fallback, err = r.FallbackGraph(ctx, scope); err != nil {
		return nil, err
	}
	if opts.SkipTranslations {
		return s, nil
	}
	targets := make([]string, 0, len(s.Translations))
	for _, l := range s.TargetLocales() {
		if len(opts.Locales) == 0 || slices.Contains(opts.Locales, l.Code) {
			targets = append(targets, l.Code)
		}
	}
	if len(targets) == 0 {
		return s, nil
	}
	trs, err := r.ProjectTranslations(ctx, scope, targets, remote.TranslationFilter{MessageState: "active"})
	if err != nil {
		return nil, err
	}
	for _, t := range trs {
		key, ok := byID[t.MessageId]
		byKey, known := s.Translations[t.Locale]
		if ok && known {
			byKey[key] = fromRemoteTranslation(key, t)
		}
	}
	if opts.Orphans {
		if err := s.readOrphans(ctx, r, scope, targets); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// readOrphans reads the translations of obsolete messages in targets:
// one page of layers.MaxOrphans per chunk of locales, in the listing's
// (key, message ID, locale) order, which is exactly what the server's
// own snapshot reads. The completeness layer sorts and cuts the rest.
func (s *Snapshot) readOrphans(ctx context.Context, r Reader, scope remote.Scope, targets []string) error {
	trs, more, err := r.FirstProjectTranslations(ctx, scope, targets,
		remote.TranslationFilter{MessageState: "obsolete", States: usableStates}, layers.MaxOrphans)
	if err != nil {
		return err
	}
	s.MoreOrphans = more
	for _, t := range trs {
		s.Orphans = append(s.Orphans, Orphan{
			MessageID: t.MessageId, Key: t.Key, Namespace: t.Namespace, Locale: t.Locale, Revision: t.Id,
		})
	}
	return nil
}

func fromRemoteMessage(m remote.Message) Message {
	out := Message{ID: m.Id, Key: m.Key, Namespace: m.Namespace, Description: m.Description, Text: m.Source.Text,
		Syntax: string(m.Source.Syntax), Revision: m.SourceRevision}
	if m.MaxLength != nil {
		out.MaxLength = *m.MaxLength
	}
	model, err := DecodeModel(m.Source.Model)
	if err != nil {
		out.Invalid = &Invalid{Code: "invalid-message", Detail: err.Error()}
		return out
	}
	out.Model = model
	out.Arguments = mf.Arguments(*model)
	return out
}

func fromRemoteTranslation(key string, t remote.ProjectTranslation) Translation {
	out := Translation{Key: key, Locale: t.Locale, Text: t.Text, Syntax: string(t.Syntax), State: string(t.State),
		Origin: string(t.Origin), SourceRevision: t.SourceRevision, Outdated: t.Outdated}
	if model, err := DecodeModel(t.Model); err == nil {
		out.Model = model
	} else {
		out.Invalid = &Invalid{Code: "invalid-message", Detail: err.Error()}
	}
	for _, w := range t.Warnings {
		f := mf.Finding{Code: mf.FindingCode(w.Code), Severity: mf.Severity(w.Severity), Message: w.Message}
		if w.Subject != nil {
			f.Subject = *w.Subject
		}
		if w.Detail != nil {
			f.Detail = *w.Detail
		}
		out.Warnings = append(out.Warnings, f)
	}
	return out
}

// decodeModel turns the wire model into the kernel's type.
func DecodeModel(v map[string]any) (*mf.Message, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m mf.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	return &m, nil
}
