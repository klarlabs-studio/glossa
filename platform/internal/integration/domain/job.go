// Package domain is the Integration context's model of import and
// export jobs (RFC 0003 §5–§6): what a job is and how it moves, the
// options each format takes, and the rules an import applies — how a
// file's review states are capped, when a merge reports a conflict
// instead of writing, how gettext messages get keys, what makes two
// imports the same. The converters themselves are the pure formats
// package; the use cases live in app.
package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Errors.
var (
	ErrInvalidFormat     = errors.New("integration: format must be xliff, json, po, tmx or tbx")
	ErrNotExportable     = errors.New("integration: this format is import only")
	ErrInvalidMode       = errors.New("integration: mode must be dry_run, merge or overwrite")
	ErrInvalidOptions    = errors.New("integration: invalid options")
	ErrProjectRequired   = errors.New("integration: catalog imports and exports belong to a project")
	ErrNotCancellable    = errors.New("integration: the job has finished")
	ErrUploadNotExpected = errors.New("integration: the job is not waiting for its file")
	ErrNotReady          = errors.New("integration: the export has not finished")
	ErrFileExpired       = errors.New("integration: the file was deleted after its retention period")
	// ErrLocaleNotInProject: an import's locale isn't one of the
	// project's.
	ErrLocaleNotInProject = errors.New("integration: the project has no such locale")
)

// Direction says whether a job reads a file into Glossa or writes one.
type Direction string

// Directions.
const (
	Import Direction = "import"
	Export Direction = "export"
)

// Kind is what a job moves.
type Kind string

// Kinds.
const (
	// KindCatalog: messages and their translations (XLIFF, JSON, PO).
	KindCatalog Kind = "catalog"
	// KindTM: translation memory (TMX).
	KindTM Kind = "tm"
	// KindTermbase: terminology (TBX).
	KindTermbase Kind = "termbase"
)

// Format is an interchange format.
type Format string

// Formats.
const (
	FormatXLIFF Format = "xliff"
	FormatJSON  Format = "json"
	FormatPO    Format = "po"
	FormatTMX   Format = "tmx"
	FormatTBX   Format = "tbx"
)

// ParseFormat validates s.
func ParseFormat(s string) (Format, error) {
	switch f := Format(s); f {
	case FormatXLIFF, FormatJSON, FormatPO, FormatTMX, FormatTBX:
		return f, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidFormat, s)
}

// Kind returns what a file in f holds.
func (f Format) Kind() Kind {
	switch f {
	case FormatTMX:
		return KindTM
	case FormatTBX:
		return KindTermbase
	}
	return KindCatalog
}

// Exportable reports whether Glossa writes f (gettext PO is read only).
func (f Format) Exportable() bool { return f != FormatPO }

// Extension is the file name extension of f.
func (f Format) Extension() string {
	switch f {
	case FormatXLIFF:
		return ".xlf"
	case FormatJSON:
		return ".json"
	case FormatPO:
		return ".po"
	case FormatTMX:
		return ".tmx"
	}
	return ".tbx"
}

// ContentType is the media type of a file in f.
func (f Format) ContentType() string {
	switch f {
	case FormatXLIFF:
		return "application/xliff+xml"
	case FormatJSON:
		return "application/json"
	case FormatPO:
		return "text/x-gettext-translation"
	case FormatTMX:
		return "application/x-tmx+xml"
	}
	return "application/x-tbx+xml"
}

// ContentTypeZip is the media type of an export of several files.
const ContentTypeZip = "application/zip"

// Mode is how an import treats what is already stored.
type Mode string

// Modes.
const (
	// ModeDryRun reports what an import would do and changes nothing.
	ModeDryRun Mode = "dry_run"
	// ModeMerge adds and updates, but never replaces an approved
	// translation, a message's source or a concept that differs from the
	// file: those are reported as conflicts.
	ModeMerge Mode = "merge"
	// ModeOverwrite makes the stored state the file's (it needs
	// integration.manage).
	ModeOverwrite Mode = "overwrite"
)

// ParseMode validates s; "" is merge.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case "":
		return ModeMerge, nil
	case ModeDryRun, ModeMerge, ModeOverwrite:
		return m, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidMode, s)
}

