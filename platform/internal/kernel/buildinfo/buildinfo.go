// Package buildinfo reports which build of a Glossa binary is running.
//
// Release images inject the version and revision at link time:
//
//	go build -ldflags "-X go.klarlabs.de/glossa/platform/internal/kernel/buildinfo.version=0.5.2 \
//	  -X go.klarlabs.de/glossa/platform/internal/kernel/buildinfo.revision=<sha>"
//
// (see platform/Dockerfile.server and platform/Dockerfile.edge). Without
// them, a binary falls back to the Go build info: the module version when
// installed with `go install …@v`, else the VCS revision, else "devel".
package buildinfo

import "runtime/debug"

// Set with -ldflags -X; empty unless the build injected them.
var (
	version  string
	revision string
)

// Version is the injected release version, or the best the Go build
// info can tell.
func Version() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return fromBuildInfo(info)
}

// Revision is the injected source revision, or the VCS revision from the
// Go build info, or "unknown".
func Revision() string {
	if revision != "" {
		return revision
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if r := setting(info, "vcs.revision"); r != "" {
			return r
		}
	}
	return "unknown"
}

func fromBuildInfo(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	if r := setting(info, "vcs.revision"); len(r) >= 12 {
		return r[:12]
	}
	return "devel"
}

func setting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}
