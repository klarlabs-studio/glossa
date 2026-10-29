package app_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	integrationapp "go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The fingerprint is load-bearing in five places, and it became so one
// wave at a time: the CLI's layers (wave 1), the capture ingest
// (wave 4), the pull-request check (wave 4), `createCheckRun` (wave 5)
// and waiver matching (wave 2, then again in wave 6). Wave 4 found the
// CLI and the server disagreeing about one; wave 6 proved a
// CLI-created waiver matches server-side. Each of those was pinned
// where it was found.
//
// This file pins the property itself, once: **one logical finding has
// one fingerprint, wherever it is produced.** It is the property every
// waiver rests on — a waiver names a finding by fingerprint and by
// nothing else — and it is exactly the kind of property that holds in
// five separate tests and still breaks, because none of them compares
// two surfaces to each other.
//
// The golden values in quality/domain/fingerprint_golden_test.go are
// the other half: these tests say the surfaces agree, and that one says
// what they agree *on*, so a change that moves every surface together
// is still caught.

// stableID is the catalog's ID for `checkout.pay`. The server knows it;
// an offline `glossa check` does not, and that asymmetry is the whole
// reason the property is hard.
var stableID = uuid.MustParse("0192f5a1-0000-7000-8000-0000000000fe")

// stableProject is the catalog the layers grade: an English source that
// takes an argument, and a French translation that drops it. Message
// IDs are present only when the reader is the server.
func stableProject(t *testing.T, withIDs bool) *layers.Project {
	t.Helper()
	m := msg(t, "checkout.pay", "Pay {amount, number}")
	m.Namespace = "checkout"
	if withIDs {
		m.ID = stableID.String()
	}
	fr := tr(t, "checkout.pay", "fr", "Payer")
	fr.SourceRevision = 7
	return &layers.Project{
		Origin:   "server",
		Locales:  []layers.Locale{{Code: "en", IsSource: true}, {Code: "fr"}},
		Messages: []layers.Message{m},
		Translations: map[string]map[string]layers.Translation{
			"fr": {"checkout.pay": fr},
		},
	}
}

// missingArgument picks the one finding this fixture is about out of a
// report, so a test fails on "the layer stopped producing it" rather
// than on an index.
func missingArgument(t *testing.T, fs []domain.Finding) domain.Finding {
	t.Helper()
	for _, f := range fs {
		if f.Layer == domain.LayerParity && f.Code == string(mf.FindingMissingArgument) {
			return f
		}
	}
	t.Fatalf("the fixture stopped producing a missing-argument finding: %+v", fs)
	return domain.Finding{}
}

