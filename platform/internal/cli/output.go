package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// printer writes a command's human output. It colors only when asked
// (a TTY without NO_COLOR or --no-color) and prints nothing with --quiet.
type printer struct {
	w     io.Writer
	color bool
	quiet bool
}

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
	ansiBold   = "\x1b[1m"
)

func (p *printer) paint(code, s string) string {
	if !p.color {
		return s
	}
	return code + s + ansiReset
}

func (p *printer) ok(s string) string   { return p.paint(ansiGreen, s) }
func (p *printer) bad(s string) string  { return p.paint(ansiRed, s) }
func (p *printer) warn(s string) string { return p.paint(ansiYellow, s) }
func (p *printer) dim(s string) string  { return p.paint(ansiDim, s) }
func (p *printer) bold(s string) string { return p.paint(ansiBold, s) }

// Marks for check-style output (§30).
func (p *printer) pass() string { return p.ok("✓") }
func (p *printer) fail() string { return p.bad("✗") }
func (p *printer) caution() string {
	return p.warn("!")
}

// line prints one line unless quiet.
func (p *printer) line(format string, args ...any) {
	if p.quiet {
		return
	}
	fmt.Fprintf(p.w, format+"\n", args...)
}

// table prints rows with aligned columns (the first row is the header).
func (p *printer) table(rows [][]string) {
	if p.quiet || len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			if n := len([]rune(c)); i < len(widths) && n > widths[i] {
				widths[i] = n
			}
		}
	}
	for ri, r := range rows {
		var b strings.Builder
		for i, c := range r {
			if i > 0 {
				b.WriteString("  ")
			}
			if i < len(r)-1 {
				c += strings.Repeat(" ", widths[i]-len([]rune(c)))
			}
			b.WriteString(c)
		}
		s := strings.TrimRight(b.String(), " ")
		if ri == 0 {
			s = p.dim(s)
		}
		fmt.Fprintln(p.w, s)
	}
}

// writeJSON prints v as indented JSON: the one document a --json run
// writes to stdout.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// printError renders err for humans on w.
func printError(w io.Writer, p *printer, e *Error) {
	fmt.Fprintf(w, "%s %s\n", p.bad("error:"), e.What)
	for _, f := range []struct{ label, value string }{{"where", e.Where}, {"why", e.Why}, {"fix", e.Fix}} {
		if f.value != "" {
			fmt.Fprintf(w, "  %s %s\n", p.dim(fmt.Sprintf("%-6s", f.label+":")), f.value)
		}
	}
}

// plural returns "1 message" / "3 messages".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
