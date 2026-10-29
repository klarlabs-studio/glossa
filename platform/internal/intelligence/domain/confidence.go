package domain

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/felixgeelhaar/decisionkit/risk"
)

// Origin says how a suggestion was made (intent §22); the values match
// Localization's revision origins.
type Origin string

// Origins of a suggestion.
const (
	OriginAI                Origin = "ai"
	OriginTranslationMemory Origin = "translation_memory"
)

// Signals are the inputs of the confidence score (RFC 0003 §3.3).
type Signals struct {
	Origin Origin
	// TMScore is the best translation-memory match (0 when none).
	TMScore       int
	TermMissing   int
	TermForbidden int
	// Repairs is how many structural repairs the draft needed (0–2).
	Repairs int
	// QAWarnings counts warning-severity structural findings.
	QAWarnings int
	// SelfAssessment is the model's own quality estimate in [0, 1], if
	// it was asked. It is weighted lightly.
	SelfAssessment *float64
	// FormalityOK is the assessment of the style guide's formality.
	FormalityOK *bool
	// SourceLength and TextLength are the literal text lengths in
	// characters; MaxLength is the message's limit, if any.
	SourceLength, TextLength int
	MaxLength                *int
	SourceLocale             string
	TargetLocale             string
	// Tags are the message's policy tags (legal, marketing).
	Tags []string
	// MarkupCount is the number of markup elements in the source.
	MarkupCount int
	// MissingPluralCategories are CLDR plural categories of the target
	// locale the translation still lacks after the allowed repairs.
	MissingPluralCategories []string
}

// Factor names one explained contribution to a score.
const (
	FactorOrigin         = "origin"
	FactorTMMatch        = "tm_match"
	FactorTermForbidden  = "term_forbidden"
	FactorTermMissing    = "term_missing"
	FactorRepairs        = "repairs"
	FactorQAWarnings     = "qa_warnings"
	FactorSelfAssessment = "self_assessment"
	FactorFormality      = "formality"
	FactorMaxLength      = "max_length"
	FactorLengthRatio    = "length_ratio"
	FactorRiskTag        = "risk_tag"
	FactorMarkup         = "markup_density"
	// FactorMissingPluralCategories: the translation lacks plural
	// categories the target locale needs; it always needs review.
	FactorMissingPluralCategories = "missing_plural_categories"
)

// Factor is one line of a score's explanation: the signal, its value and
// how much it moved the confidence (negative lowers it).
type Factor struct {
	Factor       string  `json:"factor"`
	Value        float64 `json:"value"`
	Contribution float64 `json:"contribution"`
	Reason       string  `json:"reason"`
}

// Confidence is a score in [0, 1] with its explanation. It prioritizes
// review; it is never a promise of correctness (intent §23).
type Confidence struct {
	Score       float64  `json:"score"`
	Explanation []Factor `json:"explanation"`
}

// Has reports whether the explanation names factor with a negative
// contribution.
func (c Confidence) Has(factor string) bool {
	return slices.ContainsFunc(c.Explanation, func(f Factor) bool {
		return f.Factor == factor && f.Contribution < 0
	})
}

// ScoringConfig holds the weights, in risk units: each signal adds risk,
// and confidence is 1 − clamped risk. The weights are configuration and
// are recorded implicitly in every explanation.
type ScoringConfig struct {
	OriginRisk map[Origin]float64
	// TMStrongMin and TMStrongWeight: an AI draft backed by a fuzzy match
	// at or above TMStrongMin lowers risk up to TMStrongWeight at 100.
	TMStrongMin    int
	TMStrongWeight float64
	// NoTMRisk is added to an AI draft with no TM support at all.
	NoTMRisk float64
	// TMExactBonus lowers the risk of TM reuse by score (100, 101).
	TMExactBonus         map[int]float64
	TermForbiddenEach    float64
	TermForbiddenCap     float64
	TermMissingEach      float64
	TermMissingCap       float64
	RepairEach           float64
	QAWarningEach        float64
	QAWarningCap         float64
	SelfAssessmentWeight float64
	FormalityRisk        float64
	MaxLengthRisk        float64
	// ExpansionFactors are typical text lengths relative to English, by
	// language; a length ratio far from the expected one adds risk.
	ExpansionFactors map[string]float64
	// LengthMinChars is the shortest source judged by length at all;
	// sources shorter than LengthMildMinChars (UI labels, whose length
	// ratio varies too much for one factor per language) are only
	// judged by the severe band.
	LengthMinChars     int
	LengthMildMinChars int
	LengthMildRisk     float64
	LengthSevereRisk   float64
	TagRisk            map[string]float64
	MarkupFreeCount    int
	MarkupEach         float64
	MarkupCap          float64
	MaxScore           float64
	// MissingPluralRisk is added when the translation lacks plural
	// categories of the target locale: enough to make it low confidence.
	MissingPluralRisk float64
	LengthMildBand    [2]float64
	LengthSevereBand  [2]float64
}

