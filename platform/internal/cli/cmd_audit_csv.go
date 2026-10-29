package cli

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	audit "go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// `glossa audit csv` converts an export's entries.jsonl to CSV locally
// (RFC 0006 §6.2: the server produces one format). `audit list --csv`
// writes the same columns.
//
// The columns are stable: new ones are only ever added at the end, and
// a change to the order is a new format. An entry holds no text, only
// identifiers, selectors and the shapes of everything else, so neither
// does a row; keep it that way.
var auditCSVColumns = []string{
	"sequence", "occurred_at", "source", "action", "actor",
	"aggregate_type", "aggregate_id", "project_id", "locale",
	"event_id", "request_id", "trace_id",
	"summary", "prev_hash", "hash",
}

// auditCSVJSON is glossa.cli.audit.csv/v1, printed with --json when
// --out names a file.
type auditCSVJSON struct {
	Schema  string   `json:"schema"`
	Source  string   `json:"source"`
	Out     string   `json:"out"`
	Rows    int      `json:"rows"`
	Columns []string `json:"columns"`
}

func (inv *invocation) auditCSV(a auditArgs, pos []string) error {
	if len(pos) != 1 {
		return usageError(inv.name, "audit csv takes one export: its directory, or its entries.jsonl")
	}
	if inv.json && a.out == "" {
		return usageError(inv.name, "audit csv writes CSV to stdout; --json describes a file, so give --out")
	}
	src := inv.resolvePath(pos[0])
	st, err := os.Stat(src)
	if err != nil {
		return &Error{Exit: ExitUsage, Code: "export_unreadable", What: "can't read the export " + pos[0],
			Why: err.Error(), Fix: "pass the export's directory, or its entries.jsonl"}
	}
	if st.IsDir() {
		src = filepath.Join(src, audit.EntriesFile)
	}
	in, err := os.Open(src)
	if err != nil {
		return &Error{Exit: ExitUsage, Code: "export_unreadable", What: "can't read " + inv.display(src), Why: err.Error(),
			Fix: "pass the export's directory, which holds entries.jsonl"}
	}
	defer in.Close()

	var dst io.Writer = inv.env.Stdout
	var tmp *os.File
	outPath := ""
	if a.out != "" {
		outPath = inv.resolvePath(a.out)
		if tmp, err = os.CreateTemp(filepath.Dir(outPath), ".glossa-audit-csv-*"); err != nil {
			return &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't write the file", Where: outPath, Why: err.Error()}
		}
		defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
		dst = tmp
	}
	rows, err := convertAuditLines(in, dst, inv.display(src))
	if err != nil {
		return err
	}
	if tmp == nil {
		return nil
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), outPath); err != nil {
		return &Error{Exit: ExitUsage, Code: "output_unwritable", What: "can't write the file", Where: outPath, Why: err.Error()}
	}
	out := auditCSVJSON{Schema: "glossa.cli.audit.csv/v1", Source: inv.display(src), Out: inv.display(outPath), Rows: rows, Columns: auditCSVColumns}
	return inv.emit(out, func(p *printer) {
		p.line("%s %s written to %s", p.pass(), plural(rows, "row", "rows"), out.Out)
		p.line("  %s", p.dim("converted, not verified: `glossa audit verify` checks the export itself"))
	})
}

// convertAuditLines reads JSON Lines and writes the header and one row
// per line, returning the number of rows.
func convertAuditLines(r io.Reader, w io.Writer, name string) (int, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write(auditCSVColumns); err != nil {
		return 0, err
	}
	br := bufio.NewReader(r)
	rows := 0
	for line := 1; ; line++ {
		raw, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(raw)) > 0 {
			row, rerr := auditCSVRow(raw)
			if rerr != nil {
				return rows, &Error{Exit: ExitUsage, Code: "entries_unreadable", What: fmt.Sprintf("line %d of %s is not an audit entry", line, name),
					Why: rerr.Error(), Fix: "`glossa audit verify` names what is wrong with an export"}
			}
			if werr := cw.Write(row); werr != nil {
				return rows, werr
			}
			rows++
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rows, &Error{Exit: ExitUsage, Code: "entries_unreadable", What: "can't read " + name, Why: err.Error()}
		}
	}
	cw.Flush()
	return rows, cw.Error()
}

// writeAuditEntriesCSV is `audit list --csv`: the API's entries in the
// export's columns.
func writeAuditEntriesCSV(w io.Writer, entries []remote.AuditEntry) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(auditCSVColumns); err != nil {
		return err
	}
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		row, err := auditCSVRow(raw)
		if err != nil {
			return err
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// auditCSVRow is one entry's cells, in auditCSVColumns order. Strings
// are verbatim, absent and null members are empty, and the summary is
// its compact JSON (keys sorted), so a row reads the same whether it
// came from a line or from the API.
func auditCSVRow(raw []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var entry map[string]any
	if err := dec.Decode(&entry); err != nil {
		return nil, err
	}
	row := make([]string, len(auditCSVColumns))
	for i, col := range auditCSVColumns {
		cell, err := csvCell(col, entry[col])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", col, err)
		}
		row[i] = cell
	}
	if row[0] == "" {
		return nil, errors.New("no sequence")
	}
	return row, nil
}

func csvCell(col string, v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		if col == "occurred_at" {
			return auditInstant(x), nil
		}
		return x, nil
	case json.Number:
		return x.String(), nil
	default:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return "", err
		}
		return string(bytes.TrimRight(b.Bytes(), "\n")), nil
	}
}

// auditInstant spells an instant the way an export line does (UTC, six
// fractional digits), so a row from the API and one from a line agree
// whatever precision the API rendered; an unparseable value stays as is.
func auditInstant(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000Z")
}
