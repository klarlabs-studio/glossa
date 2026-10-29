package delivery

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// DefaultEnvironments are the environments every project has. Keys
// created before scopes existed were migrated to them.
var DefaultEnvironments = []string{"development", "preview", "staging", "production"}

// MaxScopeEnvironments bounds a key's allowlist.
const MaxScopeEnvironments = 50

// ErrInvalidScope: a scope names 1 to MaxScopeEnvironments valid
// environments (none is fine when it reads branches), and never a branch
// environment by name: those are reached through the branches flag.
var ErrInvalidScope = errors.New("delivery: a key scope allows 1-50 environments by name " +
	"(none when it allows branches), never a branch environment (pr-<n>, br-<hash>)")

// Scope is what a delivery key may read (RFC 0004 §4.3): the
// environments on its allowlist and, for a preview key (Branches), every
// branch environment. Branch environments hold unreleased copy, so a key
// shipped in a production bundle must not reach them. Anything outside
// the scope answers 404, exactly like an unknown key.
type Scope struct {
	Environments []string `json:"environments"`
	Branches     bool     `json:"branches"`
}

// NewScope validates a scope and returns its allowlist sorted and
// without duplicates.
func NewScope(environments []string, branches bool) (Scope, error) {
	envs := slices.Clone(environments)
	slices.Sort(envs)
	envs = slices.Compact(envs)
	if len(envs) > MaxScopeEnvironments || (len(envs) == 0 && !branches) {
		return Scope{}, ErrInvalidScope
	}
	for _, e := range envs {
		if !ValidEnvironment(e) || IsBranchEnvironment(e) {
			return Scope{}, fmt.Errorf("%w: %q", ErrInvalidScope, e)
		}
	}
	if envs == nil {
		envs = []string{}
	}
	return Scope{Environments: envs, Branches: branches}, nil
}

// DefaultScope is a new key's scope: production only.
func DefaultScope() Scope { return Scope{Environments: []string{"production"}} }

// Allows reports whether the scope reaches environment.
func (s Scope) Allows(environment string) bool {
	return slices.Contains(s.Environments, environment) || (s.Branches && IsBranchEnvironment(environment))
}

// Equal reports whether s and o allow the same.
func (s Scope) Equal(o Scope) bool {
	return slices.Equal(s.Environments, o.Environments) && s.Branches == o.Branches
}

// Branch environments are named pr-<number> for a pull request and
// br-<first 8 hex of sha256(branch)> otherwise (RFC 0004 §4.2). The names
// are reserved for them, so the edge tells a branch environment by its
// name alone, without reading anything.
var branchEnvironmentPattern = regexp.MustCompile(`^(pr-[1-9][0-9]{0,17}|br-[0-9a-f]{8})$`)

// IsBranchEnvironment reports whether name is a branch environment's.
func IsBranchEnvironment(name string) bool { return branchEnvironmentPattern.MatchString(name) }

// BranchEnvironmentName names the environment of branch: pr-<pr> when
// the branch has a pull request (pr > 0), br-<hash> otherwise.
func BranchEnvironmentName(branch string, pr int) string {
	if pr > 0 {
		return "pr-" + strconv.Itoa(pr)
	}
	return "br-" + Digest([]byte(branch))[:8]
}
