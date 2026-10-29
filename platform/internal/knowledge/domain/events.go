// Package domain is the Knowledge context's model (RFC 0003 §2): four
// separate kinds of linguistic knowledge, never one "AI context" blob
// (intent §19) — translation memory derived from approved translations,
// a concept-oriented termbase with recognition and terminology QA, and
// structured, scoped, versioned style guides. Product knowledge is
// Phase 2. Nothing here does I/O.
package domain

// Knowledge's domain events (platform/README.md, "Domain events"). TM
// units are derived state and publish none.
const (
	AggregateConcept    = "concept"
	AggregateStyleGuide = "style_guide"

	EventConceptCreated    = "knowledge.concept.created"
	EventConceptUpdated    = "knowledge.concept.updated"
	EventConceptDeleted    = "knowledge.concept.deleted"
	EventStyleGuideCreated = "knowledge.style_guide.created"
	EventStyleGuideUpdated = "knowledge.style_guide.updated"
	EventStyleGuideDeleted = "knowledge.style_guide.deleted"
)

// ConceptEvent is the payload of concept events.
type ConceptEvent struct {
	ConceptID string `json:"concept_id"`
	// ProjectID is empty for a tenant-wide concept.
	ProjectID string `json:"project_id,omitempty"`
	Version   int    `json:"version"`
	By        string `json:"by"`
}

// ConceptEventOf builds the payload for c.
func ConceptEventOf(c Concept, by string) ConceptEvent {
	e := ConceptEvent{ConceptID: c.ID.String(), Version: c.Version, By: by}
	if c.ProjectID != nil {
		e.ProjectID = c.ProjectID.String()
	}
	return e
}

// StyleGuideEvent is the payload of style guide events.
type StyleGuideEvent struct {
	StyleGuideID string `json:"style_guide_id"`
	ProjectID    string `json:"project_id,omitempty"`
	Locale       string `json:"locale,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Version      int    `json:"version"`
	By           string `json:"by"`
}

// StyleGuideEventOf builds the payload for g.
func StyleGuideEventOf(g StyleGuide, by string) StyleGuideEvent {
	e := StyleGuideEvent{StyleGuideID: g.ID.String(), Namespace: g.Scope.Namespace, Version: g.Version, By: by}
	if g.Scope.ProjectID != nil {
		e.ProjectID = g.Scope.ProjectID.String()
	}
	if g.Scope.Locale != nil {
		e.Locale = g.Scope.Locale.String()
	}
	return e
}
