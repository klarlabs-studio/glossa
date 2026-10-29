package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/jsoncat"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/po"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tbx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tmx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/xliff"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
)

// batchSize is how many entries, units or concepts an import applies
// per checkpoint: the bulk limit of the ports it writes through.
const batchSize = 500

// runImport reads a job's file and applies it batch by batch, resuming
// after the last checkpoint when an earlier attempt stopped.
func (s *Service) runImport(ctx context.Context, c Claim, j *domain.Job) error {
	var project ProjectInfo
	if j.ProjectID != nil {
		var err error
		if project, err = s.catalog.Project(ctx, *j.ProjectID); errors.Is(err, ErrProjectNotFound) {
			return permanent(domain.FailureProjectGone, "the project was deleted")
		} else if err != nil {
			return err
		}
	}
	if j.File == nil {
		return permanent(domain.FailureInternal, "the job has no file")
	}
	rc, err := s.objects.Open(ctx, j.File.Key)
	if errors.Is(err, objectstore.ErrNotFound) {
		return permanent(domain.FailureInternal, "the uploaded file is gone")
	}
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	file := &trackingReader{r: rc}
	switch j.Kind {
	case domain.KindTM:
		return s.importTM(ctx, c, j, file)
	case domain.KindTermbase:
		return s.importTermbase(ctx, c, j, file)
	}
	return s.importCatalog(ctx, c, j, project, file)
}

// trackingReader remembers a failure of the file's own reader (object
// storage), which the format readers would report as a malformed file.
type trackingReader struct {
	r   io.Reader
	err error
}

func (t *trackingReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && t.err == nil {
		t.err = err
	}
	return n, err
}

// fileError turns a reader's failure into the job's end: a malformed,
// unsupported or oversized file fails the job with the problem as its
// next result (line, column, item); a storage failure is retried.
func (s *Service) fileError(ctx context.Context, c Claim, j *domain.Job, file *trackingReader, kind domain.ItemKind, err error) error {
	if file.err != nil {
		return fmt.Errorf("integration: read the uploaded file: %w", file.err)
	}
	code := domain.FailureInvalidFile
	switch {
	case errors.Is(err, formats.ErrTooLarge):
		code = domain.FailureTooLarge
	case errors.Is(err, formats.ErrUnsupported):
		code = domain.FailureUnsupported
	case !errors.Is(err, formats.ErrInvalid):
		return err
	}
	it := domain.Item{Seq: j.Summary.Total(), Kind: kind, Status: domain.ItemInvalid, Code: code, Detail: truncate(err.Error(), 2000)}
	var fe *formats.Error
	if errors.As(err, &fe) {
		it.Key, it.Line, it.Column = truncate(fe.Item, 1000), fe.Line, fe.Column
	}
	j.Summary.Add(it)
	if err := s.checkpoint(ctx, c, j, []domain.Item{it}); err != nil {
		return err
	}
	return permanent(code, "%v", err)
}

// ── catalogs ─────────────────────────────────────────────────────────

func (s *Service) importCatalog(ctx context.Context, c Claim, j *domain.Job, p ProjectInfo, file *trackingReader) error {
	cat, err := s.readCatalog(j, p, file)
	if err != nil {
		return s.fileError(ctx, c, j, file, domain.ItemMessage, err)
	}
	if !cat.SourceLocale.IsZero() && cat.SourceLocale != p.SourceLocale {
		return permanent(domain.FailureSourceLocale, "the file's source locale is %s, the project's %s", cat.SourceLocale, p.SourceLocale)
	}
	if err := s.checkTargetLocales(ctx, p, cat); err != nil {
		return err
	}
	entries := cat.Entries
	if j.Format == domain.FormatPO {
		entries = poEntries(entries, j.Options.Namespace)
	}
	j.TotalItems = len(entries)
	if err := s.checkpoint(ctx, c, j, nil); err != nil {
		return err
	}
	for start := j.ProcessedItems; start < len(entries); start += batchSize {
		end := min(start+batchSize, len(entries))
		items, err := s.importEntries(ctx, j, p, entries[start:end])
		if err != nil {
			return err
		}
		for i := range items {
			items[i].Seq = j.Summary.Total()
			j.Summary.Add(items[i])
		}
		j.ProcessedItems = end
		if err := s.checkpoint(ctx, c, j, items); err != nil {
			return err
		}
	}
	return s.finish(ctx, c, j, domain.StateSucceeded, "", "")
}

