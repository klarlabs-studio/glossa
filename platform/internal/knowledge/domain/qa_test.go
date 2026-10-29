package domain_test

import (
	"fmt"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func qaTermbase(t *testing.T) *domain.Termbase {
	t.Helper()
	return domain.NewTermbase([]domain.Concept{
		concept(t, "workspace",
			"en:preferred:workspace",
			"de:preferred:Arbeitsbereich", "de:forbidden:Workspace", "de:deprecated:Projektbereich",
			"fr:preferred:espace de travail", "fr:admitted:espace",
			"es:preferred:espacio de trabajo", "es:forbidden:workspace",
			"ja:preferred:ワークスペース", "ja:forbidden:作業領域"),
		concept(t, "account", "en:preferred:account", "de:preferred:Konto", "fr:preferred:compte",
			"es:preferred:cuenta", "ja:preferred:アカウント"),
		// A source-only concept: nothing to require in a target.
		concept(t, "brand", "en:preferred:Brotwerk!"),
		// Konto is a bank account's preferred term and deprecated for a
		// user profile.
		concept(t, "profile", "en:preferred:profile", "de:preferred:Profil", "de:deprecated:Konto"),
	})
}

// findings renders QA findings as "code(severity) side:text@start-end → suggestions".
func findings(fs []domain.TermFinding) string {
	var out []string
	for _, f := range fs {
		s := fmt.Sprintf("%s(%s) %s:%s@%d-%d", f.Code, f.Severity, f.Side, f.Text, f.Start, f.End)
		if len(f.Suggestions) > 0 {
			s += " → " + strings.Join(f.Suggestions, "|")
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}

func TestCheckTerminology(t *testing.T) {
	tb := qaTermbase(t)
	tests := []struct {
		name, source, target, targetLocale, want string
	}{
		{"de: compliant", "Open your workspace", "Öffne deinen Arbeitsbereich", "de", ""},
		{"de: inflected target term is compliant", "Your workspaces", "Deine Arbeitsbereiche", "de", ""},
		{"de: missing term", "Open your workspace", "Öffne deinen Bereich", "de",
			"term_missing(warning) source:workspace@10-19 → Arbeitsbereich"},
		{"de: forbidden term used", "Open your workspace", "Öffne deinen Workspace", "de",
			"term_missing(warning) source:workspace@10-19 → Arbeitsbereich; " +
				"term_forbidden(error) target:Workspace@14-23 → Arbeitsbereich"},
		{"de: deprecated term is a warning", "Open your workspace", "Öffne deinen Arbeitsbereich im Projektbereich", "de",
			"term_forbidden(warning) target:Projektbereich@32-46 → Arbeitsbereich"},
		{"de: deprecated for one concept, required by another", "Delete account", "Konto löschen", "de", ""},
		{"de: deprecated when the source means the other concept", "Edit profile", "Konto bearbeiten", "de",
			"term_missing(warning) source:profile@5-12 → Profil; term_forbidden(warning) target:Konto@0-5 → Profil"},
		{"de-AT uses de terms", "Open your workspace", "Öffnen Sie Ihren Workspace", "de-AT",
			"term_missing(warning) source:workspace@10-19 → Arbeitsbereich; " +
				"term_forbidden(error) target:Workspace@18-27 → Arbeitsbereich"},
		{"fr: admitted term satisfies", "Your workspace", "Votre espace", "fr", ""},
		{"fr: two concepts", "Link account to workspace", "Associer le compte à l'espace de travail", "fr", ""},
		{"fr: missing", "Delete account", "Supprimer le profil", "fr", "term_missing(warning) source:account@7-14 → compte"},
		{"es: forbidden anglicism", "Your workspace", "Tu workspace", "es",
			"term_missing(warning) source:workspace@5-14 → espacio de trabajo; " +
				"term_forbidden(error) target:workspace@3-12 → espacio de trabajo"},
		{"es: compliant plural", "Your accounts", "Tus cuentas", "es", ""},
		{"ja: compliant", "Create a workspace", "ワークスペースを作成", "ja", ""},
		{"ja: forbidden", "Create a workspace", "作業領域を作成", "ja",
			"term_missing(warning) source:workspace@9-18 → ワークスペース; term_forbidden(error) target:作業領域@0-12 → ワークスペース"},
		{"a concept without target terms requires nothing", "Brotwerk account", "Brotwerk-Konto", "de", ""},
		{"no terms, no findings", "Hello", "Hallo", "de", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findings(domain.CheckTerminology(tb, tc.source, bcp47.MustParse("en"), tc.target, bcp47.MustParse(tc.targetLocale)))
			if got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestCheckTerminologyFindsEveryForbiddenUse(t *testing.T) {
	fs := domain.CheckTerminology(qaTermbase(t), "Hello", bcp47.MustParse("en"), "Workspace und Workspace", bcp47.MustParse("de"))
	if got := findings(fs); got != "term_forbidden(error) target:Workspace@0-9 → Arbeitsbereich; term_forbidden(error) target:Workspace@14-23 → Arbeitsbereich" {
		t.Errorf("findings = %q", got)
	}
	for _, f := range fs {
		if f.Message == "" || f.TermID == [16]byte{} {
			t.Errorf("finding lacks a message or term: %+v", f)
		}
	}
}
