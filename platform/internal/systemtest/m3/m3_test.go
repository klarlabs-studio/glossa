//go:build system

package m3_test

import (
	"os"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/capture/capturetest"
	"go.klarlabs.de/glossa/platform/internal/systemtest/m3/fixture"
)

// TestM3Exit is RFC 0004 §12's exit test. It runs the four parts in
// order against one real glossa-server: the platform learns where every
// message of the fixture application appears, answers a pull request
// with a check and a branch environment, keeps the in-product editor out
// of production, and renders the Go runtime's documents in five locales.
func TestM3Exit(t *testing.T) {
	started := time.Now()
	f, err := fixture.Load("testdata")
	if err != nil {
		t.Fatal(err)
	}
	s := &scenario{t: t, f: f, phases: map[string]time.Duration{}}
	// The browser first: without one there is nothing to prove about the
	// captures or about the editor's guards, and finding that out before
	// Docker starts saves two minutes. It skips here, and fails in CI.
	s.chrome = capturetest.StartChrome(t)

	s.phase("deploy", func() { s.d = deploy(t) })
	s.phase("tenant, project and application", s.setup)
	// 1. Context.
	s.phase("push the catalogs", s.pushCatalog)
	s.phase("upload the build's and the extractor's usages", s.uploadUsages)
	s.phase("capture 8 routes in de and ja at two viewports", s.captureRoutes)
	s.phase("check the context coverage", s.checkCoverage)
	s.phase("read a capture's image", s.checkImages)
	// 2. Pull request.
	s.phase("connect the repository", s.connectRepository)
	s.phase("open the pull request", s.openPullRequest)
	s.phase("CI push: 5 new keys, 1 invalid", s.firstPush)
	s.phase("check: failure with annotations", s.checkFails)
	s.phase("repair and translate through an in-context grant", s.repair)
	s.phase("check: success, the same comment", s.checkSucceeds)
	s.phase("read pr-7 through the edge", s.readBranchEnvironment)
	s.phase("merge and push the default branch", s.merge)
	s.phase("close the pull request", s.closePullRequest)
	// 3. Overlay guards.
	s.phase("scan the production build for the loader", s.scanBundles)
	s.phase("refuse the editor against a production manifest", s.refuseOverlay)
	// 4. Documents.
	s.phase("render the documents in five locales", s.renderDocuments)

	if t.Failed() {
		return
	}
	if err := os.WriteFile("REPORT.md", s.report(), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.phaseNames {
		t.Logf("%-50s %6.1fs", p, s.phases[p].Seconds())
	}
	t.Logf("total %.1fs; wrote REPORT.md", time.Since(started).Seconds())
}

type scenario struct {
	t *testing.T
	f *fixture.Fixture
	d *deployment

	owner       *client
	ci          *runner
	tenant      string
	project     string
	application string
	ciToken     string
	appURL      string
	chrome      string

	phases     map[string]time.Duration
	phaseNames []string

	// Part 1.
	pushed                             map[string]int
	pushedMessages, pushedTranslations int
	pluginBuild                        contextBuild
	extractBuild                       contextBuild
	extracted                          int
	capture                            captureJSON
	coverage                           *coverage
	imageBytes                         int

	// Part 2.
	connection                string
	branchID                  string
	prOpened                  time.Time
	branchUsages              contextBuild
	timeline                  []event
	checks                    []checkRun
	comments                  []comment
	grant                     grant
	inContextEdits            int
	activated                 int
	previewKey, productionKey string
	edgeReads                 []edgeRead

	// Part 3.
	bundles []bundleScan
	overlay overlayProbe

	// Part 4.
	documents documentRun
}

func (s *scenario) phase(name string, fn func()) {
	if s.t.Failed() {
		return
	}
	start := time.Now()
	fn()
	s.phases[name] = time.Since(start)
	s.phaseNames = append(s.phaseNames, name)
}

func (s *scenario) tenantPath(p string) string { return "/v1/tenants/" + s.tenant + p }
func (s *scenario) projectPath(p string) string {
	return "/v1/tenants/" + s.tenant + "/projects/" + s.project + p
}
