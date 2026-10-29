package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

var (
	t0  = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	en  = bcp47.MustParse("en")
	ada = domain.Author("person:0192f0c4-0000-7000-8000-000000000001")
)

func content(t *testing.T, syntax mfcontent.Syntax, text string) mfcontent.Content {
	t.Helper()
	c, err := mfcontent.Parse(syntax, text, en)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return c
}

func newMessage(t *testing.T) (domain.Message, domain.SourceRevision) {
	t.Helper()
	m, rev, err := domain.NewMessage(domain.NewProjectID(), "checkout.pay",
		domain.Details{Namespace: domain.DefaultNamespace}, content(t, mfcontent.MF1, "Pay now"), ada, t0)
	if err != nil {
		t.Fatal(err)
	}
	return m, rev
}

func TestMessageKeys(t *testing.T) {
	for _, ok := range []string{"checkout.pay", "a", "checkout.payment.submit", "x_1.y-2", "0.1"} {
		if _, err := domain.ParseMessageKey(ok); err != nil {
			t.Errorf("ParseMessageKey(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Checkout.pay", "checkout..pay", ".pay", "pay.", "pay now", "pay/now",
		"ünïcode", strings.Repeat("a", domain.MaxKeyLen+1)} {
		if _, err := domain.ParseMessageKey(bad); !errors.Is(err, domain.ErrInvalidKey) {
			t.Errorf("ParseMessageKey(%q) = %v, want ErrInvalidKey", bad, err)
		}
	}
}

func TestNamespaces(t *testing.T) {
	if ns, err := domain.ParseNamespace(""); err != nil || ns != domain.DefaultNamespace {
		t.Errorf("empty namespace = %q, %v", ns, err)
	}
	for _, bad := range []string{"-x", "A", "a.b", strings.Repeat("a", 65)} {
		if _, err := domain.ParseNamespace(bad); !errors.Is(err, domain.ErrInvalidNamespace) {
			t.Errorf("ParseNamespace(%q) = %v", bad, err)
		}
	}
}

func TestNewMessageStartsAtRevisionOne(t *testing.T) {
	m, rev := newMessage(t)
	if m.Revision != 1 || rev.Number != 1 || rev.MessageID != m.ID || m.Version != 1 {
		t.Errorf("message = %+v, revision = %+v", m, rev)
	}
	if m.State != domain.MessageActive || rev.Author != ada {
		t.Errorf("state %q author %q", m.State, rev.Author)
	}
}

// Intent §8: changing the text never changes the message's identity.
func TestRevisingSourceKeepsIdentity(t *testing.T) {
	m, _ := newMessage(t)
	id, key := m.ID, m.Key

	rev, changed, err := m.ReviseSource(content(t, mfcontent.MF1, "Complete payment"), ada, t0.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("revise: changed=%v err=%v", changed, err)
	}
	if m.ID != id || m.Key != key {
		t.Error("revising the source changed the message's identity")
	}
	if rev.Number != 2 || m.Revision != 2 || m.Version != 2 || m.Source.Text != "Complete payment" {
		t.Errorf("after revise: message %+v, revision %d", m, rev.Number)
	}
}

func TestRevisionNumbersAreGapless(t *testing.T) {
	m, _ := newMessage(t)
	for i, text := range []string{"A", "B", "C"} {
		rev, changed, err := m.ReviseSource(content(t, mfcontent.MF1, text), ada, t0)
		if err != nil || !changed || rev.Number != i+2 {
			t.Fatalf("revision %d: %+v changed=%v err=%v", i+2, rev, changed, err)
		}
	}
}

// Re-pushing the same message, in either syntax, is not a revision.
func TestSameModelIsNoRevision(t *testing.T) {
	m, _ := newMessage(t)
	_, _, _ = m.ReviseSource(content(t, mfcontent.MF1, "Hello {name}"), ada, t0)
	before := m
	for _, c := range []mfcontent.Content{content(t, mfcontent.MF1, "Hello {name}"), content(t, mfcontent.MF2, "Hello {$name}")} {
		_, changed, err := m.ReviseSource(c, ada, t0.Add(time.Hour))
		if err != nil || changed {
			t.Errorf("same model revised: changed=%v err=%v", changed, err)
		}
	}
	if m.Revision != before.Revision || m.Version != before.Version || m.Source.Syntax != mfcontent.MF1 {
		t.Errorf("no-op revise changed the message: %+v", m)
	}
}

func TestObsoleteMessageCannotBeRevisedUntilReactivated(t *testing.T) {
	m, _ := newMessage(t)
	if !m.Obsolete(t0) || m.Obsolete(t0) {
		t.Fatal("Obsolete should change once")
	}
	if _, _, err := m.ReviseSource(content(t, mfcontent.MF1, "New"), ada, t0); !errors.Is(err, domain.ErrMessageObsolete) {
		t.Errorf("revise obsolete: %v", err)
	}
	if !m.Reactivate(t0) || m.Reactivate(t0) {
		t.Fatal("Reactivate should change once")
	}
	if _, changed, err := m.ReviseSource(content(t, mfcontent.MF1, "New"), ada, t0); err != nil || !changed {
		t.Errorf("revise after reactivation: %v", err)
	}
	if m.Version != 4 {
		t.Errorf("version = %d, want 4 (obsolete, reactivate, revise)", m.Version)
	}
}

func TestProposedMessageLifecycle(t *testing.T) {
	m, rev, err := domain.NewProposedMessage(domain.NewProjectID(), "checkout.new",
		domain.Details{Namespace: domain.DefaultNamespace}, content(t, mfcontent.MF1, "New"), ada, t0)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != domain.MessageProposed || m.Revision != 1 || rev.Number != 1 || !m.Translatable() {
		t.Fatalf("proposed = %+v", m)
	}
	// Its source can be revised while proposed: that is its own history.
	if _, changed, err := m.ReviseSource(content(t, mfcontent.MF1, "Newer"), ada, t0); err != nil || !changed {
		t.Fatalf("revise proposed: %v %v", changed, err)
	}
	if m.Reactivate(t0) {
		t.Error("Reactivate took a proposed message live")
	}
	if !m.Obsolete(t0) || m.Translatable() {
		t.Fatal("obsolete")
	}
	if m.Activate(t0) {
		t.Error("Activate took an obsolete message live")
	}
	if !m.Repropose(t0) || m.Repropose(t0) || m.State != domain.MessageProposed {
		t.Fatalf("repropose: %+v", m)
	}
	if !m.Activate(t0) || m.Activate(t0) || m.State != domain.MessageActive {
		t.Fatalf("activate: %+v", m)
	}
	if m.Repropose(t0) {
		t.Error("an active message was proposed again")
	}
	if m.Version != 5 {
		t.Errorf("version = %d, want 5 (revise, obsolete, repropose, activate)", m.Version)
	}
	if s, err := domain.ParseMessageState("proposed"); err != nil || s != domain.MessageProposed {
		t.Errorf("ParseMessageState(proposed) = %q, %v", s, err)
	}
}

func TestRenameKeepsIDAndRevisions(t *testing.T) {
	m, _ := newMessage(t)
	id, rev := m.ID, m.Revision
	if !m.Rename("checkout.submit", t0) || m.Rename("checkout.submit", t0) {
		t.Fatal("Rename should change once")
	}
	if m.ID != id || m.Revision != rev || m.Key != "checkout.submit" || m.Version != 2 {
		t.Errorf("after rename: %+v", m)
	}
}

func TestChangeDetails(t *testing.T) {
	m, _ := newMessage(t)
	limit := 20
	changed, err := m.ChangeDetails(domain.Details{Namespace: "emails", Description: "Pay button", MaxLength: &limit}, t0)
	if err != nil || !changed || m.Revision != 1 || m.Version != 2 {
		t.Fatalf("change details: changed=%v err=%v %+v", changed, err, m)
	}
	same := 20
	if changed, _ := m.ChangeDetails(domain.Details{Namespace: "emails", Description: "Pay button", MaxLength: &same}, t0); changed {
		t.Error("equal details reported a change")
	}
	zero := 0
	if _, err := m.ChangeDetails(domain.Details{Namespace: "emails", MaxLength: &zero}, t0); !errors.Is(err, domain.ErrInvalidMaxLength) {
		t.Errorf("max_length 0: %v", err)
	}
	if _, err := m.ChangeDetails(domain.Details{Namespace: "emails", Description: strings.Repeat("x", 2001)}, t0); !errors.Is(err, domain.ErrInvalidDescription) {
		t.Errorf("long description: %v", err)
	}
}

func TestSnapshotCarriesOrderingVersion(t *testing.T) {
	m, _ := newMessage(t)
	_, _, _ = m.ReviseSource(content(t, mfcontent.MF1, "Other"), ada, t0)
	s := domain.SnapshotOf(m)
	if s.MessageID != m.ID.String() || s.SourceRevision != 2 || s.Version != 2 || s.Key != "checkout.pay" || s.State != "active" {
		t.Errorf("snapshot = %+v", s)
	}
}
