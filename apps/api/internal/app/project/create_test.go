package projectapp_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	projectapp "go.klarlabs.de/glossa/apps/api/internal/app/project"
	"go.klarlabs.de/glossa/apps/api/internal/domain/apikey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
)

// stubLocaleRepo captures Save calls so the test can assert the
// default locale was seeded; nothing else is exercised here.
type stubLocaleRepo struct {
	saved []locale.Locale
}

func (r *stubLocaleRepo) Save(_ context.Context, l locale.Locale) error {
	r.saved = append(r.saved, l)
	return nil
}
func (r *stubLocaleRepo) ListForProject(context.Context, uuid.UUID) ([]locale.Locale, error) {
	return nil, nil
}
func (r *stubLocaleRepo) SetEnabled(context.Context, uuid.UUID, bool) error { return nil }
func (r *stubLocaleRepo) Delete(context.Context, uuid.UUID) error           { return nil }

// stubAPIKeyRepo captures the bootstrap key the use case mints when
// a new project is created.
type stubAPIKeyRepo struct {
	created []apikey.Key
}

func (r *stubAPIKeyRepo) Create(_ context.Context, projectID uuid.UUID, hash []byte, scope apikey.Scope, label string) (apikey.Key, error) {
	k := apikey.Key{
		ID:        uuid.New(),
		ProjectID: projectID,
		Scope:     scope,
		Label:     label,
	}
	_ = hash
	r.created = append(r.created, k)
	return k, nil
}
func (r *stubAPIKeyRepo) List(context.Context, uuid.UUID) ([]apikey.Key, error) {
	return nil, nil
}
func (r *stubAPIKeyRepo) ResolveByHash(context.Context, []byte) (apikey.Resolution, error) {
	return apikey.Resolution{}, apikey.ErrNotFound
}
func (r *stubAPIKeyRepo) Touch(context.Context, uuid.UUID) error  { return nil }
func (r *stubAPIKeyRepo) Revoke(context.Context, uuid.UUID) error { return nil }

// inMemoryRepo is a test-only adapter so use-case tests don't reach
// the DB. Lives in *_test.go per project convention.
type inMemoryRepo struct {
	mu       sync.Mutex
	projects map[uuid.UUID]project.Project
	saveOK   bool
}

func newInMemoryRepo() *inMemoryRepo {
	return &inMemoryRepo{projects: map[uuid.UUID]project.Project{}, saveOK: true}
}

func (r *inMemoryRepo) Save(_ context.Context, p project.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.saveOK {
		return errors.New("simulated repo failure")
	}
	r.projects[p.ID] = p
	return nil
}

func (r *inMemoryRepo) Find(_ context.Context, _ uuid.UUID, _ project.Slug) (project.Project, error) {
	return project.Project{}, errors.New("not used in these tests")
}

func (r *inMemoryRepo) ListForTenant(_ context.Context, _ uuid.UUID) ([]project.Project, error) {
	return nil, errors.New("not used in these tests")
}

func newCreate(repo project.Repository) (*projectapp.CreateProject, *stubAPIKeyRepo) {
	keys := &stubAPIKeyRepo{}
	return projectapp.NewCreateProject(repo, &stubLocaleRepo{}, keys, nil), keys
}