func (s *Service) readCatalog(j *domain.Job, p ProjectInfo, r io.Reader) (formats.Catalog, error) {
	limits := formats.Limits{MaxBytes: s.cfg.MaxUploadBytes}
	o := j.Options
	switch j.Format {
	case domain.FormatJSON:
		locale := p.SourceLocale
		if o.Locale != "" {
			locale = bcp47.MustParse(o.Locale)
		}
		return jsoncat.Read(r, jsoncat.ReadOptions{
			Locale: locale, SourceLocale: p.SourceLocale, Namespace: o.Namespace, Syntax: mfcontent.Syntax(o.Syntax),
			State: formats.State(o.State), Limits: limits,
		})
	case domain.FormatPO:
		var locale bcp47.Tag
		if o.Locale != "" {
			locale = bcp47.MustParse(o.Locale)
		}
		return po.Read(r, po.ReadOptions{
			Locale: locale, SourceLocale: p.SourceLocale, PluralVariable: o.PluralVariable,
			TranslatedState: formats.State(o.State), Limits: limits,
		})
	}
	var target bcp47.Tag
	if o.Locale != "" {
		target = bcp47.MustParse(o.Locale)
	}
	return xliff.Read(r, xliff.ReadOptions{Limits: limits, PlainSyntax: mfcontent.Syntax(o.Syntax), TargetLocale: target})
}

// checkTargetLocales fails an import whose translations are in a
// locale the project doesn't have — a German file for a project with
// de-AT only, say — with the locale to choose instead, rather than
// reporting every translation of the file invalid.
func (s *Service) checkTargetLocales(ctx context.Context, p ProjectInfo, cat formats.Catalog) error {
	var locales []bcp47.Tag
	for _, e := range cat.Entries {
		for _, t := range e.Targets {
			if !slices.Contains(locales, t.Locale) {
				locales = append(locales, t.Locale)
			}
		}
	}
	if len(locales) == 0 {
		return nil
	}
	targets, err := s.localization.Locales(ctx, p.ID)
	if err != nil {
		return err
	}
	unknown := domain.UnknownTargetLocales(locales, targets)
	if len(unknown) == 0 {
		return nil
	}
	have := joinTags(targets)
	if have == "" {
		have = "none yet"
	}
	return permanent(domain.FailureTargetLocale,
		"the file's translations are in %s, which the project doesn't have (its target locales: %s); import the file as one of them with the locale option",
		joinTags(unknown), have)
}

func joinTags(tags []bcp47.Tag) string {
	s := make([]string, len(tags))
	for i, t := range tags {
		s[i] = t.String()
	}
	return strings.Join(s, ", ")
}

// poEntries gives gettext entries catalog keys (domain.POMessageKey)
// and the import's namespace: msgctxt disambiguates a key, it isn't a
// bundle.
func poEntries(entries []formats.Entry, namespace string) []formats.Entry {
	out := make([]formats.Entry, len(entries))
	for i, e := range entries {
		e.ID, e.Namespace = domain.POMessageKey(e.Namespace, e.ID), namespace
		out[i] = e
	}
	return out
}

// entryPlan is what happens to one entry of a batch.
type entryPlan struct {
	entry formats.Entry
	// msgItem is the index of the entry's message result, -1 when the
	// file carries no source for it.
	msgItem int
	// blocked, when set, is the result every translation of the entry
	// gets instead of being written.
	blocked *domain.Item
	// predicted: a dry run's message that would be created, so its
	// translations can't be checked yet and would be created too.
	predicted bool
}

