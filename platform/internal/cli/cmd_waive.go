package cli

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa waive` (RFC 0005 §13 wave 6): accepting a finding, listing
// what has been accepted, and taking one back.
//
// Two properties carry the whole command.
//
// The **reason is mandatory**, here as everywhere else. The server
// refuses a blank one with a 400, and this refuses it before the
// request, with a sentence that says why rather than a field name: a
// waiver without a reason is a suppression nobody has to justify, and a
// suppression system that does not ask is the one that ends up
// suppressing everything (RFC 0005 §2.3, §14 decision 5).
//
// And a waiver is **by fingerprint**, never by key, locale or layer. The
// fingerprint is the finding's identity across the terminal, the pull
// request and the server (domain.Fingerprint), so a waiver created here
// accepts the same finding there. `glossa findings` prints the
// fingerprint next to every finding for exactly this reason.

const (
	// waiveSchema is a single waiver's document: creating one, or
	// revoking one.
	waiveSchema = "glossa.cli.waive/v1"
	// waiversSchema is the list.
	waiversSchema = "glossa.cli.waivers/v1"
)

// fingerprintPattern is the shape of a finding's identity
// (domain.FingerprintPrefix + hex). It catches the common mistake —
// pasting a message key — before a request goes out.
var fingerprintPattern = regexp.MustCompile(`^` + domain.FingerprintPrefix + `[0-9a-f]+$`)

// waiverJSON is one waiver.
type waiverJSON struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Reason      string `json:"reason"`
	// Scope is project or branch; Ref names the branch of a
	// branch-scoped waiver.
	Scope string `json:"scope"`
	Ref   string `json:"ref,omitempty"`
	// SourceRevision is the revision the waiver was made against. It
	// dies when the source moves past it, and the finding comes back
	// saying so (RFC 0005 §2.3).
	SourceRevision int `json:"source_revision"`
	// Active says it stands now: neither revoked nor expired.
	Active    bool   `json:"active"`
	ExpiresAt string `json:"expires_at,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	// Accepts is the finding the waiver accepts, from the most recent
	// stored one carrying the fingerprint. It is empty where no stored
	// finding carries it any more — which is exactly the unexamined
	// waiver a dashboard should show.
	Accepts *waiverFindingJSON `json:"accepts"`
}