// DefaultScoring returns the M2 weights.
func DefaultScoring() ScoringConfig {
	return ScoringConfig{
		OriginRisk:           map[Origin]float64{OriginAI: 0.20, OriginTranslationMemory: 0.05},
		TMStrongMin:          75,
		TMStrongWeight:       0.12,
		NoTMRisk:             0.05,
		TMExactBonus:         map[int]float64{ScoreExact: 0.02, ScoreInContextExact: 0.04},
		TermForbiddenEach:    0.35,
		TermForbiddenCap:     0.70,
		TermMissingEach:      0.20,
		TermMissingCap:       0.40,
		RepairEach:           0.10,
		QAWarningEach:        0.04,
		QAWarningCap:         0.12,
		SelfAssessmentWeight: 0.15,
		FormalityRisk:        0.15,
		MaxLengthRisk:        0.40,
		ExpansionFactors: map[string]float64{
			"en": 1.0, "de": 1.3, "nl": 1.25, "fr": 1.2, "es": 1.25, "it": 1.2, "pt": 1.25,
			"pl": 1.2, "ru": 1.15, "sv": 1.1, "da": 1.1, "fi": 1.2, "tr": 1.15,
			"ja": 0.55, "zh": 0.5, "ko": 0.6,
		},
		LengthMinChars:     12,
		LengthMildMinChars: 40,
		LengthMildRisk:     0.07,
		LengthSevereRisk:   0.15,
		LengthMildBand:     [2]float64{0.67, 1.5},
		LengthSevereBand:   [2]float64{0.5, 2.0},
		TagRisk:            map[string]float64{TagLegal: 0.30, TagMarketing: 0.15},
		MarkupFreeCount:    2,
		MarkupEach:         0.02,
		MarkupCap:          0.10,
		MissingPluralRisk:  0.60,
		MaxScore:           0.99,
	}
}

// Score turns signals into a confidence with its explanation. It is pure
// and deterministic: the same signals always explain the same way.
func (c ScoringConfig) Score(s Signals) Confidence {
	a, values := c.assess(s)
	conf := Confidence{Score: round3(math.Min(1-a.Score, c.MaxScore)), Explanation: make([]Factor, 0, len(a.Factors))}
	for i, f := range a.Factors {
		conf.Explanation = append(conf.Explanation, Factor{
			Factor: f.Type, Value: values[i], Contribution: round3(-f.Weight), Reason: f.Reason,
		})
	}
	return conf
}

