//go:build integration

package app_test

import (
	"fmt"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
)

// seededTM is a small product catalog's approved en→de memory.
var seededTM = map[string][2]string{
	"auth.sign_in":         {"Sign in", "Anmelden"},
	"auth.sign_out":        {"Sign out", "Abmelden"},
	"auth.forgot":          {"Forgot your password?", "Passwort vergessen?"},
	"auth.reset":           {"Reset your password", "Passwort zurücksetzen"},
	"auth.remember":        {"Remember me on this device", "Auf diesem Gerät angemeldet bleiben"},
	"cart.empty":           {"Your shopping cart is empty", "Dein Warenkorb ist leer"},
	"cart.add":             {"Add to shopping cart", "In den Warenkorb"},
	"cart.remove":          {"Remove {item} from your cart", "{item} aus dem Warenkorb entfernen"},
	"cart.items":           {"{count, plural, one {# item in your cart} other {# items in your cart}}", "{count, plural, one {# Artikel im Warenkorb} other {# Artikel im Warenkorb}}"},
	"checkout.pay":         {"Pay {amount, number} now", "Jetzt {amount, number} zahlen"},
	"checkout.total":       {"Order total", "Gesamtsumme"},
	"checkout.shipping":    {"Shipping address", "Lieferadresse"},
	"checkout.billing":     {"Billing address", "Rechnungsadresse"},
	"checkout.confirm":     {"Confirm your order", "Bestellung bestätigen"},
	"checkout.thanks":      {"Thank you for your order, {name}!", "Danke für deine Bestellung, {name}!"},
	"orders.none":          {"You have no orders yet", "Du hast noch keine Bestellungen"},
	"orders.track":         {"Track your package", "Sendung verfolgen"},
	"orders.cancel":        {"Cancel order", "Bestellung stornieren"},
	"settings.save":        {"Save changes", "Änderungen speichern"},
	"settings.discard":     {"Discard changes", "Änderungen verwerfen"},
	"settings.saved":       {"Your changes have been saved", "Deine Änderungen wurden gespeichert"},
	"settings.language":    {"Language", "Sprache"},
	"settings.notify":      {"Email notifications", "E-Mail-Benachrichtigungen"},
	"settings.delete":      {"Delete your account", "Konto löschen"},
	"settings.delete_warn": {"This will permanently delete your account and all data.", "Dadurch werden dein Konto und alle Daten dauerhaft gelöscht."},
	"errors.network":       {"Check your internet connection and try again.", "Prüfe deine Internetverbindung und versuche es erneut."},
	"errors.not_found":     {"We couldn't find that page", "Wir konnten diese Seite nicht finden"},
	"errors.generic":       {"Something went wrong", "Etwas ist schiefgelaufen"},
	"search.placeholder":   {"Search products", "Produkte suchen"},
	"search.none":          {"No results for {query}", "Keine Ergebnisse für {query}"},
	"search.filters":       {"Show filters", "Filter anzeigen"},
	"profile.edit":         {"Edit profile", "Profil bearbeiten"},
	"profile.photo":        {"Upload a profile photo", "Profilbild hochladen"},
	"profile.welcome":      {"Welcome back, {name}", "Willkommen zurück, {name}"},
	"help.contact":         {"Contact support", "Support kontaktieren"},
	"help.faq":             {"Frequently asked questions", "Häufig gestellte Fragen"},
	"invite.sent":          {"Invitation sent to {email}", "Einladung an {email} gesendet"},
	"invite.accept":        {"Accept invitation", "Einladung annehmen"},
	"team.members":         {"{count, plural, one {# member} other {# members}}", "{count, plural, one {# Mitglied} other {# Mitglieder}}"},
	"team.leave":           {"Leave team", "Team verlassen"},
}