// TestOneFindingKeepsOneFingerprintAcrossEverySurface drives the same
// logical finding — "the French checkout.pay drops {amount}" — through
// every producer there is, and asserts they all mint one string.
func TestOneFindingKeepsOneFingerprintAcrossEverySurface(t *testing.T) {
	// 1. The layers, as the server runs them. This is the reference:
	//    every other surface has to arrive here.
	server := stableProject(t, true)
	fromLayers := missingArgument(t, app.Run(server, checkpolicy.Policy{}, layers.Parity{}).Findings)
	want := fromLayers.Fingerprint

	prints := map[string][]string{want: {"the layers, on the server"}}
	agree := func(what, got string) {
		t.Helper()
		prints[got] = append(prints[got], what)
		if got != want {
			t.Errorf("%s minted %s, want %s", what, got, want)
		}
	}

	// 2. `glossa check` against the server. It runs the same layers over
	//    the snapshot it read, which carries the catalog's IDs, so it
	//    arrives at the reference by running the same code — the one
	//    property RFC 0005 §2.2 rule 1 exists to protect.
	agree("`glossa check` over a server snapshot",
		missingArgument(t, app.Run(stableProject(t, true), checkpolicy.Policy{}, layers.Parity{}).Findings).Fingerprint)

	// 3. `glossa check --offline`, over local catalog files with no IDs
	//    at all, and then Project.Identify — which is how the CLI gives a
	//    finding measured without a catalog the identity the catalog
	//    would have given it.
	offline := missingArgument(t, app.Run(stableProject(t, false), checkpolicy.Policy{}, layers.Parity{}).Findings)
	if offline.Fingerprint == want {
		t.Error("a key and a message ID fingerprinted the same; one of them is not being hashed")
	}
	identified := server.Identify([]domain.Finding{offline})
	agree("the CLI's layers, identified against the catalog", identified[0].Fingerprint)

	// 4. createCheckRun. The CLI posts what it found by *key* — a
	//    reporter has no message ID and `ReportedFinding` has no
	//    fingerprint field to hand one in — and the ingest resolves the
	//    key and seals it. This is the seam wave 4 found broken.
	store := recordingStore()
	svc, catalog, project := serviceAndCatalog(store)
	catalog.messages = map[string]uuid.UUID{"checkout.pay": stableID}
	rev := 7
	if _, err := svc.ReportCheckRun(writeCtx(t), app.ReportCheckRun{
		Project: project, Ref: "feat/checkout", Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{{
			Layer: offline.Layer, Code: offline.Code, Severity: offline.Severity,
			Locus: app.ReportedLocus{
				Key: offline.Locus.Key, Locale: offline.Locus.Locale, Namespace: offline.Locus.Namespace,
				// A reporter's file and line, which the print excludes on
				// purpose: reformatting a file must not re-open a waiver.
				File: "src/checkout/PaymentFooter.vue", Line: 42,
			},
			Explanation: "the translation doesn't use {amount}", Subject: offline.Subject,
			Detail: offline.Detail, SourceRevision: &rev,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("stored %d findings", len(store.inserted))
	}
	agree("createCheckRun, sealing a reported key", store.inserted[0].Fingerprint)

	// 5. The pull-request check. It renders domain.Finding directly and
	//    may fill in a file:line from the usages on the way — which must
	//    not move the print, or the pull request and the terminal would
	//    disagree about what a waiver covers (RFC 0005 §12.3).
	report := integrationapp.BuildCheckReport(integrationapp.CheckInput{
		Policy: checkpolicy.Policy{},
		Status: integrationapp.BranchStatus{Name: "feat/checkout"},
		Quality: integrationapp.BranchQuality{
			Locales: []string{"fr"}, Findings: []domain.Finding{fromLayers},
		},
		Usages: integrationapp.BranchUsages{
			Unknown: []integrationapp.UnknownKey{{Key: "checkout.pay", File: "src/Pay.vue", Line: 9}},
		},
		Now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
	})
	agree("the pull-request check", missingArgument(t, report.Findings).Fingerprint)

	// 6. Waiver matching. A waiver written against any one of the above
	//    accepts the finding every other one produces, which is the only
	//    reason any of this matters.
	waiver := domain.Waiver{
		ID: uuid.New(), Fingerprint: store.inserted[0].Fingerprint, Reason: "the French button says Payer on purpose",
		Scope: domain.WaiverProject, SourceRevision: 7,
	}
	waived := domain.Waivers([]domain.Waiver{waiver}, []domain.Finding{fromLayers}, "feat/checkout", time.Now())
	if waived[0].Severity != domain.Waived {
		t.Errorf("a waiver made from the CLI's report did not cover the server's own finding (%+v)", waived[0])
	}

	if len(prints) != 1 {
		t.Errorf("the surfaces minted %d different fingerprints: %v", len(prints), prints)
	}
}

// TestTheCaptureIngestAndCaptureCheckAgree: the visual layer's
// measurements are taken in a browser, which holds keys and never the
// catalog's IDs, so both surfaces that seal them have to resolve the ID
// first — the ingest from the upload, `glossa capture --check` through
// Project.Identify — or a waiver written in the terminal would not match
// the one the server stored.
func TestTheCaptureIngestAndCaptureCheckAgree(t *testing.T) {
	store := recordingStore()
	svc, project := serviceFor(store)
	if _, err := svc.RecordVisualFindings(writeCtx(t), upload(project, clipped())); err != nil {
		t.Fatal(err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("stored %d findings", len(store.inserted))
	}
	server := store.inserted[0].Fingerprint

	// `glossa capture --check`: the same probe finding, sealed by
	// PromoteVisual after the CLI has resolved the key against the
	// project it read.
	catalog := &layers.Project{
		Origin:   "server",
		Locales:  []layers.Locale{{Code: "en", IsSource: true}, {Code: "fr"}},
		Messages: []layers.Message{{ID: messageID.String(), Key: "checkout.pay"}},
	}
	probe := domain.Finding{
		Layer: domain.LayerVisual, Code: "text-clipped", Severity: domain.Warning,
		Locus:   domain.Locus{Key: "checkout.pay", Capture: capture.String(), Region: "r_0"},
		Message: "Clipped: 412x20 px of text in 358x20 px.",
	}
	local, _ := layers.PromoteVisual(layers.Seen{},
		[]layers.Probed{{
			Scope:    layers.VisualScope{Route: "/checkout", Width: 390, Height: 844, Locale: "fr"},
			Findings: catalog.Identify([]domain.Finding{probe}),
		}}, checkpolicy.Policy{}.Visual())
	if len(local.Findings) != 1 {
		t.Fatalf("the local pass produced %d findings", len(local.Findings))
	}
	if got := local.Findings[0].Fingerprint; got != server {
		t.Errorf("`glossa capture --check` minted %s, the ingest %s", got, server)
	}

	// And the probe's own print, without the catalog, is a different
	// one — which is why neither surface may skip the resolution.
	unresolved, _ := layers.PromoteVisual(layers.Seen{},
		[]layers.Probed{{
			Scope:    layers.VisualScope{Route: "/checkout", Width: 390, Height: 844, Locale: "fr"},
			Findings: []domain.Finding{probe},
		}}, checkpolicy.Policy{}.Visual())
	if unresolved.Findings[0].Fingerprint == server {
		t.Error("a key and a message ID fingerprinted the same; the resolution is doing nothing")
	}
}

// A change that should move the fingerprint has to move it on every
// surface at once, or one of them will keep a waiver alive that the
// others have retired. RFC 0005 §2.1 names the five things that are
// hashed; this walks them through the seam most likely to lose one —
// createCheckRun, which rebuilds the finding from a reporter's fields.
func TestTheFiveHashedPartsMoveThePrintThroughTheIngest(t *testing.T) {
	base := app.ReportedFinding{
		Layer: domain.LayerParity, Code: "argument_missing", Severity: domain.Error,
		Locus:       app.ReportedLocus{Key: "checkout.pay", Locale: "fr", Namespace: "checkout"},
		Explanation: "the translation doesn't use {amount}", Subject: "amount",
	}
	seal := func(t *testing.T, f app.ReportedFinding, known map[string]uuid.UUID) string {
		t.Helper()
		store := recordingStore()
		svc, catalog, project := serviceAndCatalog(store)
		catalog.messages = known
		if _, err := svc.ReportCheckRun(writeCtx(t), app.ReportCheckRun{
			Project: project, Ref: "main", Trigger: domain.TriggerCLI,
			Layers: []domain.Layer{domain.LayerParity}, Findings: []app.ReportedFinding{f},
		}); err != nil {
			t.Fatal(err)
		}
		return store.inserted[0].Fingerprint
	}
	want := seal(t, base, nil)

	// The four the print excludes: rewording, relocating, regrading and
	// a new source revision must all leave it alone.
	rev := 9
	for what, mutate := range map[string]func(*app.ReportedFinding){
		"a reworded explanation": func(f *app.ReportedFinding) { f.Explanation = "something else entirely" },
		"a moved file":           func(f *app.ReportedFinding) { f.Locus.File, f.Locus.Line = "src/Other.vue", 900 },
		"a tightened policy":     func(f *app.ReportedFinding) { f.Severity = domain.Warning },
		"a new source revision":  func(f *app.ReportedFinding) { f.SourceRevision = &rev },
		"a padded subject":       func(f *app.ReportedFinding) { f.Subject = "  amount " },
	} {
		got := base
		mutate(&got)
		if fp := seal(t, got, nil); fp != want {
			t.Errorf("%s moved the fingerprint: %s -> %s", what, want, fp)
		}
	}

	// The five that are hashed: each has to move it, and to somewhere
	// nothing else went.
	seen := map[string]string{want: "the finding itself"}
	for what, mutate := range map[string]func(*app.ReportedFinding){
		"another layer":   func(f *app.ReportedFinding) { f.Layer = domain.LayerStructure },
		"another code":    func(f *app.ReportedFinding) { f.Code = "argument_extra" },
		"another key":     func(f *app.ReportedFinding) { f.Locus.Key = "checkout.total" },
		"another locale":  func(f *app.ReportedFinding) { f.Locus.Locale = "de" },
		"another subject": func(f *app.ReportedFinding) { f.Subject = "count" },
	} {
		got := base
		mutate(&got)
		fp := seal(t, got, nil)
		if other, ok := seen[fp]; ok {
			t.Errorf("%s collides with %s (%s)", what, other, fp)
		}
		seen[fp] = what
	}

	// And the sixth thing, which is the catalog's rather than the
	// finding's: a key the catalog knows hashes to the message, not the
	// key, so a rename never re-opens a waiver.
	byID := seal(t, base, map[string]uuid.UUID{"checkout.pay": stableID})
	if byID == want {
		t.Error("the ingest ignored the catalog's message ID")
	}
	renamed := base
	renamed.Locus.Key = "checkout.pay_now"
	if got := seal(t, renamed, map[string]uuid.UUID{"checkout.pay_now": stableID}); got != byID {
		t.Errorf("renaming a message the catalog knows by ID moved its fingerprint: %s -> %s", byID, got)
	}
}