// assess builds a decisionkit risk assessment — the factors with their
// risk weights, the score clamped to [0, 1] — and each factor's value.
func (c ScoringConfig) assess(s Signals) (risk.RiskAssessment, []float64) {
	b := &riskBuilder{}
	b.add(FactorOrigin, 0, c.OriginRisk[s.Origin], fmt.Sprintf("%s suggestion", s.Origin))
	c.tm(b, s)
	if s.TermForbidden > 0 {
		b.add(FactorTermForbidden, float64(s.TermForbidden), math.Min(float64(s.TermForbidden)*c.TermForbiddenEach, c.TermForbiddenCap),
			"the translation uses a forbidden or deprecated term")
	}
	if s.TermMissing > 0 {
		b.add(FactorTermMissing, float64(s.TermMissing), math.Min(float64(s.TermMissing)*c.TermMissingEach, c.TermMissingCap),
			"a source term's preferred translation is missing")
	}
	if s.Repairs > 0 {
		b.add(FactorRepairs, float64(s.Repairs), float64(s.Repairs)*c.RepairEach, "the draft needed structural repairs")
	}
	if s.QAWarnings > 0 {
		b.add(FactorQAWarnings, float64(s.QAWarnings), math.Min(float64(s.QAWarnings)*c.QAWarningEach, c.QAWarningCap),
			"structural QA reported warnings")
	}
	if s.SelfAssessment != nil {
		v := math.Max(0, math.Min(1, *s.SelfAssessment))
		b.add(FactorSelfAssessment, v, (0.5-v)*c.SelfAssessmentWeight, "the model's self-assessment (weighted lightly)")
	}
	if s.FormalityOK != nil && !*s.FormalityOK {
		b.add(FactorFormality, 0, c.FormalityRisk, "the form of address does not follow the style guide")
	}
	c.length(b, s)
	if n := len(s.MissingPluralCategories); n > 0 {
		b.add(FactorMissingPluralCategories, float64(n), c.MissingPluralRisk,
			fmt.Sprintf("the translation lacks the %s plural categories %s", s.TargetLocale, strings.Join(s.MissingPluralCategories, ", ")))
	}
	for _, tag := range slices.Sorted(slices.Values(s.Tags)) {
		if w, ok := c.TagRisk[tag]; ok {
			b.add(FactorRiskTag, 0, w, fmt.Sprintf("the namespace is tagged %s", tag))
		}
	}
	if extra := s.MarkupCount - c.MarkupFreeCount; extra > 0 {
		b.add(FactorMarkup, float64(s.MarkupCount), math.Min(float64(extra)*c.MarkupEach, c.MarkupCap), "the message has dense markup")
	}
	return risk.RiskAssessment{Score: math.Max(0, math.Min(1, b.total)), Factors: b.factors}, b.values
}

func (c ScoringConfig) tm(b *riskBuilder, s Signals) {
	switch {
	case s.Origin == OriginTranslationMemory:
		if bonus := c.TMExactBonus[s.TMScore]; bonus > 0 {
			b.add(FactorTMMatch, float64(s.TMScore), -bonus, "reused an exact translation-memory match")
		}
	case s.TMScore >= c.TMStrongMin:
		share := float64(min(s.TMScore, ScoreExact)-c.TMStrongMin) / float64(ScoreExact-c.TMStrongMin)
		b.add(FactorTMMatch, float64(s.TMScore), -share*c.TMStrongWeight, "a strong translation-memory match supported the draft")
	case s.TMScore == 0:
		b.add(FactorTMMatch, 0, c.NoTMRisk, "no translation-memory support")
	}
}

func (c ScoringConfig) length(b *riskBuilder, s Signals) {
	if s.MaxLength != nil && s.TextLength > *s.MaxLength {
		b.add(FactorMaxLength, float64(s.TextLength), c.MaxLengthRisk, fmt.Sprintf("the text exceeds max_length %d", *s.MaxLength))
	}
	if s.SourceLength < c.LengthMinChars {
		return
	}
	src, tgt := c.ExpansionFactors[language(s.SourceLocale)], c.ExpansionFactors[language(s.TargetLocale)]
	if src == 0 || tgt == 0 {
		return
	}
	dev := (float64(s.TextLength) / float64(s.SourceLength)) / (tgt / src)
	switch {
	case dev < c.LengthSevereBand[0] || dev > c.LengthSevereBand[1]:
		b.add(FactorLengthRatio, round3(dev), c.LengthSevereRisk, "the length is far from the target locale's norm")
	case s.SourceLength < c.LengthMildMinChars:
	case dev < c.LengthMildBand[0] || dev > c.LengthMildBand[1]:
		b.add(FactorLengthRatio, round3(dev), c.LengthMildRisk, "the length is unusual for the target locale")
	}
}

type riskBuilder struct {
	factors []risk.RiskFactor
	values  []float64
	total   float64
}

func (b *riskBuilder) add(factor string, value, weight float64, reason string) {
	if weight == 0 {
		return
	}
	b.factors = append(b.factors, risk.RiskFactor{Type: factor, Weight: weight, Reason: reason})
	b.values = append(b.values, value)
	b.total += weight
}

func language(tag string) string {
	lang, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return lang
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
