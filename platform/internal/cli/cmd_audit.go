package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	audit "go.klarlabs.de/glossa/platform/internal/audit/domain"
)

// The audit trail (RFC 0006 §6). `verify` and `csv` work offline on an
// export; `list` and `export` read the server's audit API
// (cmd_audit_api.go).

const auditUsage = `audit list|export|csv|verify …

  audit list [--from T --to T --actor A --action X --project P --source S --limit N] [--json|--csv]
  audit export (--from T --to T | --first-sequence N [--last-sequence M]) --out <dir> [--public-key K] [--no-wait] [--force]
  audit csv <export dir|entries.jsonl> [--out file]
  audit verify <export> --public-key <key> [--public-key …] [--json]

verify checks a glossa.audit/v1 export offline, with no server: the manifest's Ed25519
signature, every line's hash and its link to the line before, the sequence with no gaps,
and that the lines are exactly the range and the file the manifest signs. <export> is the
export's directory (entries.jsonl and manifest.json), or its manifest.json.

--public-key is the trust anchor, never taken from the export itself: a glossa.audit.keys/1
document (what glossa-server serves at /.well-known/glossa-audit-keys.json, saved and
pinned), or keyId=base64 for one key. Repeat it to trust several keys, e.g. a retired one.

Exits 0 when the export verifies and 1 at the first thing that does not, which it names.`

// auditVerifyJSON is `audit verify --json`.
type auditVerifyJSON struct {
	Schema        string            `json:"schema"`
	Path          string            `json:"path"`
	OK            bool              `json:"ok"`
	TenantID      string            `json:"tenant_id,omitempty"`
	KeyID         string            `json:"key_id,omitempty"`
	CreatedAt     string            `json:"created_at,omitempty"`
	FirstSequence int64             `json:"first_sequence,omitempty"`
	LastSequence  int64             `json:"last_sequence,omitempty"`
	LastHash      string            `json:"last_hash,omitempty"`
	EntryCount    int64             `json:"entry_count"`
	Verified      int64             `json:"verified"`
	Failure       *auditFailureJSON `json:"failure"`
}

