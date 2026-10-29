package app

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// These fakes implement only what message_context reads; the embedded
// interfaces panic if anything else is called.

type contextCatalog struct {
	Catalog
	messages map[uuid.UUID]SourceMessage
}

func (c contextCatalog) MessagesByIDs(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]SourceMessage, error) {
	var out []SourceMessage
	for id, m := range c.messages { // unordered, like the real one
		if slices.Contains(ids, id) {
			out = append(out, m)
		}
	}
	return out, nil
}

type contextLocalization struct {
	Localization
	translations map[string]TranslationState // key → state
	byPrefix     []Neighbour
	prefix       string
}

func (l *contextLocalization) Translation(_ context.Context, _ uuid.UUID, key, _ string) (TranslationState, error) {
	t, ok := l.translations[key]
	if !ok {
		return TranslationState{}, ErrNotFound
	}
	return t, nil
}

func (l *contextLocalization) Neighbours(_ context.Context, _ uuid.UUID, prefix, key, _ string, limit int) ([]Neighbour, error) {
	l.prefix = prefix
	var out []Neighbour
	for _, n := range l.byPrefix {
		if n.Key != key && len(out) < limit {
			out = append(out, n)
		}
	}
	return out, nil
}

type usageContext struct {
	usages    []Usage
	colocated []uuid.UUID
	limits    []int
}

func (u *usageContext) Usages(_ context.Context, _, _ uuid.UUID, limit int) ([]Usage, error) {
	u.limits = append(u.limits, limit)
	return u.usages, nil
}

func (u *usageContext) CoLocated(_ context.Context, _, _ uuid.UUID, limit int) ([]uuid.UUID, error) {
	u.limits = append(u.limits, limit)
	return u.colocated, nil
}

func source(t *testing.T, key, text string, active bool) SourceMessage {
	t.Helper()
	m, err := mf.ParseMF2(text)
	if err != nil {
		t.Fatal(err)
	}
	return SourceMessage{ID: uuid.New(), Key: key, Active: active, Source: m}
}

func neighbourKeys(ns []domain.Neighbour) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Key + "=" + n.Source + "→" + n.Translation
	}
	return out
}

func TestMessageContextFillsUsagesAndCoLocatedNeighbours(t *testing.T) {
	msg := source(t, "checkout.pay", "Pay", true)
	help := source(t, "checkout.help", "Help", true)
	gone := source(t, "cart.gone", "Gone", false)
	total := source(t, "cart.total", "Total", true)
	uc := &usageContext{
		usages: []Usage{
			{File: "src/Payment.vue", Line: 9, Component: "PaymentFooter", Route: "/checkout"},
			{File: "internal/mail/pay.go", Line: 3, Component: "mail.Pay"},
			{File: "src/pages/index.astro", Line: 1, Route: "/"},
			{File: "index.html", Line: 2},
			{File: "src/Payment.vue", Line: 9, Component: "PaymentFooter", Route: "/checkout"}, // another collector
		},
		colocated: []uuid.UUID{help.ID, gone.ID, total.ID},
	}
	loc := &contextLocalization{
		translations: map[string]TranslationState{
			"checkout.help": {Exists: true, State: "approved", Text: "Hilfe"},
			"cart.total":    {Exists: true, State: "rejected", Text: "Falsch"},
		},
		byPrefix: []Neighbour{
			{Key: "checkout.help", SourceMF2: "Help", Translation: "Hilfe"},
			{Key: "checkout.back", SourceMF2: "Back"},
			{Key: "checkout.cancel", SourceMF2: "Cancel"},
			{Key: "checkout.done", SourceMF2: "Done"},
			{Key: "checkout.edit", SourceMF2: "Edit"},
		},
	}
	s := &Service{Deps: Deps{
		Catalog:      contextCatalog{messages: map[uuid.UUID]SourceMessage{help.ID: help, gone.ID: gone, total.ID: total}},
		Localization: loc, Usages: uc,
	}}
	c, err := s.messageContext(context.Background(), msg, "de")
	if err != nil {
		t.Fatal(err)
	}
	wantUsages := []string{
		"src/Payment.vue:9 (PaymentFooter, /checkout)", "internal/mail/pay.go:3 (mail.Pay)", "src/pages/index.astro:1 (/)",
		"index.html:2",
	}
	if !slices.Equal(c.Usages, wantUsages) {
		t.Errorf("usages = %q, want %q", c.Usages, wantUsages)
	}
	// Co-located first (in the port's order, active only, rejected
	// translations left out), then the key prefix, up to 5.
	wantNeighbours := []string{"checkout.help=Help→Hilfe", "cart.total=Total→", "checkout.back=Back→",
		"checkout.cancel=Cancel→", "checkout.done=Done→"}
	if got := neighbourKeys(c.Neighbours); !slices.Equal(got, wantNeighbours) {
		t.Errorf("neighbours = %q, want %q", got, wantNeighbours)
	}
	if !slices.Equal(uc.limits, []int{usageLimit, neighbourLimit}) {
		t.Errorf("limits asked = %v", uc.limits)
	}
	if loc.prefix != "checkout." {
		t.Errorf("key prefix = %q", loc.prefix)
	}
}

func TestMessageContextWithoutUsagesFallsBackToTheKeyPrefix(t *testing.T) {
	msg := source(t, "checkout.pay", "Pay", true)
	loc := &contextLocalization{byPrefix: []Neighbour{{Key: "checkout.back", SourceMF2: "Back", Translation: "Zurück"}}}
	s := &Service{Deps: Deps{Localization: loc}}
	c, err := s.messageContext(context.Background(), msg, "de")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Usages) != 0 || !slices.Equal(neighbourKeys(c.Neighbours), []string{"checkout.back=Back→Zurück"}) {
		t.Errorf("context = %+v", c)
	}
	root := source(t, "title", "Title", true)
	if c, err := s.messageContext(context.Background(), root, "de"); err != nil || len(c.Neighbours) != 0 {
		t.Errorf("a key without a prefix: %+v, %v", c, err)
	}
}

func TestUsageFormat(t *testing.T) {
	cases := map[Usage]string{
		{File: "a.vue", Line: 1, Component: "A", Route: "/a"}: "a.vue:1 (A, /a)",
		{File: "a.go", Line: 2, Component: "pkg.F"}:           "a.go:2 (pkg.F)",
		{File: "a.astro", Line: 3, Route: "/"}:                "a.astro:3 (/)",
		{File: "a.html", Line: 4}:                             "a.html:4",
	}
	for u, want := range cases {
		if got := u.String(); got != want {
			t.Errorf("%+v = %q, want %q", u, got, want)
		}
	}
}
