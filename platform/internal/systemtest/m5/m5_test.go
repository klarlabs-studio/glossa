//go:build system

package m5_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestM5Exit is RFC 0006 §12's exit test: the seven numbered criteria,
// in order, against one real glossa-server, one real glossa-edge, a
// v0.3 server built from apps/api, and the three runtimes.
//
// It exists from wave 1 with every criterion written and red (§11.1):
// each later wave turns some of it green. So it is written to be honest
// before it is written to be green. Every criterion is a sequence of
// steps against public surfaces; a step that cannot hold records
// exactly what is missing, the steps that depend on it are reported as
// not reached, REPORT.md is written whatever the outcome, and the test
// then fails if any criterion did not hold. Nothing is skipped to keep
// the board tidy: a criterion nobody could attempt is a failure in the
// report, not an absence from it.
func TestM5Exit(t *testing.T) {
	started := time.Now()
	s := &scenario{t: t, phases: map[string]time.Duration{}, log: &callLog{}}
	for _, id := range []string{"12.1", "12.2", "12.3", "12.4", "12.5", "12.6", "12.7"} {
		s.c(id)
	}

	// 7. Earlier exits hold — first, and alone: the M2, M3 and M4 exit
	// tests each start their own server, and two exit tests running at
	// once collide at sign-in. They run before this test deploys
	// anything.
	s.phase("§12.7 the M2, M3 and M4 exit tests, unchanged", s.earlierExits)

	s.phase("deploy", func() { s.d = deploy(t) })
	if failure := s.guarded("fixture: people, projects A and B, canaries", s.fixture); failure != "" {
		// Without the fixture no criterion can be attempted. Each is
		// reported as not run, with the reason, rather than dropped.
		for _, c := range s.criteria {
			if c.id != "12.7" && len(c.steps) == 0 {
				s.skip(c.id, "the fixture could not be built: %s", failure)
			}
		}
	} else {
		// 1–4 make the changes §12.5 then looks for in the audit export.
		s.criterionPhase("12.1", "§12.1 two workflows, one event", s.twoWorkflows)
		s.criterionPhase("12.2", "§12.2 vendor visibility on every surface", s.vendorVisibility)
		s.criterionPhase("12.3", "§12.3 release approvals", s.releaseApprovals)
		s.criterionPhase("12.4", "§12.4 staged rollout across three runtimes", s.stagedRollout)
		s.criterionPhase("12.5", "§12.5 audit export", s.auditExport)
		s.criterionPhase("12.6", "§12.6 v0.3 imports and renders the same", s.migratable)
	}

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
		t.Errorf("M5 is not done: %d of the 7 exit criteria of RFC 0006 §12 do not hold (%s). "+
			"REPORT.md says, for each one, what held and what is missing.", len(unmet), strings.Join(unmet, ", "))
	}
}

// criterion is one of the seven: the steps it took, and the shortfalls
// that stop it holding.
type criterion struct {
	id, title string
	steps     []stepResult
	notes     []string
	// gaps are the reasons it does not hold. A criterion with no gap
	// holds.
	gaps []string
	// skipped says the criterion could not be attempted at all here;
	// it counts as unmet.
	skipped string
}

// stepResult is one step of a criterion, in the order it ran.
type stepResult struct {
	Name   string
	State  string // held, failed, not reached
	Detail string
}

const (
	held       = "held"
	failed     = "failed"
	notReached = "not reached"
)

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

var criterionTitles = map[string]string{
	"12.1": "Two workflows, one event",
	"12.2": "Vendor visibility on every surface",
	"12.3": "Release approvals",
	"12.4": "Staged rollout across three runtimes",
	"12.5": "Audit export",
	"12.6": "v0.3 imports and renders the same",
	"12.7": "Earlier exits hold",
}

