package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/cli/v0"
)

// historyJSON is what `--history` did with the audit-entry plan.
type historyJSON struct {
	// Sent is how many rows went to the server; Recorded how many it
	// recorded now; Existing how many an earlier import (of this or
	// another project of the tenant) had recorded already.
	Sent     int `json:"sent"`
	Recorded int `json:"recorded"`
	Existing int `json:"existing"`
}

// The codes an unresolved row is sent with: the plan's sentence is for
// people, and the trail records no prose.
const (
	unresolvedDeleted       = "translation_deleted"
	unresolvedNoTranslation = "no_translation"
)

// historyRows turns the audit-entry plan into what the audit import
// takes: each row's actor, time and translation, and the SHA-256 of its
// text before and after — never the text, which RFC 0006 §6.1 keeps out
// of every audit entry. The digests are of the UTF-8 bytes exactly as
// v0.3 stored them, so whoever holds the archived dump can show which
// text a digest stands for.
func historyRows(entries []v0.AuditEntry) []remote.V0HistoryRow {
	rows := make([]remote.V0HistoryRow, 0, len(entries))
	for _, e := range entries {
		r := remote.V0HistoryRow{
			V0ID: strconv.FormatInt(e.V0ID, 10), Action: e.Action, Actor: e.Actor, OccurredAt: e.OccurredAt.UTC(),
			Key: e.Key, Locale: e.Locale, BeforeSHA256: textSHA256(e.Before), AfterSHA256: textSHA256(e.After),
		}
		if e.Unresolved != "" {
			r.Key, r.Locale, r.Unresolved = "", "", unresolvedDeleted
			if e.V0TranslationID == "" {
				r.Unresolved = unresolvedNoTranslation
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// textSHA256 is the hex SHA-256 of a v0.3 value; "" for none (a row
// that created or deleted the translation).
func textSHA256(s *string) string {
	if s == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(*s))
	return hex.EncodeToString(sum[:])
}

// sendHistory sends v0.3's history to the Audit context in batches
// (RFC 0006 §7.2). Every row is idempotent on its v0.3 id, so a re-run,
// a retried batch, or the import of another project of the same tenant
// — whose plan carries the rows whose translation is gone again —
// records nothing twice.
func (inv *invocation) sendHistory(ctx context.Context, p *project, plan v0.DBPlan) (*historyJSON, error) {
	rows := historyRows(plan.AuditEntries)
	out := &historyJSON{}
	for start := 0; start < len(rows); start += remote.MaxV0HistoryBatch {
		batch := rows[start:min(start+remote.MaxV0HistoryBatch, len(rows))]
		r, err := p.client.ImportV0History(ctx, p.scope, remote.V0HistoryImport{
			Restore: plan.Restore.DumpName, RestoreSHA256: plan.Restore.DumpSHA256, Entries: batch,
		})
		if err != nil {
			return out, historyError(inv.apiError(err, "can't import v0.3's history into the audit trail"), err)
		}
		out.Sent += len(batch)
		out.Recorded += r.Recorded
		out.Existing += r.Existing
	}
	return out, nil
}

// historyError says what the audit import's own refusals mean.
func historyError(e, cause error) error {
	var ce *Error
	var ae *remote.APIError
	if !errors.As(e, &ce) || !errors.As(cause, &ae) {
		return e
	}
	switch ae.Status {
	case 403:
		ce.Fix = "importing history writes the organisation's audit trail, which only an owner may (audit.import); no API " +
			"token scope reaches it. Sign in as the owner with `glossa login --device` and run it again, or drop --history to import without it"
	case 404:
		if ae.Code == "not_found" && ce.Why != "" {
			ce.Fix = "check tenant and project in glossa.yaml; a server older than RFC 0006 wave 4 has no audit-imports route"
		}
	}
	return ce
}
