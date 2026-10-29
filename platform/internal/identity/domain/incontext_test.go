package domain_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestParseOriginCanonicalizes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://preview.example.com", "https://preview.example.com"},
		{"HTTPS://Preview.Example.COM", "https://preview.example.com"},
		{"https://preview.example.com:443", "https://preview.example.com"},
		{"https://preview.example.com:8443", "https://preview.example.com:8443"},
		{"  https://preview.example.com  ", "https://preview.example.com"},
		// http is a developer's own machine, and only that.
		{"http://localhost:5173", "http://localhost:5173"},
		{"http://localhost:80", "http://localhost"},
		{"http://127.0.0.1:3000", "http://127.0.0.1:3000"},
		{"http://[::1]:3000", "http://[::1]:3000"},
		{"http://app.localhost:4000", "http://app.localhost:4000"},
	} {
		got, err := domain.ParseOrigin(tc.in)
		if err != nil {
			t.Errorf("ParseOrigin(%q): %v", tc.in, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("ParseOrigin(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseOriginRefuses(t *testing.T) {
	for _, in := range []string{
		"",
		"example.com",                         // no scheme
		"https://",                            // no host
		"http://preview.example.com",          // http off loopback
		"http://evil.com/#.localhost",         // a suffix trick, not a loopback name
		"ftp://preview.example.com",           // not a web origin
		"https://preview.example.com/editor",  // a path
		"https://preview.example.com?x=1",     // a query
		"https://preview.example.com#f",       // a fragment
		"https://user@preview.example.com",    // userinfo
		"https://preview.example.com:0",       // no such port
		"https://preview.example.com:99999",   // no such port
		"https://" + strings.Repeat("a", 300), // too long
		"*",
		"null",
	} {
		if got, err := domain.ParseOrigin(in); err == nil {
			t.Errorf("ParseOrigin(%q) = %q, want an error", in, got)
		}
	}
}

func TestOriginDevelopmentIsPlainHTTP(t *testing.T) {
	local, _ := domain.ParseOrigin("http://localhost:5173")
	shared, _ := domain.ParseOrigin("https://preview.example.com")
	if !local.Development() {
		t.Error("a loopback http origin is a development one")
	}
	if shared.Development() {
		t.Error("an https origin is not a development one")
	}
}

func TestInContextSecretIsItsOwnCredential(t *testing.T) {
	secret, err := domain.NewInContextSecret()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret.String(), domain.InContextGrantPrefix) {
		t.Fatalf("secret %q lacks the grant prefix", secret)
	}
	if !domain.IsInContextSecret(secret.String()) {
		t.Error("IsInContextSecret should recognize its own secret")
	}
	// An API token is never mistaken for a grant, or the reverse: the
	// prefix decides which table a credential is checked against.
	if _, err := domain.ParseTokenSecret(secret.String()); err == nil {
		t.Error("ParseTokenSecret accepted an in-context grant")
	}
	api, err := domain.NewTokenSecret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.ParseInContextSecret(api.String()); err == nil {
		t.Error("ParseInContextSecret accepted an API token")
	}
	if domain.IsInContextSecret(api.String()) {
		t.Error("IsInContextSecret should not recognize an API token")
	}
}

func TestGrantIntersectKeepsLocaleScopes(t *testing.T) {
	scope, err := domain.ParseLocaleScope([]string{"de"})
	if err != nil {
		t.Fatal(err)
	}
	// A translator limited to de: translations.write and
	// intelligence.translate are locale-scoped, the reads are not.
	person := domain.GrantForMember(domain.Roles{domain.RoleTranslator}, scope)
	got := person.Intersect(domain.InContextPermissions()...)

	want := []domain.Permission{
		domain.PermCatalogRead, domain.PermIntelligenceRead, domain.PermIntelligenceTranslate,
		domain.PermKnowledgeRead, domain.PermTranslationsRead, domain.PermTranslationsWrite,
	}
	if !slices.Equal(got.Permissions(), want) {
		t.Errorf("permissions = %v, want %v", got.Permissions(), want)
	}
	de, _ := domain.ParseLocale("de")
	fr, _ := domain.ParseLocale("fr")
	if !got.AllowsFor(domain.PermTranslationsWrite, de) {
		t.Error("the grant should keep translations.write for de")
	}
	if got.AllowsFor(domain.PermTranslationsWrite, fr) {
		t.Error("the grant must not widen translations.write to fr")
	}
	if !got.Allows(domain.PermCatalogRead) {
		t.Error("catalog.read is not locale-scoped and should be unrestricted")
	}
	// Nothing outside the ceiling survives, whatever the person may do.
	admin := domain.GrantForMember(domain.Roles{domain.RoleAdmin}, domain.LocaleScope{})
	cut := admin.Intersect(domain.InContextPermissions()...)
	for _, p := range []domain.Permission{
		domain.PermTranslationsReview, domain.PermReleasesPublish, domain.PermTokensManage,
		domain.PermCatalogWrite, domain.PermKnowledgeWrite, domain.PermIntelligenceManage,
	} {
		if cut.Allows(p) {
			t.Errorf("an in-context grant must never allow %s", p)
		}
	}
	if !slices.Equal(cut.Permissions(), want) {
		t.Errorf("an owner's grant should cut to %v, got %v", want, cut.Permissions())
	}
}

func TestGrantLocaleScopesRoundTrip(t *testing.T) {
	scope, _ := domain.ParseLocaleScope([]string{"pt-BR", "de"})
	original := domain.GrantForMember(domain.Roles{domain.RoleReviewer}, scope).
		Intersect(domain.InContextPermissions()...)

	back, err := domain.GrantFromLocaleScopes(original.LocaleScopes())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(back.Permissions(), original.Permissions()) {
		t.Fatalf("permissions = %v, want %v", back.Permissions(), original.Permissions())
	}
	for _, p := range original.Permissions() {
		wantScope, _ := original.Locales(p)
		gotScope, ok := back.Locales(p)
		if !ok || !slices.Equal(gotScope.Strings(), wantScope.Strings()) {
			t.Errorf("%s locales = %v, want %v", p, gotScope.Strings(), wantScope.Strings())
		}
	}
	if _, err := domain.GrantFromLocaleScopes(map[domain.Permission][]string{"not.a.permission": nil}); err == nil {
		t.Error("an unknown permission should not rebuild into a grant")
	}
}

func TestNewInContextGrantBindsAndExpires(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	tenant, project := tenancy.NewID(), domain.ProjectRef(domain.NewPersonID().UUID())
	person := domain.NewPersonID()
	origin, _ := domain.ParseOrigin("https://preview.example.com")
	person1 := domain.GrantForMember(domain.Roles{domain.RoleDeveloper}, domain.LocaleScope{})

	g, secret, err := domain.NewInContextGrant(tenant, project, person, origin, person1, now)
	if err != nil {
		t.Fatal(err)
	}
	if g.ExpiresAt.Sub(now) != domain.InContextGrantTTL {
		t.Errorf("TTL = %v, want %v", g.ExpiresAt.Sub(now), domain.InContextGrantTTL)
	}
	if g.Hash != secret.Hash() || g.Hash == "" {
		t.Error("the grant stores the secret's hash and nothing else")
	}
	if err := g.CheckUsable(now.Add(domain.InContextGrantTTL - time.Second)); err != nil {
		t.Errorf("a grant inside its TTL should be usable: %v", err)
	}
	if err := g.CheckUsable(g.ExpiresAt); err == nil {
		t.Error("a grant at its expiry should not be usable")
	}
	if !g.BoundTo(origin) {
		t.Error("the grant should be bound to the origin it was minted for")
	}
	other, _ := domain.ParseOrigin("https://evil.example.com")
	if g.BoundTo(other) {
		t.Error("the grant must not be usable from another origin")
	}
	if g.BoundTo(domain.Origin{}) {
		t.Error("a request with no origin is not the bound origin")
	}
}

func TestNewInContextGrantRefusesAnEmptyIntersection(t *testing.T) {
	now := time.Now().UTC()
	origin, _ := domain.ParseOrigin("https://preview.example.com")
	// A grant holding nothing an editor uses opens no editor session.
	empty := domain.GrantOf(domain.PermReleasesPublish)
	_, _, err := domain.NewInContextGrant(
		tenancy.NewID(), domain.ProjectRef(domain.NewPersonID().UUID()), domain.NewPersonID(), origin, empty, now)
	if err == nil {
		t.Fatal("want an error for a grant that allows nothing in context")
	}
}
