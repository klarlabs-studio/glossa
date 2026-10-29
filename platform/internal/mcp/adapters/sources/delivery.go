package sources

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	release "go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Delivery adapts Release's application service to tools.Delivery.
type Delivery struct{ release *releaseapp.Service }

// NewDelivery returns the adapter.
func NewDelivery(s *releaseapp.Service) *Delivery { return &Delivery{release: s} }

var _ tools.Delivery = (*Delivery)(nil)

// releaseNotFound lists Release's own not-found errors.
var releaseNotFound = []error{releaseapp.ErrNotFound, releaseapp.ErrReleaseNotInProject}

// Served implements tools.Delivery.
func (a *Delivery) Served(
	ctx context.Context, project uuid.UUID, environment string, id uuid.UUID,
) (tools.Served, error) {
	if id == uuid.Nil {
		env, err := a.release.GetEnvironment(ctx, project, environment)
		if err != nil {
			return tools.Served{}, notFound(err, releaseNotFound...)
		}
		if env.Current == uuid.Nil {
			// An environment that has never published serves nothing, and
			// "nothing" is not a resolution to explain.
			return tools.Served{}, tools.ErrNotFound
		}
		id = env.Current
	}
	rel, err := a.release.GetRelease(ctx, project, id)
	if err != nil {
		return tools.Served{}, notFound(err, releaseNotFound...)
	}
	return servedOf(rel, environment), nil
}

func servedOf(rel release.Release, environment string) tools.Served {
	out := tools.Served{
		ReleaseID: rel.ID.String(), Version: rel.Version, Digest: rel.Digest,
		CreatedAt: rel.CreatedAt.UTC().Format(time.RFC3339), Environment: environment,
		SourceLocale: rel.Content.SourceLocale, Fallback: rel.Content.Fallback,
		Artifacts: map[string]map[string]string{},
	}
	for _, l := range rel.Content.Locales {
		out.Locales = append(out.Locales, l.Code)
	}
	for locale, namespaces := range rel.Content.Artifacts {
		refs := make(map[string]string, len(namespaces))
		for ns, ref := range namespaces {
			refs[ns] = ref.SHA256
		}
		out.Artifacts[locale] = refs
	}
	return out
}

// artifactDoc is the part of a stored artifact this adapter reads.
// Whether a locale carries a message is a key lookup, so the values are
// left as raw JSON and never decoded: MCP asks where a message resolves
// from, not what it says there.
type artifactDoc struct {
	Messages map[string]json.RawMessage `json:"messages"`
}

// ArtifactHasMessage implements tools.Delivery.
func (a *Delivery) ArtifactHasMessage(
	ctx context.Context, project, id uuid.UUID, digest, key string,
) (bool, error) {
	body, err := a.release.ReleaseArtifact(ctx, project, id, digest)
	if err != nil {
		return false, notFound(err, releaseNotFound...)
	}
	var doc artifactDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, err
	}
	_, ok := doc.Messages[key]
	return ok, nil
}
