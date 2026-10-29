package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The approvals_at_least guard reads Subject.Approvers and applies its
// own distinct_from_author: what an approval reports must make the
// guard agree with the approval's own count.
func TestApproversFeedTheGuard(t *testing.T) {
	guard, _, err := guardPrimitives["approvals_at_least"].compile([]byte(`{"n":2,"distinct_from_author":true}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	a, err := NewApproval(uuid.New(), ApprovalSubject{Kind: SubjectTranslation, ID: uuid.New(), Locale: "de"},
		2, RoleAssignee("reviewer"), true, nil, "person:"+uuid.NewString(), now)
	if err != nil {
		t.Fatal(err)
	}
	author, first := "person:"+uuid.NewString(), "person:"+uuid.NewString()
	grant := func(who string) {
		t.Helper()
		if _, err := a.Decide(Ballot{Principal: who, Verdict: VerdictGranted, Eligible: true, Author: author, At: now}); err != nil {
			t.Fatal(err)
		}
	}
	grant(first)
	grant(first)
	step := Step{Subject: Subject{Author: author, Approvers: a.Approvers()}}
	if guard(step) {
		t.Fatal("one person granting twice passed a two-approval guard")
	}
	grant("person:" + uuid.NewString())
	step.Subject.Approvers = a.Approvers()
	if !guard(step) || a.State != ApprovalGranted {
		t.Fatalf("two distinct grants: guard %v, state %s", guard(step), a.State)
	}
}
