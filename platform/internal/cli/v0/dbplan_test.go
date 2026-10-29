package v0_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
)

var (
	t0    = time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	alice = "11111111-1111-1111-1111-111111111111"
	bob   = "22222222-2222-2222-2222-222222222222"
)

func str(s string) *string { return &s }

func snapshot() v0.Snapshot {
	return v0.Snapshot{
		Restore: v0.Restore{Database: "v0_restore", DumpName: "glossa-20261001.sql.gz", DumpSHA256: strings.Repeat("a", 64)},
		Tenant:  v0.Tenant{ID: "ten", Slug: "klarlabs", Name: "Klarlabs"},
		Project: v0.Project{ID: "prj", Slug: "brotwerk", Name: "Brotwerk", DefaultLocale: "de"},
		Locales: []v0.Locale{{Code: "de", Label: "Deutsch", Enabled: true}, {Code: "en", Label: "English (UK)", Enabled: true},
			{Code: "fr", Label: "Français", Enabled: false}},
		Keys: []v0.Key{{Key: "cart.items", Description: "Cart badge; count is the number of items", FirstSeenAt: t0},
			{Key: "home.title", FirstSeenAt: t0}},
		Translations: []v0.Row{
			{ID: "t-de-cart", Key: "cart.items", Locale: "de", Value: "{count, plural, one {# Artikel} other {# Artikel}}", Status: "approved", UpdatedBy: alice, UpdatedAt: t0},
			{ID: "t-de-home", Key: "home.title", Locale: "de", Value: "Willkommen", Status: "approved", UpdatedAt: t0},
			{ID: "t-en-cart", Key: "cart.items", Locale: "en", Value: "{count, plural, one {# item} other {# items}}", Status: "approved", UpdatedBy: bob, UpdatedAt: t0.Add(time.Hour)},
			{ID: "t-en-home", Key: "home.title", Locale: "en", Value: "Welcome", Status: "ai_translated", UpdatedAt: t0.Add(2 * time.Hour),
				LastActorKind: "ai", LastActorLabel: "openai"},
			{ID: "t-fr-home", Key: "home.title", Locale: "fr", Value: "Bienvenue", Status: "pending", UpdatedAt: t0},
		},
		Users: []v0.User{
			{ID: alice, Email: "alice@example.com", Role: "admin", Locales: []string{"de"}, CreatedAt: t0},
			{ID: bob, Email: "bob@example.com", Role: "translator", Locales: []string{"en_gb", "de"}, CreatedAt: t0},
			{ID: "33333333-3333-3333-3333-333333333333", Email: "carol@example.com", Role: "translator", Locales: []string{}, CreatedAt: t0},
		},
		History: []v0.Change{
			{ID: 1, TranslationID: "t-en-cart", Key: "cart.items", Locale: "en", Before: nil, After: str("x"), ChangedBy: bob, ActorKind: "user", ChangedAt: t0},
			{ID: 2, TranslationID: "t-en-home", Key: "home.title", Locale: "en", Before: str("x"), After: str("Welcome"), ActorKind: "ai", ActorLabel: "openai", ChangedAt: t0},
			{ID: 3, TranslationID: "gone", Before: str("a"), After: str("b"), ActorKind: "user", ChangedAt: t0},
			{ID: 4, TranslationID: "t-fr-home", Key: "home.title", Locale: "fr", After: str("Bienvenue"), ActorKind: "system", ActorLabel: "bootstrap", ChangedAt: t0},
		},
		Uncarried: v0.Uncarried{APIKeys: 2, AIProviders: 1, AnalyticsEvents: 40},
	}
}