// importEntries applies one batch of a catalog: messages first (created,
// or revised in overwrite mode), then translations, capped to the
// requester's access. Results come back in file order: each message,
// then its translations.
func (s *Service) importEntries(ctx context.Context, j *domain.Job, p ProjectInfo, entries []formats.Entry) ([]domain.Item, error) {
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.ID
	}
	existing, err := s.catalog.MessagesByKeys(ctx, p.ID, keys)
	if err != nil {
		return nil, err
	}
	dry := j.Mode == domain.ModeDryRun
	var (
		items    []domain.Item
		plans    []entryPlan
		writes   []MessageWrite
		writeIdx []int // plan index of each write
		seen     = map[string]bool{}
	)
	for _, e := range entries {
		pl := entryPlan{entry: e, msgItem: -1}
		if seen[e.ID] {
			dup := domain.Item{Kind: domain.ItemMessage, Key: e.ID, Status: domain.ItemInvalid, Code: "duplicate_key",
				Detail: "the key appears earlier in the file"}.At(e.Pos)
			pl.blocked = &dup
		}
		seen[e.ID] = true
		if !e.Source.IsZero() {
			it := domain.Item{Kind: domain.ItemMessage, Key: e.ID}.At(e.Pos)
			switch {
			case pl.blocked != nil:
				it = *pl.blocked
			default:
				m, exists := existing[e.ID]
				plan := domain.PlanMessage(exists, exists && m.Source.SameModel(e.Source), j.Mode, j.Access.Manage)
				it.Status, it.Code, it.Detail = plan.Status, plan.Code, plan.Detail
				w := MessageWrite{Key: e.ID, Namespace: e.Namespace, Description: e.Description, MaxLength: e.MaxLength,
					Source: e.Source, SourceOnly: plan.Action == domain.ActionRevise}
				if plan.Action != domain.ActionNone {
					if code, detail := s.catalog.CheckMessage(p, w); code != "" {
						it.Status, it.Code, it.Detail = domain.ItemInvalid, code, detail
					} else if dry {
						pl.predicted = plan.Action == domain.ActionCreate
					} else {
						writes, writeIdx = append(writes, w), append(writeIdx, len(plans))
					}
				}
			}
			pl.msgItem = len(items)
			items = append(items, it)
		}
		plans = append(plans, pl)
	}
	if len(writes) > 0 {
		res, err := s.catalog.UpsertMessages(ctx, p.ID, writes)
		if err != nil {
			return nil, err
		}
		for i, r := range res {
			it := &items[plans[writeIdx[i]].msgItem]
			switch {
			case r.Code != "":
				it.Status, it.Code, it.Detail = domain.ItemInvalid, r.Code, truncate(r.Detail, 2000)
			case r.Status == "unchanged":
				it.Status = domain.ItemUnchanged
			}
		}
	}
	return s.importTranslations(ctx, j, p, plans, items)
}

// importTranslations adds each entry's translation results after its
// message's and writes the ones that may be written.
func (s *Service) importTranslations(ctx context.Context, j *domain.Job, p ProjectInfo, plans []entryPlan, msgItems []domain.Item) ([]domain.Item, error) {
	dry := j.Mode == domain.ModeDryRun
	detail, err := json.Marshal(map[string]string{
		"job": j.ID.String(), "file": j.FileName, "format": string(j.Format), "requested_by": j.Access.Actor,
	})
	if err != nil {
		return nil, err
	}
	var (
		items  []domain.Item
		writes []TranslationWrite
		idx    []int
	)
	for _, pl := range plans {
		if pl.msgItem >= 0 {
			items = append(items, msgItems[pl.msgItem])
		}
		blocked := pl.blocked
		if pl.msgItem >= 0 && blocked == nil {
			switch m := msgItems[pl.msgItem]; m.Status {
			case domain.ItemConflict:
				blocked = &domain.Item{Status: domain.ItemConflict, Code: domain.CodeSourceDiffers,
					Detail: "the translation was made for other source text than the message has; merge keeps the message and skips it"}
			case domain.ItemInvalid:
				blocked = &domain.Item{Status: domain.ItemInvalid, Code: "message_invalid", Detail: "its message wasn't imported: " + m.Code}
				if m.Code == domain.CodeForbidden {
					blocked.Code, blocked.Detail = domain.CodeMessageNotFound, "the message doesn't exist and this import may not create it"
				}
			}
		}
		for _, t := range pl.entry.Targets {
			it := domain.Item{Kind: domain.ItemTranslation, Key: pl.entry.ID, Locale: t.Locale.String()}.At(t.Pos.Or(pl.entry.Pos))
			switch {
			case !domain.Covers(j.Access.Import, t.Locale):
				it.Status, it.Code, it.Detail = domain.ItemInvalid, domain.CodeForbidden, "no integration.import for "+t.Locale.String()
			case blocked != nil:
				it.Status, it.Code, it.Detail = blocked.Status, blocked.Code, blocked.Detail
			case pl.predicted:
				it.Status = domain.ItemCreated
			default:
				writes = append(writes, TranslationWrite{
					Key: pl.entry.ID, Locale: t.Locale, Content: t.Content, OriginDetail: detail,
					State: domain.RequestedState(t.State, domain.Covers(j.Access.Review, t.Locale), p.ReviewRequired),
				})
				idx = append(idx, len(items))
			}
			items = append(items, it)
		}
	}
	for start := 0; start < len(writes); start += batchSize {
		end := min(start+batchSize, len(writes))
		res, err := s.localization.ImportTranslations(ctx, p.ID, writes[start:end], j.Mode != domain.ModeOverwrite, dry)
		if err != nil {
			return nil, err
		}
		for i, r := range res {
			it := &items[idx[start+i]]
			it.Status, it.Code, it.Detail = domain.TranslationStatus(r.Status, r.Code), r.Code, truncate(r.Detail, 2000)
		}
	}
	return items, nil
}

