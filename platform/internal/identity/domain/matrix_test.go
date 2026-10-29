package domain_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// pinnedPermissions is every permission there is, spelled out. A
// permission added to the domain without being added here fails
// TestEveryPermissionIsPinned, so nothing joins the matrix unseen.
var pinnedPermissions = []string{
	"approvals.decide",
	"assignments.manage", "assignments.read",
	"audit.export", "audit.import", "audit.read",
	"catalog.read", "catalog.write",
	"integration.import", "integration.manage", "integration.read",
	"intelligence.manage", "intelligence.read", "intelligence.translate",
	"knowledge.read", "knowledge.write",
	"members.manage", "members.read",
	"owners.manage",
	"releases.publish", "releases.read",
	"tenant.manage", "tenant.read",
	"tokens.manage", "tokens.read",
	"translations.read", "translations.review", "translations.write",
	"vendors.manage",
	"workflows.manage", "workflows.read",
}

// pinnedRoleMatrix is the role matrix with every row written out in
// full. No row is derived from AllPermissions, so a permission added to
// the domain is held by nobody here until someone decides who holds it
// (RFC 0006 §4.2) — and an owner who silently gained it fails the test.
// Changing a row is a product decision, not a refactor.
var pinnedRoleMatrix = map[string][]string{
	"owner": {
		"approvals.decide", "assignments.manage", "assignments.read", "audit.export", "audit.import", "audit.read",
		"catalog.read", "catalog.write", "integration.import", "integration.manage", "integration.read",
		"intelligence.manage", "intelligence.read", "intelligence.translate", "knowledge.read", "knowledge.write",
		"members.manage", "members.read", "owners.manage", "releases.publish", "releases.read",
		"tenant.manage", "tenant.read", "tokens.manage", "tokens.read",
		"translations.read", "translations.review", "translations.write",
		"vendors.manage", "workflows.manage", "workflows.read",
	},
	// admin: everything but owners, the audit export and the audit
	// import, which only an owner holds by default (§4.2, §7.2).
	"admin": {
		"approvals.decide", "assignments.manage", "assignments.read", "audit.read",
		"catalog.read", "catalog.write", "integration.import", "integration.manage", "integration.read",
		"intelligence.manage", "intelligence.read", "intelligence.translate", "knowledge.read", "knowledge.write",
		"members.manage", "members.read", "releases.publish", "releases.read",
		"tenant.manage", "tenant.read", "tokens.manage", "tokens.read",
		"translations.read", "translations.review", "translations.write",
		"vendors.manage", "workflows.manage", "workflows.read",
	},
	"developer": {
		"assignments.read", "catalog.read", "catalog.write",
		"integration.import", "integration.manage", "integration.read",
		"intelligence.read", "intelligence.translate", "knowledge.read", "knowledge.write",
		"members.read", "releases.publish", "releases.read", "tenant.read", "tokens.manage", "tokens.read",
		"translations.read", "translations.write", "workflows.read",
	},
	"translator": {
		"assignments.read", "catalog.read", "integration.import", "integration.read",
		"intelligence.read", "intelligence.translate", "knowledge.read", "members.read", "releases.read",
		"tenant.read", "translations.read", "translations.write", "workflows.read",
	},
	// reviewer: decides translation approvals for its locales (§4.2).
	"reviewer": {
		"approvals.decide", "assignments.read", "catalog.read", "integration.import", "integration.read",
		"intelligence.read", "intelligence.translate", "knowledge.read", "members.read", "releases.read",
		"tenant.read", "translations.read", "translations.review", "translations.write", "workflows.read",
	},
}