// fuzzyCases are near-misses of seeded messages (edits a writer makes
// between releases) and the key each should find first.
var fuzzyCases = []struct{ query, want string }{
	{"Forgot password?", "auth.forgot"},
	{"Reset password", "auth.reset"},
	{"Remember me on this computer", "auth.remember"},
	{"Your shopping cart is currently empty", "cart.empty"},
	{"Add to cart", "cart.add"},
	{"Remove {product} from your shopping cart", "cart.remove"},
	{"{n, plural, one {# item in the cart} other {# items in the cart}}", "cart.items"},
	{"Pay {total, number}", "checkout.pay"},
	{"Shipping address (optional)", "checkout.shipping"},
	{"Billing addresses", "checkout.billing"},
	{"Please confirm your order", "checkout.confirm"},
	{"Thanks for your order, {user}!", "checkout.thanks"},
	{"You have no orders", "orders.none"},
	{"Track package", "orders.track"},
	{"Save all changes", "settings.save"},
	{"Your changes were saved", "settings.saved"},
	{"Email notification", "settings.notify"},
	{"Delete account", "settings.delete"},
	{"This permanently deletes your account and all of its data.", "settings.delete_warn"},
	{"Check your connection and try again.", "errors.network"},
	{"We could not find that page", "errors.not_found"},
	{"Something went wrong.", "errors.generic"},
	{"Search all products", "search.placeholder"},
	{"No results found for {q}", "search.none"},
	{"Edit your profile", "profile.edit"},
	{"Welcome back, {user}!", "profile.welcome"},
	{"Contact our support team", "help.contact"},
	{"Invitation was sent to {address}", "invite.sent"},
	{"{n, plural, one {# team member} other {# team members}}", "team.members"},
	{"Leave this team", "team.leave"},
}

// unrelated are messages with no counterpart in the memory.
var unrelated = []string{
	"Download the invoice as PDF",
	"Your subscription renews on {date}",
	"Dark mode",
	"Two-factor authentication is enabled",
	"Drag files here to upload",
	"Keyboard shortcuts",
	"Export to CSV",
	"Terms of service",
}

// TestFuzzyMatchingQuality measures fuzzy lookup (MinScore 50, top 3)
// on the seeded memory: how often the intended unit ranks first, how
// often it is in the top 3, and how many unrelated messages get any
// match at all. The thresholds pin today's quality so a change to
// normalization or scoring can't silently regress it.
func TestFuzzyMatchingQuality(t *testing.T) {
	h := newHarness(t)
	messages := map[string]string{}
	for k, pair := range seededTM {
		messages[k] = pair[0]
	}
	p := h.project(t, "shop", false, []string{"de"}, messages)
	for k, pair := range seededTM {
		h.translate(t, p, k, "de", pair[1], nil)
	}
	h.drain(t)
	byUnit := map[string]string{}
	for _, u := range units(t, h, app.UnitFilter{}) {
		byUnit[u.ID.String()] = u.MessageKey
	}
	if len(byUnit) != len(seededTM) {
		t.Fatalf("units = %d, want %d", len(byUnit), len(seededTM))
	}

	var top1, top3 int
	for _, c := range fuzzyCases {
		ms := lookup(t, h, app.TMQuery{ProjectID: &p, SourceLocale: tag("en"), TargetLocale: tag("de"), Source: mf1(t, c.query), Limit: 3})
		var got []string
		for i, m := range ms {
			key := byUnit[m.Unit.ID.String()]
			got = append(got, fmt.Sprintf("%s(%d)", key, m.Score))
			if key == c.want {
				if i == 0 {
					top1++
				}
				top3++
			}
		}
		if len(ms) == 0 || byUnit[ms[0].Unit.ID.String()] != c.want {
			t.Logf("miss: %q → %v, want %s", c.query, got, c.want)
		}
	}
	var falsePositives int
	for _, q := range unrelated {
		ms := lookup(t, h, app.TMQuery{ProjectID: &p, SourceLocale: tag("en"), TargetLocale: tag("de"), Source: mf1(t, q), Limit: 3})
		if len(ms) > 0 {
			falsePositives++
			t.Logf("false positive: %q → %s (%d)", q, byUnit[ms[0].Unit.ID.String()], ms[0].Score)
		}
	}
	n := len(fuzzyCases)
	t.Logf("fuzzy quality on %d near-misses over %d units: top-1 %d/%d (%.0f%%), top-3 %d/%d (%.0f%%); unrelated with a match: %d/%d",
		n, len(seededTM), top1, n, 100*float64(top1)/float64(n), top3, n, 100*float64(top3)/float64(n),
		falsePositives, len(unrelated))
	if float64(top1)/float64(n) < 0.8 || float64(top3)/float64(n) < 0.85 || falsePositives > 1 {
		t.Errorf("fuzzy quality regressed: top-1 %d/%d, top-3 %d/%d, false positives %d/%d",
			top1, n, top3, n, falsePositives, len(unrelated))
	}
}
