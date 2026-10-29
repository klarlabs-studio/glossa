package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// MaxListedLocales bounds the locales one project translation listing
// covers.
const MaxListedLocales = 20

// MaxListedKeys bounds the keys one listing may name. It is for the
// messages on a screen, not for a whole catalog: a caller with more
// than this pages by key prefix instead.
const MaxListedKeys = 50

// Listing errors.
var (
	ErrLocaleCount         = errors.New("localization: list 1 to 20 locales")
	ErrKeyCount            = errors.New("localization: name at most 50 keys")
	ErrInvalidMessageState = errors.New("localization: message state must be active or obsolete")
)

// TranslationFilter narrows a project's translation listing. Filters
// combine; nil and empty mean "any".
type TranslationFilter struct {
	// Locales are the locales to list, 1 to MaxListedLocales.
	Locales []string
	States  []string
	// Origins narrow by provenance (intent §22). `agent` — an
	// autonomous agent writing through MCP — is an origin of its own
	// since migration 0032, and this is what makes the difference
	// readable through the API rather than only in SQL: anything that
	// matters is reachable through the API, not only through the admin
	// UI.
	Origins      []string
	Outdated     *bool
	Namespace    *string
	KeyPrefix    string
	MessageState *string
	// Keys names messages exactly, for asking about the ones on a
	// screen — the in-product editor asks about one — where KeyPrefix
	// would also match everything below the key.
	Keys []string
}

// ProjectTranslationQuery is a validated TranslationFilter plus the
// keyset position, as the store runs it.
type ProjectTranslationQuery struct {
	Locales      []bcp47.Tag
	States       []domain.ReviewState
	Origins      []domain.Origin
	Outdated     *bool
	Namespace    *string
	KeyPrefix    string
	MessageState *string
	Keys         []string
	// Units, when not nil, limits the listing to these units: an
	// assigned member's (RFC 0006 §3.3). Empty and not nil is nothing.
	Units []authz.Unit
	After TranslationCursor
	Limit int
}

// TranslationCursor is the keyset position after a listed translation:
// (message key, message ID, locale).
type TranslationCursor struct {
	Key     string
	Message uuid.UUID
	Locale  string
}

// cursorSep can't occur in keys, IDs or locale codes.
const cursorSep = "\n"

func (c TranslationCursor) String() string {
	return c.Key + cursorSep + c.Message.String() + cursorSep + c.Locale
}

func parseTranslationCursor(s string) (TranslationCursor, error) {
	if s == "" {
		return TranslationCursor{}, nil
	}
	parts := strings.Split(s, cursorSep)
	if len(parts) != 3 {
		return TranslationCursor{}, invalidPageToken()
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return TranslationCursor{}, invalidPageToken()
	}
	return TranslationCursor{Key: parts[0], Message: id, Locale: parts[2]}, nil
}

// ProjectTranslationView is a translation with the message it belongs to,
// as Localization's projection knows it.
type ProjectTranslationView struct {
	TranslationView
	Key          string
	Namespace    string
	MessageState string
}

// ProjectTranslationRow is a stored translation joined with its message.
type ProjectTranslationRow struct {
	TranslationRow
	Key          string
	Namespace    string
	MessageState string
}

func (f TranslationFilter) query(page pagination.Page) (ProjectTranslationQuery, error) {
	if len(f.Locales) == 0 || len(f.Locales) > MaxListedLocales {
		return ProjectTranslationQuery{}, ErrLocaleCount
	}
	after, err := parseTranslationCursor(page.After)
	if err != nil {
		return ProjectTranslationQuery{}, err
	}
	if len(f.Keys) > MaxListedKeys {
		return ProjectTranslationQuery{}, ErrKeyCount
	}
	q := ProjectTranslationQuery{
		Outdated: f.Outdated, Namespace: f.Namespace, KeyPrefix: f.KeyPrefix, Keys: f.Keys,
		After: after, Limit: page.Limit(),
	}
	for _, l := range f.Locales {
		tag, err := bcp47.Parse(l)
		if err != nil {
			return ProjectTranslationQuery{}, err
		}
		q.Locales = append(q.Locales, tag)
	}
	for _, s := range f.States {
		st, err := domain.ParseReviewState(s)
		if err != nil {
			return ProjectTranslationQuery{}, err
		}
		q.States = append(q.States, st)
	}
	for _, o := range f.Origins {
		// "" is not a wildcard here: an empty member of a repeated
		// filter is a typo, and ParseOrigin's default would silently
		// turn it into "every human translation".
		origin, err := domain.ParseOrigin(o, "")
		if err != nil || origin == "" {
			return ProjectTranslationQuery{}, fmt.Errorf("%w: %q", domain.ErrInvalidOrigin, o)
		}
		q.Origins = append(q.Origins, origin)
	}
	if f.MessageState != nil {
		if *f.MessageState != "active" && *f.MessageState != "obsolete" {
			return ProjectTranslationQuery{}, fmt.Errorf("%w: %q", ErrInvalidMessageState, *f.MessageState)
		}
		q.MessageState = f.MessageState
	}
	return q, nil
}

// ListProjectTranslations lists a project's translations in some locales
// across messages, by message key and then locale, with each message's
// key, namespace and state — the CLI's and Studio's bulk read. It is one
// query per page over Localization's projection of the catalog. An
// assigned member's listing holds only their units, filtered in the
// query (RFC 0006 §3.3).
func (s *Service) ListProjectTranslations(ctx context.Context, project uuid.UUID, f TranslationFilter, page pagination.Page) ([]ProjectTranslationView, *string, error) {
	vis, err := authz.Visible(ctx, authz.TranslationsRead, project)
	if err != nil {
		return nil, nil, err
	}
	q, err := f.query(page)
	if err != nil {
		return nil, nil, err
	}
	q.Units = vis.Units()
	if _, err := s.catalog.Project(ctx, project); err != nil {
		return nil, nil, err
	}
	var rows []ProjectTranslationView
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		stored, err := st.ProjectTranslations(ctx, project, q)
		for _, r := range stored {
			rows = append(rows, ProjectTranslationView{
				TranslationView: view(r.TranslationRow), Key: r.Key, Namespace: r.Namespace, MessageState: r.MessageState,
			})
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(v ProjectTranslationView) string {
		return TranslationCursor{Key: v.Key, Message: v.MessageID, Locale: v.Locale.String()}.String()
	})
	return items, next, nil
}