// pinnedScopeMatrix is what each token scope grants on its own (every
// scope implies read). No scope grants translations.review or
// approvals.decide: review and approval are human decisions.
var pinnedScopeMatrix = map[string][]string{
	"read": {
		"assignments.read", "catalog.read", "integration.read", "intelligence.read", "knowledge.read",
		"members.read", "releases.read", "tenant.read", "tokens.read", "translations.read", "workflows.read",
	},
	"write": {
		"assignments.read", "catalog.read", "catalog.write", "integration.import", "integration.manage",
		"integration.read", "intelligence.read", "intelligence.translate", "knowledge.read", "knowledge.write",
		"members.read", "releases.read", "tenant.read", "tokens.read",
		"translations.read", "translations.write", "workflows.read",
	},
	"publish": {
		"assignments.read", "catalog.read", "integration.read", "intelligence.read", "knowledge.read",
		"members.read", "releases.publish", "releases.read", "tenant.read", "tokens.read",
		"translations.read", "workflows.read",
	},
	"admin": {
		"assignments.read", "catalog.read", "integration.read", "intelligence.manage", "intelligence.read",
		"knowledge.read", "members.manage", "members.read", "releases.read", "tenant.manage", "tenant.read",
		"tokens.manage", "tokens.read", "translations.read", "workflows.read",
	},
	// workflows: what `glossa workflow push` needs, and nothing a person
	// decides — no review, no approvals.decide, no assignments.manage.
	"workflows": {
		"assignments.read", "catalog.read", "integration.read", "intelligence.read", "knowledge.read",
		"members.read", "releases.read", "tenant.read", "tokens.read", "translations.read",
		"workflows.manage", "workflows.read",
	},
}

func permissionNames(ps []domain.Permission) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

func TestEveryPermissionIsPinned(t *testing.T) {
	if got := permissionNames(domain.AllPermissions()); !reflect.DeepEqual(got, pinnedPermissions) {
		t.Errorf("AllPermissions() = %v\nwant (pinned)   %v\n"+
			"A new permission is a product decision: pin it here and decide its row in pinnedRoleMatrix.",
			got, pinnedPermissions)
	}
}

// The role matrix is the authorization contract other contexts rely on.
// Every role's exact permission set is pinned: a permission granted by
// accident fails here as surely as one taken away.
func TestRolePermissionMatrix(t *testing.T) {
	for _, role := range []string{"owner", "admin", "developer", "translator", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			g := domain.GrantForMember(mustRoles(t, role), domain.LocaleScope{})
			want := pinnedRoleMatrix[role]
			if got := permissionNames(g.Permissions()); !reflect.DeepEqual(got, want) {
				t.Errorf("%s holds %v\nwant (pinned) %v", role, got, want)
			}
			for _, p := range domain.AllPermissions() {
				if exp := contains(toPerms(want), p); g.Allows(p) != exp {
					t.Errorf("%s allows %s = %t, want %t", role, p, g.Allows(p), exp)
				}
			}
		})
	}
}

func TestScopePermissionMatrix(t *testing.T) {
	for scope, want := range pinnedScopeMatrix {
		t.Run(scope, func(t *testing.T) {
			s, err := domain.ParseScopes([]string{scope})
			if err != nil {
				t.Fatal(err)
			}
			if got := permissionNames(domain.GrantForScopes(s).Permissions()); !reflect.DeepEqual(got, want) {
				t.Errorf("scope %s grants %v\nwant (pinned) %v", scope, got, want)
			}
		})
	}
}

// RFC 0006 §3.2 and §9.3: approvals are human. No token scope, alone or
// in any combination, reaches approvals.decide — nor translations.review
// — and neither does the CI ceiling a GitHub Actions token is cut from.
func TestNoScopeCombinationReachesAHumanOnlyPermission(t *testing.T) {
	for _, p := range []domain.Permission{domain.PermApprovalsDecide, domain.PermTranslationsReview} {
		if !p.HumanOnly() {
			t.Errorf("%s must be human-only", p)
		}
	}
	all := []string{"read", "write", "publish", "admin", "workflows"}
	for mask := 1; mask < 1<<len(all); mask++ {
		var names []string
		for i, s := range all {
			if mask&(1<<i) != 0 {
				names = append(names, s)
			}
		}
		s, err := domain.ParseScopes(names)
		if err != nil {
			t.Fatal(err)
		}
		g := domain.GrantForScopes(s)
		for _, p := range domain.AllPermissions() {
			if _, held := g.Locales(p); held && p.HumanOnly() {
				t.Errorf("scopes %v reach %s, a human decision no token may hold", names, p)
			}
		}
		if _, held := g.Locales(domain.PermApprovalsDecide); held {
			t.Errorf("scopes %v reach approvals.decide", names)
		}
	}
	for _, p := range domain.CIPermissions() {
		if p.HumanOnly() {
			t.Errorf("the CI ceiling holds %s", p)
		}
	}
}

