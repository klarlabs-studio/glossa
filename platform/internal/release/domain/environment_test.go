package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

func TestNewPolicy(t *testing.T) {
	p, err := domain.NewPolicy([]string{"approved", "draft", "approved"}, false)
	if err != nil || !slices.Equal(p.States, []string{"draft", "approved"}) || p.IncludeOutdated {
		t.Fatalf("policy = %+v, %v", p, err)
	}
	for _, bad := range [][]string{nil, {}, {"rejected"}, {"approved", "published"}} {
		if _, err := domain.NewPolicy(bad, true); !errors.Is(err, domain.ErrInvalidPolicy) {
			t.Errorf("NewPolicy(%v) = %v", bad, err)
		}
	}
}

func TestDefaultPolicies(t *testing.T) {
	cases := map[string][]string{
		"production":  {"approved"},
		"staging":     {"approved"},
		"preview":     {"draft", "needs_review", "approved"},
		"development": {"draft", "needs_review", "approved"},
		"pr-42":       {"draft", "needs_review", "approved"},
	}
	for env, want := range cases {
		if got := domain.DefaultPolicy(env); !slices.Equal(got.States, want) || !got.IncludeOutdated {
			t.Errorf("%s: %+v", env, got)
		}
	}
}

func TestPolicyCovers(t *testing.T) {
	prod := domain.DefaultPolicy("production")
	preview := domain.DefaultPolicy("preview")
	strict := domain.Policy{States: []string{"approved"}}
	cases := []struct {
		name        string
		target, rel domain.Policy
		want        bool
	}{
		{"same", prod, prod, true},
		{"preview release into production", prod, preview, false},
		{"production release into preview", preview, prod, true},
		{"outdated into a policy without outdated", strict, prod, false},
		{"no outdated into a policy with outdated", prod, strict, true},
	}
	for _, tc := range cases {
		if got := tc.target.Covers(tc.rel); got != tc.want {
			t.Errorf("%s: Covers = %v", tc.name, got)
		}
	}
}

// The default environments make one path to production work: publish
// to staging, promote to production. Development and preview ship work
// in progress, which production refuses.
func TestDefaultPromotionPath(t *testing.T) {
	prod := domain.DefaultPolicy(domain.Production)
	if !prod.Covers(domain.DefaultPolicy(domain.Staging)) {
		t.Error("a staging release can't be promoted to production")
	}
	for _, env := range []string{domain.Development, domain.Preview} {
		if prod.Covers(domain.DefaultPolicy(env)) {
			t.Errorf("a %s release (drafts) can be promoted to production", env)
		}
		if !domain.DefaultPolicy(env).Covers(prod) {
			t.Errorf("a production release can't be promoted to %s", env)
		}
	}
}

func TestIneligibleExplainsThePolicyMismatch(t *testing.T) {
	err := domain.Ineligible(domain.Production, domain.DefaultPolicy(domain.Production), 3, domain.Development, domain.DefaultPolicy(domain.Development))
	if !errors.Is(err, domain.ErrIneligible) {
		t.Fatalf("%v is not ErrIneligible", err)
	}
	msg := err.Error()
	for _, want := range []string{"v3", "development", "production", "draft, needs_review", "approved", "staging"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q lacks %q", msg, want)
		}
	}
	strict := domain.Policy{States: []string{"approved"}}
	msg = domain.Ineligible("qa", strict, 5, domain.Staging, domain.DefaultPolicy(domain.Staging)).Error()
	if !strings.Contains(msg, "outdated") {
		t.Errorf("%q doesn't say outdated text is the difference", msg)
	}
}

