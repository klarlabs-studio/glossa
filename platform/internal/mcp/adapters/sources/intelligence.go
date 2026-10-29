package sources

import (
	"context"
	"errors"

	"github.com/google/uuid"

	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// Intelligence adapts Intelligence's application service to
// tools.Translator.
//
// It hands the request to the same RequestFill that Studio's "Fill with
// AI" and the CLI's translate call, so everything that use case decides
// stays decided there: which providers to route to, which budget to
// spend, and that a sensitive namespace is never machine-translated
// (RFC 0003 §7, RFC 0005 §7.4). No provider, model or key appears in
// this file, because none crosses MCP in either direction.
type Intelligence struct{ intelligence *intelligenceapp.Service }

// NewIntelligence returns the adapter.
func NewIntelligence(s *intelligenceapp.Service) *Intelligence { return &Intelligence{intelligence: s} }

var _ tools.Translator = (*Intelligence)(nil)

// Translate implements tools.Translator.
func (a *Intelligence) Translate(
	ctx context.Context, project uuid.UUID, in tools.TranslateRequest,
) (tools.TranslateJob, error) {
	filter := intelligenceapp.FillFilter{
		Namespace: in.Namespace, KeyPrefix: in.KeyPrefix, Keys: in.Keys,
		Select: intelligenceapp.FillSelect(in.Select),
	}
	// No idempotency key: a tool call is not a retried HTTP request, and
	// a fill that reuses its jobs is already idempotent where it
	// matters — an existing job for the same message, locale and source
	// revision is counted as existing rather than queued again.
	res, _, err := a.intelligence.RequestFill(ctx, project, intelligenceapp.FillRequest{
		Locales: in.Locales, Filter: filter,
	}, "")
	if err != nil {
		if errors.Is(err, intelligenceapp.ErrInvalidQuery) {
			return tools.TranslateJob{}, &app.InvalidArgumentError{
				Argument: "select", Reason: "the filter is not one Intelligence accepts",
			}
		}
		return tools.TranslateJob{}, notFound(err, intelligenceapp.ErrNotFound)
	}
	out := tools.TranslateJob{
		Fill: res.Fill.ID.String(), Locales: res.Fill.Locales,
		Select: string(res.Fill.Filter.Selection()), Warnings: res.Warnings,
		Skipped: res.Fill.Skipped, JobsCreated: res.Fill.JobsCreated, JobsExisting: res.Fill.JobsExisting,
	}
	for _, id := range res.Fill.JobIDs {
		out.Jobs = append(out.Jobs, id.String())
	}
	return out, nil
}
