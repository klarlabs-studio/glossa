package cli

import (
	"context"
	"sort"
	"strconv"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// Branches (RFC 0004 §4): what a feature branch proposes before it
// merges, and the preview deployment CI built for it.

const branchUsage = `branch status|close [<name>] [--json]

status shows what the branch proposes: new keys, source proposals, the live keys its last
complete push no longer had, conflicts with other open branches, and how many translations
per locale merging it will make outdated.
close closes an unmerged branch: its preview environment goes away, and its proposed
messages become obsolete 14 days later unless the branch is reopened.

Without <name>, the branch is taken from the CI environment (GITHUB_HEAD_REF, then
GITHUB_REF_NAME).`

const previewUsage = `preview register --url <url> [--branch <name>] [--json]

Records where CI deployed the branch's preview. Studio and the pull request comment link
to it, and it is where the in-product editor runs. Without --branch, the branch comes from
the CI environment (GITHUB_HEAD_REF, then GITHUB_REF_NAME).`

// branchJSON is a branch and its status report, in `--json` output.
type branchJSON struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	State           string         `json:"state"`
	PR              *int           `json:"pr_number,omitempty"`
	HeadCommit      string         `json:"head_commit,omitempty"`
	PreviewURL      string         `json:"preview_url,omitempty"`
	NewKeys         []string       `json:"new_keys,omitempty"`
	SourceProposals []string       `json:"source_proposals,omitempty"`
	Removed         []string       `json:"removed,omitempty"`
	Conflicts       []conflictJSON `json:"conflicts,omitempty"`
	Outdated        map[string]int `json:"outdated,omitempty"`
}

type conflictJSON struct {
	Key      string   `json:"key"`
	Branches []string `json:"branches"`
}

type branchCmdJSON struct {
	Schema string     `json:"schema"`
	Action string     `json:"action"`
	Branch branchJSON `json:"branch"`
}

type previewJSON struct {
	Schema string     `json:"schema"`
	URL    string     `json:"url"`
	Branch branchJSON `json:"branch"`
}

func branchOf(b remote.Branch) branchJSON {
	out := branchJSON{ID: b.Id, Name: b.Name, State: string(b.State), PR: b.PrNumber}
	if b.HeadCommit != nil {
		out.HeadCommit = *b.HeadCommit
	}
	if b.PreviewUrl != nil {
		out.PreviewURL = *b.PreviewUrl
	}
	return out
}

func statusOf(s remote.BranchStatus) branchJSON {
	out := branchOf(s.Branch)
	out.NewKeys, out.SourceProposals, out.Removed = s.NewKeys, s.SourceProposals, s.Removed
	for _, c := range s.Conflicts {
		out.Conflicts = append(out.Conflicts, conflictJSON{Key: c.Key, Branches: c.Branches})
	}
	if len(s.Outdated) > 0 {
		out.Outdated = s.Outdated
	}
	return out
}

func runBranch(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(branchUsage)
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 || (pos[0] != "status" && pos[0] != "close") || len(pos) > 2 {
		return usageError(inv.name, "branch takes one action: status or close (`glossa branch status feature/checkout-copy`)")
	}
	name := ""
	if len(pos) == 2 {
		name = pos[1]
	}
	p, b, err := inv.branch(ctx, name)
	if err != nil {
		return err
	}
	if pos[0] == "close" {
		closed, err := p.client.CloseBranch(ctx, p.scope, b.Id)
		if err != nil {
			return inv.apiError(err, "can't close the branch")
		}
		out := branchCmdJSON{Schema: "glossa.cli.branch/v1", Action: "close", Branch: branchOf(closed)}
		return inv.emit(out, func(pr *printer) {
			pr.line("%s Closed %s: its preview environment is gone, and its proposals expire in 14 days",
				pr.pass(), closed.Name)
		})
	}
	status, err := p.client.BranchStatus(ctx, p.scope, b.Id)
	if err != nil {
		return inv.apiError(err, "can't read the branch")
	}
	out := branchCmdJSON{Schema: "glossa.cli.branch/v1", Action: "status", Branch: statusOf(status)}
	err = inv.emit(out, func(pr *printer) { printBranch(pr, p, out.Branch) })
	if err != nil {
		return err
	}
	if len(out.Branch.Conflicts) > 0 {
		return silentExit(ExitCheckFailed, "key_conflict")
	}
	return nil
}