type auditFailureJSON struct {
	Check    string `json:"check"`
	Line     int64  `json:"line,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
	Reason   string `json:"reason"`
}

func runAudit(ctx context.Context, inv *invocation, args []string) error {
	var a auditArgs
	fs := inv.flags(auditUsage)
	a.register(fs)
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usageError(inv.name, "audit needs a subcommand: list, export, csv or verify")
	}
	if err := a.checkFlags(fs, pos[0]); err != nil {
		return usageError(inv.name, "%v", err)
	}
	switch pos[0] {
	case "list":
		return inv.auditList(ctx, a, pos[1:])
	case "export":
		return inv.auditExport(ctx, a, pos[1:])
	case "csv":
		return inv.auditCSV(a, pos[1:])
	case "verify":
	default:
		return usageError(inv.name, "unknown audit subcommand %q (list, export, csv, verify)", pos[0])
	}
	if len(pos) != 2 {
		return usageError(inv.name, "audit verify takes one export: a directory, or its manifest.json")
	}
	trusted, err := inv.auditKeys(a.keys)
	if err != nil {
		return err
	}
	dir, err := inv.exportDir(pos[1])
	if err != nil {
		return err
	}
	report, err := verifyExportDir(dir, trusted)
	if err != nil {
		return err
	}
	return inv.reportAuditVerify(dir, report)
}

// auditKeys reads the --public-key values.
func (inv *invocation) auditKeys(values []string) ([]audit.PublicKey, error) {
	if len(values) == 0 {
		return nil, &Error{Exit: ExitUsage, Code: "public_key_required",
			What: "audit verify needs the public key the export was signed with",
			Why:  "a signature checked with a key that came with the export proves nothing",
			Fix: "pass --public-key with the glossa.audit.keys/1 document glossa-server serves at " +
				"/.well-known/glossa-audit-keys.json (saved once, then pinned), or keyId=base64"}
	}
	var out []audit.PublicKey
	for _, v := range values {
		path := inv.resolvePath(v)
		if raw, err := os.ReadFile(path); err == nil {
			ks, err := audit.ParseKeyDocument(raw)
			if err != nil {
				return nil, &Error{Exit: ExitUsage, Code: "invalid_public_key", What: "--public-key " + v + " is not a key document",
					Why: err.Error(), Fix: "save the JSON glossa-server serves at /.well-known/glossa-audit-keys.json"}
			}
			out = append(out, ks...)
			continue
		} else if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(v, "=") {
			return nil, &Error{Exit: ExitUsage, Code: "invalid_public_key", What: "--public-key " + v + " can't be read",
				Why: err.Error(), Fix: "pass a glossa.audit.keys/1 file, or keyId=base64"}
		}
		k, err := audit.ParsePublicKeyPair(v)
		if err != nil {
			return nil, &Error{Exit: ExitUsage, Code: "invalid_public_key", What: "--public-key " + v + " is not a key",
				Why: err.Error(), Fix: "keyId=base64 of a 32-byte Ed25519 public key, or a glossa.audit.keys/1 file"}
		}
		out = append(out, k)
	}
	return out, nil
}

// exportDir resolves <export>: a directory, or the manifest in one.
func (inv *invocation) exportDir(arg string) (string, error) {
	path := inv.resolvePath(arg)
	st, err := os.Stat(path)
	if err != nil {
		return "", &Error{Exit: ExitUsage, Code: "export_unreadable", What: "can't read the export " + arg,
			Why: err.Error(), Fix: "pass the export's directory, which holds entries.jsonl and manifest.json"}
	}
	if !st.IsDir() {
		if filepath.Base(path) != audit.ManifestFile {
			return "", usageError(inv.name, "%s is neither an export directory nor its %s", arg, audit.ManifestFile)
		}
		path = filepath.Dir(path)
	}
	return path, nil
}

// verifyExportDir reads an export's two files and verifies them. A file
// that is missing is a failed verification, not a usage error: an
// export someone removed a file from does not verify.
func verifyExportDir(dir string, keys []audit.PublicKey) (audit.ExportReport, error) {
	manifest, err := os.ReadFile(filepath.Join(dir, audit.ManifestFile))
	if err != nil {
		return audit.ExportReport{Failure: &audit.ExportFailure{Check: audit.CheckManifest,
			Reason: fmt.Sprintf("%s can't be read: %v", audit.ManifestFile, err)}}, nil
	}
	f, err := os.Open(filepath.Join(dir, audit.EntriesFile))
	if err != nil {
		return audit.ExportReport{Failure: &audit.ExportFailure{Check: audit.CheckUnreadable,
			Reason: fmt.Sprintf("%s can't be read: %v", audit.EntriesFile, err)}}, nil
	}
	defer f.Close()
	return audit.VerifyExport(manifest, f, keys), nil
}

// auditVerifyDoc is the `audit verify --json` document for a report.
func auditVerifyDoc(dir string, r audit.ExportReport) auditVerifyJSON {
	out := auditVerifyJSON{Schema: "glossa.cli.audit.verify/v1", Path: dir, OK: r.OK, Verified: r.Verified}
	if m := r.Manifest; m != nil {
		out.TenantID, out.KeyID, out.CreatedAt = m.TenantID, m.KeyID, m.CreatedAt
		out.FirstSequence, out.LastSequence, out.LastHash = m.Range.FirstSequence, m.Range.LastSequence, m.Range.LastHash
		out.EntryCount = m.EntryCount
	}
	if f := r.Failure; f != nil {
		out.Failure = &auditFailureJSON{Check: string(f.Check), Line: f.Line, Sequence: f.Sequence, Reason: f.Reason}
	}
	return out
}

// printAuditVerify is verify's human report.
func printAuditVerify(p *printer, dir string, r audit.ExportReport, out auditVerifyJSON) {
	if r.OK {
		p.line("%s audit export verified: %s, sequences %d–%d of tenant %s",
			p.pass(), plural(int(out.EntryCount), "entry", "entries"), out.FirstSequence, out.LastSequence, out.TenantID)
		p.line("  signed with %s at %s", out.KeyID, out.CreatedAt)
		p.line("  last hash %s", out.LastHash)
		return
	}
	p.line("%s audit export does not verify: %s", p.fail(), r.Failure.Error())
	if r.Verified > 0 {
		p.line("  %s verified before it", plural(int(r.Verified), "entry", "entries"))
	}
	p.line("  %s", p.dim(dir))
}

func (inv *invocation) reportAuditVerify(dir string, r audit.ExportReport) error {
	out := auditVerifyDoc(dir, r)
	if err := inv.emit(out, func(p *printer) { printAuditVerify(p, dir, r, out) }); err != nil {
		return err
	}
	if !r.OK {
		return silentExit(ExitCheckFailed, "audit_verify_failed")
	}
	return nil
}
