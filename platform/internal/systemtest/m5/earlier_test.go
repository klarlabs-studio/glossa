//go:build system

package m5_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ── §12.7 Earlier exits hold ─────────────────────────────────────────

// earlierExit is one earlier milestone's exit test, run unchanged
// against this build.
type earlierExit struct {
	Name     string
	Pass     bool
	Took     time.Duration
	Verdicts []string
	Err      string
}

// earlierExitTests are M2's, M3's and M4's exit tests with the timeouts
// their own make targets give them.
var earlierExitTests = []struct {
	name, pkg, timeout string
}{
	{"M2", "./internal/systemtest/m2/...", "600s"},
	{"M3", "./internal/systemtest/m3/...", "900s"},
	{"M4", "./internal/systemtest/m4/...", "2700s"},
}

var (
	goTestVerdict = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL): .*$`)
	goTestSummary = regexp.MustCompile(`(?m)^(ok|FAIL)\s+\S+.*$`)
)

// earlierExits runs the M2, M3 and M4 exit tests, one after the other,
// exactly as `make system-m2`, `system-m3` and `system-m4` do: the same
// packages, the same tags, nothing changed. They use no M5 feature, so
// they are the check that M5's changes to shared seams — the outbox
// envelope, authz, Localization's ports, Release's pointer and
// manifest, runtimes/SPEC.md — broke nothing they prove (RFC 0006
// §11.2).
//
// Each one rewrites its own REPORT.md. Their verdict lines are copied
// into this report and the committed reports are put back, so a run of
// the M5 test leaves the earlier milestones' records as they were.
//
// GLOSSA_M5_EARLIER=off leaves them out for a quick local iteration on
// §12.1–§12.6. The criterion is then "not run" — unmet, and said so in
// the report — never quietly green.
func (s *scenario) earlierExits() {
	const id = "12.7"
	if os.Getenv("GLOSSA_M5_EARLIER") == "off" {
		s.skip(id, "GLOSSA_M5_EARLIER=off: the M2, M3 and M4 exit tests were left out of this run")
		return
	}
	for _, e := range earlierExitTests {
		report := filepath.Join(platformDir(), "internal", "systemtest", strings.ToLower(e.name), "REPORT.md")
		committed, readErr := os.ReadFile(report)
		start := time.Now()
		cmd := exec.Command("go", "test", "-tags=system", "-timeout="+e.timeout, "-count=1", "-v", e.pkg)
		cmd.Dir = platformDir()
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		run := earlierExit{Name: e.name, Pass: err == nil, Took: time.Since(start)}
		text := out.String()
		run.Verdicts = append(run.Verdicts, goTestVerdict.FindAllString(text, -1)...)
		run.Verdicts = append(run.Verdicts, goTestSummary.FindAllString(text, -1)...)
		if fresh, ferr := os.ReadFile(report); ferr == nil {
			run.Verdicts = append(run.Verdicts, verdictTable(string(fresh))...)
		}
		if readErr == nil {
			_ = os.WriteFile(report, committed, 0o644)
		}
		if err != nil {
			run.Err = err.Error()
			s.gap(id, "%s's exit test fails against this build (%s): %s", e.name, err, lastFailure(text))
		} else {
			s.note(id, "%s's exit test passed in %.0fs.", e.name, run.Took.Seconds())
		}
		s.earlier = append(s.earlier, run)
	}
}

// verdictTable copies an earlier report's verdict — the "N of the M
// exit criteria hold" line and its table — where the report has one.
func verdictTable(report string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(report, "\n") {
		switch {
		case strings.HasPrefix(l, "## The verdict"):
			in = true
			continue
		case in && strings.HasPrefix(l, "## "):
			return out
		case in && strings.TrimSpace(l) != "":
			out = append(out, l)
		}
	}
	return out
}

// lastFailure is the first failing line of a go test run, for the
// one-line reason.
func lastFailure(out string) string {
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "--- FAIL") || strings.Contains(t, "_test.go:") && strings.Contains(t, "does not hold") {
			return t
		}
	}
	return fmt.Sprintf("see the run's output (%s)", lastLines(out, 2))
}
