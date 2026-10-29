//go:build system

package m4_test

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/capture/capturetest"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// TestM4Exit is RFC 0005 §12's exit test: the eight numbered criteria,
// in order, against one real glossa-server.
//
// It is written to be honest before it is written to be green. Every
// criterion is recorded with what actually happened — including the
// ones that cannot hold, and why — REPORT.md is written whatever the
// outcome, and the test then fails if any criterion did not hold. A
// green exit test that does not test the exit criterion is worse than a
// red one.
func TestM4Exit(t *testing.T) {
	started := time.Now()
	s := &scenario{t: t, phases: map[string]time.Duration{}}
	// All eight, in order, before anything runs: a criterion nothing
	// touched has to appear in the report as unproven rather than be
	// missing from it.
	for _, id := range []string{"12.1", "12.2", "12.3", "12.4", "12.5", "12.6", "12.7", "12.8"} {
		s.c(id)
	}

	// The browser first: without one there is nothing to prove about
	// the visual layer, and finding that out before Docker starts saves
	// two minutes. It skips here, and fails in CI.
	s.chrome = capturetest.StartChrome(t)

	s.phase("deploy", func() { s.d = deploy(t) })
	s.phase("tenant, project, application and tokens", s.setup)

	// 1. A fixture repository with real CI.
	s.phase("§12.1 materialize the repository and save policy v3", s.fixtureRepository)
	// 2. Findings across layers. The pull request is opened before the
	// two checks run, so both surfaces grade the same commit on the
	// same branch — which is what §12.3 then compares.
	s.phase("§12.2 push the default branch's catalogs", s.pushBase)
	s.phase("§12.3 connect the repository", s.connectRepository)
	// 4, first half. The pull request §12.4's preview has to name: one
	// that is green under v3, opened and checked while `main` is still
	// the default branch's catalogs.
	s.phase("§12.4 open a pull request that passes under v3", s.greenPullRequest)
	s.phase("§12.2 push the seeded catalogs and the usages", s.seedCatalogs)
	s.phase("§12.2 the termbase", s.seedTermbase)
	s.phase("§12.3 open the pull request", s.openPullRequest)
	s.phase("§12.2 run the workflow's check", s.runCheck)
	s.phase("§12.2 capture the checkout route and check with it", s.captureAndCheck)
	s.phase("§12.2 crop the clipped region out of the stored image", s.cropRegion)
	s.phase("§12.2 the nine layers", s.nineLayers)
	// 3. The PR check agrees.
	s.phase("§12.3 the check run, and whether it agrees with the CLI", s.agreement)
	// 4. Waivers and policy rollout.
	s.phase("§12.4 waive, revoke and outlive a finding", s.waivers)
	s.phase("§12.4 policy v4: dry run, grace, two live versions", s.policyRollout)
	// 5. Release gate.
	s.phase("§12.5 the release gate", s.releaseGate)
	// 6. MCP.
	s.phase("§12.6 MCP: the refusals, the write session, the second tenant", s.mcp)
	// 7. Flutter.
	s.phase("§12.7 the Dart runtime and the §6.4 budgets", s.flutter)
	// 8. Dashboard.
	s.phase("§12.8 Studio's quality view against this server", s.dashboard)

	s.phase("the provider guard", s.providerGuard)

	if err := os.WriteFile("REPORT.md", s.report(), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.phaseNames {
		t.Logf("%-62s %6.1fs", p, s.phases[p].Seconds())
	}
	t.Logf("total %.1fs; wrote REPORT.md", time.Since(started).Seconds())

	var unmet []string
	for _, c := range s.criteria {
		if !c.met() {
			unmet = append(unmet, c.id)
		}
	}
	if len(unmet) > 0 {
		t.Errorf("M4 is not done: %d of the 8 exit criteria of RFC 0005 §12 do not hold (%s). "+
			"REPORT.md says, for each one, what held and what did not.", len(unmet), strings.Join(unmet, ", "))
	}
}

// criterion is one of the eight. It holds what the test observed, and
// the shortfalls that stop it holding.
type criterion struct {
	id, title string
	// notes are what happened, in the order it happened.
	notes []string
	// gaps are the reasons it does not hold. A criterion with no gap
	// holds.
	gaps []string
	// skipped says the criterion could not be attempted at all here (no
	// toolchain, no build); it counts as unmet.
	skipped string
}

func (c *criterion) met() bool { return len(c.gaps) == 0 && c.skipped == "" }

func (c *criterion) verdict() string {
	switch {
	case c.skipped != "":
		return "not run"
	case len(c.gaps) > 0:
		return "**not met**"
	}
	return "met"
}

type scenario struct {
	t *testing.T
	d *deployment

	owner   *client
	ci      *runner
	tenant  string
	project string
	// second is another tenant, for §12.6's isolation case.
	secondTenant, secondProject string

	application string
	ciToken     string
	readToken   string
	writeToken  string
	otherToken  string
	chrome      string
	appURL      string

	criteria []*criterion
	byID     map[string]*criterion

	phases     map[string]time.Duration
	phaseNames []string

	// What the parts observe, for the report.
	repo          *repo
	policyVersion int
	// gradedVersion is the version §12.2's check ran against, kept
	// because §12.4 then moves policyVersion on.
	gradedVersion int
	pushed        map[string]int

	cliCheck   checkJSON
	cliCapture captureJSON
	// greenRun is the green pull request's newest recorded run: the
	// second sighting of its captures, graded under v3.
	greenRun *checkJSON
	// cliCaptureHead is the capture-and-check run of the pull request's
	// own head commit — the run CI recorded for it, and therefore the
	// one §12.3 compares the check run against.
	cliCaptureHead   *captureJSON
	cliLayers        map[domain.Layer]layerCount
	layerStatus      []layerVerdict
	crop             cropResult
	uploadedFindings int
	// structure is how §12.2's `structure` case was proven.
	structure structureProof

	prCheck     checkRunView
	prLayers    map[domain.Layer]layerCount
	agreeRows   []agreementRow
	agreedWith  string
	stickyFound int

	waiverSteps []step
	policySteps []step
	releaseRows []step
	mcpRows     []mcpRow

	dart       dartReport
	studio     studioReport
	guardCalls int
	guardPaths []string
}

// step is one observation with its outcome, for the report's tables.
type step struct{ What, Then string }

// layerCount is a layer's findings by severity.
type layerCount struct{ Errors, Warnings, Waived int }

func (l layerCount) total() int { return l.Errors + l.Warnings + l.Waived }

// layerVerdict is one of §12.2's nine, and what the run found for it.
type layerVerdict struct {
	Layer domain.Layer
	// Want is the case §12.2 names.
	Want string
	// Got is what the run produced.
	Got string
	// Codes are the finding codes seen in this layer.
	Codes []string
	OK    bool
	// Why explains a layer that produced nothing.
	Why string
}

// agreementRow is one line of the CLI-versus-pull-request comparison.
type agreementRow struct {
	What    string
	CLI, PR string
	Agrees  bool
}

func (s *scenario) phase(name string, fn func()) {
	start := time.Now()
	fn()
	s.phases[name] = time.Since(start)
	s.phaseNames = append(s.phaseNames, name)
}

// criterion returns the record for one of the eight, creating it once.
func (s *scenario) c(id string) *criterion {
	if s.byID == nil {
		s.byID = map[string]*criterion{}
	}
	if c, ok := s.byID[id]; ok {
		return c
	}
	c := &criterion{id: id, title: criterionTitles[id]}
	s.byID[id] = c
	s.criteria = append(s.criteria, c)
	return c
}

func (s *scenario) note(id, format string, args ...any) {
	s.c(id).notes = append(s.c(id).notes, fmt.Sprintf(format, args...))
}

// gap records why a criterion does not hold. It never stops the run:
// the report has to be able to say what every other criterion did.
func (s *scenario) gap(id, format string, args ...any) {
	s.c(id).gaps = append(s.c(id).gaps, fmt.Sprintf(format, args...))
	s.t.Logf("§%s does not hold: %s", id, fmt.Sprintf(format, args...))
}

func (s *scenario) skip(id, format string, args ...any) {
	s.c(id).skipped = fmt.Sprintf(format, args...)
	s.t.Logf("§%s was not run: %s", id, s.c(id).skipped)
}

var criterionTitles = map[string]string{
	"12.1": "A fixture repository with real CI",
	"12.2": "Findings across layers",
	"12.3": "The PR check agrees",
	"12.4": "Waivers and policy rollout",
	"12.5": "Release gate",
	"12.6": "MCP",
	"12.7": "Flutter",
	"12.8": "Dashboard",
}

func (s *scenario) tenantPath(p string) string { return "/v1/tenants/" + s.tenant + p }
func (s *scenario) projectPath(p string) string {
	return "/v1/tenants/" + s.tenant + "/projects/" + s.project + p
}

// byLayer groups findings the way the check run and the pull request
// both group them, so the two can be compared number for number.
func byLayer(fs []domain.Finding) map[domain.Layer]layerCount {
	out := map[domain.Layer]layerCount{}
	for _, f := range fs {
		c := out[f.Layer]
		switch f.Severity {
		case domain.Error:
			c.Errors++
		case domain.Waived:
			c.Waived++
		default:
			c.Warnings++
		}
		out[f.Layer] = c
	}
	return out
}

func codesOf(fs []domain.Finding, layer domain.Layer) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fs {
		if f.Layer != layer || seen[f.Code] {
			continue
		}
		seen[f.Code] = true
		out = append(out, f.Code)
	}
	sort.Strings(out)
	return out
}

func codeList(codes []string) string {
	if len(codes) == 0 {
		return "—"
	}
	return "`" + strings.Join(codes, "`, `") + "`"
}