type waiverFindingJSON struct {
	Layer     string `json:"layer,omitempty"`
	Code      string `json:"code,omitempty"`
	Locale    string `json:"locale,omitempty"`
	Key       string `json:"key,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Message   string `json:"message,omitempty"`
}

type waiveJSON struct {
	Schema string `json:"schema"`
	// Action is created, unchanged (the project already had a live
	// waiver for this finding and reach) or revoked.
	Action string     `json:"action"`
	Waiver waiverJSON `json:"waiver"`
}

type waiversJSON struct {
	Schema  string       `json:"schema"`
	Waivers []waiverJSON `json:"waivers"`
}

const waiveUsage = `waive <fingerprint> --reason "why this is fine" [flags]
       waive --list [filters]
       waive --revoke <waiver-id>

A waiver accepts one finding, by the fingerprint ` + "`glossa findings`" + ` prints.
The reason is required: a waived finding is still computed, still reported
and counted on its own, and the reason is what makes it reviewable.

Creating:
  --reason <why>        required, non-empty
  --scope project|branch  how far it reaches (default: project)
  --ref <branch>        the branch, for --scope branch (default: the CI branch)
  --expires <when>      YYYY-MM-DD (through the end of that day, UTC) or an RFC 3339 timestamp
  --source-revision <n> the revision to waive against (default: the one the finding carries)
Listing:
  --list [--fingerprint F] [--layer L] [--code C] [--message KEY] [--active[=false]] [--limit N]`

func runWaive(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(waiveUsage)
	var (
		reason    = fs.String("reason", "", "why this finding is fine (required)")
		scope     = fs.String("scope", "", "how far the waiver reaches: project (default) or branch")
		ref       = fs.String("ref", "", "the branch, for --scope branch")
		expires   = fs.String("expires", "", "when the waiver retires: YYYY-MM-DD or an RFC 3339 timestamp")
		revision  = fs.Int("source-revision", 0, "the source revision to waive against")
		list      = fs.Bool("list", false, "list the project's waivers instead of creating one")
		revoke    = fs.String("revoke", "", "revoke this waiver, by ID")
		fpFilter  = fs.String("fingerprint", "", "list: only waivers for this fingerprint")
		layer     = fs.String("layer", "", "list: only waivers accepting this layer's findings")
		code      = fs.String("code", "", "list: only waivers accepting this code")
		key       = fs.String("message", "", "list: only waivers accepting findings about this message key")
		active    = fs.Bool("active", false, "list: only the waivers that stand now (--active=false: only the revoked and expired)")
		limit     = fs.Int("limit", maxFindingsListed, "list: read at most this many waivers (0: all of them)")
		activeSet bool
	)
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	activeSet = isSet(fs, "active")
	switch {
	case *list && *revoke != "":
		return usageError(inv.name, "--list and --revoke do different things: pick one")
	case *list:
		if err := noMore(inv, pos); err != nil {
			return err
		}
		f := remote.WaiverFilter{Fingerprint: *fpFilter, Code: *code, Key: *key}
		if *layer != "" {
			if !domain.Layer(*layer).Valid() {
				return usageError(inv.name, "%q is not a QA layer; the layers are %s", *layer, strings.Join(layerNames(), ", "))
			}
			f.Layer = *layer
		}
		if activeSet {
			f.Active = active
		}
		return inv.listWaivers(ctx, f, *limit)
	case *revoke != "":
		if err := noMore(inv, pos); err != nil {
			return err
		}
		return inv.revokeWaiver(ctx, *revoke)
	}

	in, err := waiverToCreate(inv, pos, *reason, *scope, *ref, *expires, *revision)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	if in.Scope == string(domain.WaiverBranch) && in.Ref == "" {
		in.Ref = inv.ciBranch()
		if in.Ref == "" {
			return usageError(inv.name, "--scope branch needs --ref <branch>: no branch in the environment to take one from")
		}
	}
	w, created, err := p.client.Waive(ctx, p.scope, in)
	if err != nil {
		return inv.qualityError(err, "can't waive the finding")
	}
	out := waiveJSON{Schema: waiveSchema, Action: "created", Waiver: toWaiverJSON(w)}
	if !created {
		out.Action = "unchanged"
	}
	return inv.emit(out, func(pr *printer) { printWaive(pr, out) })
}

// waiverToCreate reads the waiver from the command line, and refuses
// the ones that cannot be justified or cannot be addressed.
func waiverToCreate(inv *invocation, pos []string, reason, scope, ref, expires string, revision int) (remote.CreateWaiver, error) {
	var out remote.CreateWaiver
	if len(pos) == 0 {
		return out, usageError(inv.name, "which finding? `glossa waive <fingerprint> --reason \"…\"`; "+
			"`glossa findings` prints a fingerprint next to every finding")
	}
	if err := noMore(inv, pos[1:]); err != nil {
		return out, err
	}
	out.Fingerprint = strings.TrimSpace(pos[0])
	if !fingerprintPattern.MatchString(out.Fingerprint) {
		return out, usageError(inv.name, "%q is not a finding's fingerprint (they look like f_7c1a3e9b40d2f815)", pos[0])
	}
	out.Reason = strings.TrimSpace(reason)
	if out.Reason == "" {
		return out, &Error{Exit: ExitUsage, Code: "waiver_needs_a_reason",
			What: "a waiver needs a reason",
			Why: "a waived finding is still computed and still reported; the reason is what makes it reviewable later, " +
				"and a waiver without one is a suppression nobody has to justify",
			Fix: `pass --reason, e.g. --reason "\"Login\" is the German term, agreed with marketing"`}
	}
	switch domain.WaiverScope(scope) {
	case "", domain.WaiverProject:
		out.Scope = string(domain.WaiverProject)
	case domain.WaiverBranch:
		out.Scope, out.Ref = string(domain.WaiverBranch), strings.TrimSpace(ref)
	default:
		return out, usageError(inv.name, "--scope is project or branch, not %q", scope)
	}
	if expires != "" {
		at, err := parseWaiverExpiry(inv, expires)
		if err != nil {
			return out, err
		}
		out.ExpiresAt = &at
	}
	if revision > 0 {
		out.SourceRevision = &revision
	}
	return out, nil
}

// parseWaiverExpiry reads --expires. A bare date is through the end of
// that day in UTC, because "expires 2026-12-31" means the waiver covers
// the 31st; midnight at its start would retire it a day early, which
// nobody writing a date means.
func parseWaiverExpiry(inv *invocation, v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if d, err := time.Parse(time.DateOnly, v); err == nil {
		return d.UTC().Add(24*time.Hour - time.Second), nil
	}
	return time.Time{}, usageError(inv.name,
		"--expires takes a date (2026-12-31, through the end of that day UTC) or an RFC 3339 timestamp, not %q", v)
}

