package httpgin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
)

// ctxWithSlug builds a request context carrying an API-key-authenticated
// project plus a :slug path parameter, which is what every /projects/:slug
// route looks like once apiKeyAuth has run.
func ctxWithSlug(t *testing.T, keyProject string, urlSlug string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "slug", Value: urlSlug}}
	c.Set(ctxKeyProject, project.Project{
		ID:   uuid.New(),
		Slug: project.Slug(keyProject),
	})

	return c
}

// The API-key path resolves the project from the key and never consults the
// URL. That is correct as far as it goes — the key is the authority — but
// nothing rejected a request whose slug named a DIFFERENT project, so the
// server answered 200 with data the URL did not ask for.
//
// It is not a privilege escalation; a key still only reaches its own project.
// It is worse in a quieter way: the slug becomes decorative, so a client
// configured against the wrong project cannot tell. A drift checker pointed at
// "pet-medical-app" while holding a marketing-site key compared two unrelated
// key sets and reported everything fine.
func TestResolveProject_RejectsSlugThatIsNotTheKeysProject(t *testing.T) {
	c := ctxWithSlug(t, "pet-medical-app", "some-other-project")

	if _, err := resolveProject(c, nil); !errors.Is(err, errSlugMismatch) {
		t.Fatalf("expected errSlugMismatch for a slug naming another project, got %v", err)
	}
}

func TestResolveProject_RejectsSlugThatExistsNowhere(t *testing.T) {
	// The shape that proved the bug in the wild: a slug that is not a project
	// at all still returned a full bundle.
	c := ctxWithSlug(t, "pet-medical-app", "x")

	if _, err := resolveProject(c, nil); !errors.Is(err, errSlugMismatch) {
		t.Fatalf("expected errSlugMismatch for a nonexistent slug, got %v", err)
	}
}

func TestResolveProject_AllowsMatchingSlug(t *testing.T) {
	c := ctxWithSlug(t, "pet-medical-app", "pet-medical-app")

	p, err := resolveProject(c, nil)
	if err != nil {
		t.Fatalf("matching slug rejected: %v", err)
	}
	if p.Slug.String() != "pet-medical-app" {
		t.Fatalf("slug = %q, want pet-medical-app", p.Slug.String())
	}
}

// Not every API-key route carries a :slug. Those must keep working — the check
// is "the URL must not contradict the key", not "the URL must be present".
func TestResolveProject_AllowsAbsentSlug(t *testing.T) {
	c := ctxWithSlug(t, "pet-medical-app", "")

	if _, err := resolveProject(c, nil); err != nil {
		t.Fatalf("absent slug rejected: %v", err)
	}
}
