//go:build system

package m4_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The dashboard (RFC 0005 §12.8): a Playwright test opens Studio's
// `quality` view against the same server and asserts the seven numbers
// match the API.
//
// "The same server" is the whole criterion, so the spec is pointed at
// this test's glossa-server rather than one of its own:
// `studio/e2e/harness.ts` takes GLOSSA_E2E_API_URL and
// GLOSSA_E2E_SERVER_LOG and then provisions nothing, and
// `playwright.config.ts` collects `e2e/m4` only when GLOSSA_M4_PROJECT
// is set. The spec is `studio/e2e/m4/quality-exit.spec.ts`.
//
// Studio has to be built first (`vite preview` serves `dist/`), which
// `make system-m4` does. Without a build this criterion is recorded as
// not run, with the command that would build it — never as met.

type studioReport struct {
	Ran     bool
	Passed  bool
	Why     string
	Spec    string
	Numbers []studioNumber
	Output  string
	Seconds float64
}

// studioNumber is one of the seven, as the API reported it.
type studioNumber struct {
	Name, Value string
	Measured    bool
	Reason      string
}

func (s *scenario) dashboard() {
	// What the API says, so the report can print the seven numbers the
	// spec compared the screen with.
	var summary map[string]any
	if _, err := s.owner.try(http.MethodGet, s.projectPath("/quality-summary"), nil, http.StatusOK, &summary); err != nil {
		s.gap("12.8", "the quality summary could not be read: %v", err)
		return
	}
	s.studio.Numbers = sevenNumbers(summary)
	if len(s.studio.Numbers) != 7 {
		s.gap("12.8", "the summary carries %d of the seven numbers", len(s.studio.Numbers))
	}

	studio, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "studio"))
	if err != nil {
		s.skip("12.8", "%v", err)
		return
	}
	s.studio.Spec = "e2e/m4/quality-exit.spec.ts"
	for _, need := range []struct{ path, why string }{
		{filepath.Join(studio, "node_modules"), "`pnpm install` has not been run"},
		{filepath.Join(studio, "dist", "index.html"), "Studio has not been built (`pnpm --filter @glossa/studio build`)"},
	} {
		if _, err := os.Stat(need.path); err != nil {
			s.skip("12.8", "%s, so the `quality` view cannot be opened against this server. "+
				"`make system-m4` builds it first.", need.why)
			s.studio.Why = need.why
			return
		}
	}
	pnpm, err := findTool("pnpm", "GLOSSA_PNPM", "")
	if err != nil {
		s.skip("12.8", "no pnpm: %v", err)
		s.studio.Why = err.Error()
		return
	}

	start := time.Now()
	cmd := exec.Command(pnpm, "exec", "playwright", "test", s.studio.Spec, "--reporter=line")
	cmd.Dir = studio
	cmd.Env = append(os.Environ(),
		"GLOSSA_E2E_API_URL="+s.d.base,
		"GLOSSA_E2E_SERVER_LOG="+s.d.logPath,
		"GLOSSA_E2E_STUDIO_PORT="+strconv.Itoa(studioPort),
		"GLOSSA_M4_TENANT="+s.tenant,
		"GLOSSA_M4_PROJECT="+s.project,
		"GLOSSA_M4_EMAIL=lena@brotwerk.example",
		"CI=1",
	)
	out, err := cmd.CombinedOutput()
	s.studio.Ran = true
	s.studio.Seconds = time.Since(start).Seconds()
	s.studio.Output = tail(string(out), 40)
	if err != nil {
		s.gap("12.8", "the Playwright spec failed: %v\n%s", err, tail(string(out), 30))
		return
	}
	s.studio.Passed = true
	s.note("12.8", "`%s` opened `/t/…/p/…/quality` against this server, found seven stats in the health header, "+
		"and every one of them matched the quality summary the API served — including the ones the API reported "+
		"as unmeasured, which the screen says are unmeasured rather than zero.", s.studio.Spec)
}

// sevenNumbers reads the summary the way §8 names its rows, so the
// report prints what the spec compared against.
func sevenNumbers(summary map[string]any) []studioNumber {
	project, _ := summary["project"].(map[string]any)
	reasons := map[string]string{}
	if list, ok := summary["unmeasured"].([]any); ok {
		for _, u := range list {
			if m, ok := u.(map[string]any); ok {
				reasons[fmt.Sprint(m["number"])] = fmt.Sprint(m["reason"])
			}
		}
	}
	names := []struct{ key, label string }{
		{"coverage", "Coverage: translated / outdated / missing"},
		{"findings", "Outstanding findings by layer and severity, plus waived"},
		{"ai", "AI acceptance rate and mean edit distance"},
		{"queue", "Review queue depth and age"},
		{"context", "Context coverage: usages and visible regions"},
		{"lead_time", "Lead time, p50/p90"},
		{"checks", "Check health: pass rate and median time to a conclusion"},
	}
	out := make([]studioNumber, 0, len(names))
	for _, n := range names {
		v, ok := project[n.key]
		row := studioNumber{Name: n.label, Measured: ok && v != nil, Reason: reasons[n.key]}
		if row.Measured {
			raw, _ := json.Marshal(v)
			row.Value = compact(string(raw))
		} else {
			row.Value = "not measured"
		}
		out = append(out, row)
	}
	return out
}

func compact(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// providerGuard is the last thing the run checks: nothing in §12
// reached for a model.
func (s *scenario) providerGuard() {
	s.guardCalls, s.guardPaths = s.d.provider.Calls()
	if s.guardCalls != 0 {
		s.t.Errorf("the exit test made %d model-provider calls (%v); RFC 0005 §14 decision 2 says a check never "+
			"calls a provider, and nothing in §12 should", s.guardCalls, s.guardPaths)
	}
}
