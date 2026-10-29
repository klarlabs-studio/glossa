package domain

import "sort"

// The per-layer breakdown of a run (RFC 0005 §12.3).
//
// The exit criterion of the milestone is that one commit's `glossa
// check` and its pull-request check agree on the conclusion, the error
// count and the counts per layer. Two surfaces that each grouped their
// own findings would agree by coincidence until the day they didn't, so
// the grouping lives here, once, beside the finding it groups.

// LayerCount is one layer's findings, counted by how the policy left
// them. Waived is counted on its own and is never inside Errors or
// Warnings, exactly as a run's Counts are (RFC 0005 §2.3).
type LayerCount struct {
	Layer  Layer  `json:"layer"`
	Counts Counts `json:"counts"`
}

// Total is every finding this layer reported.
func (l LayerCount) Total() int { return l.Counts.Total() }

// ByLayer groups fs by layer, in Layers order.
//
// A layer that found nothing is left out: this says what was reported,
// and which layers ran at all is CheckRun.Layers — a different question,
// and the one that tells "clean" from "not looked at". A finding from a
// layer this build has never heard of is kept, after the known ones, so
// that the columns always sum to the run's own counts.
func ByLayer(fs []Finding) []LayerCount {
	counts := map[Layer]*Counts{}
	for i := range fs {
		c, ok := counts[fs[i].Layer]
		if !ok {
			c = &Counts{}
			counts[fs[i].Layer] = c
		}
		c.Count(fs[i])
	}
	out := make([]LayerCount, 0, len(counts))
	for l, c := range counts {
		out = append(out, LayerCount{Layer: l, Counts: *c})
	}
	return SortLayerCounts(out)
}

// SortLayerCounts puts counts into Layers order, for the callers that
// count in SQL rather than over findings — the quality summary's second
// number does (RFC 0005 §8). A layer this build has never heard of is
// kept, after the known ones and in name order, so the columns always
// sum to the run's own counts.
func SortLayerCounts(cs []LayerCount) []LayerCount {
	rank := func(l Layer) int {
		for i, k := range Layers {
			if k == l {
				return i
			}
		}
		return len(Layers)
	}
	sort.SliceStable(cs, func(i, j int) bool {
		ri, rj := rank(cs[i].Layer), rank(cs[j].Layer)
		if ri != rj {
			return ri < rj
		}
		return cs[i].Layer < cs[j].Layer
	})
	return cs
}