func TestDBPlanCarriesDescriptionsAndProvenance(t *testing.T) {
	plan, err := v0.BuildDBPlan("de", snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := map[string]string{}
	for _, m := range plan.Messages {
		desc[m.Key] = m.Description
	}
	if desc["cart.items"] != "Cart badge; count is the number of items" || desc["home.title"] != "" {
		t.Errorf("descriptions = %v", desc)
	}
	detail := map[string]map[string]any{}
	for _, tr := range plan.Translations {
		detail[tr.Locale+" "+tr.Key] = tr.Detail
	}
	cart := detail["en cart.items"]
	if cart["v0_status"] != "approved" || cart["v0_updated_by"] != "v0:"+bob || cart["v0_updated_at"] != "2025-03-01T11:00:00Z" ||
		cart["v0_translation_id"] != "t-en-cart" {
		t.Errorf("en cart.items detail = %v", cart)
	}
	// v0.3 leaves updated_by NULL for AI writes; the audit row names the provider.
	if home := detail["en home.title"]; home["v0_updated_by"] != "v0:ai:openai" || home["v0_status"] != "ai_translated" {
		t.Errorf("en home.title detail = %v", home)
	}
	// Who is unknown stays explicitly unknown: the key is there, null.
	fr := detail["fr home.title"]
	if v, ok := fr["v0_updated_by"]; !ok || v != nil {
		t.Errorf("fr home.title detail = %v; an unknown author must be an explicit null", fr)
	}
	if _, err := json.Marshal(cart); err != nil {
		t.Fatal(err)
	}
}

func TestDBPlanLocalesKeepLabelsAndWarnAboutDisabledOnes(t *testing.T) {
	plan, err := v0.BuildDBPlan("de", snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.LocaleInfo) != 3 || plan.LocaleInfo[1].Code != "en" || plan.LocaleInfo[1].Label != "English (UK)" ||
		plan.LocaleInfo[2].Enabled || !plan.LocaleInfo[0].Source {
		t.Fatalf("locales = %+v", plan.LocaleInfo)
	}
	if !containsWith(plan.Warnings, "fr") {
		t.Errorf("warnings = %v; a disabled v0.3 locale must be called out", plan.Warnings)
	}
	only, err := v0.BuildDBPlan("de", snapshot(), map[string]bool{"en": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(only.Locales, ",") != "en" {
		t.Errorf("--locales en: locales = %v", only.Locales)
	}
}

func TestDBPlanWarnsWhenTheSourceLocaleDiffers(t *testing.T) {
	s := snapshot()
	s.Project.DefaultLocale = "en"
	plan, err := v0.BuildDBPlan("de", s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsWith(plan.Warnings, "default locale") {
		t.Errorf("warnings = %v", plan.Warnings)
	}
}

func TestUsersBecomeInvitationPlans(t *testing.T) {
	plan, err := v0.BuildDBPlan("de", snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	inv := map[string]v0.Invitation{}
	for _, i := range plan.Invitations {
		inv[i.Email] = i
	}
	if a := inv["alice@example.com"]; a.Status != "planned" || strings.Join(a.Roles, ",") != "admin" || len(a.Locales) != 0 ||
		a.V0UserID != alice || a.V0Role != "admin" || strings.Join(a.V0Locales, ",") != "de" {
		t.Errorf("admin = %+v; a v0.3 admin edits every locale, so the platform admin has no locale scope", a)
	}
	if b := inv["bob@example.com"]; b.Status != "planned" || strings.Join(b.Roles, ",") != "translator" || strings.Join(b.Locales, ",") != "de,en-GB" {
		t.Errorf("translator = %+v", b)
	}
	// v0.3: a translator with no locales edits nothing. The platform reads
	// an empty locale scope as every locale, so sending it would widen access.
	if c := inv["carol@example.com"]; c.Status != "held" || !strings.Contains(c.Reason, "every locale") {
		t.Errorf("scopeless translator = %+v", c)
	}
}

func TestInvalidV0LocaleHoldsTheInvitation(t *testing.T) {
	s := snapshot()
	s.Users = []v0.User{{ID: bob, Email: "bob@example.com", Role: "translator", Locales: []string{"not a locale"}}}
	plan, err := v0.BuildDBPlan("de", s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if i := plan.Invitations[0]; i.Status != "held" || !strings.Contains(i.Reason, "not a locale") {
		t.Errorf("invitation = %+v", i)
	}
}

func TestHistoryBecomesAnAuditEntryPlan(t *testing.T) {
	plan, err := v0.BuildDBPlan("de", snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.AuditEntries) != 4 {
		t.Fatalf("audit entries = %+v", plan.AuditEntries)
	}
	byID := map[int64]v0.AuditEntry{}
	for _, e := range plan.AuditEntries {
		if e.Action != "v0.translation.changed" {
			t.Errorf("action = %q", e.Action)
		}
		byID[e.V0ID] = e
	}
	if e := byID[1]; e.Actor != "v0:"+bob || e.Key != "cart.items" || e.Locale != "en" || e.Before != nil || *e.After != "x" {
		t.Errorf("entry 1 = %+v", e)
	}
	if e := byID[2]; e.Actor != "v0:ai:openai" {
		t.Errorf("entry 2 = %+v", e)
	}
	// A user-kind change with no changed_by: the actor is unknown, not invented.
	if e := byID[3]; e.Actor != "v0:unknown" || e.Key != "" || e.Unresolved == "" || e.V0TranslationID != "gone" {
		t.Errorf("entry 3 = %+v", e)
	}
	if e := byID[4]; e.Actor != "v0:system:bootstrap" {
		t.Errorf("entry 4 = %+v", e)
	}
	only, err := v0.BuildDBPlan("de", snapshot(), map[string]bool{"en": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range only.AuditEntries {
		if e.Locale == "fr" {
			t.Errorf("--locales en planned fr history: %+v", e)
		}
	}
}

func TestEveryUncarriedV0FieldIsReportedWithAReason(t *testing.T) {
	plan, err := v0.BuildDBPlan("de", snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]v0.NotCarried{}
	for _, n := range plan.NotCarried {
		if n.Why == "" {
			t.Errorf("%s has no reason", n.What)
		}
		got[n.What] = n
	}
	for _, what := range []string{
		"users.password_hash", "project_api_keys", "ai_translation_providers", "analytics_events", "keys.first_seen_at",
		"locales.label", "locales.created_at", "tenants", "projects.name", "projects.created_at",
		"translations (source locale): status, updated_by, updated_at",
	} {
		if _, ok := got[what]; !ok {
			t.Errorf("%s is not reported as not carried", what)
		}
	}
	if got["project_api_keys"].Count != 2 || got["analytics_events"].Count != 40 || got["users.password_hash"].Count != 3 {
		t.Errorf("counts = %+v", got)
	}
}

func containsWith(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