// The workflows scope is opt-in (RFC 0006 §4.2): no other scope, alone
// or together, reaches workflows.manage, so every token issued before it
// existed — and every token issued without naming it — keeps leaving the
// process alone. Nor does the CI ceiling.
func TestTheWorkflowsScopeIsOptIn(t *testing.T) {
	others, err := domain.ParseScopes([]string{"read", "write", "publish", "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, held := domain.GrantForScopes(others).Locales(domain.PermWorkflowsManage); held {
		t.Error("read, write, publish and admin together reach workflows.manage; only the workflows scope may")
	}
	if slices.Contains(domain.CIPermissions(), domain.PermWorkflowsManage) {
		t.Error("the CI ceiling holds workflows.manage")
	}
	only, err := domain.ParseScopes([]string{"workflows"})
	if err != nil {
		t.Fatal(err)
	}
	g := domain.GrantForScopes(only)
	for _, p := range []domain.Permission{domain.PermApprovalsDecide, domain.PermTranslationsReview, domain.PermAssignmentsManage} {
		if _, held := g.Locales(p); held {
			t.Errorf("the workflows scope reaches %s", p)
		}
	}
}

func TestApprovalsDecideIsLocaleScoped(t *testing.T) {
	g := domain.GrantForMember(mustRoles(t, "reviewer"), mustLocales(t, "de"))
	if !g.AllowsFor(domain.PermApprovalsDecide, mustLocale(t, "de-AT")) {
		t.Error("a reviewer scoped to de decides de-AT approvals")
	}
	if g.AllowsFor(domain.PermApprovalsDecide, mustLocale(t, "ja")) || g.Allows(domain.PermApprovalsDecide) {
		t.Error("a reviewer scoped to de must not decide ja approvals")
	}
}

// TestApprovalsDecideIsEnvironmentScopedForReleases pins who decides a
// release request (RFC 0006 §4.2, §5.1): approvals.decide is the only
// environment-scoped permission, held — in any locale — by owner, admin
// and reviewer and by no other role or token scope; and an environment
// scope is every environment until it names some.
func TestApprovalsDecideIsEnvironmentScopedForReleases(t *testing.T) {
	for _, p := range domain.AllPermissions() {
		if got, want := p.EnvironmentScoped(), p == domain.PermApprovalsDecide; got != want {
			t.Errorf("%s.EnvironmentScoped() = %v, want %v", p, got, want)
		}
	}
	holds := map[string]bool{"owner": true, "admin": true, "reviewer": true, "developer": false, "translator": false}
	for role, want := range holds {
		g := domain.GrantForMember(mustRoles(t, role), mustLocales(t, "de"))
		if got := g.Holds(domain.PermApprovalsDecide); got != want {
			t.Errorf("%s limited to de holds approvals.decide = %v, want %v", role, got, want)
		}
	}
	for _, s := range []string{"read", "write", "publish", "admin"} {
		ss, err := domain.ParseScopes([]string{s})
		if err != nil {
			t.Fatal(err)
		}
		if domain.GrantForScopes(ss).Holds(domain.PermApprovalsDecide) {
			t.Errorf("token scope %s holds approvals.decide", s)
		}
	}

	all, err := domain.ParseEnvironmentScope(nil)
	if err != nil || !all.All() || !all.Covers("production") {
		t.Fatalf("an empty environment scope = %+v (%v), want every environment", all, err)
	}
	some, err := domain.ParseEnvironmentScope([]string{"staging", "production", "staging"})
	if err != nil || some.All() || !some.Covers("production") || some.Covers("development") {
		t.Fatalf("scope = %v (%v)", some.Strings(), err)
	}
	if _, err := domain.ParseEnvironmentScope([]string{"Prod"}); !errors.Is(err, domain.ErrInvalidEnvironmentScope) {
		t.Errorf("an invalid name = %v, want ErrInvalidEnvironmentScope", err)
	}
}

func toPerms(names []string) []domain.Permission {
	out := make([]domain.Permission, len(names))
	for i, n := range names {
		out[i] = domain.Permission(n)
	}
	return out
}