func TestCreateProject_MintsWriteScopeBootstrapKey(t *testing.T) {
	repo := newInMemoryRepo()
	uc, keys := newCreate(repo)

	out, err := uc.Execute(context.Background(), projectapp.CreateInput{
		TenantID:      uuid.New(),
		Slug:          "brotwerk-web",
		Name:          "Brotwerk Web",
		DefaultLocale: "de",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	if !strings.HasPrefix(out.APIKeyRaw, "glossa_") {
		t.Errorf("raw key should be prefixed with glossa_: %q", out.APIKeyRaw)
	}
	if len(out.APIKeyRaw) < 32 {
		t.Errorf("raw key too short: %d", len(out.APIKeyRaw))
	}
	if _, ok := repo.projects[out.Project.ID]; !ok {
		t.Fatal("expected project to be persisted")
	}
	if len(keys.created) != 1 {
		t.Fatalf("expected exactly one bootstrap key, got %d", len(keys.created))
	}
	if keys.created[0].Scope != apikey.ScopeWrite {
		t.Errorf("bootstrap key scope = %q, want write", keys.created[0].Scope)
	}
	if keys.created[0].Label == "" {
		t.Error("bootstrap key label should not be empty")
	}
}

func TestCreateProject_DefaultsLocaleToDe(t *testing.T) {
	repo := newInMemoryRepo()
	uc, _ := newCreate(repo)

	out, err := uc.Execute(context.Background(), projectapp.CreateInput{
		TenantID: uuid.New(),
		Slug:     "x",
		Name:     "X",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Project.DefaultLocale != "de" {
		t.Errorf("expected default locale 'de', got %q", out.Project.DefaultLocale)
	}
}

func TestCreateProject_SeedsDefaultLocaleRow(t *testing.T) {
	repo := newInMemoryRepo()
	locales := &stubLocaleRepo{}
	keys := &stubAPIKeyRepo{}
	uc := projectapp.NewCreateProject(repo, locales, keys, nil)

	out, err := uc.Execute(context.Background(), projectapp.CreateInput{
		TenantID:      uuid.New(),
		Slug:          "brotwerk-site",
		Name:          "Brotwerk",
		DefaultLocale: "en-US",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(locales.saved) != 1 {
		t.Fatalf("expected 1 locale seeded, got %d", len(locales.saved))
	}
	got := locales.saved[0]
	if got.ProjectID != out.Project.ID {
		t.Errorf("seeded locale.ProjectID = %s, want %s", got.ProjectID, out.Project.ID)
	}
	if got.Code.String() != "en-US" {
		t.Errorf("seeded locale.Code = %q, want en-US", got.Code)
	}
	if !got.Enabled {
		t.Error("seeded locale should be enabled by default")
	}
}

func TestCreateProject_RejectsBlankTenantID(t *testing.T) {
	repo := newInMemoryRepo()
	uc, _ := newCreate(repo)

	_, err := uc.Execute(context.Background(), projectapp.CreateInput{
		Slug: "x",
		Name: "X",
	})
	if !errors.Is(err, projectapp.ErrInvalidTenantID) {
		t.Fatalf("expected ErrInvalidTenantID, got %v", err)
	}
}

func TestCreateProject_RejectsInvalidSlug(t *testing.T) {
	repo := newInMemoryRepo()
	uc, _ := newCreate(repo)

	_, err := uc.Execute(context.Background(), projectapp.CreateInput{
		TenantID: uuid.New(),
		Slug:     "Invalid Slug!",
		Name:     "X",
	})
	if !errors.Is(err, project.ErrInvalidSlug) {
		t.Fatalf("expected ErrInvalidSlug, got %v", err)
	}
}

func TestCreateProject_PropagatesRepoFailure(t *testing.T) {
	repo := newInMemoryRepo()
	repo.saveOK = false
	uc, _ := newCreate(repo)

	_, err := uc.Execute(context.Background(), projectapp.CreateInput{
		TenantID: uuid.New(),
		Slug:     "x",
		Name:     "X",
	})
	if err == nil {
		t.Fatal("expected repo error to bubble")
	}
}

// The default locale is the project's source locale, so it gets the
// same BCP 47 canonicalization as every other locale. Before, an
// invalid default was stored as-is and the seed step silently
// skipped it, leaving a project whose source locale didn't exist.
func TestCreateProject_CanonicalizesDefaultLocale(t *testing.T) {
	repo := newInMemoryRepo()
	locales := &stubLocaleRepo{}
	uc := projectapp.NewCreateProject(repo, locales, &stubAPIKeyRepo{}, nil)

	out, err := uc.Execute(context.Background(), projectapp.CreateInput{
		TenantID:      uuid.New(),
		Slug:          "x",
		Name:          "X",
		DefaultLocale: "zh_hant_tw",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Project.DefaultLocale != "zh-Hant-TW" {
		t.Errorf("DefaultLocale = %q, want zh-Hant-TW", out.Project.DefaultLocale)
	}
	if len(locales.saved) != 1 || locales.saved[0].Code.String() != "zh-Hant-TW" {
		t.Errorf("seeded locales = %+v, want one zh-Hant-TW row", locales.saved)
	}
}

func TestCreateProject_RejectsInvalidDefaultLocale(t *testing.T) {
	repo := newInMemoryRepo()
	uc, _ := newCreate(repo)

	_, err := uc.Execute(context.Background(), projectapp.CreateInput{
		TenantID:      uuid.New(),
		Slug:          "x",
		Name:          "X",
		DefaultLocale: "not a locale",
	})
	if !errors.Is(err, locale.ErrInvalidCode) {
		t.Fatalf("expected locale.ErrInvalidCode, got %v", err)
	}
	if len(repo.projects) != 0 {
		t.Error("project was saved despite an invalid default locale")
	}
}