// ── translation memory ───────────────────────────────────────────────

func (s *Service) importTM(ctx context.Context, c Claim, j *domain.Job, file *trackingReader) error {
	rd, err := tmx.NewReader(file, tmx.ReadOptions{Limits: formats.Limits{MaxBytes: s.cfg.MaxUploadBytes}})
	if err != nil {
		return s.fileError(ctx, c, j, file, domain.ItemTMUnit, err)
	}
	for range j.ProcessedItems { // resume: skip what an earlier attempt applied
		if _, err := rd.Next(); err != nil {
			return s.fileError(ctx, c, j, file, domain.ItemTMUnit, err)
		}
	}
	for {
		batch, err := nextUnits(rd)
		if len(batch) > 0 {
			if err := s.importUnits(ctx, c, j, batch); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return s.finish(ctx, c, j, domain.StateSucceeded, "", "")
		}
		if err != nil {
			return s.fileError(ctx, c, j, file, domain.ItemTMUnit, err)
		}
	}
}

// nextUnits reads up to a batch of units; io.EOF after the last.
func nextUnits(rd *tmx.Reader) ([]formats.TMUnit, error) {
	var batch []formats.TMUnit
	for len(batch) < batchSize {
		u, err := rd.Next()
		if err != nil {
			return batch, err
		}
		batch = append(batch, u)
	}
	return batch, nil
}

func (s *Service) importUnits(ctx context.Context, c Claim, j *domain.Job, batch []formats.TMUnit) error {
	writes := make([]TMWrite, len(batch))
	for i, u := range batch {
		writes[i] = TMWrite{SourceLocale: u.SourceLocale, TargetLocale: u.TargetLocale, Source: u.Source.Model, Target: u.Target.Model}
	}
	res, err := s.knowledge.ImportTMUnits(ctx, j.ProjectID, writes, j.Mode == domain.ModeDryRun)
	if err != nil {
		return err
	}
	items := make([]domain.Item, len(batch))
	for i, r := range res {
		items[i] = domain.Item{
			Seq: j.Summary.Total(), Kind: domain.ItemTMUnit, Key: truncate(batch[i].ID, 1000), Locale: batch[i].TargetLocale.String(),
			Status: domain.ItemStatus(r.Status), Code: r.Code, Detail: truncate(r.Detail, 2000),
		}.At(batch[i].Pos)
		j.Summary.Add(items[i])
	}
	j.ProcessedItems += len(batch)
	j.TotalItems = j.ProcessedItems
	return s.checkpoint(ctx, c, j, items)
}

// ── termbases ────────────────────────────────────────────────────────

func (s *Service) importTermbase(ctx context.Context, c Claim, j *domain.Job, file *trackingReader) error {
	tb, err := tbx.Read(file, tbx.ReadOptions{Limits: formats.Limits{MaxBytes: s.cfg.MaxUploadBytes}})
	if err != nil {
		return s.fileError(ctx, c, j, file, domain.ItemConcept, err)
	}
	j.TotalItems = len(tb.Concepts)
	if err := s.checkpoint(ctx, c, j, nil); err != nil {
		return err
	}
	for start := j.ProcessedItems; start < len(tb.Concepts); start += batchSize {
		end := min(start+batchSize, len(tb.Concepts))
		if err := s.importConcepts(ctx, c, j, tb, tb.Concepts[start:end]); err != nil {
			return err
		}
	}
	return s.finish(ctx, c, j, domain.StateSucceeded, "", "")
}

func (s *Service) importConcepts(ctx context.Context, c Claim, j *domain.Job, tb formats.Termbase, batch []formats.Concept) error {
	writes := make([]ConceptWrite, len(batch))
	for i, fc := range batch {
		w := ConceptFromFile(tb, fc)
		id, err := s.conceptID(ctx, j, fc)
		if err != nil {
			return err
		}
		w.ID = id
		writes[i] = w
	}
	res, err := s.knowledge.ImportConcepts(ctx, j.ProjectID, writes, j.Mode == domain.ModeOverwrite, j.Mode == domain.ModeDryRun)
	if err != nil {
		return err
	}
	items := make([]domain.Item, len(batch))
	for i, r := range res {
		items[i] = domain.Item{
			Seq: j.Summary.Total(), Kind: domain.ItemConcept, Key: truncate(conceptFileID(batch[i]), 1000),
			Status: domain.ItemStatus(r.Status), Code: r.Code, Detail: truncate(r.Detail, 2000),
		}.At(batch[i].Pos)
		j.Summary.Add(items[i])
	}
	j.ProcessedItems += len(batch)
	return s.checkpoint(ctx, c, j, items)
}