// State is where a job is.
type State string

// States.
const (
	// StateAwaitingUpload: an import created, its file not uploaded yet.
	StateAwaitingUpload State = "awaiting_upload"
	StateQueued         State = "queued"
	StateRunning        State = "running"
	StateSucceeded      State = "succeeded"
	StateFailed         State = "failed"
	StateCancelled      State = "cancelled"
)

// Terminal reports whether a job in s is done.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

// Failure codes of failed jobs.
const (
	// FailureInvalidFile: the file violates its format (see the job's
	// first result for the line and column).
	FailureInvalidFile = "invalid_file"
	// FailureUnsupported: the file uses a version or feature the reader
	// doesn't support (XLIFF 1.2, a DTD).
	FailureUnsupported = "unsupported_file"
	// FailureTooLarge: the file exceeds the reader's limits.
	FailureTooLarge = "file_too_large"
	// FailureSourceLocale: the file's source locale isn't the project's.
	FailureSourceLocale = "source_locale_mismatch"
	// FailureTargetLocale: the file's translations are in a locale the
	// project doesn't have (options.locale imports them as one it has).
	FailureTargetLocale = "target_locale_mismatch"
	// FailureProjectGone: the project was deleted.
	FailureProjectGone = "project_not_found"
	// FailureUploadExpired: no file was uploaded in time.
	FailureUploadExpired = "upload_expired"
	// FailureInternal: storage or the database kept failing.
	FailureInternal = "internal"
)

// File is a job's object in storage: an import's upload, an export's
// output.
type File struct {
	Key         string
	Size        int64
	SHA256      string
	ContentType string
}

// Job is one import or export.
type Job struct {
	ID        uuid.UUID
	Direction Direction
	// ProjectID is nil for a tenant-wide translation memory or termbase.
	ProjectID *uuid.UUID
	Kind      Kind
	Format    Format
	// Mode is set for imports only.
	Mode     Mode
	Options  Options
	Access   Access
	State    State
	FileName string
	File     *File
	// Fingerprint identifies an import's file and options; ReusedJobID
	// names the earlier job whose result an identical import reused.
	Fingerprint     string
	ReusedJobID     *uuid.UUID
	Summary         Summary
	TotalItems      int
	ProcessedItems  int
	FailureCode     string
	FailureMessage  string
	CancelRequested bool
	Attempts        int
	MaxAttempts     int
	AvailableAt     time.Time
	CreatedBy       string
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	UpdatedAt       time.Time
	// ExpiresAt is when retention deletes the job's file.
	ExpiresAt      time.Time
	FilesDeletedAt *time.Time
}

// MaxAttempts is how often a job is run before it fails for good.
const MaxAttempts = 3

// Backoff is the delay before attempt n+1 of a job that failed
// transiently: 30 s doubling, at most 10 minutes.
func Backoff(attempt int) time.Duration {
	d := 30 * time.Second
	for range max(attempt-1, 0) {
		d *= 2
		if d >= 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return d
}

// Cancel stops a job that hasn't finished: a queued or waiting job at
// once, a running one at its next batch (requested). Cancelling a
// cancelled job changes nothing.
func (j *Job) Cancel(now time.Time) (changed bool, err error) {
	switch j.State {
	case StateCancelled:
		return false, nil
	case StateAwaitingUpload, StateQueued:
		j.State, j.FinishedAt, j.UpdatedAt = StateCancelled, &now, now
		return true, nil
	case StateRunning:
		if j.CancelRequested {
			return false, nil
		}
		j.CancelRequested, j.UpdatedAt = true, now
		return true, nil
	}
	return false, ErrNotCancellable
}

// MaxFileNameRunes bounds a job's file name.
const MaxFileNameRunes = 255

// CleanFileName reduces a client's file name to a safe base name for
// display and Content-Disposition: no directories, no control or
// quoting characters, at most MaxFileNameRunes.
func CleanFileName(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || r == '"' || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > MaxFileNameRunes {
		out = string([]rune(out)[:MaxFileNameRunes])
	}
	return out
}
