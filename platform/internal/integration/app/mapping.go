package app

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// The mapping between the exchange model (formats) and what the other
// contexts store — the anti-corruption layer of imports and exports.

// ConceptFromFile maps a TBX concept onto a termbase concept. Glossa
// keeps one definition and one note per concept and per term: the
// definition without a language (else the document's language's, else
// the first) is the definition, the others join the note as
// "Definition (de): …"; a term's context joins its note as
// "Context: …". Terms without a status are admitted.
func ConceptFromFile(tb formats.Termbase, c formats.Concept) ConceptWrite {
	w := ConceptWrite{Domain: c.Domain}
	main := -1
	for i, d := range c.Definitions {
		if d.Locale.IsZero() {
			main = i
			break
		}
		if main < 0 && d.Locale == tb.Language {
			main = i
		}
	}
	if main < 0 && len(c.Definitions) > 0 {
		main = 0
	}
	notes := slices.Clone(c.Notes)
	for i, d := range c.Definitions {
		if i == main {
			w.Definition = d.Text
			continue
		}
		notes = append(notes, "Definition ("+d.Locale.String()+"): "+d.Text)
	}
	w.Note = strings.Join(notes, "\n\n")
	for _, t := range c.Terms {
		status := string(t.Status)
		if status == "" {
			status = string(formats.TermAdmitted)
		}
		tn := slices.Clone(t.Notes)
		if t.Context != "" {
			tn = append(tn, "Context: "+t.Context)
		}
		w.Terms = append(w.Terms, TermWrite{
			Locale: t.Locale.String(), Text: t.Text, Status: status, PartOfSpeech: t.PartOfSpeech, Note: strings.Join(tn, "\n\n"),
		})
	}
	return w
}

// ConceptToFile maps a stored concept onto a TBX concept, under its ID.
func ConceptToFile(c ConceptView) formats.Concept {
	out := formats.Concept{ID: c.ID.String(), Domain: c.Domain}
	if c.Definition != "" {
		out.Definitions = []formats.Definition{{Text: c.Definition}}
	}
	if c.Note != "" {
		out.Notes = []string{c.Note}
	}
	for _, t := range c.Terms {
		tag, err := bcp47.Parse(t.Locale)
		if err != nil {
			continue
		}
		term := formats.Term{Locale: tag, Text: t.Text, Status: formats.TermStatus(t.Status), PartOfSpeech: t.PartOfSpeech}
		if t.Note != "" {
			term.Notes = []string{t.Note}
		}
		out.Terms = append(out.Terms, term)
	}
	return out
}

// conceptNamespace seeds concept IDs derived from files.
var conceptNamespace = uuid.MustParse("5b0e2f7c-2a61-4d0c-9f0e-6c1d7a3e9b42")

// conceptFileID is a concept's identity in its file: its ID, else its
// first term.
func conceptFileID(c formats.Concept) string {
	if c.ID != "" {
		return c.ID
	}
	if len(c.Terms) > 0 {
		return "term:" + c.Terms[0].Locale.String() + ":" + c.Terms[0].Text
	}
	return ""
}

// conceptID is the ID a file's concept is stored under: its own when it
// is a UUID the tenant already has (a Glossa export coming back), else
// one derived from the tenant, the import's scope and the file's ID —
// so importing the same file again finds the same concepts, and two
// tenants importing one file never collide.
func (s *Service) conceptID(ctx context.Context, j *domain.Job, c formats.Concept) (uuid.UUID, error) {
	fileID := conceptFileID(c)
	if id, err := uuid.Parse(fileID); err == nil {
		exists, err := s.knowledge.ConceptExists(ctx, id)
		if err != nil || exists {
			return id, err
		}
	}
	scope := "tenant"
	if j.ProjectID != nil {
		scope = j.ProjectID.String()
	}
	return uuid.NewSHA1(conceptNamespace, []byte(tenantOf(ctx).String()+"\x00"+scope+"\x00"+fileID)), nil
}

// TMUnitToFile maps a stored TM unit onto a TMX unit. The message key a
// derived unit was approved for travels as the prop
// x-glossa-message-key.
func TMUnitToFile(u TMUnitView) (formats.TMUnit, error) {
	src, err := formats.ParseContent(mfcontent.MF2, u.SourceMF2, u.SourceLocale)
	if err != nil {
		return formats.TMUnit{}, err
	}
	tgt, err := formats.ParseContent(mfcontent.MF2, u.TargetMF2, u.TargetLocale)
	if err != nil {
		return formats.TMUnit{}, err
	}
	out := formats.TMUnit{
		ID: u.ID.String(), SourceLocale: u.SourceLocale, TargetLocale: u.TargetLocale, Source: src, Target: tgt,
		CreatedAt: u.CreatedAt, ChangedAt: u.UpdatedAt, UsageCount: u.HitCount,
	}
	if u.LastHitAt != nil {
		out.LastUsedAt = *u.LastHitAt
	}
	if u.MessageKey != "" {
		out.Props = []formats.Prop{{Type: "x-glossa-message-key", Value: u.MessageKey}}
	}
	return out, nil
}

// CatalogFromSnapshot is the exchange catalog of a project's snapshot:
// its active messages (in namespaces, when given) with their source and
// every translation the snapshot holds.
func CatalogFromSnapshot(snap Snapshot, namespaces []string) formats.Catalog {
	cat := formats.Catalog{SourceLocale: snap.Project.SourceLocale}
	for _, m := range snap.Messages {
		if len(namespaces) > 0 && !slices.Contains(namespaces, m.Namespace) {
			continue
		}
		e := formats.Entry{
			ID: m.Key, Namespace: m.Namespace, Description: m.Description, MaxLength: m.MaxLength, Source: m.Source,
		}
		locales := make([]bcp47.Tag, 0, len(m.Translations))
		for l := range m.Translations {
			locales = append(locales, l)
		}
		slices.SortFunc(locales, func(a, b bcp47.Tag) int { return strings.Compare(a.String(), b.String()) })
		for _, l := range locales {
			t := m.Translations[l]
			e.Targets = append(e.Targets, formats.Target{Locale: l, Content: t.Content, State: formats.State(t.State)})
		}
		cat.Entries = append(cat.Entries, e)
	}
	return cat
}
