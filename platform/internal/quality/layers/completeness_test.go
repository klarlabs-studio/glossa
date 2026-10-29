package layers_test

import (
	"fmt"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// orphanProject is a project read from the server whose catalog has
// obsoleted `help.legacy.title` while its translations stayed behind.
func orphanProject() *layers.Project {
	return &layers.Project{
		Origin: "server", SourceLocale: "de",
		Locales: []layers.Locale{{Code: "de", IsSource: true}, {Code: "fr"}, {Code: "ja"}},
		Messages: []layers.Message{
			{ID: "0192f5a1-0000-0000-0000-00000000000a", Key: "checkout.pay", Namespace: "default"},
		},
		Translations: map[string]map[string]layers.Translation{
			"fr": {"checkout.pay": {Key: "checkout.pay", Locale: "fr", State: "approved"}},
			"ja": {"checkout.pay": {Key: "checkout.pay", Locale: "ja", State: "approved"}},
		},
	}
}

const obsoleteID = "0192f5a1-0000-0000-0000-00000000000b"

func unknownKeys(fs []domain.Finding) []domain.Finding {
	var out []domain.Finding
	for _, f := range fs {
		if f.Code == checkpolicy.CodeUnknownKey {
			out = append(out, f)
		}
	}
	return out
}

// A translation whose message the catalog has obsoleted is reported:
// before, it silently vanished from every check.
func TestCompletenessReportsTranslationsOfObsoleteMessages(t *testing.T) {
	p := orphanProject()
	p.Orphans = []layers.Orphan{
		{MessageID: obsoleteID, Key: "help.legacy.title", Namespace: "help", Locale: "ja", Revision: "tr-ja"},
		{MessageID: obsoleteID, Key: "help.legacy.title", Namespace: "help", Locale: "fr", Revision: "tr-fr"},
	}
	got := unknownKeys(layers.Completeness{}.Check(p, checkpolicy.Policy{}))
	if len(got) != 2 {
		t.Fatalf("unknown-key findings = %+v, want one per orphaned translation", got)
	}
	for i, locale := range []string{"fr", "ja"} {
		f := got[i]
		want := domain.Locus{
			Message: obsoleteID, Key: "help.legacy.title", Namespace: "help", Locale: locale, Revision: "tr-" + locale,
		}
		if f.Locus != want {
			t.Errorf("locus = %+v, want %+v", f.Locus, want)
		}
		if f.Layer != domain.LayerCompleteness || f.Severity != domain.Warning {
			t.Errorf("finding = %+v, want a completeness warning", f)
		}
		// The identity is the obsolete message's ID, which is what every
		// surface that reads the server computes — the terminal, the
		// server's own run and the pull request that renders it.
		if print := domain.Fingerprint(domain.LayerCompleteness, checkpolicy.CodeUnknownKey,
			domain.Locus{Message: obsoleteID, Locale: locale}, ""); f.Fingerprint != print {
			t.Errorf("fingerprint = %s, want %s", f.Fingerprint, print)
		}
	}
}

// An orphan in a locale the project no longer has, or in its source
// locale, or one whose key an active message has since taken, is not a
// key the product asks for in vain: none of them is reported.
func TestCompletenessIgnoresOrphansTheProjectCannotShip(t *testing.T) {
	p := orphanProject()
	p.Orphans = []layers.Orphan{
		{MessageID: obsoleteID, Key: "help.legacy.title", Locale: "it"},
		{MessageID: obsoleteID, Key: "help.legacy.title", Locale: "de"},
		{MessageID: "0192f5a1-0000-0000-0000-00000000000c", Key: "checkout.pay", Locale: "fr"},
	}
	if got := unknownKeys(layers.Completeness{}.Check(p, checkpolicy.Policy{})); len(got) != 0 {
		t.Errorf("unknown-key findings = %+v, want none", got)
	}
}

// A project that obsoleted thousands of messages reports the first
// MaxOrphans orphaned translations by key, and says in one more finding
// that there are more, rather than dropping them in silence.
func TestCompletenessBoundsOrphansAndSaysSo(t *testing.T) {
	p := orphanProject()
	// Read in no particular order, and from more than one listing.
	for i := layers.MaxOrphans + 4; i >= 0; i-- {
		p.Orphans = append(p.Orphans, layers.Orphan{
			MessageID: fmt.Sprintf("0192f5a1-0000-0000-0000-%012d", i), Key: fmt.Sprintf("old.%04d", i), Locale: "fr",
		})
	}
	got := unknownKeys(layers.Completeness{}.Check(p, checkpolicy.Policy{}))
	if len(got) != layers.MaxOrphans+1 {
		t.Fatalf("%d unknown-key findings, want %d and one that says there are more", len(got), layers.MaxOrphans+1)
	}
	if got[0].Locus.Key != "old.0000" || got[layers.MaxOrphans-1].Locus.Key != fmt.Sprintf("old.%04d", layers.MaxOrphans-1) {
		t.Errorf("reported %s … %s, want the first %d by key", got[0].Locus.Key, got[layers.MaxOrphans-1].Locus.Key,
			layers.MaxOrphans)
	}
	more := got[layers.MaxOrphans]
	if more.Locus != (domain.Locus{}) || more.Severity != domain.Warning || more.Fingerprint == "" {
		t.Errorf("the roll-up = %+v, want a project-wide warning", more)
	}
	// Exactly MaxOrphans is not more.
	p.Orphans = p.Orphans[len(p.Orphans)-layers.MaxOrphans:]
	if got := unknownKeys(layers.Completeness{}.Check(p, checkpolicy.Policy{})); len(got) != layers.MaxOrphans {
		t.Errorf("%d unknown-key findings for exactly %d orphans", len(got), layers.MaxOrphans)
	}
	// A reader that stopped at a page it did not follow says so, and the
	// layer believes it whatever it was handed.
	p.Orphans, p.MoreOrphans = p.Orphans[:3], true
	got = unknownKeys(layers.Completeness{}.Check(p, checkpolicy.Policy{}))
	if len(got) != 4 || got[3].Locus != (domain.Locus{}) {
		t.Errorf("unknown-key findings = %+v, want the three read and the roll-up", got)
	}
}
