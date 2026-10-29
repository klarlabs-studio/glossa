package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/cli/v0"
)

// importV0DB is `glossa import --from v0 --v0-db DSN` (RFC 0006 §7.2):
// the import of a restored v0.3 backup. It writes what the platform's
// API takes — locales, messages with their descriptions, translations
// with v0.3's provenance — and reports v0.3's users as invitations,
// which --invite sends through Identity, and its history as audit
// entries, which --history sends to Audit.
func (inv *invocation) importV0DB(ctx context.Context, fs *flag.FlagSet, f importFlags) error {
	for _, name := range []string{"v0-url", "v0-key-env"} {
		if isSet(fs, name) {
			return usageError(inv.name, "--%s belongs to an API import; --v0-db reads a restored backup instead", name)
		}
	}
	if f.invite && f.dryRun {
		return usageError(inv.name, "--invite sends invitations and --dry-run sends nothing: drop --dry-run, or drop --invite to see the plan")
	}
	if f.history && f.dryRun {
		return usageError(inv.name, "--history writes the audit trail and --dry-run writes nothing: drop --dry-run, or drop --history to see the plan")
	}
	only, err := localeSet(inv, f.locales)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	f.project = orDefault(f.project, p.cfg.Project)
	snap, err := v0.ReadRestore(ctx, f.db, f.tenant, f.project)
	if err != nil {
		return v0DBError(err, f.db)
	}
	plan, err := v0.BuildDBPlan(p.info.SourceLocale, snap, only)
	if err != nil {
		return &Error{Exit: ExitUsage, Code: "source_locale_missing", What: "can't import this v0.3 project", Why: err.Error(),
			Fix: "import into a project whose source locale the v0.3 project has"}
	}
	restore := plan.Restore
	out := importJSON{Schema: "glossa.cli.import/v1", From: "v0", DryRun: f.dryRun, LocalesAdded: []string{},
		Source: plan.ReportSource(f.db), Restore: &restore, Locales: plan.LocaleInfo, Invitations: plan.Invitations,
		AuditEntries: plan.AuditEntries, NotCarried: plan.NotCarried, Warnings: plan.Warnings}
	if f.dryRun {
		out.Items = planItems(plan.Plan)
	} else if out, err = inv.applyImport(ctx, p, plan.Plan, plan.OriginDetail(), out); err != nil {
		return err
	}
	if f.invite {
		if out.Invitations, err = inv.sendInvitations(ctx, p, out.Invitations); err != nil {
			return err
		}
	}
	if f.history {
		if out.History, err = inv.sendHistory(ctx, p, plan); err != nil {
			return err
		}
	}
	out.Summary = importSummary(out.Items)
	if err := inv.emit(out, func(pr *printer) { printImport(pr, out) }); err != nil {
		return err
	}
	failed := out.Summary["message"]["failed"] + out.Summary["translation"]["failed"]
	for _, i := range out.Invitations {
		if i.Status == v0.InvitationFailed {
			failed++
		}
	}
	if failed > 0 {
		return silentExit(ExitPartial, "partial_failure")
	}
	return nil
}

// sendInvitations sends the planned invitations — never the held ones —
// through Identity's invitation API, with their mapped roles and
// locales (RFC 0006 §7.2). It is idempotent on the address: one that is
// already a member or invited is reported as "exists" and not invited
// again, and each invitation carries an Idempotency-Key derived from
// its address, so a retried request does not invite twice either.
func (inv *invocation) sendInvitations(ctx context.Context, p *project, plans []v0.Invitation) ([]v0.Invitation, error) {
	members, err := p.client.Members(ctx, p.scope.Tenant)
	if err != nil {
		return nil, inv.apiError(err, "can't list the members the invitations would join")
	}
	known := map[string]string{}
	for _, m := range members {
		known[strings.ToLower(string(m.Email))] = m.Id
	}
	out := slices.Clone(plans)
	for i := range out {
		in := &out[i]
		if in.Status != v0.InvitationPlanned {
			continue
		}
		email := strings.ToLower(in.Email)
		if id, ok := known[email]; ok {
			in.Status, in.MemberID = v0.InvitationExists, id
			continue
		}
		sum := sha256.Sum256([]byte(email))
		m, _, err := p.client.InviteMember(ctx, p.scope.Tenant,
			remote.Invitation{Email: in.Email, Roles: in.Roles, Locales: in.Locales}, "v0-invite-"+hex.EncodeToString(sum[:16]))
		var ae *remote.APIError
		switch {
		case err == nil:
			in.Status, in.MemberID = v0.InvitationInvited, m.Id
			known[email] = m.Id
		case errors.As(err, &ae) && ae.Code == "already_member":
			in.Status = v0.InvitationExists
		case errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403):
			// Nobody can be invited with this credential: say so once.
			e := inv.apiError(err, "can't send the invitations")
			var ce *Error
			if errors.As(e, &ce) {
				ce.Fix = "use a token that may invite members (the admin scope), or drop --invite to report the plan only"
			}
			return nil, e
		default:
			in.Status, in.Reason = v0.InvitationFailed, err.Error()
		}
	}
	return out, nil
}

