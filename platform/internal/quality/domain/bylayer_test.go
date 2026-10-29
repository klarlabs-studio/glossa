package domain_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func at(layer domain.Layer, severity domain.Severity) domain.Finding {
	return domain.Finding{Layer: layer, Severity: severity}
}

// TestByLayerCountsInReportOrder: the breakdown is the number both
// surfaces show, so its order is the layers' own and its arithmetic is
// the run's (RFC 0005 §12.3).
func TestByLayerCountsInReportOrder(t *testing.T) {
	got := domain.ByLayer([]domain.Finding{
		at(domain.LayerCompleteness, domain.Error),
		at(domain.LayerStructure, domain.Error),
		at(domain.LayerCompleteness, domain.Warning),
		at(domain.LayerTerminology, domain.Waived),
		at(domain.LayerCompleteness, domain.Error),
	})
	want := []domain.LayerCount{
		{Layer: domain.LayerStructure, Counts: domain.Counts{Errors: 1}},
		{Layer: domain.LayerCompleteness, Counts: domain.Counts{Errors: 2, Warnings: 1}},
		{Layer: domain.LayerTerminology, Counts: domain.Counts{Waived: 1}},
	}
	if len(got) != len(want) {
		t.Fatalf("by layer = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("by layer[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestByLayerLeavesOutALayerThatFoundNothing: this counts what was
// reported. Which layers ran at all is CheckRun.Layers, and a table
// that answered both questions would answer neither.
func TestByLayerLeavesOutALayerThatFoundNothing(t *testing.T) {
	got := domain.ByLayer([]domain.Finding{at(domain.LayerParity, domain.Warning)})
	if len(got) != 1 || got[0].Layer != domain.LayerParity {
		t.Fatalf("by layer = %+v, want only parity", got)
	}
	if n := len(domain.ByLayer(nil)); n != 0 {
		t.Fatalf("by layer of nothing = %d rows, want none", n)
	}
}

// TestByLayerAddsUpToTheRunsCounts is the property the breakdown
// exists for: a per-layer table whose columns do not sum to the run's
// totals is a table that lets the two surfaces disagree quietly.
func TestByLayerAddsUpToTheRunsCounts(t *testing.T) {
	fs := []domain.Finding{
		at(domain.LayerStructure, domain.Error),
		at(domain.LayerParity, domain.Error),
		at(domain.LayerCompleteness, domain.Warning),
		at(domain.LayerLength, domain.Waived),
		// A layer nobody has heard of is still a finding: dropping it
		// would break exactly this sum.
		at(domain.Layer("invented"), domain.Warning),
	}
	var total domain.Counts
	for _, f := range fs {
		total.Count(f)
	}
	var sum domain.Counts
	for _, l := range domain.ByLayer(fs) {
		sum.Errors += l.Counts.Errors
		sum.Warnings += l.Counts.Warnings
		sum.Waived += l.Counts.Waived
	}
	if sum != total {
		t.Fatalf("the layers sum to %+v, the run counts %+v", sum, total)
	}
}
