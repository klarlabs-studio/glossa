package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// Service implements Localization's use cases.
type Service struct {
	tx        Transactor
	catalog   SourceCatalog
	flow      domain.ReviewFlow
	reviewers Reviewers
	now       func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithReviewers wires Identity's answer to "could anyone else review
// this locale?". Without it the answer is always yes: the author of a
// text never approves it (strict four-eyes).
func WithReviewers(r Reviewers) Option { return func(s *Service) { s.reviewers = r } }

// othersCanReview reports whether an active member other than the
// principal by holds translations.review for locale in project.
func (s *Service) othersCanReview(ctx context.Context, project uuid.UUID, locale bcp47.Tag, by string) (bool, error) {
	if s.reviewers == nil {
		return true, nil
	}
	return s.reviewers.OthersCanReview(ctx, project, locale, by)
}

// New returns the service.
func New(tx Transactor, catalog SourceCatalog, opts ...Option) *Service {
	s := &Service{tx: tx, catalog: catalog, flow: domain.DefaultReviewFlow(), now: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(s)
	}
	return s
}

// actor returns the acting principal after checking perm.
func actor(ctx context.Context, perm authz.Permission) (string, error) {
	if err := authz.Require(ctx, perm); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// actorIn returns the acting principal after checking perm in project:
// a project outside the caller's scope is authz.ErrNotVisible, the
// answer for one that does not exist (RFC 0006 §4.1).
func actorIn(ctx context.Context, perm authz.Permission, project uuid.UUID) (string, error) {
	if err := authz.RequireIn(ctx, perm, project); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// actorFor checks a locale-scoped permission.
func actorFor(ctx context.Context, perm authz.Permission, locale bcp47.Tag) (string, error) {
	l, err := authz.ParseLocale(locale.String())
	if err != nil {
		return "", err
	}
	if err := authz.RequireFor(ctx, perm, l); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// actorForIn checks a locale-scoped permission in project.
func actorForIn(ctx context.Context, perm authz.Permission, locale bcp47.Tag, project uuid.UUID) (string, error) {
	l, err := authz.ParseLocale(locale.String())
	if err != nil {
		return "", err
	}
	if err := authz.RequireForIn(ctx, perm, l, project); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// allowedForIn reports a locale-scoped permission in project without
// failing.
func allowedForIn(ctx context.Context, perm authz.Permission, locale bcp47.Tag, project uuid.UUID) bool {
	_, err := actorForIn(ctx, perm, locale, project)
	return err == nil
}

// assigned reports whether the caller reads only their assignments
// (RFC 0006 §3.3).
func assigned(ctx context.Context) bool {
	p, ok := authz.From(ctx)
	return ok && p.Assigned()
}

// unitActor returns the acting principal after checking perm for one
// unit — message in locale, in project — as authz.RequireUnit does: an
// assigned member only in a unit their assignments cover, and a unit
// outside them is Localization's own not-found.
func unitActor(ctx context.Context, perm authz.Permission, project, message uuid.UUID, locale bcp47.Tag) (string, error) {
	l, err := authz.ParseLocale(locale.String())
	if err != nil {
		return "", err
	}
	if err := authz.RequireUnit(ctx, perm, project, message, l); err != nil {
		if errors.Is(err, authz.ErrNotVisible) {
			return "", ErrNotFound
		}
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// allowedFor reports a locale-scoped permission without failing.
func allowedFor(ctx context.Context, perm authz.Permission, locale bcp47.Tag) bool {
	_, err := actorFor(ctx, perm, locale)
	return err == nil
}

// localeFromPath parses a locale in a URL, where a malformed one is
// simply not found.
func localeFromPath(s string) (bcp47.Tag, error) {
	t, err := bcp47.Parse(s)
	if err != nil {
		return bcp47.Tag{}, ErrLocaleNotFound
	}
	return t, nil
}

func invalidPageToken() error {
	return problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this list issued")
}

// ensureSourceLocale records the project's source locale; it exists from
// the project's creation, whichever path sees the project first.
func (s *Service) ensureSourceLocale(ctx context.Context, st Store, p ProjectInfo, by string) error {
	_, err := st.InsertLocale(ctx, domain.NewLocale(p.ID, p.SourceLocale, true, s.now()), by)
	return err
}

// AddLocale adds a locale to a project. Adding one that exists returns
// it with created false.
func (s *Service) AddLocale(ctx context.Context, project uuid.UUID, code string) (l domain.Locale, created bool, err error) {
	by, err := actorIn(ctx, authz.CatalogWrite, project)
	if err != nil {
		return domain.Locale{}, false, err
	}
	tag, err := bcp47.Parse(code)
	if err != nil {
		return domain.Locale{}, false, err
	}
	p, err := s.catalog.Project(ctx, project)
	if err != nil {
		return domain.Locale{}, false, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureSourceLocale(ctx, st, p, by); err != nil {
			return err
		}
		l = domain.NewLocale(project, tag, false, s.now())
		if created, err = st.InsertLocale(ctx, l, by); err != nil {
			return err
		}
		if !created {
			l, err = st.Locale(ctx, project, tag)
			return err
		}
		return st.Publish(ctx, localeEvent(domain.EventLocaleAdded, l, by))
	})
	return l, created, err
}

func localeEvent(typ string, l domain.Locale, by string) outbox.Event {
	return outbox.Event{
		Type: typ, AggregateType: domain.AggregateLocale, AggregateID: l.ProjectID.String() + ":" + l.Code.String(),
		Actor: outbox.Actor(by),
		Payload: domain.LocaleEvent{
			ProjectID: l.ProjectID.String(), Locale: l.Code.String(), Direction: string(l.Direction()), By: by,
		},
	}
}

// GetLocale returns one locale of a project. An assigned member reads
// the locales of a project their assignments reach into: translating a
// unit needs its locale's direction (RFC 0006 §3.3).
func (s *Service) GetLocale(ctx context.Context, project uuid.UUID, code string) (domain.Locale, error) {
	if err := authz.RequireProject(ctx, authz.TranslationsRead, project); err != nil {
		return domain.Locale{}, err
	}
	tag, err := localeFromPath(code)
	if err != nil {
		return domain.Locale{}, err
	}
	var l domain.Locale
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		l, err = st.Locale(ctx, project, tag)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return domain.Locale{}, ErrLocaleNotFound
	}
	return l, err
}

// ListLocales lists a project's locales by code, the source locale
// included.
func (s *Service) ListLocales(ctx context.Context, project uuid.UUID, page pagination.Page) ([]domain.Locale, *string, error) {
	if err := authz.RequireProject(ctx, authz.TranslationsRead, project); err != nil {
		return nil, nil, err
	}
	p, err := s.catalog.Project(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Locale
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		// A project seen before its created event was handled still
		// lists its source locale.
		if page.After == "" {
			if _, err := st.Locale(ctx, project, p.SourceLocale); errors.Is(err, ErrNotFound) {
				rows = append(rows, domain.NewLocale(project, p.SourceLocale, true, s.now()))
			}
		}
		stored, err := st.Locales(ctx, project, page.After, page.Limit())
		rows = sortedLocales(append(rows, stored...))
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(l domain.Locale) string { return l.Code.String() })
	return items, next, nil
}

// CheckPolicyLocaleCodes lists a project's locale codes, the source
// locale included, for one purpose: the invariant check inside another
// context's own write. Catalog validates a check policy's
// require_complete against the project's locales rather than against
// its own tables, because locales are Localization's (RFC 0002 §4), and
// that check runs while the caller is saving the policy.
//
// It checks catalog.write — the permission the write it serves has
// already proved — and deliberately not translations.read. Checking
// that a locale a writer named exists is an internal invariant, not a
// user-facing read: it discloses nothing the caller could not learn by
// saving the policy and reading `invalid_check_policy` back. Requiring
// translations.read on top would mean a CI token (catalog.read and
// catalog.write, the whole CI ceiling) could not write a policy naming
// locales, which is policy-as-code's whole point.
//
// It is narrow on purpose and may not be widened. It returns locale
// codes and nothing else — no direction, no timestamps, no counts and
// no translated text — and it runs in tenant scope like every other
// read, so row-level security still confines it. It is not a general
// way around translations.read: anything a caller *reads* goes through
// ListLocales, which keeps asking for it.
func (s *Service) CheckPolicyLocaleCodes(ctx context.Context, project uuid.UUID) ([]string, error) {
	if err := authz.RequireIn(ctx, authz.CatalogWrite, project); err != nil {
		return nil, err
	}
	p, err := s.catalog.Project(ctx, project)
	if err != nil {
		return nil, err
	}
	var codes []string
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		ls, err := st.AllLocales(ctx, project)
		if err != nil {
			return err
		}
		codes = make([]string, 0, len(ls)+1)
		for _, l := range ls {
			codes = append(codes, l.Code.String())
		}
		// The source locale exists from the project's creation, even
		// while the row that records it hasn't been written yet
		// (ListLocales says the same).
		if !slices.Contains(codes, p.SourceLocale.String()) {
			codes = append(codes, p.SourceLocale.String())
		}
		slices.Sort(codes)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func sortedLocales(ls []domain.Locale) []domain.Locale {
	slices.SortFunc(ls, func(a, b domain.Locale) int { return strings.Compare(a.Code.String(), b.Code.String()) })
	return ls
}

// RemoveLocale removes a locale from a project. Its translations stay
// (history is never dropped) and return if the locale is added again.
// The source locale can't be removed, nor a locale the fallback graph
// still names.
func (s *Service) RemoveLocale(ctx context.Context, project uuid.UUID, code string) error {
	by, err := actorIn(ctx, authz.CatalogWrite, project)
	if err != nil {
		return err
	}
	tag, err := localeFromPath(code)
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		l, err := st.Locale(ctx, project, tag)
		if errors.Is(err, ErrNotFound) {
			return ErrLocaleNotFound
		}
		if err != nil {
			return err
		}
		if l.IsSource {
			return domain.ErrSourceLocale
		}
		g, err := st.FallbackGraph(ctx, project, true)
		if err != nil {
			return err
		}
		if mentions(g.Edges, tag) {
			return ErrLocaleInFallback
		}
		if err := st.DeleteLocale(ctx, project, tag); err != nil {
			return err
		}
		return st.Publish(ctx, localeEvent(domain.EventLocaleRemoved, l, by))
	})
}

// mentions reports whether a stored graph names tag, as a key or in a
// chain.
func mentions(edges map[string][]string, tag bcp47.Tag) bool {
	for k, chain := range edges {
		if k == tag.String() || slices.Contains(chain, tag.String()) {
			return true
		}
	}
	return false
}

// FallbackGraph returns a project's fallback graph (empty with version
// 0 when none was set).
func (s *Service) FallbackGraph(ctx context.Context, project uuid.UUID) (Graph, error) {
	if err := authz.RequireIn(ctx, authz.TranslationsRead, project); err != nil {
		return Graph{}, err
	}
	if _, err := s.catalog.Project(ctx, project); err != nil {
		return Graph{}, err
	}
	var g Graph
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		g, err = st.FallbackGraph(ctx, project, false)
		return err
	})
	return g, err
}

// PutFallbackGraph replaces a project's fallback graph. ifMatch must be
// the stored version when a graph exists and absent when none does.
func (s *Service) PutFallbackGraph(ctx context.Context, project uuid.UUID, raw map[string][]string, ifMatch *int) (Graph, error) {
	by, err := actorIn(ctx, authz.CatalogWrite, project)
	if err != nil {
		return Graph{}, err
	}
	p, err := s.catalog.Project(ctx, project)
	if err != nil {
		return Graph{}, err
	}
	var out Graph
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if err := s.ensureSourceLocale(ctx, st, p, by); err != nil {
			return err
		}
		cur, err := st.FallbackGraph(ctx, project, true)
		if err != nil {
			return err
		}
		if err := checkIfMatch(cur.Version, ifMatch); err != nil {
			return err
		}
		locales, err := st.AllLocales(ctx, project)
		if err != nil {
			return err
		}
		tags := make([]bcp47.Tag, len(locales))
		for i, l := range locales {
			tags[i] = l.Code
		}
		g, err := domain.NewFallbackGraph(raw, tags)
		if err != nil {
			return err
		}
		out = Graph{Edges: g.Edges(), Version: cur.Version + 1}
		if err := st.SaveFallbackGraph(ctx, project, out.Edges, out.Version, cur.Version, by, s.now()); err != nil {
			return err
		}
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventFallbackGraphChanged, AggregateType: domain.AggregateFallbackGraph, AggregateID: project.String(),
			Actor:   outbox.Actor(by),
			Payload: domain.FallbackGraphChanged{ProjectID: project.String(), Fallback: out.Edges, Version: out.Version, By: by},
		})
	})
	return out, err
}

// checkIfMatch applies the create-or-replace concurrency rule: a stored
// resource (version > 0) needs its version; a missing one takes none.
func checkIfMatch(version int, ifMatch *int) error {
	switch {
	case version > 0 && ifMatch == nil:
		return ErrPreconditionRequired
	case version > 0 && *ifMatch != version, version == 0 && ifMatch != nil:
		return ErrPreconditionFailed
	}
	return nil
}

// beforeRevision reads a page cursor holding a revision number.
func beforeRevision(s string) (int, error) {
	if s == "" {
		return int(^uint32(0) >> 1), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, invalidPageToken()
	}
	return n, nil
}
