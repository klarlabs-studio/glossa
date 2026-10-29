package domain

import "github.com/google/uuid"

// Workflow's domain events (platform/README.md, "Domain events"). Their
// names and payloads are a published contract: a breaking change is a
// new type, never an edit. Each is published in the transaction that
// made the change and names its actor (RFC 0006 §6.1), so the audit
// projection can say who changed how an organisation works.
const (
	AggregateDefinition = "workflow_definition"
	AggregateBinding    = "workflow_binding"

	// EventDefinitionSaved: a definition version was stored — version 1
	// of a new definition, or the next version of an existing one.
	EventDefinitionSaved = "workflow.definition_saved"
	// EventDefinitionDeleted: a definition was deleted. Its versions
	// stay; its bindings went with it (BindingsRemoved says how many).
	EventDefinitionDeleted = "workflow.definition_deleted"
	// EventBindingChanged: a binding was created or removed, so what
	// runs for some project's subjects changed.
	EventBindingChanged = "workflow.binding_changed"
	// EventInstanceRebased: a running instance was moved to a newer
	// version of its definition, in the state it was in (RFC 0006 §2.3).
	EventInstanceRebased = "workflow.instance.rebased"
)

// InstanceRebased is the payload of workflow.instance.rebased: which
// instance, from which version to which, and the state it kept. A state
// is a name in the definition, never text anyone wrote about a subject.
type InstanceRebased struct {
	InstanceID   string `json:"instance_id"`
	ProjectID    string `json:"project_id"`
	DefinitionID string `json:"definition_id"`
	SubjectKind  string `json:"subject_kind"`
	// Locale is a translation unit's; empty for a release request.
	Locale      string `json:"locale,omitempty"`
	FromVersion int    `json:"from_version"`
	ToVersion   int    `json:"to_version"`
	// State is the state the instance was in and stays in; empty for an
	// instance that had not started.
	State string `json:"state"`
}

// InstanceRebasedOf builds the payload for from, rebased to version.
func InstanceRebasedOf(from Instance, version int) InstanceRebased {
	return InstanceRebased{InstanceID: from.ID.String(), ProjectID: from.Project.String(),
		DefinitionID: from.Definition.String(), SubjectKind: string(from.Kind), Locale: from.Locale,
		FromVersion: from.Version, ToVersion: version, State: from.State}
}

// Binding changes.
const (
	BindingCreated = "created"
	BindingDeleted = "deleted"
)

// DefinitionSaved is the payload of workflow.definition_saved. It
// carries no document: a subscriber that needs it reads the version,
// which is immutable.
type DefinitionSaved struct {
	DefinitionID string `json:"definition_id"`
	// ProjectID is set for a project-scoped definition.
	ProjectID string `json:"project_id,omitempty"`
	Name      string `json:"name"`
	Subject   string `json:"subject"`
	Version   int    `json:"version"`
}

// DefinitionSavedOf builds the payload for version v of def.
func DefinitionSavedOf(def DefinitionRecord, v Version) DefinitionSaved {
	return DefinitionSaved{DefinitionID: def.ID.String(), ProjectID: optionalID(def.ProjectID),
		Name: def.Name, Subject: string(def.Subject), Version: v.Number}
}

// DefinitionDeleted is the payload of workflow.definition_deleted.
type DefinitionDeleted struct {
	DefinitionID string `json:"definition_id"`
	ProjectID    string `json:"project_id,omitempty"`
	Name         string `json:"name"`
	// Latest is the version that was current when it was deleted.
	Latest int `json:"latest"`
	// BindingsRemoved is how many bindings went with it: from now on
	// those projects' subjects run under no workflow, or a less
	// specific binding.
	BindingsRemoved int `json:"bindings_removed"`
}

// DefinitionDeletedOf builds the payload for def, whose deletion removed
// bindings bindings.
func DefinitionDeletedOf(def DefinitionRecord, bindings int) DefinitionDeleted {
	return DefinitionDeleted{DefinitionID: def.ID.String(), ProjectID: optionalID(def.ProjectID),
		Name: def.Name, Latest: def.Latest, BindingsRemoved: bindings}
}

// BindingChanged is the payload of workflow.binding_changed.
type BindingChanged struct {
	BindingID    string   `json:"binding_id"`
	ProjectID    string   `json:"project_id"`
	DefinitionID string   `json:"definition_id"`
	Subject      string   `json:"subject"`
	Locales      []string `json:"locales"`
	Namespace    string   `json:"namespace,omitempty"`
	// Change is BindingCreated or BindingDeleted.
	Change string `json:"change"`
}

// BindingChangedOf builds the payload for a change to b.
func BindingChangedOf(b Binding, change string) BindingChanged {
	locales := b.Locales
	if locales == nil {
		locales = []string{}
	}
	return BindingChanged{BindingID: b.ID.String(), ProjectID: b.ProjectID.String(),
		DefinitionID: b.DefinitionID.String(), Subject: string(b.Subject), Locales: locales,
		Namespace: b.Namespace, Change: change}
}

func optionalID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
