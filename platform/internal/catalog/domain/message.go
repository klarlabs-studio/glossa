package domain

import (
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// MessageKey is the stable, human-chosen ID of a message within a
// project: a dotted path of [a-z0-9_-] segments ("checkout.payment.submit",
// runtimes/SPEC.md §1.2). Runtimes look messages up by it. It changes only
// through an explicit rename, never because the text changed (intent §8).
type MessageKey string

// MaxKeyLen bounds a message key.
const MaxKeyLen = 200

var keyPattern = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)*$`)

// ParseMessageKey validates s.
func ParseMessageKey(s string) (MessageKey, error) {
	if len(s) > MaxKeyLen || !keyPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidKey, s)
	}
	return MessageKey(s), nil
}

// Namespace groups messages into separately loadable bundles
// (runtimes/SPEC.md §1.1: one artifact per locale and namespace). Keys
// are unique per project, not per namespace, so moving a message between
// namespaces never changes what a key means.
type Namespace string

// DefaultNamespace always exists.
const DefaultNamespace Namespace = "default"

var namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ParseNamespace validates s; "" is the default namespace.
func ParseNamespace(s string) (Namespace, error) {
	if s == "" {
		return DefaultNamespace, nil
	}
	if !namespacePattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidNamespace, s)
	}
	return Namespace(s), nil
}

// MessageState says whether a message is still in use.
type MessageState string

// Message states. An obsolete message keeps its history and translations
// but is no longer released. A proposed message exists only on branches
// (RFC 0004 §4.1): it is translated like any other, but no release
// outside a branch's preview carries it until the default branch's push
// makes it active.
const (
	MessageActive   MessageState = "active"
	MessageProposed MessageState = "proposed"
	MessageObsolete MessageState = "obsolete"
)

// ParseMessageState validates s.
func ParseMessageState(s string) (MessageState, error) {
	switch MessageState(s) {
	case MessageActive, MessageProposed, MessageObsolete:
		return MessageState(s), nil
	}
	return "", fmt.Errorf("catalog: invalid message state %q", s)
}

// Author is who made a change: Identity's actor string, "person:<id>" or
// "token:<id>".
type Author string

// Details are a message's descriptive metadata. Changing them is not a
// source revision.
type Details struct {
	Namespace   Namespace
	Description string
	// MaxLength is an optional limit on the rendered length, in
	// characters, that translation QA checks.
	MaxLength *int
}

// Limits on details.
const (
	MaxDescriptionLen = 2000
	MaxMaxLength      = 100000
)

// Validate checks the details.
func (d Details) Validate() error {
	if _, err := ParseNamespace(string(d.Namespace)); err != nil || d.Namespace == "" {
		return fmt.Errorf("%w: %q", ErrInvalidNamespace, d.Namespace)
	}
	if utf8.RuneCountInString(d.Description) > MaxDescriptionLen {
		return ErrInvalidDescription
	}
	if d.MaxLength != nil && (*d.MaxLength < 1 || *d.MaxLength > MaxMaxLength) {
		return ErrInvalidMaxLength
	}
	return nil
}

func (d Details) equal(o Details) bool {
	sameMax := (d.MaxLength == nil) == (o.MaxLength == nil) &&
		(d.MaxLength == nil || *d.MaxLength == *o.MaxLength)
	return d.Namespace == o.Namespace && d.Description == o.Description && sameMax
}

// SourceRevision is one entry of a message's append-only source log.
// Revisions are numbered from 1 without gaps; the message's current
// source is always its highest revision.
type SourceRevision struct {
	MessageID MessageID
	Number    int
	Content   mfcontent.Content
	Author    Author
	CreatedAt time.Time
}

// Message is the aggregate root of the domain: a piece of product
// communication with a durable identity (ID, plus the key runtimes use),
// its source content in canonical form, and the log of how that content
// evolved. Translations belong to the Localization context and refer to
// the message by ID and to the source revision they were made against.
type Message struct {
	ID        MessageID
	ProjectID ProjectID
	Key       MessageKey
	Details
	State MessageState
	// Source is the current source content: revision Revision's.
	Source   mfcontent.Content
	Revision int
	// Version increments with every change of any kind; it is the
	// message's ETag and orders its events for projections.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewMessage creates a message with its first source revision.
func NewMessage(project ProjectID, key MessageKey, details Details, source mfcontent.Content, by Author, now time.Time) (Message, SourceRevision, error) {
	if err := details.Validate(); err != nil {
		return Message{}, SourceRevision{}, err
	}
	m := Message{
		ID: NewMessageID(), ProjectID: project, Key: key, Details: details, State: MessageActive,
		Source: source, Revision: 1, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	return m, SourceRevision{MessageID: m.ID, Number: 1, Content: source, Author: by, CreatedAt: now}, nil
}

// NewProposedMessage creates a message a branch proposes, with its first
// source revision.
func NewProposedMessage(project ProjectID, key MessageKey, details Details, source mfcontent.Content, by Author, now time.Time) (Message, SourceRevision, error) {
	m, rev, err := NewMessage(project, key, details, source, by, now)
	m.State = MessageProposed
	return m, rev, err
}

// Translatable reports whether translators (and the AI agent) work on
// the message: it is active or proposed.
func (m Message) Translatable() bool { return m.State != MessageObsolete }

// ReviseSource makes source the current content. Content that parses to
// the same model is no change, however it was written, so re-pushing a
// catalog never creates revisions (and never makes translations
// outdated). An obsolete message must be reactivated first.
func (m *Message) ReviseSource(source mfcontent.Content, by Author, now time.Time) (SourceRevision, bool, error) {
	if m.State == MessageObsolete {
		return SourceRevision{}, false, ErrMessageObsolete
	}
	if m.Source.SameModel(source) {
		return SourceRevision{}, false, nil
	}
	m.Source = source
	m.Revision++
	m.touch(now)
	return SourceRevision{MessageID: m.ID, Number: m.Revision, Content: source, Author: by, CreatedAt: now}, true, nil
}

// ChangeDetails replaces the descriptive metadata.
func (m *Message) ChangeDetails(d Details, now time.Time) (bool, error) {
	if err := d.Validate(); err != nil {
		return false, err
	}
	if m.Details.equal(d) {
		return false, nil
	}
	m.Details = d
	m.touch(now)
	return true, nil
}

// Obsolete retires the message; it reports false if it already was.
func (m *Message) Obsolete(now time.Time) bool {
	if m.State == MessageObsolete {
		return false
	}
	m.State = MessageObsolete
	m.touch(now)
	return true
}

// Reactivate brings an obsolete message back; false if it wasn't
// obsolete.
func (m *Message) Reactivate(now time.Time) bool {
	return m.move(MessageObsolete, MessageActive, now)
}

// Activate takes a proposed message live; false if it wasn't proposed.
func (m *Message) Activate(now time.Time) bool {
	return m.move(MessageProposed, MessageActive, now)
}

// Repropose makes an obsolete message proposed again, with its
// translations and history; false if it wasn't obsolete.
func (m *Message) Repropose(now time.Time) bool {
	return m.move(MessageObsolete, MessageProposed, now)
}

func (m *Message) move(from, to MessageState, now time.Time) bool {
	if m.State != from {
		return false
	}
	m.State = to
	m.touch(now)
	return true
}

// Rename gives the message a new key. Its ID, source revisions and
// translations stay; only the name runtimes use changes.
func (m *Message) Rename(key MessageKey, now time.Time) bool {
	if m.Key == key {
		return false
	}
	m.Key = key
	m.touch(now)
	return true
}

func (m *Message) touch(now time.Time) {
	m.Version++
	m.UpdatedAt = now
}
