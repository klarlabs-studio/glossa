package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

var reviewers = domain.ApprovalParty{Role: "reviewer"}

func TestNewApprovalPolicy(t *testing.T) {
	if _, err := domain.NewApprovalPolicy(2, reviewers, true); err != nil {
		t.Fatalf("two reviewers: %v", err)
	}
	for name, c := range map[string]struct {
		n        int
		from     domain.ApprovalParty
		distinct bool
	}{
		"no approvals":           {0, reviewers, true},
		"eleven approvals":       {11, reviewers, true},
		"no party":               {1, domain.ApprovalParty{}, true},
		"two parties":            {1, domain.ApprovalParty{Role: "reviewer", Group: "legal"}, true},
		"a ref with spaces":      {1, domain.ApprovalParty{Group: "the legal team"}, true},
		"self-approval (§15 q6)": {1, reviewers, false},
	} {
		if _, err := domain.NewApprovalPolicy(c.n, c.from, c.distinct); !errors.Is(err, domain.ErrInvalidApproval) {
			t.Errorf("%s: err = %v, want ErrInvalidApproval", name, err)
		}
	}
}

// n distinct people, never the requester, one entry per person however
// often they granted.
func TestApprovalIsSatisfiedByDistinctPeopleOtherThanTheRequester(t *testing.T) {
	a, _ := domain.NewApprovalPolicy(2, reviewers, true)
	for name, c := range map[string]struct {
		grants []string
		want   bool
	}{
		"none":                       {nil, false},
		"one":                        {[]string{"person:a"}, false},
		"one, twice":                 {[]string{"person:a", "person:a"}, false},
		"the requester and one":      {[]string{"person:req", "person:a"}, false},
		"two distinct":               {[]string{"person:a", "person:b"}, true},
		"two distinct and requester": {[]string{"person:req", "person:a", "person:b"}, true},
	} {
		if got := a.SatisfiedBy(c.grants, "person:req"); got != c.want {
			t.Errorf("%s: satisfied = %v, want %v", name, got, c.want)
		}
	}
}

func TestEnvironmentApprovalIsVersionedAndNeverOnABranch(t *testing.T) {
	now := time.Now()
	e, _ := domain.NewEnvironment(uuid.New(), "production", domain.DefaultPolicy("production"), now)
	a, _ := domain.NewApprovalPolicy(2, reviewers, true)
	if changed, err := e.ChangeApproval(&a, now); err != nil || !changed || e.Version != 2 || e.Approval.N != 2 {
		t.Fatalf("set: %v %v, env %+v", changed, err, e)
	}
	same := a
	if changed, err := e.ChangeApproval(&same, now); err != nil || changed || e.Version != 2 {
		t.Fatalf("setting the same requirement: %v %v, version %d", changed, err, e.Version)
	}
	if changed, err := e.ChangeApproval(nil, now); err != nil || !changed || e.Approval != nil || e.Version != 3 {
		t.Fatalf("clear: %v %v, env %+v", changed, err, e)
	}
	b, _ := domain.NewBranchEnvironment(uuid.New(), "feature/x", 7, now)
	if _, err := b.ChangeApproval(&a, now); !errors.Is(err, domain.ErrApprovalOnBranch) {
		t.Fatalf("a branch environment requiring approval: err = %v", err)
	}
}

func TestReleaseRequestLifecycle(t *testing.T) {
	now := time.Now()
	e, _ := domain.NewEnvironment(uuid.New(), "production", domain.DefaultPolicy("production"), now)
	if _, err := domain.NewReleaseRequest(uuid.New(), e, uuid.New(), domain.ActionPublish, "person:req",
		domain.GateVerdict{Met: true}, domain.Override{}, now); err == nil {
		t.Fatal("a request into an environment that requires no approval")
	}
	a, _ := domain.NewApprovalPolicy(2, reviewers, true)
	_, _ = e.ChangeApproval(&a, now)
	if _, err := domain.NewReleaseRequest(uuid.New(), e, uuid.New(), domain.ActionRollback, "person:req",
		domain.GateVerdict{Met: true}, domain.Override{}, now); err == nil {
		t.Fatal("a rollback was held for approval: rollback never waits (§5.1)")
	}

	forced, _ := domain.NewOverride("ship it")
	r, err := domain.NewReleaseRequest(uuid.New(), e, uuid.New(), domain.ActionPublish, "person:req",
		domain.GateVerdict{Unmet: []string{"de must be complete"}}, forced, now)
	if err != nil || !r.Open() || r.Approval.N != 2 || !r.Override.Forced || r.Version != 1 {
		t.Fatalf("request = %+v, %v", r, err)
	}

	deployed := r
	if err := deployed.Deploy("person:b", now); err != nil || deployed.State != domain.RequestDeployed ||
		deployed.DecidedBy != "person:b" || deployed.Version != 2 {
		t.Fatalf("deploy: %+v, %v", deployed, err)
	}
	for name, close := range map[string]func(*domain.ReleaseRequest) error{
		"deploy":   func(r *domain.ReleaseRequest) error { return r.Deploy("person:c", now) },
		"deny":     func(r *domain.ReleaseRequest) error { return r.Deny("person:c", now) },
		"withdraw": func(r *domain.ReleaseRequest) error { return r.Withdraw("person:req", "", now) },
		"refuse":   func(r *domain.ReleaseRequest) error { return r.Refuse("person:c", "gate", now) },
	} {
		closed := deployed
		if err := close(&closed); !errors.Is(err, domain.ErrRequestClosed) {
			t.Errorf("%s after a deploy: err = %v, want ErrRequestClosed", name, err)
		}
	}

	refused := r
	if err := refused.Refuse("person:b", "de must be complete", now); err != nil || refused.State != domain.RequestRefused || refused.Reason == "" {
		t.Fatalf("refuse: %+v, %v", refused, err)
	}
	denied := r
	if err := denied.Deny("person:a", now); err != nil || denied.State != domain.RequestDenied {
		t.Fatalf("deny: %+v, %v", denied, err)
	}
	withdrawn := r
	long := make([]byte, domain.MaxForceReasonLen+1)
	for i := range long {
		long[i] = 'x'
	}
	if err := withdrawn.Withdraw("person:req", string(long), now); !errors.Is(err, domain.ErrInvalidWithdrawReason) {
		t.Fatalf("an overlong reason: %v", err)
	}
	if err := withdrawn.Withdraw("person:req", " not today ", now); err != nil || withdrawn.Reason != "not today" {
		t.Fatalf("withdraw: %+v, %v", withdrawn, err)
	}

	held := &domain.HeldError{Request: r}
	if !errors.Is(held, domain.ErrApprovalRequired) {
		t.Error("a HeldError is not ErrApprovalRequired")
	}
	refusal := &domain.DeployRefusedError{Request: refused, Cause: &domain.PolicyNotMetError{Environment: "production"}}
	if !errors.Is(refusal, domain.ErrPolicyNotMet) {
		t.Error("a refused deploy does not say why")
	}
}

func TestVerdictOf(t *testing.T) {
	if v := domain.VerdictOf(nil); !v.Met || len(v.Unmet) != 0 {
		t.Errorf("a met gate = %+v", v)
	}
	v := domain.VerdictOf(&domain.PolicyNotMetError{Environment: "production", Unmet: []domain.Unmet{{Locale: "de", Detail: "de must be complete"}}})
	if v.Met || len(v.Unmet) != 1 || v.Unmet[0] != "de must be complete" {
		t.Errorf("an unmet gate = %+v", v)
	}
}