func runPreview(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(previewUsage)
	url := fs.String("url", "", "where CI deployed the branch's preview")
	branch := fs.String("branch", "", "the branch (default: the CI environment's)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "register" {
		return usageError(inv.name, "preview takes one action: register --url <url>")
	}
	if *url == "" {
		return usageError(inv.name, "preview register needs --url (`glossa preview register --url \"$PREVIEW_URL\"`)")
	}
	p, b, err := inv.branch(ctx, *branch)
	if err != nil {
		return err
	}
	updated, err := p.client.SetBranchPreview(ctx, p.scope, b.Id, *url)
	if err != nil {
		return inv.apiError(err, "can't register the preview")
	}
	out := previewJSON{Schema: "glossa.cli.preview/v1", URL: *url, Branch: branchOf(updated)}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Registered %s as the preview of %s", pr.pass(), *url, updated.Name)
	})
}

// branch connects and resolves a branch by name, defaulting to the CI
// environment's. An unknown branch is a usage error: nothing pushed it.
func (inv *invocation) branch(ctx context.Context, name string) (*project, remote.Branch, error) {
	if name == "" {
		name = inv.ciBranch()
	}
	if name == "" {
		return nil, remote.Branch{}, usageError(inv.name,
			"name the branch (`glossa %s … feature/checkout-copy`), or run this in CI, where GITHUB_HEAD_REF names it", inv.name)
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return nil, remote.Branch{}, err
	}
	b, found, err := p.client.Branch(ctx, p.scope, name)
	if err != nil {
		return nil, remote.Branch{}, inv.apiError(err, "can't read the branch")
	}
	if !found {
		return nil, remote.Branch{}, &Error{Exit: ExitUsage, Code: "branch_not_found",
			What: "the project has no branch " + name,
			Why:  "a branch exists once CI pushes it", Fix: "run `glossa push --branch " + name + "` from the branch first"}
	}
	return p, b, nil
}

// ciBranch is the branch the CI environment says this run is on.
func (inv *invocation) ciBranch() string {
	for _, key := range []string{"GITHUB_HEAD_REF", "GITHUB_REF_NAME"} {
		if v := inv.env.getenv(key); v != "" {
			return v
		}
	}
	return ""
}

func printBranch(pr *printer, p *project, b branchJSON) {
	label := b.Name
	if b.PR != nil {
		label += pr.dim(" (pull request #" + strconv.Itoa(*b.PR) + ")")
	}
	pr.line("%s of %s (%s) is %s", label, p.info.Slug, p.cfg.Server, b.State)
	pr.line("%s %s · %s · %s", pr.pass(),
		plural(len(b.NewKeys), "new key", "new keys"),
		plural(len(b.SourceProposals), "source proposal", "source proposals"),
		plural(len(b.Removed), "removed key", "removed keys"))
	if len(b.Outdated) > 0 {
		locales := make([]string, 0, len(b.Outdated))
		for l := range b.Outdated {
			locales = append(locales, l)
		}
		sort.Strings(locales)
		for _, l := range locales {
			pr.line("  %s  %s outdated when it merges", l, plural(b.Outdated[l], "translation", "translations"))
		}
	}
	if b.PreviewURL != "" {
		pr.line("  preview: %s", b.PreviewURL)
	}
	for _, c := range b.Conflicts {
		pr.line("%s %s is proposed with different source by %s", pr.fail(), c.Key, joinNames(c.Branches))
	}
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return "no other branch"
	case 1:
		return names[0]
	}
	return names[0] + " and " + plural(len(names)-1, "one other branch", "others")
}
