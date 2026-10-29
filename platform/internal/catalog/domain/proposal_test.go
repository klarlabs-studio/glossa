package domain_test

import (
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

func TestProposals(t *testing.T) {
	b := newBranch(t)
	m, _ := newMessage(t)
	p := domain.ProposeSourceChange(b.ID, m, content(t, mfcontent.MF1, "Pay securely"), ada, t0)
	if p.Kind != domain.ProposalSourceChange || p.BaseRevision != 1 || p.MessageID != m.ID || p.Key != m.Key {
		t.Fatalf("source change = %+v", p)
	}
	if p.Matches(m) {
		t.Error("a change matches the source it changes")
	}
	_, _, _ = m.ReviseSource(content(t, mfcontent.MF2, "Pay securely"), ada, t0)
	if !p.Matches(m) {
		t.Error("a change doesn't match the source it became")
	}

	nm, _, _ := domain.NewProposedMessage(m.ProjectID, "checkout.new", domain.Details{Namespace: domain.DefaultNamespace},
		content(t, mfcontent.MF1, "New"), ada, t0)
	k := domain.ProposeNewKey(b.ID, nm, nm.Source, ada, t0)
	if k.Kind != domain.ProposalNewKey || k.BaseRevision != 0 || !k.Matches(nm) {
		t.Fatalf("new key = %+v", k)
	}
	// A new source for the same key updates the proposal in place.
	if !k.Update(content(t, mfcontent.MF1, "Brand new"), 0, t0.Add(time.Minute)) ||
		k.Update(content(t, mfcontent.MF1, "Brand new"), 0, t0) {
		t.Error("Update should change once")
	}
	if k.Matches(nm) || !k.UpdatedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("after update: %+v", k)
	}
	// A change proposed again against a newer revision is rebased.
	if !p.Update(p.Source, 2, t0) || p.BaseRevision != 2 {
		t.Errorf("rebase: %+v", p)
	}
}
