package app

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// Reader is the narrow, read-only view of Knowledge that the
// Intelligence context consumes (RFC 0003 §3.2): its agent's tm_lookup,
// term_lookup and style_rules tools, and the terminology part of its
// validate tool. It carries no HTTP types. Every method is authorized
// with knowledge.read in the tenant on ctx — a background job acts
// through authz.Background(ctx, name, authz.KnowledgeRead, …) — and runs
// in its own read transaction.
//
//   - LookupTM: exact (100; 101 in context) and fuzzy (50–99) matches,
//     best first, targets adapted to the query's variable names, with
//     each unit's provenance (translation, revision, project, key,
//     author). An exact hit with no terminology findings may be reused
//     without a model call.
//   - RecognizeTerms: concepts and terms found in the source, with the
//     target locale's terms and their status when TargetLocale is set.
//   - CheckTerminology: term_missing / term_forbidden findings for a
//     draft (plain text; use domain.VisibleText for a message).
//   - EffectiveStyle: the merged guide for a project, locale and
//     namespace, and the guide versions it used (for provenance).
type Reader interface {
	LookupTM(ctx context.Context, q TMQuery) ([]TMMatch, error)
	RecognizeTerms(ctx context.Context, q TermQuery) ([]RecognizedTerm, error)
	CheckTerminology(ctx context.Context, c TermCheck) ([]domain.TermFinding, error)
	EffectiveStyle(ctx context.Context, q StyleQuery) (domain.EffectiveStyle, error)
}

var _ Reader = (*Service)(nil)
