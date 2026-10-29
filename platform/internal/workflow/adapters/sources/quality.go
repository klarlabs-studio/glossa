package sources

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	intelligencedomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
)

// QualityFacts implements app.QualityFacts (RFC 0006 §3.4) over the
// contexts that own each number, each read through its own use case as
// the principal on ctx: the source's words from Catalog, the delivered
// and the current text from Localization's revision log, the open
// findings from Quality. The edit between the delivered and the current
// text is Intelligence's computation, the one GET …/ai-metrics reports,
// so the two can never disagree about what an edit is.
type QualityFacts struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	findings     *Findings
}

// NewQualityFacts returns the adapter.
func NewQualityFacts(c *catalogapp.Service, l *localizationapp.Service, q *qualityapp.Service) *QualityFacts {
	return &QualityFacts{catalog: c, localization: l, findings: NewFindings(q)}
}

var _ app.QualityFacts = (*QualityFacts)(nil)

// maxRevisionPages bounds how far back one unit's log is read for the
// text that stood at completion.
const maxRevisionPages = 10

// UnitQuality implements app.QualityFacts.
func (q *QualityFacts) UnitQuality(ctx context.Context, project, message uuid.UUID, locale string, completed time.Time) (app.UnitQuality, error) {
	m, err := q.catalog.MessageByID(ctx, catalogdomain.ProjectID(project), catalogdomain.MessageID(message))
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.UnitQuality{}, nil
	}
	if err != nil {
		return app.UnitQuality{}, err
	}
	key := string(m.Key)
	current, delivered, found, err := q.texts(ctx, project, key, locale, completed)
	if err != nil || !found {
		return app.UnitQuality{}, err
	}
	out := app.UnitQuality{
		Found:       true,
		SourceWords: words(m.Source.Model),
		FromTM:      delivered.Provenance.Origin == localizationdomain.OriginTranslationMemory,
		ReviewState: string(current.State),
		Changed:     current.Content.Text != delivered.Content.Text,
	}
	if out.Changed {
		d := intelligencedomain.Diff(delivered.Content.Model, current.Content.Model, nil, nil, "")
		out.EditDistance, out.EditRatio = d.Distance, d.Ratio
	}
	if out.Findings, err = q.findings.Open(ctx, project, message, locale, key); err != nil {
		return app.UnitQuality{}, err
	}
	return out, nil
}

// texts reads the unit's newest revision and the one that stood at
// completed. found is false when the translation was never written, or
// was written only after the assignment was completed.
func (q *QualityFacts) texts(ctx context.Context, project uuid.UUID, key, locale string, completed time.Time,
) (current, delivered localizationdomain.Revision, found bool, err error) {
	page := pagination.Page{Size: 50}
	for range maxRevisionPages {
		revs, next, err := q.localization.TranslationRevisions(ctx, project, key, locale, page)
		if errors.Is(err, localizationapp.ErrNotFound) || errors.Is(err, localizationapp.ErrLocaleNotFound) {
			return current, delivered, false, nil
		}
		if err != nil {
			return current, delivered, false, err
		}
		for i, r := range revs {
			if i == 0 && page.After == "" {
				current = r
			}
			if !r.CreatedAt.After(completed) {
				return current, r, true, nil
			}
		}
		if next == nil {
			break
		}
		page.After = *next
	}
	return current, delivered, false, nil
}

// words counts the words of a message's visible text, every variant's
// distinct text once.
func words(m mf.Message) int {
	return len(strings.Fields(intelligencedomain.PlainText(m)))
}
