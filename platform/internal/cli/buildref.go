package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// buildRef is the commit a build is for, as CI or git knows it (RFC 0004
// §6.3: every upload carries the commit).
type buildRef struct {
	Commit string
	// Branch is the short branch name (feat/checkout-copy).
	Branch string
}

// The usages document's commit and branch (glossa.usages/v1).
var (
	commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	branchPattern = regexp.MustCompile(`^[^/.\x00-\x20\x7f~^:?*\[\\{](?:[^/.\x00-\x20\x7f~^:?*\[\\{]|\.[^/.\x00-\x20\x7f~^:?*\[\\{])*(?:/[^/.\x00-\x20\x7f~^:?*\[\\{](?:[^/.\x00-\x20\x7f~^:?*\[\\{]|\.[^/.\x00-\x20\x7f~^:?*\[\\{])*)*$`)
)

func validBranch(b string) bool { return len(b) <= 255 && branchPattern.MatchString(b) }

// detectBuild finds the commit and branch: GLOSSA_COMMIT and
// GLOSSA_BRANCH first, then GitHub Actions and GitLab CI, then git in
// dir. Fields it can't find stay empty.
func detectBuild(ctx context.Context, getenv func(string) string, dir string) buildRef {
	var ref buildRef
	for _, source := range []func() buildRef{
		func() buildRef {
			return buildRef{Commit: getenv("GLOSSA_COMMIT"), Branch: getenv("GLOSSA_BRANCH")}
		},
		func() buildRef { return githubBuild(getenv) },
		func() buildRef {
			return buildRef{Commit: getenv("CI_COMMIT_SHA"),
				Branch: firstOf(getenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME"), getenv("CI_COMMIT_BRANCH"))}
		},
		func() buildRef { return gitBuild(ctx, dir) },
	} {
		if ref.Commit != "" && ref.Branch != "" {
			break
		}
		next := source()
		ref.Commit = firstOf(ref.Commit, strings.ToLower(strings.TrimSpace(next.Commit)))
		ref.Branch = firstOf(ref.Branch, strings.TrimSpace(next.Branch))
	}
	return ref
}

// githubBuild reads GitHub Actions: for a pull request, the head commit
// and branch (GITHUB_SHA is the merge commit there), from the event.
func githubBuild(getenv func(string) string) buildRef {
	if getenv("GITHUB_ACTIONS") != "true" {
		return buildRef{}
	}
	ref := buildRef{Commit: getenv("GITHUB_SHA"), Branch: getenv("GITHUB_HEAD_REF")}
	if ref.Branch == "" && strings.HasPrefix(getenv("GITHUB_REF"), "refs/heads/") {
		ref.Branch = getenv("GITHUB_REF_NAME")
	}
	var event struct {
		PullRequest *struct {
			Head struct {
				SHA string `json:"sha"`
				Ref string `json:"ref"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if p := getenv("GITHUB_EVENT_PATH"); p != "" {
		if raw, err := os.ReadFile(p); err == nil && json.Unmarshal(raw, &event) == nil { //nolint:gosec // the runner's event file
			if pr := event.PullRequest; pr != nil {
				ref.Commit, ref.Branch = pr.Head.SHA, firstOf(ref.Branch, pr.Head.Ref)
			}
		}
	}
	return ref
}

// gitBuild asks git: HEAD and the checked-out branch (none when
// detached). The default branch is the project's setting on the server.
func gitBuild(ctx context.Context, dir string) buildRef {
	git := func(args ...string) string {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	ref := buildRef{Commit: git("rev-parse", "HEAD")}
	if ref.Commit == "" {
		return ref
	}
	if b := git("symbolic-ref", "--quiet", "--short", "HEAD"); b != "" {
		ref.Branch = b
	}
	return ref
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
