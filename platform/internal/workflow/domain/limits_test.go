package domain_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The limits of RFC 0006 §9.6 that a definition carries, tested at the
// boundary: the largest allowed document saves, one more is refused.

// chain is a definition of n states: n-1 waiting states, each moved on
// by a review, and a final one.
func chain(n int) []byte {
	states := map[string]any{}
	for i := range n - 1 {
		next := fmt.Sprintf("s%d", i+1)
		if i == n-2 {
			next = "done"
		}
		states[fmt.Sprintf("s%d", i)] = map[string]any{"type": "atomic", "transitions": []any{
			map[string]any{"event": "translation.reviewed", "target": next},
		}}
	}
	states["done"] = map[string]any{"type": "final"}
	doc, _ := json.Marshal(map[string]any{
		"schema": domain.SchemaV1, "name": "chain", "subject": "translation",
		"chart":  map[string]any{"id": "chain", "initial": "s0", "states": states},
		"guards": map[string]any{}, "actions": map[string]any{},
	})
	return doc
}

func refusedFor(t *testing.T, err error, rule string) {
	t.Helper()
	var inv *domain.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("err = %v, want *InvalidError", err)
	}
	if !slices.ContainsFunc(inv.Findings, func(f domain.Finding) bool { return f.Rule == rule }) {
		t.Fatalf("findings %v do not include %s", inv.Findings, rule)
	}
}

func TestAChartHoldsAtMost200States(t *testing.T) {
	if _, err := domain.Compile(chain(domain.MaxStates)); err != nil {
		t.Fatalf("%d states: %v", domain.MaxStates, err)
	}
	_, err := domain.Compile(chain(domain.MaxStates + 1))
	refusedFor(t, err, domain.RuleTooManyStates)
}

func TestADocumentIsAtMost256KiB(t *testing.T) {
	doc := chain(3)
	pad := func(n int) []byte { return append(slices.Clone(doc), bytes.Repeat([]byte(" "), n-len(doc))...) }
	if _, err := domain.Compile(pad(domain.MaxDocumentBytes)); err != nil {
		t.Fatalf("a document of exactly %d bytes: %v", domain.MaxDocumentBytes, err)
	}
	_, err := domain.Compile(pad(domain.MaxDocumentBytes + 1))
	refusedFor(t, err, domain.RuleEnvelope)
}
