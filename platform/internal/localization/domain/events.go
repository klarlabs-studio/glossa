package domain

// Localization's domain events (platform/README.md, "Domain events").
const (
	AggregateLocale        = "locale"
	AggregateFallbackGraph = "fallback_graph"
	AggregateTranslation   = "translation"

	EventLocaleAdded          = "localization.locale.added"
	EventLocaleRemoved        = "localization.locale.removed"
	EventFallbackGraphChanged = "localization.fallback_graph.changed"
	EventTranslationRevised   = "localization.translation.revised"
	EventTranslationReviewed  = "localization.translation.reviewed"
	// EventTranslationOutdated is published once per translation when a
	// source revision leaves it behind: the trigger for re-translation
	// (Intelligence, M2) and review routing.
	EventTranslationOutdated = "localization.translation.outdated"
)

// LocaleEvent is the payload of locale events.
type LocaleEvent struct {
	ProjectID string `json:"project_id"`
	Locale    string `json:"locale"`
	Direction string `json:"direction"`
	By        string `json:"by"`
}

// FallbackGraphChanged is the payload of fallback_graph.changed.
type FallbackGraphChanged struct {
	ProjectID string              `json:"project_id"`
	Fallback  map[string][]string `json:"fallback"`
	Version   int                 `json:"version"`
	By        string              `json:"by"`
}

// TranslationEvent is the payload of translation.revised and .reviewed.
type TranslationEvent struct {
	TranslationID  string `json:"translation_id"`
	ProjectID      string `json:"project_id"`
	MessageID      string `json:"message_id"`
	Locale         string `json:"locale"`
	Revision       int    `json:"revision"`
	SourceRevision int    `json:"source_revision"`
	State          string `json:"state"`
	Origin         string `json:"origin"`
	By             string `json:"by"`
	// SelfReview is set when the decision was the author's own on their
	// own text (no one else could review). It names no text.
	SelfReview bool `json:"self_review,omitempty"`
}

// TranslationEventOf builds the payload for t's latest revision.
func TranslationEventOf(t Translation, by string) TranslationEvent {
	return TranslationEvent{
		TranslationID: t.ID.String(), ProjectID: t.ProjectID.String(), MessageID: t.MessageID.String(),
		Locale: t.Locale.String(), Revision: t.Revision, SourceRevision: t.SourceRevision,
		State: string(t.State), Origin: string(t.Origin), By: by,
	}
}

// TranslationOutdated is the payload of translation.outdated.
type TranslationOutdated struct {
	TranslationID         string `json:"translation_id"`
	ProjectID             string `json:"project_id"`
	MessageID             string `json:"message_id"`
	Locale                string `json:"locale"`
	SourceRevision        int    `json:"source_revision"`
	CurrentSourceRevision int    `json:"current_source_revision"`
}
