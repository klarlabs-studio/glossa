package terminology

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func finding(sev string) remote.TermFinding {
	return remote.TermFinding{Code: "term_forbidden", Severity: apiclient.TermFindingSeverity(sev), Side: "target",
		Text: "Einkaufswagen", Message: "forbidden", Suggestions: []string{"Warenkorb"}}
}

// Pages are summed: checked counts per locale, findings by locale (in
// the order asked for) and key.
func TestRunSumsTheServersPages(t *testing.T) {
	var queries []remote.TermFindingsQuery
	fetch := func(_ context.Context, q remote.TermFindingsQuery, fn func(remote.TermFindingsPage) error) error {
		queries = append(queries, q)
		pages := []remote.TermFindingsPage{
			{Checked: map[string]int{"de": 2, "fr": 1}, Items: []remote.TranslationFindings{
				{MessageKey: "b", Locale: "de", Findings: []remote.TermFinding{finding("warning")}},
			}},
			{Checked: map[string]int{"de": 1}, Items: []remote.TranslationFindings{
				{MessageKey: "a", Locale: "fr", Findings: []remote.TermFinding{finding("error")}},
				{MessageKey: "a", Locale: "de", Findings: []remote.TermFinding{finding("error"), finding("warning")}},
			}},
		}
		for _, p := range pages {
			if err := fn(p); err != nil {
				return err
			}
		}
		return nil
	}
	r, err := Run(context.Background(), Options{Locales: []string{"fr", "de"}, States: []string{"approved"}}, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || !slices.Equal(queries[0].Locales, []string{"fr", "de"}) || !slices.Equal(queries[0].States, []string{"approved"}) {
		t.Errorf("queries = %+v", queries)
	}
	if r.Checked != 4 || r.Errors != 2 || r.Warnings != 2 || len(r.Locales) != 2 || r.Locales[0].Code != "fr" ||
		r.Locales[0].Checked != 1 || r.Locales[1].Errors != 1 || r.Locales[1].Warnings != 2 {
		t.Fatalf("report = %+v", r)
	}
	var order []string
	for _, f := range r.Findings {
		order = append(order, f.Locale+" "+f.Key)
	}
	if fmt.Sprint(order) != "[fr a de a de a de b]" {
		t.Errorf("order = %v", order)
	}
	qf := r.QA()
	if len(qf) != 4 || qf[0].Layer != domain.LayerTerminology || qf[0].Severity != domain.Error ||
		qf[0].Locus.Locale != "fr" || qf[0].Locus.Key != "a" {
		t.Errorf("qa = %+v", qf)
	}
	// The span, the offending text and the allowed terms survive the
	// constructor; the conversion this replaced dropped all three.
	if qf[0].Locus.Span == nil || qf[0].Locus.Span.Side != domain.SideTarget ||
		qf[0].Subject == "" || qf[0].Evidence["suggestions"] == nil ||
		qf[0].Fix == nil || qf[0].Fingerprint == "" {
		t.Errorf("qa[0] lost what the server said: %+v", qf[0])
	}
}

// More than 20 locales take several requests (the server's limit).
func TestRunSplitsManyLocales(t *testing.T) {
	var locales []string
	for i := range 45 {
		locales = append(locales, fmt.Sprintf("x%02d", i))
	}
	var sizes []int
	r, err := Run(context.Background(), Options{Locales: locales}, func(_ context.Context, q remote.TermFindingsQuery, fn func(remote.TermFindingsPage) error) error {
		sizes = append(sizes, len(q.Locales))
		checked := map[string]int{}
		for _, l := range q.Locales {
			checked[l] = 1
		}
		return fn(remote.TermFindingsPage{Checked: checked})
	})
	if err != nil || fmt.Sprint(sizes) != "[20 20 5]" || r.Checked != 45 || len(r.Locales) != 45 {
		t.Errorf("sizes = %v, report = %+v, %v", sizes, r, err)
	}
}

func TestRunReturnsTheServersError(t *testing.T) {
	boom := errors.New("boom")
	_, err := Run(context.Background(), Options{Locales: []string{"de"}}, func(context.Context, remote.TermFindingsQuery, func(remote.TermFindingsPage) error) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// onePage answers with a single translation's findings in namespace.
func onePage(namespace string) Fetcher {
	return func(_ context.Context, _ remote.TermFindingsQuery, fn func(remote.TermFindingsPage) error) error {
		return fn(remote.TermFindingsPage{Checked: map[string]int{"de": 1}, Items: []remote.TranslationFindings{
			{MessageKey: "terms.accept", Namespace: namespace, Locale: "de",
				Findings: []remote.TermFinding{finding("error")}},
		}})
	}
}

// The namespace the server reports crosses into the finding's locus.
//
// It is what a policy rule selects on: `{layer: terminology, namespace:
// legal, severity: error}` can only ever match a finding that carries
// one, and a selector that silently matches nothing is worse than one
// that errors — the policy reads as if it does something.
func TestTheNamespaceReachesTheLocus(t *testing.T) {
	r, err := Run(context.Background(), Options{Locales: []string{"de"}}, onePage("legal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || r.Findings[0].Namespace != "legal" {
		t.Fatalf("report = %+v", r.Findings)
	}
	qf := r.QA()
	if len(qf) != 1 || qf[0].Locus.Namespace != "legal" {
		t.Fatalf("locus = %+v, want the namespace the server reported", qf[0].Locus)
	}
}

// And it does not move the fingerprint. `locus.namespace` is not one of
// the five hashed parts (RFC 0005 §2.1), so filling it is context and
// never identity: every waiver written against a terminology finding
// before it was filled still matches after.
func TestTheNamespaceDoesNotMoveTheFingerprint(t *testing.T) {
	prints := map[string]string{}
	for _, ns := range []string{"", "legal", "marketing"} {
		r, err := Run(context.Background(), Options{Locales: []string{"de"}}, onePage(ns))
		if err != nil {
			t.Fatal(err)
		}
		prints[ns] = r.QA()[0].Fingerprint
	}
	if prints[""] != prints["legal"] || prints[""] != prints["marketing"] {
		t.Errorf("the namespace moved the print: %v", prints)
	}
	// And it is the print the five parts alone give.
	want := domain.Fingerprint(domain.LayerTerminology, "term_forbidden",
		domain.Locus{Key: "terms.accept", Locale: "de"}, "Einkaufswagen")
	if prints["legal"] != want {
		t.Errorf("fingerprint = %s, want %s", prints["legal"], want)
	}
}

// revisionPage answers with a single finding computed against source
// revision rev.
func revisionPage(rev int) Fetcher {
	return func(_ context.Context, _ remote.TermFindingsQuery, fn func(remote.TermFindingsPage) error) error {
		return fn(remote.TermFindingsPage{Checked: map[string]int{"de": 1}, Items: []remote.TranslationFindings{
			{MessageKey: "terms.accept", Locale: "de", SourceRevision: rev,
				Findings: []remote.TermFinding{finding("warning")}},
		}})
	}
}

// The source revision the server checked against reaches the finding.
//
// A waiver dies when the source it was made against moves (RFC 0005
// §2.3), and domain.Waiver.Stale can only see that on a finding that
// says which revision it was computed against. Without one, a waived
// terminology finding never came back, whatever happened to the German
// under it.
func TestTheSourceRevisionReachesTheFinding(t *testing.T) {
	r, err := Run(context.Background(), Options{Locales: []string{"de"}}, revisionPage(7))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || r.Findings[0].SourceRevision != 7 {
		t.Fatalf("report = %+v", r.Findings)
	}
	qf := r.QA()
	if len(qf) != 1 || qf[0].SourceRevision == nil || *qf[0].SourceRevision != 7 {
		t.Fatalf("finding = %+v, want source revision 7", qf)
	}
	// And the waiver rule now has something to disagree with.
	w := domain.Waiver{Fingerprint: qf[0].Fingerprint, Reason: "known", SourceRevision: 6}
	if !w.Stale(qf[0]) {
		t.Errorf("a waiver made against revision 6 still covers a finding at 7")
	}
}

// The revision is context, not identity: it is not one of the five
// hashed parts (RFC 0005 §2.1), so recording it moves no print, and a
// waiver stored against a terminology finding before it was recorded
// still names the same finding.
func TestTheSourceRevisionDoesNotMoveTheFingerprint(t *testing.T) {
	prints := map[int]string{}
	for _, rev := range []int{0, 1, 9} {
		r, err := Run(context.Background(), Options{Locales: []string{"de"}}, revisionPage(rev))
		if err != nil {
			t.Fatal(err)
		}
		prints[rev] = r.QA()[0].Fingerprint
	}
	if prints[0] != prints[1] || prints[0] != prints[9] {
		t.Errorf("the revision moved the print: %v", prints)
	}
	// A server that names none gives a finding that claims none.
	r, _ := Run(context.Background(), Options{Locales: []string{"de"}}, revisionPage(0))
	if got := r.QA()[0].SourceRevision; got != nil {
		t.Errorf("source revision = %d, want none when the server named none", *got)
	}
}
