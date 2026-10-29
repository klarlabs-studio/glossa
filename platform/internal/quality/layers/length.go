package layers

import (
	"fmt"
	"math"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The length layer (RFC 0005 §3.3): does the translation fit?
//
// Three questions, in order of how much the layer knows.
//
//  1. `max_length` — the message says how long it may be, so the answer
//     is arithmetic and an error.
//  2. The expansion ratio — nobody said how long it may be, but German
//     into Japanese does not triple in length, and one that did is worth
//     a look. A warning, measured against the norm for the pair
//     (checkpolicy.LengthThresholds).
//  3. The layout budget — a capture measured the box the string renders
//     in, so there are real pixels. A warning, and the cheap,
//     deterministic, browser-free half of the visual layer (§5): the
//     same question asked from a font metric instead of from a page.
//
// Where `max-length-exceeded` lives, and why it moved. Before M4 it was
// computed at write time by localization/domain.CheckStructure, stored
// with the translation, and surfaced by the **parity** layer from that
// stored warning — so the one length rule that existed was reported
// under the wrong layer. It moves here, and parity stops relaying it,
// for three reasons. RFC 0005 §3.3 lists it as this layer's first rule.
// A policy selects on layer before it selects on code, so
// `{layer: length, severity: off}` has to switch off every length rule
// including this one — checkpolicy's own rule fixtures already write
// `{layer: "length", code: "max-length-exceeded"}` and would have
// matched nothing. And the move costs a fingerprint change (the layer
// is hashed into it, RFC 0005 §2.1), which is a cost worth paying
// before M4 ships and not after, when it would silently reopen every
// waiver anyone had made.
//
// The layer does not merely relay it any more, either. Where the caller
// carries the constraint (Message.MaxLength) it is recomputed here, so
// an offline `glossa check` over local catalogs finds it too — the
// server's stored warning was the only way to see it before, which
// meant the rule did not exist offline. Where the caller carries the
// warning and not the constraint, the stored finding is relayed
// unchanged. Both paths produce one finding, never two.
type Length struct{}

// Length codes.
const (
	// CodeMaxLengthExceeded is a translation longer than the message's
	// max_length. It keeps the spelling it had under parity: a code is
	// stable and shared, and only its layer moved.
	CodeMaxLengthExceeded = "max-length-exceeded"
	// CodeExpansionExcessive is a ratio well outside the pair's norm.
	CodeExpansionExcessive = "expansion-excessive"
	// CodeExpansionSuspicious is a ratio outside it.
	CodeExpansionSuspicious = "expansion-suspicious"
	// CodeLayoutOverflowPredicted is a translation whose predicted
	// advance width exceeds the box a capture measured for it.
	CodeLayoutOverflowPredicted = "layout-overflow-predicted"
)

// Layer implements Checker.
func (Length) Layer() domain.Layer { return domain.LayerLength }

// Check implements Checker.
func (Length) Check(p *Project, policy checkpolicy.Policy) []domain.Finding {
	th := policy.Length()
	var out []domain.Finding
	for _, l := range p.TargetLocales() {
		for _, t := range p.SortedTranslations(l.Code) {
			m, ok := p.Message(t.Key)
			if !ok || t.State == "rejected" {
				continue
			}
			out = append(out, maxLengthFindings(m, t, l.Code)...)
			if f, ok := expansionFinding(p.SourceLocale, m, t, l.Code, th); ok {
				out = append(out, f)
			}
			if f, ok := layoutFinding(p, m, t, l.Code, th); ok {
				out = append(out, f)
			}
		}
	}
	return out
}

// maxLengthFindings is the message's own limit, computed where the
// caller carries it and relayed where it does not.
func maxLengthFindings(m *Message, t Translation, locale string) []domain.Finding {
	if m.MaxLength > 0 && t.Model != nil {
		n := RenderedLength(t.Model)
		if n <= m.MaxLength {
			return nil
		}
		limit := m.MaxLength
		v := LongestVariant(t.Model)
		return []domain.Finding{domain.New(domain.Finding{
			Layer: domain.LayerLength, Code: CodeMaxLengthExceeded, Severity: domain.Error,
			Locus:   locusOf(m, t, locale, SpanOf(t.Text, v.Text, domain.SideTarget)),
			Message: fmt.Sprintf("%d characters; the message allows %d", n, limit),
			Evidence: map[string]any{
				"length": n, "max_length": limit, "over_by": n - limit,
			},
			Fix:            &domain.Fix{Kind: domain.FixShorten, To: &limit},
			SourceRevision: sourceRevision(t),
		})}
	}
	// No constraint in hand: the server computed this one at write time
	// and stored it, and relaying it is the only way the layer can see
	// it at all.
	var out []domain.Finding
	for _, w := range t.Warnings {
		if string(w.Code) != CodeMaxLengthExceeded {
			continue
		}
		out = append(out, fromKernel(domain.LayerLength, w, locale, m, t))
	}
	return out
}

// expansionFinding compares the translation's rendered length with its
// source's against the pair's norm.
func expansionFinding(
	sourceLocale string, m *Message, t Translation, locale string, th checkpolicy.LengthThresholds,
) (domain.Finding, bool) {
	if m.Model == nil || t.Model == nil {
		return domain.Finding{}, false
	}
	src, tgt := RenderedLength(m.Model), RenderedLength(t.Model)
	// A source too short to measure, or a message that is all
	// placeholders, gives a ratio that is a cliff rather than a
	// measurement. `max_length` is what bounds those.
	if src < th.MinSourceRunes || tgt == 0 {
		return domain.Finding{}, false
	}
	ratio := float64(tgt) / float64(src)
	norm := th.NormFor(sourceLocale, locale)
	var (
		code     string
		severity = domain.Warning
		text     string
	)
	switch {
	case ratio > norm.Excessive:
		code = CodeExpansionExcessive
		text = fmt.Sprintf("%.0f %% longer than the source; %s expands by %.0f–%.0f %% here",
			(ratio-1)*100, locale, (norm.Low-1)*100, (norm.High-1)*100)
	case ratio > norm.High:
		code = CodeExpansionSuspicious
		text = fmt.Sprintf("%.0f %% longer than the source; the norm for %s tops out at %.0f %%",
			(ratio-1)*100, locale, (norm.High-1)*100)
	case ratio < norm.Low:
		code = CodeExpansionSuspicious
		text = fmt.Sprintf("%.0f %% of the source's length; %s is not normally shorter than %.0f %%",
			ratio*100, locale, norm.Low*100)
	default:
		return domain.Finding{}, false
	}
	f := domain.Finding{
		Layer: domain.LayerLength, Code: code, Severity: severity,
		Locus:   locusOf(m, t, locale, nil),
		Message: text,
		Evidence: map[string]any{
			"ratio": round2(ratio), "length": tgt, "source_length": src,
			"norm_low": norm.Low, "norm_high": norm.High,
		},
		SourceRevision: sourceRevision(t),
	}
	if ratio > norm.High {
		to := int(math.Round(float64(src) * norm.High))
		f.Fix = &domain.Fix{Kind: domain.FixShorten, To: &to}
	}
	return domain.New(f), true
}

// layoutFinding predicts whether the translation fits the box a capture
// measured, from that box's own font metric and without a browser.
//
// The metric is the box itself. A region was measured while the page
// showed one locale's text, so its width over that text's length is the
// advance that font gave a character — a measurement, not an estimate.
// What the layer refuses to do is derive one from a box it cannot read
// that way: a box tall enough to have wrapped, a box that held three
// characters, a box measured in the very locale being checked (there
// the capture already *saw* whether it fits, and saying so is the
// visual layer's job, not a prediction).
//
// One finding per message and locale, about the region that overflows
// worst. Two regions showing the same string are two places one problem
// shows, and a fingerprint per region would move with the region ids a
// capture happens to hand out.
func layoutFinding(
	p *Project, m *Message, t Translation, locale string, th checkpolicy.LengthThresholds,
) (domain.Finding, bool) {
	if t.Model == nil {
		return domain.Finding{}, false
	}
	tgt := RenderedLength(t.Model)
	if tgt == 0 {
		return domain.Finding{}, false
	}
	var (
		worst    Region
		predict  float64
		measured float64
		count    int
	)
	for _, r := range p.RegionsFor(m.Key) {
		if !advanceUsable(r, locale) {
			continue
		}
		advance, ok := advanceOf(p, r, th)
		if !ok {
			continue
		}
		width := advance * float64(tgt)
		if width <= r.Width*(1+th.OverflowSlackPercent/100) {
			continue
		}
		count++
		if width-r.Width > predict-measured {
			worst, predict, measured = r, width, r.Width
		}
	}
	if count == 0 {
		return domain.Finding{}, false
	}
	locus := locusOf(m, t, locale, nil)
	locus.Capture, locus.Region = worst.Capture, worst.ID
	fits := int(math.Floor(measured / (predict / float64(tgt))))
	return domain.New(domain.Finding{
		Layer: domain.LayerLength, Code: CodeLayoutOverflowPredicted, Severity: domain.Warning,
		Locus: locus,
		Message: fmt.Sprintf("predicted %.0f px of text in a %.0f px box measured in %s",
			predict, measured, worst.Locale),
		Evidence: map[string]any{
			"predicted_width_px": round2(predict), "region_width_px": round2(measured),
			"measured_in": worst.Locale, "length": tgt, "regions": count,
		},
		Fix:            &domain.Fix{Kind: domain.FixShorten, To: &fits},
		SourceRevision: sourceRevision(t),
	}), true
}

// advanceOf is the CSS pixels per character that region r's font gave,
// and whether r can yield one at all.
func advanceOf(p *Project, r Region, th checkpolicy.LengthThresholds) (float64, bool) {
	if r.AdvancePerRunePx > 0 {
		return r.AdvancePerRunePx, true
	}
	switch {
	case r.Width <= 0:
		return 0, false
	case r.Height > th.SingleLineMaxHeightPx:
		// Tall enough to have wrapped: width over characters is not an
		// advance any more, and the layer would rather say nothing.
		return 0, false
	}
	sample, ok := textIn(p, r)
	if !ok || sample < th.MinRegionSampleRunes {
		return 0, false
	}
	return r.Width / float64(sample), true
}

// textIn is the rendered length of the text the region held when it was
// measured: the translation in the region's locale, or the source
// message where the region was captured in the source locale.
func textIn(p *Project, r Region) (int, bool) {
	if r.Locale == p.SourceLocale {
		m, ok := p.Message(r.Key)
		if !ok || m.Model == nil {
			return 0, false
		}
		return RenderedLength(m.Model), true
	}
	t, ok := p.Translations[r.Locale][r.Key]
	if !ok || t.Model == nil {
		return 0, false
	}
	return RenderedLength(t.Model), true
}

// advanceUsable reports whether r may be read against locale at all: a
// region measured in the locale being checked is not a prediction.
func advanceUsable(r Region, locale string) bool { return r.Locale != "" && r.Locale != locale }

// locusOf is the locus every length finding carries: the catalog
// message ID where the caller has one, because that is what the
// fingerprint is hashed over and a fingerprint over a key is not the
// one the other surfaces compute (RFC 0005 §2.1).
func locusOf(m *Message, t Translation, locale string, span *domain.Span) domain.Locus {
	l := domain.Locus{
		Key: t.Key, Locale: locale, Revision: t.Revision, File: t.File, Span: span,
	}
	if m != nil {
		l.Message, l.Namespace = m.ID, m.Namespace
	}
	return l
}

// fromKernel is a MessageFormat finding reported under layer, for the
// stored warnings a layer relays rather than recomputes.
func fromKernel(layer domain.Layer, f mf.Finding, locale string, m *Message, t Translation) domain.Finding {
	severity := domain.Warning
	if f.Severity == mf.SeverityError {
		severity = domain.Error
	}
	return domain.New(domain.Finding{
		Layer: layer, Code: string(f.Code), Severity: severity,
		Locus:   locusOf(m, t, locale, nil),
		Message: f.Message, Subject: f.Subject, Detail: f.Detail, SourceRevision: sourceRevision(t),
	})
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
