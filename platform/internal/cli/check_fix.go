package cli

import (
	"errors"
	"fmt"
	"io/fs"

	"go.klarlabs.de/glossa/platform/internal/cli/catalog"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa check --fix` (RFC 0005 §13 wave 6): applying the structured
// fixes findings carry.
//
// A `domain.Fix` is a hint, and RFC 0005 §2.1 is explicit that nothing
// applies one without a person or an explicit `--fix`. This is that
// flag, and its whole discipline is one sentence: **apply only what a
// fix actually describes.**
//
// Two of the four kinds describe an edit exactly, and two do not.
//
//   - `replace` and `shorten` name the text to write, in `hint`. The
//     message becomes that text. `shorten` without a hint names a
//     length and no words — there is no edit in it, only a target — so
//     it is reported and not applied.
//   - `use-term` names a term and, through the finding's span, the
//     bytes it replaces. It is applied only when the catalog's text
//     still reads exactly the subject the layer found at those
//     offsets; a span into text that has since changed points at the
//     wrong words, and writing there would be a guess dressed as a fix.
//   - `adopt-source-change` asks for a translation made against the new
//     source. That is a translation, not an edit, and no amount of
//     string handling makes one.
//
// A run applies at most one fix per message, because a second fix's
// span was measured against the text the first one replaced. The rest
// are reported and the command says to run again — two passes that are
// each correct, rather than one that is probably correct.
//
// The edit goes to the local catalog (`catalogs.path`), rewritten in the
// canonical form `glossa pull` writes: sorted keys, two-space indent.
// A waived finding is never touched — somebody accepted it on purpose.

// fixJSON is one fix, applied or not. A fix that was not applied is in
// the document with its reason, because "I could not do this" is an
// answer a person has to be able to read.
type fixJSON struct {
	Fingerprint string         `json:"fingerprint"`
	Layer       domain.Layer   `json:"layer"`
	Code        string         `json:"code"`
	Kind        domain.FixKind `json:"kind"`
	Locale      string         `json:"locale,omitempty"`
	Key         string         `json:"key,omitempty"`
	Applied     bool           `json:"applied"`
	// File is the catalog the edit went to, relative to glossa.yaml.
	File string `json:"file,omitempty"`
	// From and To are the message's text before and after.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Why says why a fix was not applied.
	Why string `json:"why,omitempty"`
}

// fixTarget is one locale's catalog while a run edits it.
type fixTarget struct {
	path    string
	entries map[string]string
	changed bool
	// err is a catalog that could not be read; every fix for the locale
	// reports it rather than the run failing, because the other locales'
	// fixes are still good.
	err string
}

// applyFixes applies what the findings describe and reports all of it.
//
// It never fails the command: a fix that cannot be applied is a line in
// the report, not an error. The check's own verdict is what `glossa
// check` is for, and losing it to a catalog permission would be the
// wrong trade.
func (inv *invocation) applyFixes(cfg *config.Config, findings []domain.Finding) []fixJSON {
	out := []fixJSON{}
	targets := map[string]*fixTarget{}
	// edited remembers the messages this run has already rewritten, so a
	// second fix's span is never applied to text the first one replaced.
	edited := map[string]bool{}
	style := catalog.Flat
	if cfg.Catalogs.Style == "nested" {
		style = catalog.Nested
	}
	for _, f := range findings {
		if f.Fix == nil {
			continue
		}
		row := fixJSON{
			Fingerprint: f.Fingerprint, Layer: f.Layer, Code: f.Code, Kind: f.Fix.Kind,
			Locale: f.Locus.Locale, Key: f.Locus.Key,
		}
		out = append(out, row)
		applyFix(inv, cfg, targets, edited, &out[len(out)-1], f)
	}
	for _, locale := range sortedKeys(targets) {
		t := targets[locale]
		if !t.changed {
			continue
		}
		if _, err := catalog.Write(t.path, t.entries, style); err != nil {
			markUnwritten(out, locale, err.Error())
		}
	}
	return out
}

// applyFix decides one fix and, where it is an edit, makes it.
func applyFix(
	inv *invocation, cfg *config.Config, targets map[string]*fixTarget, edited map[string]bool,
	row *fixJSON, f domain.Finding,
) {
	switch {
	case f.Severity == domain.Waived:
		row.Why = "the finding is waived: somebody accepted it, and a fix would undo that on their behalf"
		return
	case f.Fix.Kind == domain.FixAdoptSourceChange:
		row.Why = "this fix asks for a translation made against the new source, which is a translation and not an edit; " +
			"`glossa translate` or a translator makes one"
		return
	case f.Locus.Key == "" || f.Locus.Locale == "":
		row.Why = "the finding names no message and locale to edit"
		return
	}
	t := inv.fixTargetFor(cfg, targets, f.Locus.Locale)
	if t.err != "" {
		row.Why = t.err
		return
	}
	row.File = relPath(cfg, t.path)
	current, ok := t.entries[f.Locus.Key]
	if !ok {
		row.Why = "the local catalog has no " + f.Locus.Key
		return
	}
	if edited[f.Locus.Locale+"\x00"+f.Locus.Key] {
		row.Why = "another fix already rewrote this message in this run; run `glossa check --fix` again to apply this one"
		return
	}
	next, why := fixedText(current, f)
	if why != "" {
		row.Why = why
		return
	}
	if next == current {
		row.Why = "the catalog already says this"
		return
	}
	t.entries[f.Locus.Key], t.changed = next, true
	edited[f.Locus.Locale+"\x00"+f.Locus.Key] = true
	row.Applied, row.From, row.To = true, current, next
}

// fixedText is the text the fix describes, or the reason there isn't
// one. It is the only place that decides what a fix means.
func fixedText(current string, f domain.Finding) (string, string) {
	fix := f.Fix
	switch fix.Kind {
	case domain.FixReplace, domain.FixShorten:
		if fix.Hint == "" {
			if fix.Kind == domain.FixShorten && fix.To != nil {
				return "", fmt.Sprintf("this fix asks for at most %d characters and names no text to write; "+
					"shortening it is a decision about the words", *fix.To)
			}
			return "", "this fix names no text to write"
		}
		return fix.Hint, ""
	case domain.FixUseTerm:
		return replaceSpan(current, f)
	}
	return "", fmt.Sprintf("%q is not a fix this command knows how to apply", string(fix.Kind))
}

// replaceSpan puts the preferred term where the offending one is.
//
// The guard is the point: the span's bytes must still read exactly the
// subject the layer found there. A span is an offset into the text the
// layer saw, and text on disk that has moved on since makes the same
// offsets point at different words.
func replaceSpan(current string, f domain.Finding) (string, string) {
	span := f.Locus.Span
	switch {
	case f.Fix.Hint == "":
		return "", "this fix names a concept but no term to write"
	case span == nil:
		return "", "this fix names a term but not which words it replaces"
	case span.Side != domain.SideTarget:
		return "", "this fix's span is in the source text, which a translation's catalog entry does not hold"
	case span.Start < 0 || span.End > len(current) || span.Start > span.End:
		return "", "this fix's span is outside the catalog's text, which has changed since the finding was made"
	case f.Subject == "" || current[span.Start:span.End] != f.Subject:
		return "", fmt.Sprintf("the catalog reads %q where the finding found %q: the text has changed since, "+
			"and writing there would be a guess", current[span.Start:span.End], f.Subject)
	}
	return current[:span.Start] + f.Fix.Hint + current[span.End:], ""
}

// fixTargetFor opens a locale's catalog once per run.
func (inv *invocation) fixTargetFor(cfg *config.Config, targets map[string]*fixTarget, locale string) *fixTarget {
	if t, ok := targets[locale]; ok {
		return t
	}
	path := cfg.CatalogPath(locale)
	t := &fixTarget{path: path, entries: map[string]string{}}
	c, err := catalog.Load(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		t.err = "there is no local catalog at " + relPath(cfg, path)
	case err != nil:
		t.err = "the local catalog can't be read: " + err.Error()
	default:
		t.entries = c.Entries
	}
	targets[locale] = t
	return t
}

// markUnwritten takes back the fixes of a catalog that could not be
// saved. A report saying "applied" about an edit still in memory would
// be the one lie this command cannot afford.
func markUnwritten(out []fixJSON, locale, why string) {
	for i := range out {
		if out[i].Applied && out[i].Locale == locale {
			out[i].Applied = false
			out[i].Why = "the catalog couldn't be written: " + why
		}
	}
}

// printFixes is the --fix half of the check's output.
func printFixes(p *printer, fixes []fixJSON) {
	if len(fixes) == 0 {
		return
	}
	applied := 0
	for _, f := range fixes {
		if f.Applied {
			applied++
		}
	}
	p.line("")
	p.line("%s", p.bold("Fixes"))
	for _, f := range fixes {
		label := f.Locus()
		if f.Applied {
			p.line("  %s %s  %s", p.ok("fixed"), label, p.dim(string(f.Kind)+" → "+f.To))
			continue
		}
		p.line("  %s %s  %s", p.dim("kept "), label, p.dim(string(f.Kind)+": "+f.Why))
	}
	if applied == 0 {
		p.line("  %s", p.dim("nothing was changed"))
		return
	}
	p.line("  %s", p.dim(fmt.Sprintf("%s written; the verdict above is the run before them, "+
		"so run `glossa check` again to confirm", plural(applied, "fix", "fixes"))))
}

// Locus names the message a fix is about, for human output.
func (f fixJSON) Locus() string {
	switch {
	case f.Locale != "" && f.Key != "":
		return f.Locale + " " + f.Key
	case f.Key != "":
		return f.Key
	}
	return f.Fingerprint
}
