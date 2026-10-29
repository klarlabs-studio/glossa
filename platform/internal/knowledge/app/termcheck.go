package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// MaxCheckedLocales bounds the locales one project check covers.
const MaxCheckedLocales = 20

// MaxCheckedKeys bounds the keys one project check may name: the
// messages on a screen, not a catalog.
const MaxCheckedKeys = 50

// Errors of a project terminology check.
var (
	ErrLocaleCount  = errors.New("knowledge: check 1 to 20 locales")
	ErrKeyCount     = errors.New("knowledge: name at most 50 keys")
	ErrInvalidState = errors.New("knowledge: state must be draft, needs_review, approved or rejected")
)

// reviewStates are the review states a translation can be in; a check
// covers every one but rejected by default.
var reviewStates = []string{"draft", "needs_review", "approved", "rejected"}

// TranslationPageQuery selects a page of a project's translations of
// active messages, in key and locale order.
type TranslationPageQuery struct {
	Locales   []bcp47.Tag
	States    []string
	Namespace *string
	KeyPrefix string
	Keys      []string
	Page      pagination.Page
}

// ProjectTranslation is a translation with its message's key, namespace
// and current source: what a project-wide check reads.
type ProjectTranslation struct {
	MessageID uuid.UUID
	Key       string
	Namespace string
	Locale    bcp47.Tag
	State     string
	// Source is the message's current source; ok false when the catalog
	// no longer has the message.
	Source    mf.Message
	HasSource bool
	// SourceRevision is Source's revision number: the message's current
	// one, which is what a check of this translation runs against.
	SourceRevision int
	Target         mf.Message
}

// ProjectTermCheck asks for terminology QA over a project's
// translations.
type ProjectTermCheck struct {
	Locales []bcp47.Tag
	// States are the review states checked; empty means every state but
	// rejected.
	States    []string
	Namespace *string
	KeyPrefix string
	// Keys names messages exactly, for asking about the ones on a
	// screen — the in-product editor asks about one — where KeyPrefix
	// would also match everything below the key.
	Keys []string
	Page pagination.Page
}

// TranslationFindings are one translation's terminology findings.
type TranslationFindings struct {
	MessageID uuid.UUID
	Key       string
	Namespace string
	Locale    bcp47.Tag
	State     string
	// SourceRevision is the source revision the findings were computed
	// against: the message's current one, whose visible text SourceText
	// is. A waiver on one of them is measured against it (RFC 0005
	// §2.3), so moving the source brings a waived finding back.
	SourceRevision int
	// SourceText and TargetText are the visible texts the findings'
	// spans point into.
	SourceText string
	TargetText string
	Findings   []domain.TermFinding
}

// ProjectTermReport is a page of a project check: the translations with
// findings, and how many it checked per locale.
type ProjectTermReport struct {
	Items   []TranslationFindings
	Checked map[string]int
	Next    *string
}

// CheckProjectTerminology runs terminology QA (CheckTerminology's rules)
// over a page of a project's translations, each against its message's
// current source, as visible text, with the project's and the
// tenant-wide concepts. A page is one read per context — the termbase,
// the translations, their sources — never one per translation. Needs
// knowledge.read (and, through the ports, translations.read and
// catalog.read).
func (s *Service) CheckProjectTerminology(ctx context.Context, project uuid.UUID, c ProjectTermCheck) (ProjectTermReport, error) {
	if err := authz.RequireIn(ctx, authz.KnowledgeRead, project); err != nil {
		return ProjectTermReport{}, err
	}
	if len(c.Locales) == 0 || len(c.Locales) > MaxCheckedLocales {
		return ProjectTermReport{}, ErrLocaleCount
	}
	if len(c.Keys) > MaxCheckedKeys {
		return ProjectTermReport{}, ErrKeyCount
	}
	states := c.States
	if len(states) == 0 {
		states = reviewStates[:3]
	}
	for _, st := range states {
		if !slices.Contains(reviewStates, st) {
			return ProjectTermReport{}, fmt.Errorf("%w: %q", ErrInvalidState, st)
		}
	}
	p, err := s.projects.Project(ctx, project)
	if err != nil {
		return ProjectTermReport{}, err
	}
	rows, next, err := s.translations.ProjectTranslations(ctx, project, TranslationPageQuery{
		Locales: c.Locales, States: states, Namespace: c.Namespace, KeyPrefix: c.KeyPrefix, Keys: c.Keys,
		Page: c.Page,
	})
	if err != nil {
		return ProjectTermReport{}, err
	}
	tb, err := s.termbase(ctx, &project, append([]bcp47.Tag{p.SourceLocale}, c.Locales...)...)
	if err != nil {
		return ProjectTermReport{}, err
	}
	out := ProjectTermReport{Items: []TranslationFindings{}, Checked: map[string]int{}, Next: next}
	for _, l := range c.Locales {
		out.Checked[l.String()] = 0
	}
	for _, r := range rows {
		if !r.HasSource {
			continue
		}
		out.Checked[r.Locale.String()]++
		src, tgt := domain.VisibleText(r.Source), domain.VisibleText(r.Target)
		fs := domain.CheckTerminology(tb, src, p.SourceLocale, tgt, r.Locale)
		if len(fs) == 0 {
			continue
		}
		out.Items = append(out.Items, TranslationFindings{
			MessageID: r.MessageID, Key: r.Key, Namespace: r.Namespace, Locale: r.Locale, State: r.State,
			SourceRevision: r.SourceRevision, SourceText: src, TargetText: tgt, Findings: fs,
		})
	}
	return out, nil
}
