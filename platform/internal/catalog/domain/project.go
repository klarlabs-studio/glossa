// Package domain is the Catalog context's model: what a product says
// (RFC 0002 §4). A Project owns Applications (its runtime surfaces) and
// Messages; the Message is the aggregate root of the whole domain
// (intent §7–8), with its source content as a canonical MessageFormat 2
// model and an append-only log of source revisions.
package domain

import (
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Slug is a project's or application's URL-safe handle, unique within
// its parent: a lowercase DNS label.
type Slug string

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ParseSlug validates s.
func ParseSlug(s string) (Slug, error) {
	if !slugPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidSlug, s)
	}
	return Slug(s), nil
}

// ParseName validates a display name.
func ParseName(s string) (string, error) {
	if n := utf8.RuneCountInString(s); n < 1 || n > 200 {
		return "", ErrInvalidName
	}
	return s, nil
}

// Settings are a project's tunables. Unset members take their defaults.
type Settings struct {
	// DefaultSyntax is the authoring syntax assumed when a write doesn't
	// name one. ICU MF1 unless the project says otherwise (RFC 0002 §5).
	DefaultSyntax mfcontent.Syntax `json:"default_syntax"`
	// ReviewRequired sends new translations to review instead of
	// approving them on write. It is the whole review policy until the
	// workflow engine (Phase 4) owns it.
	ReviewRequired bool `json:"review_required"`
	// DefaultBranch is the repository's default branch (RFC 0004 §2.2):
	// the Context context's builds of it are what every view of the
	// current usages falls back to. main unless the project says
	// otherwise; a Git connection will set it.
	DefaultBranch BranchName `json:"default_branch"`
	// CheckPolicy is what `glossa check` and the Glossa PR check decide
	// by (RFC 0004 §6.4): which locales must be complete, which
	// severity fails, and whether an untranslated key is an error. nil
	// is the documented default — every locale, errors fail — and is
	// what a project written before the setting existed reads as.
	CheckPolicy *checkpolicy.Policy `json:"check_policy,omitempty"`
}

// DefaultBranch is a new project's default branch.
const DefaultBranch BranchName = "main"

// DefaultSettings are a new project's settings.
func DefaultSettings() Settings {
	return Settings{DefaultSyntax: mfcontent.MF1, ReviewRequired: true, DefaultBranch: DefaultBranch}
}

// Policy is the project's check policy, the default when it stores
// none. The zero policy *is* that default, so there is one answer here,
// not two.
func (s Settings) Policy() checkpolicy.Policy {
	if s.CheckPolicy == nil {
		return checkpolicy.Policy{}
	}
	return *s.CheckPolicy
}

// Validate checks every member. known is the project's locales for the
// check policy's required ones; nil skips that check (see
// checkpolicy.Policy.Validate).
func (s Settings) Validate(known []string) (Settings, error) {
	_, err := mfcontent.ParseSyntax(string(s.DefaultSyntax), "")
	if err != nil || s.DefaultSyntax == "" {
		return s, fmt.Errorf("%w: default_syntax", mfcontent.ErrInvalidSyntax)
	}
	if _, err := ParseBranchName(string(s.DefaultBranch)); err != nil {
		return s, fmt.Errorf("default_branch: %w", err)
	}
	if s.CheckPolicy != nil {
		p, err := s.CheckPolicy.Validate(known)
		if err != nil {
			return s, err
		}
		s.CheckPolicy = &p
	}
	return s, nil
}

// Equal compares settings by value: CheckPolicy is a pointer, so ==
// would compare two equal policies as different.
func (s Settings) Equal(o Settings) bool {
	if s.DefaultSyntax != o.DefaultSyntax || s.ReviewRequired != o.ReviewRequired ||
		s.DefaultBranch != o.DefaultBranch {
		return false
	}
	if (s.CheckPolicy == nil) != (o.CheckPolicy == nil) {
		// One stores the default and the other does not. They decide the
		// same way today, but the project says different things.
		return false
	}
	return s.CheckPolicy == nil || s.CheckPolicy.Equal(*o.CheckPolicy)
}

// orCurrent fills the members a write left out with the project's:
// settings written without a default branch or check policy keep them.
func (s Settings) orCurrent(current Settings) Settings {
	if s.DefaultBranch == "" {
		s.DefaultBranch = current.DefaultBranch
	}
	switch {
	case s.CheckPolicy == nil:
		s.CheckPolicy = current.CheckPolicy
	case current.CheckPolicy != nil && s.CheckPolicy.BaseOnly():
		// A settings write that says only the three base fields is the
		// project-settings API, which is all it can say — the document's
		// rules, environments, version and history are the policy API's
		// (RFC 0005 §13 wave 3). Taking such a write at face value would
		// drop them, so it changes the base and the document keeps the
		// rest. A policy nobody meant to touch is not a policy anybody
		// deleted. A write that does carry a document replaces one.
		kept := current.CheckPolicy.WithBase(*s.CheckPolicy)
		s.CheckPolicy = &kept
	}
	return s
}

