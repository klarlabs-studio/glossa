package prompts

// The answer contracts of the prompts, as JSON Schemas in the subset every
// provider's structured output accepts (all properties required, no
// additional properties, no numeric bounds; string enums where a field is
// a closed vocabulary, which every provider's structured output honours).

// DraftAnswer is the translate and repair answer.
type DraftAnswer struct {
	Message string `json:"message"`
	Notes   string `json:"notes"`
}

// DraftSchema constrains DraftAnswer.
func DraftSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{"type": "string", "description": "The translation in MessageFormat 2 syntax."},
			"notes":   map[string]any{"type": "string", "description": "One short sentence about an ambiguity, or empty."},
		},
		"required":             []any{"message", "notes"},
		"additionalProperties": false,
	}
}

// AssessAnswer is the assess answer.
type AssessAnswer struct {
	Score       float64  `json:"score"`
	FormalityOK bool     `json:"formality_ok"`
	Issues      []string `json:"issues"`
}

// AssessSchema constrains AssessAnswer.
func AssessSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"score":        map[string]any{"type": "number", "description": "Probability in [0, 1] that a reviewer approves without edits."},
			"formality_ok": map[string]any{"type": "boolean"},
			"issues":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required":             []any{"score", "formality_ok", "issues"},
		"additionalProperties": false,
	}
}

// ReviewNote is one note of the linguistic answer (RFC 0005 §3.8):
// (code, span, explanation, optional suggestion). The span is a verbatim
// quote rather than a pair of offsets, because a model counts bytes badly
// and quotes exactly — and because a quote can be *verified* against the
// text it claims to point into, which a pair of offsets cannot. The
// caller resolves it to offsets and rejects a quote that is not there.
type ReviewNote struct {
	Code        string `json:"code"`
	Side        string `json:"side"`
	Quote       string `json:"quote"`
	Explanation string `json:"explanation"`
	Suggestion  string `json:"suggestion"`
}

// LinguisticAnswer is the linguistic review answer.
type LinguisticAnswer struct {
	Findings []ReviewNote `json:"findings"`
}

// LinguisticSchema constrains LinguisticAnswer to the given codes. The
// codes are the caller's — the Quality context owns the layer's
// vocabulary — so the schema, the prompt and the parser are handed one
// list and cannot disagree about it.
func LinguisticSchema(codes []string) map[string]any {
	enum := make([]any, len(codes))
	for i, c := range codes {
		enum[i] = c
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"findings": map[string]any{
				"type":        "array",
				"description": "The suspected problems, most important first; empty when the translation is sound.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code": map[string]any{"type": "string", "enum": enum},
						"side": map[string]any{"type": "string", "enum": []any{"source", "target"}, "description": "Which text the quote is from."},
						"quote": map[string]any{
							"type":        "string",
							"description": "The exact substring of that side's text the finding is about, copied character for character.",
						},
						"explanation": map[string]any{"type": "string", "description": "One sentence, in English, naming what is wrong."},
						"suggestion":  map[string]any{"type": "string", "description": "The text to use instead, or an empty string."},
					},
					"required":             []any{"code", "side", "quote", "explanation", "suggestion"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"findings"},
		"additionalProperties": false,
	}
}
