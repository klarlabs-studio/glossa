package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/release"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

// releaseRef names a release in output.
type releaseRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

func refOf(r release.Release) *releaseRef { return &releaseRef{ID: r.ID, Version: r.Version} }

// environmentJSON is an environment with the release it serves (null
// when nothing was published or promoted to it).
type environmentJSON struct {
	Name      string         `json:"name"`
	Release   *releaseRef    `json:"release"`
	Policy    release.Policy `json:"policy"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type releasePublishJSON struct {
	Schema         string          `json:"schema"`
	Replayed       bool            `json:"replayed"`
	IdempotencyKey string          `json:"idempotency_key"`
	Release        release.Release `json:"release"`
	// Held is true when the environment requires approvals: the release
	// was recorded, ReleaseRequest waits for them, nothing was deployed
	// (exit 5).
	Held           bool             `json:"held"`
	ReleaseRequest *release.Request `json:"release_request,omitempty"`
}

// releasePreviewJSON is `release publish --dry-run`: what the publish
// would ship, compared with what the environment serves.
type releasePreviewJSON struct {
	Schema      string         `json:"schema"`
	Environment string         `json:"environment"`
	Policy      release.Policy `json:"policy"`
	// Base is what the environment serves now (null: nothing).
	Base       *releaseRef       `json:"base"`
	Releasable bool              `json:"releasable"`
	Problems   []release.Problem `json:"problems"`
	// Release is null when the catalog isn't releasable.
	Release *release.PreviewRelease `json:"release"`
	Changes []localeDiffJSON        `json:"changes"`
	// Identical is true when publishing would change nothing.
	Identical bool `json:"identical"`
}

type releaseItemJSON struct {
	release.Release
	// Serving lists the environments that serve it now.
	Serving []string `json:"serving"`
}

type releaseListJSON struct {
	Schema   string            `json:"schema"`
	Releases []releaseItemJSON `json:"releases"`
}

type releaseShowJSON struct {
	Schema  string          `json:"schema"`
	Release release.Release `json:"release"`
	Serving []string        `json:"serving"`
}

type localeDiffJSON = release.LocaleDiff

type releaseDiffJSON struct {
	Schema  string           `json:"schema"`
	Release releaseRef       `json:"release"`
	Base    *releaseRef      `json:"base"`
	Locales []localeDiffJSON `json:"locales"`
	// Identical is true when nothing was added, changed or removed.
	Identical bool `json:"identical"`
}

type releaseMoveJSON struct {
	Schema      string          `json:"schema"`
	Environment environmentJSON `json:"environment"`
	// Previous is what the environment served before (null: nothing).
	Previous *releaseRef `json:"previous"`
	// Held (promote only) is true when the environment requires
	// approvals: Environment is unchanged, Release is what
	// ReleaseRequest would deploy (exit 5).
	Held           bool             `json:"held"`
	Release        *releaseRef      `json:"release,omitempty"`
	ReleaseRequest *release.Request `json:"release_request,omitempty"`
}

type releaseEnvironmentsJSON struct {
	Schema       string            `json:"schema"`
	Environments []environmentJSON `json:"environments"`
}

type releaseKeysJSON struct {
	Schema string                `json:"schema"`
	Keys   []release.DeliveryKey `json:"keys"`
}

type releaseKeyJSON struct {
	Schema string              `json:"schema"`
	Action string              `json:"action"` // created, scoped, revoked
	Key    release.DeliveryKey `json:"key"`
}

// ── arguments ───────────────────────────────────────────────────────

// releaseArgs is a parsed `glossa release` invocation.
type releaseArgs struct {
	action         string // publish, list, show, diff, promote, rollback, environments, keys
	keysAction     string // list, create, revoke
	environment    string
	releases       []string // release references: IDs or v<N>
	note           string
	idempotencyKey string
	dryRun         bool
	limit          int
	name           string // keys create: the name; keys revoke, keys scope: the ID or name
	// environments and preview are the scope of keys create and keys
	// scope: what the key may read at the edge (RFC 0004 4.3).
	environments []string
	preview      bool
	scopeGiven   bool
}

const releaseUsage = `release <action> [flags]

Actions:
  publish       [--environment NAME] [--note TEXT] [--idempotency-key KEY] [--dry-run]
                publish eligible translations (default environment: development);
                --dry-run shows what would ship and what would change, and stores nothing
  list          [--limit N]                        releases, newest first, and where they're served
  show          <release>                          one release: counts, locales, policy, environments
  diff          [<base>] <release>                 message IDs added, changed, removed per locale
                                                   (one release: compared with its parent)
  promote       <release> --to NAME                point an environment at a release
  rollback      --environment NAME [--to RELEASE]  point it back (default: the release it served before)
  environments                                     environments and the release each serves
  keys          [list]                             delivery keys for glossa-edge
  keys create   <name> [--environments a,b] [--preview]
                                                   create one (publishable: it ships in app bundles);
                                                   it reads production only unless the scope says otherwise
  keys scope    <id|name> [--environments a,b] [--preview]
                                                   change what a key reads (the key itself stays)
  keys revoke   <id|name>                          revoke one
  requests      [list|show|withdraw]               release requests: publishes and promotes held for
                                                   approval (glossa release requests --help)
  rollout       start|status|list|advance|complete|abort
                                                   staged rollouts (glossa release rollout --help)

A <release> is its ID or v<N> (its version). A publish or promote into an environment
that requires approvals is held as a release request and deploys nothing: it exits 5
and names the request, which people approve with ` + "`glossa approve <request>`" + `.`

var idempotencyKeyPattern = regexp.MustCompile(`^[\x21-\x7E]{1,255}$`)

func parseReleaseArgs(inv *invocation, args []string) (releaseArgs, error) {
	fs := inv.flags(releaseUsage)
	var r releaseArgs
	fs.StringVar(&r.environment, "environment", "", "publish: the environment (default development); rollback: the environment")
	to := fs.String("to", "", "promote: the environment; rollback: the release to go back to")
	fs.StringVar(&r.note, "note", "", "publish: a note stored with the release")
	fs.StringVar(&r.idempotencyKey, "idempotency-key", "", "publish, keys create: the Idempotency-Key (default: a new one per invocation)")
	fs.IntVar(&r.limit, "limit", 20, "list: at most this many releases (0: all)")
	fs.BoolVar(&r.dryRun, "dry-run", false, "publish: show what would ship and what would change; store nothing")
	environments := fs.String("environments", "", "keys create, keys scope: the environments the key reads, comma-separated (default: production)")
	fs.BoolVar(&r.preview, "preview", false, "keys create, keys scope: a preview key, which also reads every branch environment")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return r, err
	}
	if len(pos) == 0 {
		return r, usageError(inv.name, "missing action: publish, list, show, diff, promote, rollback, environments, keys, requests or rollout")
	}
	if r.idempotencyKey != "" && !idempotencyKeyPattern.MatchString(r.idempotencyKey) {
		return r, usageError(inv.name, "--idempotency-key must be 1-255 printable ASCII characters without spaces")
	}
	for _, e := range strings.Split(*environments, ",") {
		if e = strings.TrimSpace(e); e != "" {
			r.environments = append(r.environments, e)
		}
	}
	r.scopeGiven = len(r.environments) > 0 || r.preview
	r.action, pos = pos[0], pos[1:]
	switch r.action {
	case "publish":
		r.environment = orDefault(r.environment, "development")
		return r, noMore(inv, pos)
	case "list":
		if r.limit < 0 {
			return r, usageError(inv.name, "--limit must be 0 (all) or more")
		}
		return r, noMore(inv, pos)
	case "show":
		if len(pos) != 1 {
			return r, usageError(inv.name, "show takes one release (ID or v<N>)")
		}
		r.releases = pos
		return r, nil
	case "diff":
		if len(pos) < 1 || len(pos) > 2 {
			return r, usageError(inv.name, "diff takes a release, or a base and a release")
		}
		r.releases = pos
		return r, nil
	case "promote":
		if len(pos) != 1 || *to == "" {
			return r, usageError(inv.name, "promote takes a release (ID or v<N>) and --to <environment>")
		}
		r.releases, r.environment = pos, *to
		return r, nil
	case "rollback":
		if r.environment == "" {
			return r, usageError(inv.name, "rollback needs --environment <name>")
		}
		if *to != "" {
			r.releases = []string{*to}
		}
		return r, noMore(inv, pos)
	case "environments":
		return r, noMore(inv, pos)
	case "keys":
		return parseKeysArgs(inv, r, pos)
	}
	return r, usageError(inv.name, "unknown action %q (publish, list, show, diff, promote, rollback, environments, keys, requests, rollout)", r.action)
}

func parseKeysArgs(inv *invocation, r releaseArgs, pos []string) (releaseArgs, error) {
	r.keysAction = "list"
	if len(pos) > 0 {
		r.keysAction, pos = pos[0], pos[1:]
	}
	switch r.keysAction {
	case "list":
		return r, noMore(inv, pos)
	case "create", "revoke", "scope":
		if len(pos) != 1 {
			what := "a name"
			if r.keysAction != "create" {
				what = "the key's ID or name"
			}
			return r, usageError(inv.name, "keys %s takes %s", r.keysAction, what)
		}
		r.name = pos[0]
		if r.keysAction == "scope" && !r.scopeGiven {
			return r, usageError(inv.name, "keys scope takes --environments, --preview, or both")
		}
		return r, nil
	}
	return r, usageError(inv.name, "unknown keys action %q (list, create, scope, revoke)", r.keysAction)
}

func noMore(inv *invocation, pos []string) error {
	if len(pos) > 0 {
		return usageError(inv.name, "unexpected argument %q", pos[0])
	}
	return nil
}

// newIdempotencyKey is a fresh key for one invocation: a retry inside
// the invocation replays, a new invocation publishes again.
func newIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "glossa-cli-" + hex.EncodeToString(b[:])
}

// ── the service and its errors ──────────────────────────────────────

// releaseClient is the Release API scoped to the configured project.
type releaseClient struct {
	svc   release.Service
	scope release.Scope
	// cache holds releases already read, by ID.
	cache map[string]release.Release
}

func (inv *invocation) releaseClient(p *project) *releaseClient {
	return &releaseClient{svc: remote.NewReleaseService(p.client),
		scope: release.Scope{Tenant: p.scope.Tenant, Project: p.scope.Project}, cache: map[string]release.Release{}}
}

var versionRef = regexp.MustCompile(`^v([1-9][0-9]*)$`)

// resolve finds a release by ID or v<N>.
func (rc *releaseClient) resolve(ctx context.Context, inv *invocation, ref string) (release.Release, error) {
	if r, ok := rc.cache[ref]; ok {
		return r, nil
	}
	if m := versionRef.FindStringSubmatch(ref); m != nil {
		version, _ := strconv.Atoi(m[1])
		rels, err := rc.svc.Releases(ctx, rc.scope, 0)
		if err != nil {
			return release.Release{}, inv.releaseError(err, "can't list releases")
		}
		for _, r := range rels {
			rc.cache[r.ID] = r
			if r.Version == version {
				return r, nil
			}
		}
		return release.Release{}, &Error{Exit: ExitNetwork, Code: "release_not_found", What: "no release " + ref,
			Why: fmt.Sprintf("the project has %s", plural(len(rels), "release", "releases")),
			Fix: "run `glossa release list` for the project's releases"}
	}
	r, err := rc.svc.Release(ctx, rc.scope, ref)
	if err != nil {
		return release.Release{}, inv.releaseError(err, "can't read release "+ref)
	}
	rc.cache[r.ID] = r
	return r, nil
}

// ref returns the release an ID names, or nil for none.
func (rc *releaseClient) ref(ctx context.Context, inv *invocation, id string) (*releaseRef, error) {
	if id == "" {
		return nil, nil
	}
	r, err := rc.resolve(ctx, inv, id)
	if err != nil {
		return nil, err
	}
	return refOf(r), nil
}

func (rc *releaseClient) environmentJSON(ctx context.Context, inv *invocation, e release.Environment) (environmentJSON, error) {
	ref, err := rc.ref(ctx, inv, e.CurrentReleaseID)
	return environmentJSON{Name: e.Name, Release: ref, Policy: e.Policy, UpdatedAt: e.UpdatedAt}, err
}

// serving maps release IDs to the environments serving them.
func (rc *releaseClient) serving(ctx context.Context, inv *invocation) (map[string][]string, error) {
	envs, err := rc.svc.Environments(ctx, rc.scope)
	if err != nil {
		return nil, inv.releaseError(err, "can't list environments")
	}
	out := map[string][]string{}
	for _, e := range envs {
		if e.CurrentReleaseID != "" {
			out[e.CurrentReleaseID] = append(out[e.CurrentReleaseID], e.Name)
		}
	}
	return out, nil
}

// releaseFixes explains the Release API's problem codes.
var releaseFixes = map[string]struct {
	exit ExitCode
	fix  string
}{
	"release_not_found":       {ExitNetwork, "run `glossa release list` for the project's releases"},
	"release_ineligible":      {ExitNetwork, "publish to the environment directly (`glossa release publish --environment <name>`), or promote a release published under a policy it covers; for production: `glossa release publish --environment staging`, then `glossa release promote <release> --to production`"},
	"no_rollback_target":      {ExitNetwork, "the environment served no earlier release; `glossa release promote <release> --to <name>` points it at any eligible one"},
	"not_in_history":          {ExitNetwork, "rollback goes only to a release the environment served; `glossa release promote` points it at any eligible one"},
	"not_releasable":          {ExitNetwork, "push the source catalog first (`glossa push`)"},
	"storage_unavailable":     {ExitNetwork, "retry; the server can't reach artifact storage right now"},
	"key_revoked":             {ExitNetwork, "the key is already revoked; `glossa release keys create <name>` makes a new one"},
	"invalid_environment":     {ExitUsage, "use development, preview, staging, production or a custom environment (`glossa release environments` lists them)"},
	"invalid_note":            {ExitUsage, "shorten the --note"},
	"invalid_key_name":        {ExitUsage, "name the key after what uses it, e.g. web or go-emails"},
	"idempotency_key_reused":  {ExitUsage, "use a new --idempotency-key, or leave it out"},
	"invalid_idempotency_key": {ExitUsage, "--idempotency-key must be 1-255 printable ASCII characters"},
	"rollout_active": {ExitNetwork, "a staged rollout is active in the environment, and a publish or promote would replace the stable side under it: " +
		"`glossa release rollout status --environment <name>`, then `complete` or `abort` it first (a rollback is never refused: it aborts the rollout)"},
	"policy_not_met": {ExitNetwork, "translate and approve what the environment's completeness requirement is short of " +
		"(`glossa status`), or publish to an environment that doesn't require it"},
	"branch_release_not_promotable": {ExitNetwork, "a branch release holds text that exists only on its branch: merge the branch, then publish from the main catalog"},
}

// releaseError explains a failed Release API request.
func (inv *invocation) releaseError(err error, what string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if f, ok := releaseFixes[ae.Code]; ok {
		e.Exit, e.Fix = f.exit, f.fix
		if ae.Detail != "" {
			e.Why = ae.Detail
		}
	} else if ae.Status == 403 {
		e.Fix = "use a token with the publish scope (read is enough for list, show, diff, environments and pull), created in Studio"
	}
	return e
}

// ── commands ────────────────────────────────────────────────────────

func runRelease(ctx context.Context, inv *invocation, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "rollout":
			inv.name = "release rollout"
			return runReleaseRollout(ctx, inv, args[1:])
		case "requests":
			inv.name = "release requests"
			return runReleaseRequests(ctx, inv, args[1:])
		}
	}
	r, err := parseReleaseArgs(inv, args)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	rc := inv.releaseClient(p)
	switch r.action {
	case "publish":
		if r.dryRun {
			return inv.releasePreview(ctx, rc, r.environment)
		}
		return inv.releasePublish(ctx, rc, r)
	case "list":
		return inv.releaseList(ctx, rc, r.limit)
	case "show":
		return inv.releaseShow(ctx, rc, r.releases[0])
	case "diff":
		return inv.releaseDiff(ctx, rc, r.releases)
	case "promote", "rollback":
		return inv.releaseMove(ctx, rc, r)
	case "environments":
		return inv.releaseEnvironments(ctx, rc)
	default:
		return inv.releaseKeys(ctx, rc, r)
	}
}

func (inv *invocation) releasePublish(ctx context.Context, rc *releaseClient, r releaseArgs) error {
	key := orDefault(r.idempotencyKey, newIdempotencyKey())
	pub, err := rc.svc.Publish(ctx, rc.scope, release.PublishRequest{Environment: r.environment, Note: r.note, IdempotencyKey: key})
	if err != nil {
		return inv.releaseError(err, "can't publish to "+r.environment)
	}
	if pub.Held != nil {
		return inv.releaseHeldPublish(ctx, rc, key, pub)
	}
	rel := pub.Release
	out := releasePublishJSON{Schema: "glossa.cli.release.publish/v1", Replayed: pub.Replayed, IdempotencyKey: key, Release: rel}
	return inv.emit(out, func(pr *printer) {
		if pub.Replayed {
			pr.line("%s v%d was already published to %s with this idempotency key (%s); nothing new was published.",
				pr.pass(), rel.Version, rel.Environment, rel.ID)
			return
		}
		pr.line("%s Published v%d to %s %s", pr.pass(), rel.Version, rel.Environment, pr.dim("("+rel.ID+")"))
		pr.line("  %s · %s · %s", plural(rel.Counts.Messages, "message", "messages"), localeCounts(rel),
			artifactCounts(rel))
		if rel.Note != "" {
			pr.line("  note: %s", rel.Note)
		}
		pr.line("  %s", pr.dim(rel.Environment+" serves it now; glossa-edge delivers it within seconds"))
	})
}

// releasePreview is `release publish --dry-run`. A catalog that can't be
// released fails like a check (exit 1), with every problem listed.
func (inv *invocation) releasePreview(ctx context.Context, rc *releaseClient, environment string) error {
	pv, err := rc.svc.PreviewPublish(ctx, rc.scope, environment)
	if err != nil {
		return inv.releaseError(err, "can't preview a publish to "+environment)
	}
	base, err := rc.ref(ctx, inv, pv.BaseReleaseID)
	if err != nil {
		return err
	}
	out := releasePreviewJSON{Schema: "glossa.cli.release.preview/v1", Environment: pv.Environment, Policy: pv.Policy,
		Base: base, Releasable: pv.Releasable, Problems: pv.Problems, Release: pv.Release, Changes: pv.Changes, Identical: pv.Releasable}
	if out.Problems == nil {
		out.Problems = []release.Problem{}
	}
	if out.Changes == nil {
		out.Changes = []localeDiffJSON{}
	}
	for _, l := range out.Changes {
		out.Identical = out.Identical && len(l.Added)+len(l.Changed)+len(l.Removed) == 0
	}
	if err := inv.emit(out, func(pr *printer) { printPreview(pr, out) }); err != nil {
		return err
	}
	if !out.Releasable {
		return silentExit(ExitCheckFailed, "not_releasable")
	}
	return nil
}

func printPreview(pr *printer, pv releasePreviewJSON) {
	pr.line("%s publishing to %s %s", pr.bold("Dry run:"), pv.Environment, pr.dim("(ships "+policyString(pv.Policy)+"; nothing was published)"))
	if !pv.Releasable {
		pr.line("%s not releasable: %s", pr.fail(), plural(len(pv.Problems), "problem", "problems"))
		for _, p := range pv.Problems {
			where := strings.TrimSpace(p.Key + " " + p.Locale)
			if where != "" {
				where += "  "
			}
			pr.line("  %s %s%s", pr.bad("error"), where, p.Detail)
		}
		return
	}
	rel := release.Release{Locales: pv.Release.Locales, Counts: pv.Release.Counts}
	pr.line("%s would ship %s · %s · %s", pr.pass(), plural(rel.Counts.Messages, "message", "messages"), localeCounts(rel), artifactCounts(rel))
	switch {
	case pv.Base == nil:
		pr.line("  %s serves nothing yet: every message is added", pv.Environment)
	case pv.Identical:
		pr.line("  nothing would change: %s already serves this (v%d)", pv.Environment, pv.Base.Version)
		return
	default:
		pr.line("  compared with v%d %s, which %s serves now:", pv.Base.Version, pr.dim("("+pv.Base.ID+")"), pv.Environment)
	}
	printLocaleDiffs(pr, pv.Changes)
}

func localeCounts(r release.Release) string {
	parts := make([]string, 0, len(r.Locales))
	for _, l := range r.Locales {
		c := r.Counts.Locales[l]
		s := fmt.Sprintf("%s %d", l, c.Messages)
		if c.Outdated > 0 {
			s += fmt.Sprintf(" (%d outdated)", c.Outdated)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func artifactCounts(r release.Release) string {
	return fmt.Sprintf("%s (%d new), %s", plural(r.Counts.Artifacts, "artifact", "artifacts"), r.Counts.NewArtifacts, humanBytes(r.Counts.Bytes))
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}

func when(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (inv *invocation) releaseList(ctx context.Context, rc *releaseClient, limit int) error {
	rels, err := rc.svc.Releases(ctx, rc.scope, limit)
	if err != nil {
		return inv.releaseError(err, "can't list releases")
	}
	serving, err := rc.serving(ctx, inv)
	if err != nil {
		return err
	}
	out := releaseListJSON{Schema: "glossa.cli.release.list/v1", Releases: make([]releaseItemJSON, len(rels))}
	for i, r := range rels {
		out.Releases[i] = releaseItemJSON{Release: r, Serving: orNone(serving[r.ID])}
	}
	return inv.emit(out, func(pr *printer) {
		if len(rels) == 0 {
			pr.line("No releases yet: `glossa release publish` publishes the first.")
			return
		}
		rows := [][]string{{"VERSION", "ENVIRONMENT", "SERVING", "MESSAGES", "LOCALES", "CREATED", "ID", "NOTE"}}
		for _, r := range out.Releases {
			rows = append(rows, []string{fmt.Sprintf("v%d", r.Version), r.Environment, dash(strings.Join(r.Serving, ", ")),
				strconv.Itoa(r.Counts.Messages), strings.Join(r.Locales, ", "), when(r.CreatedAt), r.ID, r.Note})
		}
		pr.table(rows)
	})
}

func orNone(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (inv *invocation) releaseShow(ctx context.Context, rc *releaseClient, ref string) error {
	r, err := rc.resolve(ctx, inv, ref)
	if err != nil {
		return err
	}
	serving, err := rc.serving(ctx, inv)
	if err != nil {
		return err
	}
	parent, err := rc.ref(ctx, inv, r.ParentID)
	if err != nil {
		return err
	}
	out := releaseShowJSON{Schema: "glossa.cli.release.show/v1", Release: r, Serving: orNone(serving[r.ID])}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s %s", pr.bold(fmt.Sprintf("Release v%d", r.Version)), pr.dim(r.ID))
		field := func(label, value string) { pr.line("  %-13s %s", label, value) }
		field("published to", fmt.Sprintf("%s, %s by %s", r.Environment, when(r.CreatedAt), r.Author))
		field("serving", dash(strings.Join(out.Serving, ", ")))
		if parent != nil {
			field("parent", fmt.Sprintf("v%d %s", parent.Version, pr.dim(parent.ID)))
		}
		if r.Note != "" {
			field("note", r.Note)
		}
		field("policy", policyString(r.Policy))
		field("source", r.SourceLocale)
		field("messages", strconv.Itoa(r.Counts.Messages))
		field("locales", localeCounts(r))
		field("artifacts", fmt.Sprintf("%d (%d new), %s", r.Counts.Artifacts, r.Counts.NewArtifacts, humanBytes(r.Counts.Bytes)))
		field("digest", r.ManifestDigest)
	})
}

func policyString(p release.Policy) string {
	s := strings.Join(p.States, ", ")
	if p.IncludeOutdated {
		s += " (outdated too)"
	}
	return s
}

func (inv *invocation) releaseDiff(ctx context.Context, rc *releaseClient, refs []string) error {
	head, err := rc.resolve(ctx, inv, refs[len(refs)-1])
	if err != nil {
		return err
	}
	var baseID string
	if len(refs) == 2 {
		base, err := rc.resolve(ctx, inv, refs[0])
		if err != nil {
			return err
		}
		baseID = base.ID
	}
	d, err := rc.svc.Diff(ctx, rc.scope, head.ID, baseID)
	if err != nil {
		return inv.releaseError(err, "can't diff release v"+strconv.Itoa(head.Version))
	}
	base, err := rc.ref(ctx, inv, d.BaseReleaseID)
	if err != nil {
		return err
	}
	out := releaseDiffJSON{Schema: "glossa.cli.release.diff/v1", Release: *refOf(head), Base: base, Locales: d.Locales, Identical: true}
	for _, l := range d.Locales {
		out.Identical = out.Identical && len(l.Added)+len(l.Changed)+len(l.Removed) == 0
	}
	return inv.emit(out, func(pr *printer) { printReleaseDiff(pr, out) })
}

func printReleaseDiff(pr *printer, d releaseDiffJSON) {
	if d.Base == nil {
		pr.line("v%d %s: nothing to compare with, every message is added", d.Release.Version, pr.dim(d.Release.ID))
	} else {
		pr.line("v%d → v%d %s", d.Base.Version, d.Release.Version, pr.dim("("+d.Base.ID+" → "+d.Release.ID+")"))
	}
	if d.Identical {
		pr.line("  %s", "no message changed in any locale")
		return
	}
	printLocaleDiffs(pr, d.Locales)
}

// printLocaleDiffs lists what each locale adds, changes and removes.
func printLocaleDiffs(pr *printer, locales []localeDiffJSON) {
	for _, l := range locales {
		if len(l.Added)+len(l.Changed)+len(l.Removed) == 0 {
			pr.line("  %s  %s", l.Locale, pr.dim("unchanged"))
			continue
		}
		pr.line("  %s  %d added, %d changed, %d removed", pr.bold(l.Locale), len(l.Added), len(l.Changed), len(l.Removed))
		for _, k := range l.Added {
			pr.line("    %s", pr.ok("+ "+k))
		}
		for _, k := range l.Changed {
			pr.line("    %s", pr.warn("~ "+k))
		}
		for _, k := range l.Removed {
			pr.line("    %s", pr.bad("- "+k))
		}
	}
}

// releaseMove promotes or rolls back: it reads what the environment
// serves first, so the output says what changed.
func (inv *invocation) releaseMove(ctx context.Context, rc *releaseClient, r releaseArgs) error {
	var target string
	if len(r.releases) == 1 {
		rel, err := rc.resolve(ctx, inv, r.releases[0])
		if err != nil {
			return err
		}
		target = rel.ID
	}
	before, err := rc.svc.Environment(ctx, rc.scope, r.environment)
	if err != nil {
		return inv.releaseError(err, "can't read environment "+r.environment)
	}
	var (
		after release.Environment
		held  *release.Held
	)
	if r.action == "promote" {
		after, held, err = rc.svc.Promote(ctx, rc.scope, target, r.environment)
	} else {
		after, err = rc.svc.Rollback(ctx, rc.scope, r.environment, target)
	}
	if err != nil {
		return inv.releaseError(err, fmt.Sprintf("can't %s %s", r.action, r.environment))
	}
	if held != nil {
		return inv.releaseHeldPromote(ctx, rc, before, held)
	}
	env, err := rc.environmentJSON(ctx, inv, after)
	if err != nil {
		return err
	}
	previous, err := rc.ref(ctx, inv, before.CurrentReleaseID)
	if err != nil {
		return err
	}
	out := releaseMoveJSON{Schema: "glossa.cli.release." + r.action + "/v1", Environment: env, Previous: previous}
	return inv.emit(out, func(pr *printer) {
		was := "nothing"
		if previous != nil {
			was = fmt.Sprintf("v%d", previous.Version)
		}
		verb := "now serves"
		if r.action == "rollback" {
			verb = "rolled back to"
		}
		pr.line("%s %s %s v%d %s; was %s", pr.pass(), env.Name, verb, env.Release.Version, pr.dim("("+env.Release.ID+")"), was)
		pr.line("  %s", pr.dim("only the pointer moved; glossa-edge serves it within seconds"))
	})
}

func (inv *invocation) releaseEnvironments(ctx context.Context, rc *releaseClient) error {
	envs, err := rc.svc.Environments(ctx, rc.scope)
	if err != nil {
		return inv.releaseError(err, "can't list environments")
	}
	out := releaseEnvironmentsJSON{Schema: "glossa.cli.release.environments/v1", Environments: make([]environmentJSON, len(envs))}
	for i, e := range envs {
		if out.Environments[i], err = rc.environmentJSON(ctx, inv, e); err != nil {
			return err
		}
	}
	return inv.emit(out, func(pr *printer) {
		rows := [][]string{{"ENVIRONMENT", "RELEASE", "PUBLISHED", "SHIPS", "ID"}}
		for _, e := range out.Environments {
			version, published, id := "—", "", ""
			if e.Release != nil {
				r := rc.cache[e.Release.ID]
				version, published, id = fmt.Sprintf("v%d", r.Version), when(r.CreatedAt), r.ID
			}
			rows = append(rows, []string{e.Name, version, published, policyString(e.Policy), id})
		}
		pr.table(rows)
	})
}

func (inv *invocation) releaseKeys(ctx context.Context, rc *releaseClient, r releaseArgs) error {
	switch r.keysAction {
	case "create":
		var scope *release.KeyScope
		if r.scopeGiven {
			scope = &release.KeyScope{Environments: r.environments, Branches: r.preview}
		}
		k, err := rc.svc.CreateDeliveryKey(ctx, rc.scope, r.name, scope, orDefault(r.idempotencyKey, newIdempotencyKey()))
		if err != nil {
			return inv.releaseError(err, "can't create delivery key "+r.name)
		}
		out := releaseKeyJSON{Schema: "glossa.cli.release.key/v1", Action: "created", Key: k}
		return inv.emit(out, func(pr *printer) {
			pr.line("%s Created delivery key %q %s", pr.pass(), k.Name, pr.dim("("+k.ID+")"))
			pr.line("  %s", pr.bold(k.Key))
			pr.line("  %s", pr.dim("It reads "+scopeText(k.Scope)+"; anything else answers 404."))
			pr.line("  %s", pr.dim("Publishable by design: runtimes fetch releases from glossa-edge with it (/v1/<key>/<environment>/manifest.json)."))
			pr.line("  %s", pr.dim(fmt.Sprintf("Revoke it with `glossa release keys revoke %s`.", k.ID)))
		})
	case "scope":
		return inv.setKeyScope(ctx, rc, r)
	case "revoke":
		return inv.revokeKey(ctx, rc, r.name)
	}
	keys, err := rc.svc.DeliveryKeys(ctx, rc.scope)
	if err != nil {
		return inv.releaseError(err, "can't list delivery keys")
	}
	out := releaseKeysJSON{Schema: "glossa.cli.release.keys/v1", Keys: keys}
	if out.Keys == nil {
		out.Keys = []release.DeliveryKey{}
	}
	return inv.emit(out, func(pr *printer) {
		if len(keys) == 0 {
			pr.line("No delivery keys yet: `glossa release keys create web` makes one.")
			return
		}
		rows := [][]string{{"NAME", "KEY", "READS", "STATUS", "CREATED", "ID"}}
		for _, k := range keys {
			status := "active"
			if k.RevokedAt != nil {
				status = "revoked " + when(*k.RevokedAt)
			}
			rows = append(rows, []string{k.Name, k.Key, scopeText(k.Scope), status, when(k.CreatedAt), k.ID})
		}
		pr.table(rows)
	})
}

// scopeText reads a key's scope back: "production", or
// "preview + branch previews".
func scopeText(s release.KeyScope) string {
	what := strings.Join(s.Environments, ", ")
	switch {
	case s.Branches && what != "":
		return what + " + branch previews"
	case s.Branches:
		return "branch previews"
	case what == "":
		return "nothing"
	}
	return what
}

// setKeyScope replaces what a key reads (keys scope). The key itself
// stays, so bundles that ship it keep working.
func (inv *invocation) setKeyScope(ctx context.Context, rc *releaseClient, r releaseArgs) error {
	keys, err := rc.svc.DeliveryKeys(ctx, rc.scope)
	if err != nil {
		return inv.releaseError(err, "can't list delivery keys")
	}
	k, err := findKey(keys, r.name)
	if err != nil {
		return err
	}
	updated, err := rc.svc.SetDeliveryKeyScope(ctx, rc.scope, k.ID, release.KeyScope{Environments: r.environments, Branches: r.preview})
	if err != nil {
		return inv.releaseError(err, fmt.Sprintf("can't change the scope of delivery key %q", k.Name))
	}
	out := releaseKeyJSON{Schema: "glossa.cli.release.key/v1", Action: "scoped", Key: updated}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Delivery key %q now reads %s", pr.pass(), updated.Name, scopeText(updated.Scope))
		pr.line("  %s", pr.dim("glossa-edge follows within its key cache TTL (30 s by default) plus any CDN max-age."))
	})
}

// revokeKey revokes a key named by ID, or by the name of exactly one
// active key.
func (inv *invocation) revokeKey(ctx context.Context, rc *releaseClient, ref string) error {
	keys, err := rc.svc.DeliveryKeys(ctx, rc.scope)
	if err != nil {
		return inv.releaseError(err, "can't list delivery keys")
	}
	k, err := findKey(keys, ref)
	if err != nil {
		return err
	}
	if err := rc.svc.RevokeDeliveryKey(ctx, rc.scope, k.ID); err != nil {
		return inv.releaseError(err, fmt.Sprintf("can't revoke delivery key %q", k.Name))
	}
	if keys, err = rc.svc.DeliveryKeys(ctx, rc.scope); err != nil {
		return inv.releaseError(err, "can't list delivery keys")
	}
	if i := slices.IndexFunc(keys, func(x release.DeliveryKey) bool { return x.ID == k.ID }); i >= 0 {
		k = keys[i]
	}
	out := releaseKeyJSON{Schema: "glossa.cli.release.key/v1", Action: "revoked", Key: k}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Revoked delivery key %q %s", pr.pass(), k.Name, pr.dim("("+k.ID+")"))
		pr.line("  %s", pr.dim("glossa-edge stops serving it within its key cache TTL (30 s by default) plus any CDN max-age"))
	})
}

func findKey(keys []release.DeliveryKey, ref string) (release.DeliveryKey, error) {
	var byName []release.DeliveryKey
	for _, k := range keys {
		if k.ID == ref {
			return k, nil
		}
		if k.Name == ref && k.RevokedAt == nil {
			byName = append(byName, k)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return release.DeliveryKey{}, &Error{Exit: ExitUsage, Code: "key_not_found", What: fmt.Sprintf("no delivery key %q", ref),
			Why: "no key has that ID, and no active key that name", Fix: "run `glossa release keys` for the project's keys"}
	}
	return release.DeliveryKey{}, &Error{Exit: ExitUsage, Code: "key_ambiguous", What: fmt.Sprintf("%d active delivery keys are named %q", len(byName), ref),
		Fix: "revoke by ID (`glossa release keys` lists them)"}
}
