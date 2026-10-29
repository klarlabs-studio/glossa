// Package projectapp wires project use cases. Application layer
// depends on the domain ports only; never reaches into infra or
// HTTP.
package projectapp

import (
	"context"
	"errors"

	"github.com/google/uuid"

	apikeyapp "go.klarlabs.de/glossa/apps/api/internal/app/apikey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
	"go.klarlabs.de/glossa/apps/api/internal/domain/apikey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
)

// ErrInvalidTenantID is the belt-and-suspender guard. Authenticated
// middleware should make this impossible, but use cases stay safe
// even when called from a broken handler.
var ErrInvalidTenantID = errors.New("projectapp: tenant_id required")

// CreateInput captures the project-create request shape. Slug/name
// come from the HTTP layer as raw strings; the use case turns them
// into validated VOs. DefaultLocale is a BCP-47 tag the admin UI
// surfaces as a dropdown — validation lives at the locale boundary
// where it belongs.
type CreateInput struct {
	TenantID      uuid.UUID
	Slug          string
	Name          string
	DefaultLocale string
}

// CreateOutput pairs the persisted Project with the freshly minted
// write-scope bootstrap key. The raw key is the only artifact
// returned in cleartext — only its SHA-256 hash hits the database.
// Callers MUST surface the raw key to the human exactly once and
// then discard it.
type CreateOutput struct {
	Project   project.Project
	APIKeyRaw string
}

// CreateProject is the use case that creates a new project under a
// tenant. Reusing IRI/Brotwerk's hex-arch shape: tiny struct, single
// Execute method, Repository injected at the boundary.
//
// A project ALWAYS gets its default locale's Locale row seeded as
// part of creation — otherwise the admin UI would render an empty
// editor until the user discovers the Locales tab. Failure to seed
// the locale is logged but does not roll the project back: a
// project without its default locale is still usable (the admin can
// add it later), and we'd rather not leak orphan-project failures
// behind a misleading 500.
type CreateProject struct {
	repo      project.Repository
	locales   locale.Repository
	keys      apikey.Repository
	analytics analytics.Recorder
}

// NewCreateProject wires the use case. analytics may be nil — emit
// calls are best-effort and gated on a non-nil recorder.
func NewCreateProject(repo project.Repository, locales locale.Repository, keys apikey.Repository, analytics analytics.Recorder) *CreateProject {
	return &CreateProject{repo: repo, locales: locales, keys: keys, analytics: analytics}
}

// Execute validates input, generates an API key, and persists the
// project. The raw API key is returned alongside the project; the
// hash is stored.
func (uc *CreateProject) Execute(ctx context.Context, in CreateInput) (CreateOutput, error) {
	if in.TenantID == uuid.Nil {
		return CreateOutput{}, ErrInvalidTenantID
	}
	slug, err := project.NewSlug(in.Slug)
	if err != nil {
		return CreateOutput{}, err
	}
	name, err := project.NewName(in.Name)
	if err != nil {
		return CreateOutput{}, err
	}
	rawLocale := in.DefaultLocale
	if rawLocale == "" {
		rawLocale = "de"
	}
	defaultCode, err := locale.NewCode(rawLocale)
	if err != nil {
		return CreateOutput{}, err
	}
	defaultLocale := defaultCode.String()

	p := project.Project{
		ID:            uuid.New(),
		TenantID:      in.TenantID,
		Slug:          slug,
		Name:          name,
		DefaultLocale: defaultLocale,
	}
	if err := uc.repo.Save(ctx, p); err != nil {
		return CreateOutput{}, err
	}

	// Mint a single write-scope bootstrap key labeled 'default' so the
	// reveal-once UX still works on create. Operators can issue
	// additional read-only or alternative-name keys after the fact
	// via the keys panel.
	raw, hash, err := apikeyapp.GenerateAPIKey()
	if err != nil {
		return CreateOutput{}, err
	}
	if _, err := uc.keys.Create(ctx, p.ID, hash, apikey.ScopeWrite, "default"); err != nil {
		return CreateOutput{}, err
	}

	// Seed the default-locale row so the editor has something to
	// render on first open. Errors here are silently swallowed —
	// see the type comment for why a failed seed isn't worth
	// rolling the project back.
	if uc.locales != nil {
		if label, lerr := locale.NewLabel(defaultLocale); lerr == nil {
			_ = uc.locales.Save(ctx, locale.Locale{
				ID:        uuid.New(),
				ProjectID: p.ID,
				Code:      defaultCode,
				Label:     label,
				Enabled:   true,
			})
		}
	}

	if uc.analytics != nil {
		pid := p.ID
		_ = uc.analytics.Record(ctx, analytics.Event{
			TenantID:  p.TenantID,
			ProjectID: &pid,
			Kind:      analytics.KindProjectCreated,
		})
	}

	return CreateOutput{Project: p, APIKeyRaw: raw}, nil
}
