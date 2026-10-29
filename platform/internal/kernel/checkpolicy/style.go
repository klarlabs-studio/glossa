package checkpolicy

// The style layer's data (RFC 0005 §3.2), in one place the policy owns.
//
// The style layer grades the **mechanical** fields of the effective
// style guide and nothing else. The guide's prose rules — a rationale
// and some good and bad examples — are prompt material for the
// translation agent and evidence for the linguistic layer, and RFC 0005
// §3.2 says plainly why they are not graded here: "a regex over a
// rationale would be a lie about what the system knows."
//
// What is here is the one thing a mechanical field cannot carry on its
// own. A guide that says `formality: formal` for German has said which
// pronouns it means, but it has not written them down, and the layer
// needs the set. FormalityForms is that set, per language, curated and
// visible — and, like the locale layer's spacing table, a table with
// holes: a language it does not name gets no `formality-mismatch`,
// because Glossa does not know that language's forms of address and
// will not guess at them.

// FormalitySet is one language's forms of address.
type FormalitySet struct {
	// Formal and Informal are the words that mark each register:
	// pronouns and the possessives that go with them.
	Formal   []string
	Informal []string
	// CaseSensitive says whether the words are matched as written.
	// German needs it — capitalized `Sie` is the formal pronoun and
	// lowercase `sie` is "she" and "they" — and no other language here
	// does.
	CaseSensitive bool
}

// StyleConventions are the curated data the style layer needs.
type StyleConventions struct {
	// FormalityForms are the forms of address, by language subtag.
	FormalityForms map[string]FormalitySet
	// Units are the unit symbols `typography-mismatch` checks the space
	// before, when the guide states one.
	Units []string
}

// DefaultStyleConventions are the forms Glossa ships knowing.
func DefaultStyleConventions() StyleConventions {
	return StyleConventions{
		FormalityForms: map[string]FormalitySet{
			"de": {
				Formal:   []string{"Sie", "Ihnen", "Ihr", "Ihre", "Ihrem", "Ihren", "Ihrer", "Ihres"},
				Informal: []string{"du", "dich", "dir", "dein", "deine", "deinem", "deinen", "deiner", "deines"},
				// `Sie` is the pronoun; `sie` is somebody else entirely.
				CaseSensitive: true,
			},
			"fr": {
				Formal:   []string{"vous", "votre", "vos"},
				Informal: []string{"tu", "te", "toi", "ton", "ta", "tes"},
			},
			"es": {
				Formal:   []string{"usted", "ustedes", "su", "sus"},
				Informal: []string{"tú", "te", "ti", "tu", "tus", "vosotros"},
			},
			"it": {
				Formal:   []string{"Lei", "Suo", "Sua", "Suoi", "Sue"},
				Informal: []string{"tu", "te", "ti", "tuo", "tua", "tuoi", "tue"},
				// Same reason as German: `lei` is "she".
				CaseSensitive: true,
			},
			"nl": {
				Formal:   []string{"u", "uw"},
				Informal: []string{"je", "jij", "jou", "jouw", "jullie"},
			},
			"pt": {
				Formal:   []string{"você", "vocês", "seu", "sua", "seus", "suas"},
				Informal: []string{"tu", "te", "ti", "teu", "tua", "teus", "tuas"},
			},
			"ru": {
				Formal:        []string{"Вы", "Вас", "Вам", "Ваш", "Ваша", "Ваши"},
				Informal:      []string{"ты", "тебя", "тебе", "твой", "твоя", "твои"},
				CaseSensitive: true,
			},
		},
		Units: []string{"%", "‰", "°", "°C", "°F", "kg", "g", "km", "m", "cm", "mm", "MB", "GB", "TB", "kB"},
	}
}

// Style is the conventions this policy's style layer reads.
func (p Policy) Style() StyleConventions { return DefaultStyleConventions() }

// FormsFor is a language's forms of address, and whether Glossa has
// them. The tag is reduced to its language subtag.
func (c StyleConventions) FormsFor(locale string) (FormalitySet, bool) {
	f, ok := c.FormalityForms[primary(locale)]
	return f, ok
}

// Formality values, shared with the effective style guide.
const (
	FormalityFormal   = "formal"
	FormalityInformal = "informal"
)

// Wanted and Unwanted are the forms a guide's formality asks for and
// the ones it rules out. A guide that names neither formality gets
// neither list, and the layer has nothing to say.
func (f FormalitySet) Wanted(formality string) []string {
	switch formality {
	case FormalityFormal:
		return f.Formal
	case FormalityInformal:
		return f.Informal
	}
	return nil
}

// Unwanted is the opposite register's forms.
func (f FormalitySet) Unwanted(formality string) []string {
	switch formality {
	case FormalityFormal:
		return f.Informal
	case FormalityInformal:
		return f.Formal
	}
	return nil
}
