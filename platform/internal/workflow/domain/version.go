package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MaxActiveDefinitions bounds a tenant's definitions (RFC 0006 §9.6).
const MaxActiveDefinitions = 50

// ErrRenamed is a new version whose document names a different
// definition. A definition's name is its identity; a new name is a new
// definition.
var ErrRenamed = errors.New("workflow: a version must keep its definition's name")

// ErrSubjectChanged is a new version about a different subject: every
// binding and running instance of the definition assumes the subject it
// was made for.
var ErrSubjectChanged = errors.New("workflow: a version must keep its definition's subject")

// DefinitionRecord is a stored definition: its identity, its optional
// project scope and its latest version. The documents are its versions.
type DefinitionRecord struct {
	ID uuid.UUID
	// ProjectID scopes the definition to one project; uuid.Nil means
	// every project of the tenant may bind it.
	ProjectID uuid.UUID
	Name      string
	Subject   SubjectKind
	// Latest is the newest version's number.
	Latest    int
	CreatedBy string
	CreatedAt time.Time
	// DeletedAt is when the definition was deleted. Its versions stay,
	// so an instance started on one can still be explained; nothing new
	// binds or starts on it.
	DeletedAt *time.Time
}

// Version is one immutable version of a definition (RFC 0006 §2.3). A
// save creates version n+1; a running instance stays on the version it
// started with; nothing ever rewrites one.
type Version struct {
	ID           uuid.UUID
	DefinitionID uuid.UUID
	Number       int
	Schema       string
	Document     json.RawMessage
	CreatedBy    string
	CreatedAt    time.Time
}

// FirstVersion is the version a new definition starts with.
func FirstVersion(def DefinitionRecord, d *Definition, by string, at time.Time) Version {
	return Version{ID: uuid.New(), DefinitionID: def.ID, Number: 1, Schema: d.Schema,
		Document: d.Document, CreatedBy: by, CreatedAt: at}
}

// NextVersion is the version a save of d after def's latest creates. It
// refuses a document that renames the definition or changes its subject.
func NextVersion(def DefinitionRecord, d *Definition, by string, at time.Time) (Version, error) {
	if d.Name != def.Name {
		return Version{}, fmt.Errorf("%w: %q is %q", ErrRenamed, d.Name, def.Name)
	}
	if d.Subject != def.Subject {
		return Version{}, fmt.Errorf("%w: %s is %s", ErrSubjectChanged, d.Subject, def.Subject)
	}
	return Version{ID: uuid.New(), DefinitionID: def.ID, Number: def.Latest + 1, Schema: d.Schema,
		Document: d.Document, CreatedBy: by, CreatedAt: at}, nil
}

// Load compiles a stored version. A stored version compiled when it was
// saved, so an error here means the platform changed under it.
func (v Version) Load() (*Definition, error) {
	d, err := Compile(v.Document)
	if err != nil {
		return nil, fmt.Errorf("workflow: version %d of %s no longer loads: %w", v.Number, v.DefinitionID, err)
	}
	return d, nil
}
