package buildinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"module version", debug.BuildInfo{Main: debug.Module{Version: "v0.5.1"}}, "v0.5.1"},
		{"vcs revision", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}}, "0123456789ab"},
		{"nothing", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "devel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromBuildInfo(&tt.info); got != tt.want {
				t.Errorf("fromBuildInfo = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInjectedVersionWins(t *testing.T) {
	version, revision = "9.9.9", "abc123"
	t.Cleanup(func() { version, revision = "", "" })
	if got := Version(); got != "9.9.9" {
		t.Errorf("Version = %q, want 9.9.9", got)
	}
	if got := Revision(); got != "abc123" {
		t.Errorf("Revision = %q, want abc123", got)
	}
}

// TestLinkerInjection builds both binaries with the -ldflags the
// Dockerfiles use and checks that -version reports the injected value,
// so a renamed package or variable cannot silently bring back "devel"
// (#65).
func TestLinkerInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	const pkg = "go.klarlabs.de/glossa/platform/internal/kernel/buildinfo"
	ldflags := "-X " + pkg + ".version=9.9.9 -X " + pkg + ".revision=deadbeef"
	for _, cmd := range []string{"glossa-server", "glossa-edge"} {
		t.Run(cmd, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), cmd)
			build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", ldflags, "-o", bin, "../../../cmd/"+cmd)
			build.Env = append(os.Environ(), "CGO_ENABLED=0")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			out, err := exec.Command(bin, "-version").CombinedOutput()
			if err != nil {
				t.Fatalf("%s -version: %v\n%s", cmd, err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "9.9.9" {
				t.Errorf("%s -version = %q, want 9.9.9", cmd, got)
			}
		})
	}
}
