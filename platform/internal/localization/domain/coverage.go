package domain

// StateCounts counts a locale's translations of active messages per
// review state.
type StateCounts struct {
	Draft, NeedsReview, Approved, Rejected int
}

// Coverage is how far one locale of a project is translated, counted
// over the project's active messages. A rejected translation is not
// usable, so its message counts as missing; Outdated counts usable
// translations made against an older source revision.
type Coverage struct {
	Messages   int
	Translated int
	Missing    int
	Outdated   int
	States     StateCounts
}

// LocaleCoverage derives a target locale's coverage from its counts.
func LocaleCoverage(messages int, states StateCounts, outdated int) Coverage {
	translated := states.Draft + states.NeedsReview + states.Approved
	return Coverage{
		Messages: messages, Translated: translated, Missing: max(messages-translated, 0),
		Outdated: outdated, States: states,
	}
}

// SourceCoverage is the source locale's: its text is the source itself,
// so every active message is translated and approved.
func SourceCoverage(messages int) Coverage {
	return Coverage{Messages: messages, Translated: messages, States: StateCounts{Approved: messages}}
}