// v0DBError explains a restore the importer can't or won't read.
func v0DBError(err error, dsn string) error {
	where := v0.DescribeDSN(dsn)
	var (
		nr  *v0.NotARestoreError
		se  *v0.SchemaError
		rs  *v0.RowSecurityError
		pnf *v0.ProjectNotFoundError
		amb *v0.AmbiguousProjectError
	)
	switch {
	case errors.As(err, &nr):
		return &Error{Exit: ExitUsage, Code: "v0_not_a_restore", What: "refusing to read this database: it is not a marked v0.3 restore",
			Where: where, Why: nr.Reason, Err: err,
			Fix: "restore the v0.3 backup into a new database with " + v0.RestoreScript + " and point --v0-db at that database — never at v0.3's own"}
	case errors.As(err, &se):
		return &Error{Exit: ExitUsage, Code: "v0_schema_mismatch", What: "the restore is not Glossa v0.3's schema", Where: where, Why: se.Error(), Err: err,
			Fix: "restore a backup of v0.3 at apps/api migration 0006 (the deployed schema)"}
	case errors.As(err, &rs):
		return &Error{Exit: ExitUsage, Code: "v0_row_security", What: "can't read every v0.3 row as this role", Where: where, Why: rs.Error(), Err: err,
			Fix: "connect as the role v0-restore.sh ran as"}
	case errors.As(err, &pnf):
		return &Error{Exit: ExitUsage, Code: "v0_project_not_found", What: pnf.Error(), Where: where, Err: err,
			Fix: "check --v0-project (and --v0-tenant)"}
	case errors.As(err, &amb):
		return &Error{Exit: ExitUsage, Code: "v0_project_ambiguous", What: amb.Error(), Where: where, Err: err,
			Fix: "add --v0-tenant " + amb.Tenants[0] + " (or " + strings.Join(amb.Tenants[1:], ", ") + ")"}
	}
	return &Error{Exit: ExitNetwork, Code: "v0_db_unreachable", What: "can't read the restored v0.3 database", Where: where, Why: err.Error(), Err: err,
		Fix: "check --v0-db (credentials are best passed in PGPASSWORD, not the DSN)"}
}

// printV0Plans prints a --v0-db import's restore, locales, plans and
// what it didn't carry.
func printV0Plans(p *printer, out importJSON) {
	if out.Restore == nil {
		return
	}
	r := out.Restore
	p.line("%s restore %s of %s (sha256 %s…), restored %s", p.pass(), r.Database, r.DumpName, r.DumpSHA256[:min(12, len(r.DumpSHA256))],
		r.RestoredAt.Format("2006-01-02 15:04 MST"))
	var labels []string
	for _, l := range out.Locales {
		s := fmt.Sprintf("%s %q", l.Code, l.Label)
		if !l.Enabled {
			s += " (disabled)"
		}
		labels = append(labels, s)
	}
	p.line("  locales: %s", strings.Join(labels, ", "))
	n := map[string]int{}
	for _, i := range out.Invitations {
		n[i.Status]++
	}
	if n[v0.InvitationInvited]+n[v0.InvitationExists]+n[v0.InvitationFailed] > 0 {
		p.line("%s %d invitations sent, %d already members or invited, %d failed, %d held", p.pass(),
			n[v0.InvitationInvited], n[v0.InvitationExists], n[v0.InvitationFailed], n[v0.InvitationHeld])
	} else {
		p.line("%s %d invitations planned, %d held — not sent: pass --invite to send them", p.pass(),
			n[v0.InvitationPlanned], n[v0.InvitationHeld])
	}
	for _, i := range out.Invitations {
		if i.Status == v0.InvitationHeld || i.Status == v0.InvitationFailed {
			p.line("  %s %s: %s", p.caution(), i.Email, i.Reason)
		}
	}
	unresolved := 0
	for _, e := range out.AuditEntries {
		if e.Unresolved != "" {
			unresolved++
		}
	}
	if h := out.History; h != nil {
		p.line("%s %d history entries sent to the audit trail (%d without a translation): %d recorded, %d already there", p.pass(),
			h.Sent, unresolved, h.Recorded, h.Existing)
	} else {
		p.line("%s %d history entries planned as imported audit entries (%d without a translation) — not written: pass --history (an owner's) to import them",
			p.pass(), len(out.AuditEntries), unresolved)
	}
	for _, w := range out.Warnings {
		p.line("%s %s", p.caution(), w)
	}
	var not []string
	for _, n := range out.NotCarried {
		not = append(not, n.What)
	}
	p.line("  not carried (see --json for why): %s", strings.Join(not, "; "))
}
