package cli

import (
	"strings"
	"testing"
)

func TestTMSearchFindsExactAndFuzzyMatches(t *testing.T) {
	srv, w := seeded(t)
	srv.addUnit("Pay {amount, number}", "Zahle {amount, number}", "en", "de", "checkout.pay")
	srv.addUnit("Pay now", "Jetzt zahlen", "en", "de", "")
	srv.addUnit("Pay {amount, number}", "Payer {amount, number}", "en", "fr", "")

	var out tmSearchJSON
	w.json(&out, "tm", "search", "Pay", "{amount,", "number}", "--to", "de").want(t, ExitOK)
	if out.Schema != "glossa.cli.tm.search/v1" || out.Query.From != "en" || out.Query.To != "de" || out.Query.Syntax != "mf1" ||
		out.Query.Text != "Pay {amount, number}" || len(out.Matches) != 1 || out.Matches[0].Score != 100 || out.Matches[0].Kind != "exact" ||
		out.Matches[0].Unit.MessageKey != "checkout.pay" || out.Matches[0].Unit.ProjectID == nil ||
		out.Matches[0].TargetText != "Zahle {amount, number}" || out.Matches[0].TargetSyntax != "mf1" {
		t.Fatalf("search = %+v", out)
	}
	w.json(&out, "tm", "search", "Pay", "--to", "de").want(t, ExitOK)
	if len(out.Matches) != 2 || out.Matches[1].Kind != "fuzzy" || out.Matches[1].TargetText != out.Matches[1].Target ||
		out.Matches[1].TargetSyntax != "mf2" {
		t.Errorf("fuzzy search (no target_text from the server) = %+v", out)
	}

	h := w.run("tm", "search", "Pay now", "--to", "de")
	h.want(t, ExitOK)
	if !strings.Contains(h.stdout, "1 match for \"Pay now\" (en → de)") || !strings.Contains(h.stdout, "Jetzt zahlen") {
		t.Errorf("human output:\n%s", h.stdout)
	}
	if h := w.run("tm", "search", "Nothing like it", "--to", "de"); !strings.Contains(h.stdout, "No translation-memory matches") {
		t.Errorf("no matches:\n%s", h.stdout)
	}

	w.run("tm", "search", "Pay").want(t, ExitUsage)                // --to is required
	w.run("tm", "search", "--to", "de").want(t, ExitUsage)         // text is required
	w.run("tm", "search", "Pay", "--to", "nöt").want(t, ExitUsage) // not a locale
	w.run("tm", "search", "Pay", "--to", "de", "--syntax", "icu").want(t, ExitUsage)
	var e errorDoc
	w.json(&e, "tm", "search", "Pay", "--to", "fr-CA").want(t, ExitUsage) // the server refuses the locale
	if e.Error.Code != "invalid_locale" || e.Error.Fix == "" {
		t.Errorf("server-refused locale = %+v", e)
	}
}

func TestTMConcordanceSearchesEitherSide(t *testing.T) {
	srv, w := seeded(t)
	srv.addUnit("Pay now", "Jetzt zahlen", "en", "de", "")
	var out tmConcordanceJSON
	w.json(&out, "tm", "concordance", "zahlen", "--side", "target").want(t, ExitOK)
	if out.Schema != "glossa.cli.tm.concordance/v1" || out.Query.Side != "target" || len(out.Matches) != 1 || out.Matches[0].Unit.Target != "Jetzt zahlen" {
		t.Fatalf("concordance = %+v", out)
	}
	w.json(&out, "tm", "concordance", "zahlen").want(t, ExitOK)
	if len(out.Matches) != 0 {
		t.Errorf("source side = %+v", out)
	}
	w.run("tm", "concordance", "x", "--side", "both").want(t, ExitUsage)
}

func TestTMUnitsListsAndRetires(t *testing.T) {
	srv, w := seeded(t)
	u := srv.addUnit("Pay now", "Jetzt zahlen", "en", "de", "checkout.pay")
	srv.addUnit("Pay now", "Payer", "en", "fr", "")

	var out tmUnitsJSON
	w.json(&out, "tm", "units", "--locale-pair", "en:de").want(t, ExitOK)
	if out.Schema != "glossa.cli.tm.units/v1" || len(out.Units) != 1 || out.Units[0].ID != u.id || out.Units[0].State != "active" {
		t.Fatalf("units = %+v", out)
	}
	h := w.run("tm", "units")
	if !strings.Contains(h.stdout, "Jetzt zahlen") || !strings.Contains(h.stdout, "en→fr") {
		t.Errorf("human units:\n%s", h.stdout)
	}

	var retired tmRetireJSON
	w.json(&retired, "tm", "units", "--retire", u.id).want(t, ExitOK)
	if retired.Schema != "glossa.cli.tm.retire/v1" || retired.Unit.State != "retired" || retired.Unit.RetiredReason != "deleted" {
		t.Fatalf("retire = %+v", retired)
	}
	w.json(&out, "tm", "units", "--locale-pair", "en:de").want(t, ExitOK)
	if len(out.Units) != 0 {
		t.Errorf("active after retire = %+v", out)
	}
	w.json(&out, "tm", "units", "--state", "retired").want(t, ExitOK)
	if len(out.Units) != 1 {
		t.Errorf("retired = %+v", out)
	}
	var e errorDoc
	w.json(&e, "tm", "units", "--retire", "00000000-0000-4000-8000-999999999999").want(t, ExitNetwork)
	if e.Error.Code != "not_found" {
		t.Errorf("retire unknown = %+v", e)
	}
	w.run("tm", "units", "--locale-pair", "en-de").want(t, ExitUsage)
	w.run("tm", "units", "--state", "gone").want(t, ExitUsage)
	w.run("tm", "units", "extra").want(t, ExitUsage)
	w.run("tm").want(t, ExitUsage)
	w.run("tm", "merge").want(t, ExitUsage)
}