func TestEnvironment(t *testing.T) {
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	project := uuid.New()
	if _, err := domain.NewEnvironment(project, "a", domain.DefaultPolicy("a"), now); !errors.Is(err, domain.ErrInvalidEnvironment) {
		t.Errorf("reserved name: %v", err)
	}
	if _, err := domain.NewEnvironment(project, "qa", domain.Policy{States: []string{"rejected"}}, now); !errors.Is(err, domain.ErrInvalidPolicy) {
		t.Errorf("bad policy: %v", err)
	}
	e, err := domain.NewEnvironment(project, "qa", domain.DefaultPolicy("qa"), now)
	if err != nil || e.Version != 1 || e.Current != uuid.Nil {
		t.Fatalf("%+v, %v", e, err)
	}
	r := uuid.New()
	if !e.Point(r, now) || e.Version != 2 || e.Current != r {
		t.Fatalf("point: %+v", e)
	}
	if e.Point(r, now) || e.Version != 2 {
		t.Error("pointing at the served release changed the environment")
	}
	if changed, _ := e.ChangePolicy(domain.DefaultPolicy("qa"), now); changed {
		t.Error("an equal policy is a change")
	}
	if changed, err := e.ChangePolicy(domain.DefaultPolicy("production"), now); err != nil || !changed || e.Version != 3 {
		t.Errorf("policy change: %+v, %v", e, err)
	}
	if e.Kind != domain.KindStandard || e.Branch != "" {
		t.Errorf("kind %q, branch %q", e.Kind, e.Branch)
	}
	// Branch environment names are reserved for branch environments.
	for _, name := range []string{"pr-7", "br-0a1b2c3d"} {
		if _, err := domain.NewEnvironment(project, name, domain.DefaultPolicy(name), now); !errors.Is(err, domain.ErrInvalidEnvironment) ||
			!strings.Contains(err.Error(), "branch") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// RFC 0004 §4.2: one environment per open branch, pr-<number> or
// br-<8 hex of sha256(branch)>, with the fixed preview policy.
func TestBranchEnvironment(t *testing.T) {
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	project := uuid.New()
	e, err := domain.NewBranchEnvironment(project, "feature/tip", 42, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "pr-42" || e.Kind != domain.KindBranch || e.Branch != "feature/tip" || e.Version != 1 {
		t.Errorf("%+v", e)
	}
	want := domain.Policy{States: []string{"draft", "needs_review", "approved"}, IncludeOutdated: true}
	if !e.Policy.Equal(want) || !domain.BranchPolicy().Equal(want) {
		t.Errorf("policy %+v", e.Policy)
	}
	noPR, err := domain.NewBranchEnvironment(project, "feature/tip", 0, now)
	if err != nil || noPR.Name != delivery.BranchEnvironmentName("feature/tip", 0) || !strings.HasPrefix(noPR.Name, "br-") {
		t.Errorf("without a PR: %+v, %v", noPR, err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 256), "a\x00b"} {
		if _, err := domain.NewBranchEnvironment(project, bad, 0, now); !errors.Is(err, domain.ErrInvalidBranch) {
			t.Errorf("branch %q: %v", bad, err)
		}
	}
	if _, err := domain.NewBranchEnvironment(project, "x", -1, now); !errors.Is(err, domain.ErrInvalidBranch) {
		t.Errorf("negative PR: %v", err)
	}
	// The policy is fixed.
	if changed, err := e.ChangePolicy(domain.DefaultPolicy("production"), now); !errors.Is(err, domain.ErrFixedPolicy) || changed || e.Version != 1 {
		t.Errorf("policy change: %v", err)
	}
	if changed, err := e.ChangePolicy(domain.BranchPolicy(), now); err != nil || changed {
		t.Errorf("unchanged policy: %v", err)
	}
}

// Branch releases hold text that exists only on their branch, so they
// can't be promoted anywhere (RFC 0004 §4.2).
func TestBranchReleasesAreNotPromotable(t *testing.T) {
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	project := uuid.New()
	env, _ := domain.NewBranchEnvironment(project, "feature/tip", 42, now)
	built, err := domain.BuildBranch(domain.Snapshot{SourceLocale: "en", Locales: []domain.Locale{{Code: "en", Direction: "ltr"}}}, env.Policy)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := domain.NewRelease(uuid.New(), project, 1, uuid.Nil, env, built, "", "system:release", now)
	if err != nil || rel.Branch != "feature/tip" || rel.Environment != "pr-42" {
		t.Fatalf("%+v, %v", rel, err)
	}
	if err := rel.Promotable(); !errors.Is(err, domain.ErrBranchReleaseNotPromotable) {
		t.Errorf("branch release: %v", err)
	}
	preview, _ := domain.NewEnvironment(project, "preview", domain.DefaultPolicy("preview"), now)
	main, _ := domain.NewRelease(uuid.New(), project, 2, uuid.Nil, preview, built, "", "person:x", now)
	if err := main.Promotable(); err != nil {
		t.Errorf("main release: %v", err)
	}
}

func TestDeliveryKey(t *testing.T) {
	now := time.Now()
	if _, err := domain.NewDeliveryKey(uuid.New(), "", delivery.DefaultScope(), "person:x", now); !errors.Is(err, domain.ErrInvalidKeyName) {
		t.Errorf("empty name: %v", err)
	}
	if _, err := domain.NewDeliveryKey(uuid.New(), "web", delivery.Scope{Environments: []string{"pr-7"}}, "person:x", now); !errors.Is(err, delivery.ErrInvalidScope) {
		t.Errorf("branch environment in the allowlist: %v", err)
	}
	preview, err := domain.NewDeliveryKey(uuid.New(), "preview", delivery.Scope{Environments: []string{"preview", "preview"}, Branches: true}, "person:x", now)
	if err != nil || !slices.Equal(preview.Scope.Environments, []string{"preview"}) || !preview.Scope.Branches {
		t.Fatalf("preview key: %+v, %v", preview, err)
	}
	k, err := domain.NewDeliveryKey(uuid.New(), "web", delivery.DefaultScope(), "person:x", now)
	if err != nil || !k.Active() || !k.Scope.Equal(delivery.DefaultScope()) {
		t.Fatal(err)
	}
	if err := k.Revoke("person:y", now); err != nil || k.Active() || k.RevokedBy != "person:y" {
		t.Fatalf("revoke: %v %+v", err, k)
	}
	if err := k.Revoke("person:y", now); !errors.Is(err, domain.ErrKeyRevoked) {
		t.Errorf("second revoke: %v", err)
	}
}