// Project is a product's localizable surface as a whole: its source
// locale, its applications and its messages. The source locale is fixed
// at creation — every source revision and translation is relative to it.
type Project struct {
	ID           ProjectID
	TenantID     tenancy.ID
	Slug         Slug
	Name         string
	SourceLocale bcp47.Tag
	Settings     Settings
	// Version increments with every change; it is the project's ETag.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewProject creates a project. A check policy set at creation is
// checked for shape only: the project has no locales yet, so there is
// nothing to require a locale against. The first update validates it
// against them, and the check itself reports a locale the project does
// not have as missing-locale.
func NewProject(tenant tenancy.ID, slug Slug, name string, source bcp47.Tag, settings Settings, now time.Time) (Project, error) {
	if _, err := ParseName(name); err != nil {
		return Project{}, err
	}
	if source.IsZero() {
		return Project{}, bcp47.ErrInvalid
	}
	settings = settings.orCurrent(Settings{DefaultBranch: DefaultBranch})
	settings, err := settings.Validate(nil)
	if err != nil {
		return Project{}, err
	}
	return Project{
		ID: NewProjectID(), TenantID: tenant, Slug: slug, Name: name, SourceLocale: source,
		Settings: settings, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ProjectChange is a partial update; nil members keep their value.
type ProjectChange struct {
	Slug     *Slug
	Name     *string
	Settings *Settings
}

// Change applies c and reports whether anything changed. known is the
// project's locales, which a check policy's required ones must be
// among; nil skips that check.
func (p *Project) Change(c ProjectChange, known []string, now time.Time) (bool, error) {
	next := *p
	if c.Slug != nil {
		next.Slug = *c.Slug
	}
	if c.Name != nil {
		if _, err := ParseName(*c.Name); err != nil {
			return false, err
		}
		next.Name = *c.Name
	}
	if c.Settings != nil {
		settings, err := c.Settings.orCurrent(p.Settings).Validate(known)
		if err != nil {
			return false, err
		}
		next.Settings = settings
	}
	if next.Slug == p.Slug && next.Name == p.Name && next.Settings.Equal(p.Settings) {
		return false, nil
	}
	next.Version++
	next.UpdatedAt = now
	*p = next
	return true, nil
}

// Platform is the kind of runtime surface an application is.
type Platform string

// Platforms.
const (
	PlatformWeb     Platform = "web"
	PlatformAPI     Platform = "api"
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformOther   Platform = "other"
)

// ParsePlatform validates p.
func ParsePlatform(p string) (Platform, error) {
	switch Platform(p) {
	case PlatformWeb, PlatformAPI, PlatformIOS, PlatformAndroid, PlatformOther:
		return Platform(p), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidPlatform, p)
}

// Application is a runtime surface of a project — a web app, an API's
// emails, an iOS app (RFC 0002 §6). Delivery keys, usage context and
// per-application releases will hang off it.
type Application struct {
	ID        ApplicationID
	ProjectID ProjectID
	Slug      Slug
	Name      string
	Platform  Platform
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewApplication creates an application of project.
func NewApplication(project ProjectID, slug Slug, name string, platform Platform, now time.Time) (Application, error) {
	if _, err := ParseName(name); err != nil {
		return Application{}, err
	}
	if _, err := ParsePlatform(string(platform)); err != nil {
		return Application{}, err
	}
	return Application{
		ID: NewApplicationID(), ProjectID: project, Slug: slug, Name: name, Platform: platform,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ApplicationChange is a partial update; nil members keep their value.
type ApplicationChange struct {
	Slug     *Slug
	Name     *string
	Platform *Platform
}

// Change applies c and reports whether anything changed.
func (a *Application) Change(c ApplicationChange, now time.Time) (bool, error) {
	next := *a
	if c.Slug != nil {
		next.Slug = *c.Slug
	}
	if c.Name != nil {
		if _, err := ParseName(*c.Name); err != nil {
			return false, err
		}
		next.Name = *c.Name
	}
	if c.Platform != nil {
		if _, err := ParsePlatform(string(*c.Platform)); err != nil {
			return false, err
		}
		next.Platform = *c.Platform
	}
	if next == *a {
		return false, nil
	}
	next.Version++
	next.UpdatedAt = now
	*a = next
	return true, nil
}
