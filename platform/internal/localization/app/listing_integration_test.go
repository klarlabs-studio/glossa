//go:build integration

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// translate writes text for key in locale as ctx, failing the test on
// any error.
func (h *harness) translate(t *testing.T, ctx context.Context, p uuid.UUID, key, locale, text string, state *string) {
	t.Helper()
	if _, _, err := h.svc.PutTranslation(ctx, p, key, locale, app.TranslationInput{Text: text, State: state}, nil); err != nil {
		t.Fatalf("translate %s/%s: %v", key, locale, err)
	}
}

// listing is a small project in a known state: cart.items has an
// outdated de translation, checkout.pay is translated in de (draft) and
// fr, home.title only in fr, legal.terms is obsolete with a de
// translation, checkout.total is untranslated.
func listing(t *testing.T) (*harness, uuid.UUID) {
	t.Helper()
	h := newHarness(t)
	p := h.setup(t, false, []string{"de", "fr"}, map[string]string{
		"cart.items":     "{count, plural, one {# item} other {# items}}",
		"checkout.pay":   "Pay {amount, number}",
		"checkout.total": "Total",
		"home.title":     "Welcome",
		"legal.terms":    "Terms",
	})
	ctx := h.developer()
	h.translate(t, ctx, p, "cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}", nil)
	h.translate(t, ctx, p, "checkout.pay", "de", "{amount, number} zahlen", ptr("draft"))
	h.translate(t, ctx, p, "checkout.pay", "fr", "Payer {amount, number}", nil)
	h.translate(t, ctx, p, "home.title", "fr", "Bienvenue", nil)
	h.translate(t, ctx, p, "legal.terms", "de", "AGB", nil)
	m, err := h.catalog.GetMessage(ctx, catalogdomain.ProjectID(p), "cart.items")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.catalog.ReviseSource(ctx, catalogdomain.ProjectID(p), "cart.items", m.Version,
		"{count, plural, one {# product} other {# products}}", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.catalog.ObsoleteMessage(ctx, catalogdomain.ProjectID(p), "legal.terms", nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	return h, p
}

// listAll pages through ListProjectTranslations and returns
// "key/locale" in order.
func listAll(t *testing.T, h *harness, ctx context.Context, p uuid.UUID, f app.TranslationFilter, size int) []string {
	t.Helper()
	var out []string
	page := pagination.Page{Size: size}
	for i := 0; ; i++ {
		items, next, err := h.svc.ListProjectTranslations(ctx, p, f, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range items {
			out = append(out, v.Key+"/"+v.Locale.String())
		}
		if next == nil {
			return out
		}
		if i > 20 {
			t.Fatal("pagination does not terminate")
		}
		if page, err = pagination.Parse(ptr(size), next); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListProjectTranslations(t *testing.T) {
	h, p := listing(t)
	ctx := h.as([]string{"translator"}, "fr") // reading isn't locale-scoped
	both := []string{"fr", "de"}

	got := listAll(t, h, ctx, p, app.TranslationFilter{Locales: both}, 2)
	want := "cart.items/de checkout.pay/de checkout.pay/fr home.title/fr legal.terms/de"
	if strings.Join(got, " ") != want {
		t.Errorf("all = %v, want %s", got, want)
	}

	items, _, err := h.svc.ListProjectTranslations(ctx, p, app.TranslationFilter{Locales: []string{"de"}}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	cart := items[0]
	if cart.Key != "cart.items" || cart.Namespace != "default" || cart.MessageState != "active" ||
		cart.SourceRevision != 1 || cart.CurrentSourceRevision != 2 || !cart.Outdated() || cart.Content.Text == "" {
		t.Errorf("cart.items/de = %+v", cart)
	}
	if items[2].Key != "legal.terms" || items[2].MessageState != "obsolete" {
		t.Errorf("legal.terms/de = %+v", items[2])
	}

	for name, tc := range map[string]struct {
		f    app.TranslationFilter
		want string
	}{
		"outdated":                {app.TranslationFilter{Locales: both, Outdated: ptr(true)}, "cart.items/de"},
		"current":                 {app.TranslationFilter{Locales: []string{"de"}, Outdated: ptr(false)}, "checkout.pay/de legal.terms/de"},
		"state":                   {app.TranslationFilter{Locales: both, States: []string{"draft"}}, "checkout.pay/de"},
		"states":                  {app.TranslationFilter{Locales: []string{"de_de", "de"}, States: []string{"draft", "approved"}, MessageState: ptr("active")}, "cart.items/de checkout.pay/de"},
		"key prefix":              {app.TranslationFilter{Locales: both, KeyPrefix: "checkout."}, "checkout.pay/de checkout.pay/fr"},
		"prefix is not a pattern": {app.TranslationFilter{Locales: both, KeyPrefix: "checkout_"}, ""},
		"namespace":               {app.TranslationFilter{Locales: both, Namespace: ptr("default"), MessageState: ptr("obsolete")}, "legal.terms/de"},
		"no namespace":            {app.TranslationFilter{Locales: both, Namespace: ptr("emails")}, ""},
		"unknown locale":          {app.TranslationFilter{Locales: []string{"it"}}, ""},
	} {
		if got := strings.Join(listAll(t, h, ctx, p, tc.f, 1), " "); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}

	// A removed locale lists nothing; its translations are kept.
	if err := h.svc.RemoveLocale(h.developer(), p, "fr"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(listAll(t, h, ctx, p, app.TranslationFilter{Locales: both}, 50), " "); got != "cart.items/de checkout.pay/de legal.terms/de" {
		t.Errorf("after removing fr: %s", got)
	}
}

// LiveTranslations is the release snapshot without the obsolete
// messages' translations, which a release keeps (it joins with Catalog's
// own active source) and a check must not read in bulk.
func TestLiveTranslationsLeaveOutObsoleteMessages(t *testing.T) {
	h, p := listing(t)
	ctx := h.developer()
	all := []domain.ReviewState{domain.StateDraft, domain.StateNeedsReview, domain.StateApproved}
	keys := func(snap app.TranslationSnapshot) int {
		n := 0
		for _, byID := range snap.Translations {
			n += len(byID)
		}
		return n
	}
	release, err := h.svc.ReleaseTranslations(ctx, p, all)
	if err != nil {
		t.Fatal(err)
	}
	live, err := h.svc.LiveTranslations(ctx, p, all)
	if err != nil {
		t.Fatal(err)
	}
	// Five translations; legal.terms/de is the obsolete message's.
	if keys(release) != 5 || keys(live) != 4 {
		t.Errorf("release read %d translations, live %d: want 5 and 4", keys(release), keys(live))
	}
	legal, err := h.catalog.GetMessage(ctx, catalogdomain.ProjectID(p), "legal.terms")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := live.Translations[bcp47.MustParse("de")][uuid.UUID(legal.ID)]; ok {
		t.Error("the live snapshot carries the obsolete message's translation")
	}
	if _, ok := release.Translations[bcp47.MustParse("de")][uuid.UUID(legal.ID)]; !ok {
		t.Error("the release snapshot lost the obsolete message's translation")
	}
}

func TestListProjectTranslationsRejects(t *testing.T) {
	h, p := listing(t)
	ctx := h.developer()
	many := make([]string, 21)
	for i := range many {
		many[i] = "de"
	}
	for name, tc := range map[string]struct {
		f    app.TranslationFilter
		want error
	}{
		"no locale":         {app.TranslationFilter{}, app.ErrLocaleCount},
		"too many locales":  {app.TranslationFilter{Locales: many}, app.ErrLocaleCount},
		"invalid locale":    {app.TranslationFilter{Locales: []string{"not a locale"}}, bcp47.ErrInvalid},
		"invalid state":     {app.TranslationFilter{Locales: []string{"de"}, States: []string{"done"}}, domain.ErrInvalidReviewState},
		"invalid msg state": {app.TranslationFilter{Locales: []string{"de"}, MessageState: ptr("gone")}, app.ErrInvalidMessageState},
	} {
		if _, _, err := h.svc.ListProjectTranslations(ctx, p, tc.f, firstPage()); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	f := app.TranslationFilter{Locales: []string{"de"}}
	if _, _, err := h.svc.ListProjectTranslations(ctx, p, f, pagination.Page{Size: 1, After: "garbage"}); err == nil {
		t.Error("accepted a malformed cursor")
	}
	if _, _, err := h.svc.ListProjectTranslations(ctx, uuid.New(), f, firstPage()); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
	if _, _, err := h.svc.ListProjectTranslations(context.Background(), p, f, firstPage()); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no principal: %v", err)
	}
}
