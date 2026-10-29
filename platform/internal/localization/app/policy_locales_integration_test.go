//go:build integration

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
)

// TestCIWritesAPolicyThatNamesLocales is the reason the narrow path
// exists: policy-as-code from CI. A CI token holds catalog.read and
// catalog.write and nothing else, and saving a check policy whose
// require_complete names locales must work with exactly that — while a
// locale the project does not have is still refused.
func TestCIWritesAPolicyThatNamesLocales(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de", "fr"}, shop)
	id := catalogdomain.ProjectID(p)
	ci := h.ci()

	cur, err := h.catalog.GetProject(ci, id)
	if err != nil {
		t.Fatal(err)
	}
	settings := cur.Settings
	settings.CheckPolicy = &checkpolicy.Policy{RequireComplete: []string{"de", "fr"}, FailOn: checkpolicy.Error}
	saved, err := h.catalog.UpdateProject(ci, id, cur.Version, catalogdomain.ProjectChange{Settings: &settings})
	if err != nil {
		t.Fatalf("CI saves a policy naming locales: %v", err)
	}
	if got := saved.Settings.CheckPolicy.RequireComplete; !slices.Equal(got, []string{"de", "fr"}) {
		t.Errorf("require_complete = %v, want [de fr]", got)
	}

	// The validation is not weakened: a locale the project lacks is
	// still refused, for CI as for anyone.
	settings.CheckPolicy = &checkpolicy.Policy{RequireComplete: []string{"ja"}, FailOn: checkpolicy.Error}
	if _, err := h.catalog.UpdateProject(ci, id, saved.Version, catalogdomain.ProjectChange{Settings: &settings}); !errors.Is(err, checkpolicy.ErrUnknownLocale) {
		t.Errorf("CI names a locale the project lacks: %v, want ErrUnknownLocale", err)
	}
}

// TestCheckPolicyLocaleCodesIsNotAGeneralRead pins the security
// property of that narrow path: it hands back locale codes to a caller
// that may already write the project, and it is not a way around
// translations.read for anything else.
func TestCheckPolicyLocaleCodesIsNotAGeneralRead(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de", "fr"}, shop)
	ci := h.ci()

	codes, err := h.svc.CheckPolicyLocaleCodes(ci, p)
	if err != nil || !slices.Equal(codes, []string{"de", "en", "fr"}) {
		t.Fatalf("CI locale codes = %v, %v; want [de en fr]", codes, err)
	}

	// Locale codes and nothing else. Every ordinary read of
	// Localization stays shut to the same caller.
	if _, _, err := h.svc.ListLocales(ci, p, firstPage()); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("CI ListLocales = %v, want forbidden", err)
	}
	if _, err := h.svc.GetLocale(ci, p, "de"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("CI GetLocale = %v, want forbidden", err)
	}
	if _, err := h.svc.FallbackGraph(ci, p); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("CI FallbackGraph = %v, want forbidden", err)
	}
	if _, _, err := h.svc.ListTranslations(ci, p, "home.title", firstPage()); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("CI ListTranslations = %v, want forbidden", err)
	}
	if _, err := h.svc.MessagesWithCoverage(ci, p, app.CoverageFilter{Locale: "de"}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("CI MessagesWithCoverage = %v, want forbidden", err)
	}

	// Nor is the path open to any authenticated caller: it asks for the
	// catalog.write a policy write has already proved. A translator
	// holds translations.read and not catalog.write, and gets nothing.
	translator := h.as([]string{"translator"}, "de")
	if _, err := h.svc.CheckPolicyLocaleCodes(translator, p); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator CheckPolicyLocaleCodes = %v, want forbidden", err)
	}

	// And an ordinary caller's ListLocales still requires
	// translations.read — this is not a general relaxation.
	if _, _, err := h.svc.ListLocales(translator, p, firstPage()); err != nil {
		t.Errorf("translator ListLocales: %v", err)
	}
	if _, _, err := h.svc.ListLocales(h.developer(), p, firstPage()); err != nil {
		t.Errorf("developer ListLocales: %v", err)
	}
	if _, _, err := h.svc.ListLocales(context.Background(), p, firstPage()); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("anonymous ListLocales = %v, want unauthenticated", err)
	}
	if _, err := h.svc.CheckPolicyLocaleCodes(context.Background(), p); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("anonymous CheckPolicyLocaleCodes = %v, want unauthenticated", err)
	}
}

// TestCheckPolicyLocaleCodesStaysInTheTenant: the narrow path runs in
// tenant scope like every other read, so another tenant's CI learns
// nothing about this project — not its locales, and not that it exists.
func TestCheckPolicyLocaleCodesStaysInTheTenant(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de", "fr"}, shop)

	other, err := env.SeedTenant(context.Background(), "rival")
	if err != nil {
		t.Fatal(err)
	}
	intruder := authztest.CIToken(context.Background(), other)
	codes, err := h.svc.CheckPolicyLocaleCodes(intruder, p)
	if len(codes) != 0 {
		t.Errorf("another tenant read locale codes: %v", codes)
	}
	if !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant's CI = %v, want not found", err)
	}
}
