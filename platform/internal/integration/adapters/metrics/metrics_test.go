package metrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/github"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/metrics"
)

func TestGitHubCallsAreCountedByStatusWithTheRateLimit(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewGitHub(reg)
	if metrics.NewGitHub(reg) == nil {
		t.Fatal("registering twice reuses the collectors")
	}

	m.OnCall(github.CallInfo{Op: "create_check_run", InstallationID: 42, Status: 201, Duration: 120 * time.Millisecond,
		RateLimit: github.RateLimit{Limit: 5000, Remaining: 4987, Resource: "core"}})
	m.OnCall(github.CallInfo{Op: "create_check_run", InstallationID: 42, Status: 201, Duration: 90 * time.Millisecond,
		RateLimit: github.RateLimit{Limit: 5000, Remaining: 4986, Resource: "core"}})
	m.OnCall(github.CallInfo{Op: "create_check_run", InstallationID: 42, Status: 422, Duration: 40 * time.Millisecond})
	// A call that never came back: no status, and the window is unknown,
	// so the gauge keeps the last figure GitHub gave.
	m.OnCall(github.CallInfo{Op: "list_installations", Duration: time.Second})

	want := `
# HELP glossa_github_calls_total Calls to the GitHub App API, by operation and HTTP status (0: no answer came back).
# TYPE glossa_github_calls_total counter
glossa_github_calls_total{op="create_check_run",status="201"} 2
glossa_github_calls_total{op="create_check_run",status="422"} 1
glossa_github_calls_total{op="list_installations",status="0"} 1
# HELP glossa_github_rate_limit_remaining Calls GitHub last said were left in the window, by resource (core, search, graphql). Each instance reports what it saw last; take the min across instances.
# TYPE glossa_github_rate_limit_remaining gauge
glossa_github_rate_limit_remaining{resource="core"} 4986
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"glossa_github_calls_total", "glossa_github_rate_limit_remaining"); err != nil {
		t.Error(err)
	}
	if n, err := testutil.GatherAndCount(reg, "glossa_github_call_duration_seconds"); err != nil || n != 2 {
		t.Errorf("duration series = %d, %v; want one per operation", n, err)
	}

	// A resource GitHub didn't name is the primary one.
	m.OnCall(github.CallInfo{Op: "get_installation", Status: 200, RateLimit: github.RateLimit{Limit: 5000, Remaining: 4000}})
	unnamed := `
# HELP glossa_github_rate_limit_remaining Calls GitHub last said were left in the window, by resource (core, search, graphql). Each instance reports what it saw last; take the min across instances.
# TYPE glossa_github_rate_limit_remaining gauge
glossa_github_rate_limit_remaining{resource="core"} 4000
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(unnamed), "glossa_github_rate_limit_remaining"); err != nil {
		t.Error(err)
	}
}

// The hook is what github.Options takes, so the adapter can be wired to
// it without an intermediate closure.
var _ func(github.CallInfo) = metrics.NewGitHub(nil).OnCall