func (inv *invocation) listWaivers(ctx context.Context, f remote.WaiverFilter, limit int) error {
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	ws, err := p.client.Waivers(ctx, p.scope, f, limit)
	if err != nil {
		return inv.qualityError(err, "can't read the project's waivers")
	}
	out := waiversJSON{Schema: waiversSchema, Waivers: make([]waiverJSON, 0, len(ws))}
	for _, w := range ws {
		out.Waivers = append(out.Waivers, toWaiverJSON(w))
	}
	return inv.emit(out, func(pr *printer) { printWaivers(pr, out) })
}

func (inv *invocation) revokeWaiver(ctx context.Context, id string) error {
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	if err := p.client.RevokeWaiver(ctx, p.scope, id); err != nil {
		return inv.qualityError(err, "can't revoke the waiver")
	}
	out := waiveJSON{Schema: waiveSchema, Action: "revoked", Waiver: waiverJSON{ID: id}}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s waiver %s revoked", pr.pass(), id)
		pr.line("  %s", pr.dim("the findings it accepted are ordinary findings again from the next check"))
	})
}

func toWaiverJSON(w remote.Waiver) waiverJSON {
	out := waiverJSON{
		ID: w.Id, Fingerprint: w.Fingerprint, Reason: w.Reason, Scope: string(w.Scope),
		Ref: derefStr(w.Ref), SourceRevision: w.SourceRevision, Active: w.Active,
		CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt.UTC().Format(time.RFC3339),
	}
	if w.ExpiresAt != nil {
		out.ExpiresAt = w.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if w.RevokedAt != nil {
		out.RevokedAt = w.RevokedAt.UTC().Format(time.RFC3339)
	}
	if a := w.Accepts; a != nil {
		out.Accepts = &waiverFindingJSON{
			Code: derefStr(a.Code), Locale: derefStr(a.Locale), Key: derefStr(a.Key),
			Namespace: derefStr(a.Namespace), Message: derefStr(a.Message),
		}
		if a.Layer != nil {
			out.Accepts.Layer = string(*a.Layer)
		}
	}
	return out
}

func printWaive(p *printer, out waiveJSON) {
	w := out.Waiver
	verb := "waived"
	if out.Action == "unchanged" {
		verb = "already waived"
	}
	p.line("%s %s %s", p.pass(), verb, p.dim(w.Fingerprint))
	if a := w.Accepts; a != nil {
		p.line("  %s", waiverSubject(*a))
	}
	p.line("  %s", p.dim("reason: "+w.Reason))
	p.line("  %s", p.dim(waiverReach(w)))
	p.line("  %s", p.dim("the finding is still computed and still reported, at severity `waived`, "+
		"and can never fail a check"))
}

func waiverReach(w waiverJSON) string {
	var b strings.Builder
	b.WriteString("scope: " + w.Scope)
	if w.Ref != "" {
		b.WriteString(" (" + w.Ref + ")")
	}
	b.WriteString(", against source revision " + strconv.Itoa(w.SourceRevision))
	if w.ExpiresAt != "" {
		b.WriteString(", expires " + w.ExpiresAt)
	}
	return b.String()
}

func printWaivers(p *printer, out waiversJSON) {
	if len(out.Waivers) == 0 {
		p.line("%s no waiver", p.pass())
		return
	}
	rows := [][]string{{"ID", "FINGERPRINT", "ACCEPTS", "SCOPE", "STATE", "REASON"}}
	for _, w := range out.Waivers {
		accepts := "—"
		if w.Accepts != nil {
			accepts = waiverSubject(*w.Accepts)
		}
		scope := w.Scope
		if w.Ref != "" {
			scope += " " + w.Ref
		}
		rows = append(rows, []string{shortID(w.ID), w.Fingerprint, accepts, scope, waiverState(w), w.Reason})
	}
	p.table(rows)
}

// waiverState says whether a waiver stands, and what ended it.
func waiverState(w waiverJSON) string {
	switch {
	case w.RevokedAt != "":
		return "revoked"
	case w.Active:
		return "active"
	case w.ExpiresAt != "":
		return "expired"
	}
	return "inactive"
}

// waiverSubject names the finding a waiver accepts. An empty one is
// said as such: a waiver nothing carries any more is technical debt
// with a reason attached, and hiding that would defeat the point of
// listing it.
func waiverSubject(a waiverFindingJSON) string {
	var parts []string
	if a.Layer != "" {
		parts = append(parts, a.Layer)
	}
	if a.Code != "" {
		parts = append(parts, a.Code)
	}
	where := strings.TrimSpace(strings.Join([]string{a.Locale, a.Key}, " "))
	if where != "" {
		parts = append(parts, where)
	}
	if len(parts) == 0 {
		return "no stored finding carries this fingerprint any more"
	}
	return strings.Join(parts, " ")
}