type scenario struct {
	t *testing.T
	d *deployment

	log *callLog

	// The fixture organisation: three people and a vendor's translator
	// (RFC 0006 §15 q6: "the exit test uses a fixture organisation with
	// three people").
	owner, reviewer1, reviewer2, vendor *client
	// ownerToken is an API token of the owner's, for the "a token's
	// grant does not count" cases.
	ownerToken *client
	tenant     string
	projectA   string
	projectB   string
	// vendorID is the vendor §3.3 creates, when it can be created.
	vendorID string
	// vendorAsVendor says the vendor's translator was invited as a
	// vendor member with visibility `assigned`. When the platform
	// refuses that, the fixture invites them as an ordinary `de`
	// translator so the sweep still runs — and shows what they see.
	vendorAsVendor bool
	vendorInvite   string

	// The units of project B the vendor is to be assigned (§12.2), and
	// the ones outside the assignment.
	assigned, unassigned []string
	// ids of things outside the vendor's assignment, for the sweep.
	outside map[string]string
	// ids inside it.
	inside map[string]string
	// sweepInside and sweepOutside are the ids the fixture made for the
	// sweep in project B and project A, for the operations whose parent
	// collection cannot hand the sweep one (sweepResources).
	sweepInside, sweepOutside map[string]string

	criteria []*criterion
	byID     map[string]*criterion

	phases     map[string]time.Duration
	phaseNames []string

	// What the parts observe, for the report.
	sweep       []sweepRow
	mcpSweep    []sweepRow
	edgeRows    []edgeRow
	rollout     rolloutReport
	audit       auditReport
	v03         v03Report
	earlier     []earlierExit
	archTest    string
	workflowLog []string
}

func (s *scenario) phase(name string, fn func()) {
	start := time.Now()
	fn()
	s.phases[name] = time.Since(start)
	s.phaseNames = append(s.phaseNames, name)
}

// guarded runs a phase and returns the harness failure that stopped it,
// if one did.
func (s *scenario) guarded(name string, fn func()) (failure string) {
	s.phase(name, func() {
		defer func() {
			if r := recover(); r != nil {
				failure = fmt.Sprint(r)
				s.t.Logf("%s stopped: %s", name, failure)
			}
		}()
		fn()
	})
	return failure
}

// criterionPhase runs one criterion with the harness's call log
// attributing every mutating call to it, and turns a harness failure
// inside it into a gap instead of the end of the run: the report has to
// be able to say what every other criterion did.
func (s *scenario) criterionPhase(id, name string, fn func()) {
	s.log.setCriterion(id)
	defer s.log.setCriterion("")
	if failure := s.guarded(name, fn); failure != "" {
		s.gap(id, "the criterion stopped on a step M2–M4 already proved: %s", failure)
	}
}

// c returns the record for one of the seven, creating it once.
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

// gap records why a criterion does not hold. It never stops the run.
func (s *scenario) gap(id, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.c(id).gaps = append(s.c(id).gaps, msg)
	s.t.Logf("§%s does not hold: %s", id, msg)
}

func (s *scenario) skip(id, format string, args ...any) {
	s.c(id).skipped = fmt.Sprintf(format, args...)
	s.t.Logf("§%s was not run: %s", id, s.c(id).skipped)
}

// step runs one step of a criterion. An error is the step's gap; the
// criterion records it and the caller decides whether what follows can
// still run.
func (s *scenario) step(id, name string, fn func() error) bool {
	err := fn()
	if err == nil {
		s.c(id).steps = append(s.c(id).steps, stepResult{Name: name, State: held})
		return true
	}
	detail := err.Error()
	s.c(id).steps = append(s.c(id).steps, stepResult{Name: name, State: failed, Detail: detail})
	s.gap(id, "%s — %s", name, detail)
	return false
}

// unreached records the steps a failed step stopped. They are not gaps
// of their own — the first failure is the reason — but the report lists
// them, so a reader sees the whole path and how far along it the
// platform is.
func (s *scenario) unreached(id string, names ...string) {
	for _, n := range names {
		s.c(id).steps = append(s.c(id).steps, stepResult{Name: n, State: notReached})
	}
}

func (s *scenario) tenantPath(p string) string { return "/v1/tenants/" + s.tenant + p }
func (s *scenario) projectPathOf(project, p string) string {
	return "/v1/tenants/" + s.tenant + "/projects/" + project + p
}

// missing phrases a refused or unrouted call as the gap it is.
func missing(what string, err error) error {
	var ae *apiError
	if errors.As(err, &ae) && absent(err) {
		return fmt.Errorf("%s does not exist on this server (%s)", what, ae.Error())
	}
	return fmt.Errorf("%s failed: %w", what, err)
}
