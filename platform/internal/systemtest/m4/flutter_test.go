//go:build system

package m4_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The Flutter runtime (RFC 0005 §12.7): `runtimes/dart` passes every
// scenario, every loading case, the MessageFormat runtime cases and
// `markup.json`; `explain()` matches SPEC §6 field for field; an
// unsigned manifest is rejected with keys configured; and the §6.4 size
// and startup budgets are measured and recorded in this report.
//
// The suites are the runtime's own — the exit test runs them rather
// than keeping a second copy, the same way M3 runs the Go runtime's
// document goldens. RFC 0005 §14 decision 9: the Dart runtime is a
// driver over the shared fixtures, and this is where the driver is
// driven.

type dartSuite struct {
	Name           string
	Passed, Failed int
	Skipped        int
	Fixtures       string
}

type dartReport struct {
	SDK, Flutter string
	Suites       []dartSuite
	Passed       int
	Failed       int
	Skipped      int
	Ran          bool
	Why          string

	// The §6.4 budgets, measured.
	Size    budget
	Startup budget
}

type budget struct {
	Ran     bool
	OK      bool
	Why     string
	Lines   []string
	Seconds float64
}

// dartDir is the runtime's package, relative to this test.
const dartDir = "../../../../runtimes/dart"

// flutterDir is the Flutter package that owns the size budget.
const flutterDir = "../../../../runtimes/dart/flutter"

// suiteFixtures says which shared fixture set each suite drives, so the
// report names the contract rather than the file.
var suiteFixtures = map[string]string{
	"scenarios_test.dart":      "runtimes/testdata/scenarios — and `explain()` field for field (SPEC §6)",
	"loading_test.dart":        "runtimes/testdata/loading — including `signatures.json`: an unsigned manifest with keys configured",
	"markup_test.dart":         "runtimes/testdata/markup.json",
	"runtime_format_test.dart": "messageformat/testdata/glossa/runtime-format.json",
	"loader_test.dart":         "the §3 loading cases a fixture cannot ship",
	"jcs_test.dart":            "RFC 8785 canonicalization vectors",
	"ed25519_test.dart":        "RFC 8032 signature vectors and their rejections",
	"locale_test.dart":         "the BCP 47 suites the Go and JS runtimes share",
	"catalog_test.dart":        "`explain()` has no side effects; the SPEC's wire spellings",
	"purity_test.dart":         "§6.4's AOT and web rules: no `dart:mirrors`, no `dart:ffi`, no `dart:io` in the core",
}

func (s *scenario) flutter() {
	dart, err := findTool("dart", "GLOSSA_DART", "/opt/homebrew/opt/dart-sdk/bin")
	if err != nil {
		s.skip("12.7", "no Dart SDK: %v", err)
		s.dart.Why = err.Error()
		return
	}
	s.dart.SDK = toolVersion(dart, "--version")

	if out, err := runIn(dartDir, dart, "pub", "get"); err != nil {
		s.gap("12.7", "`dart pub get` failed: %v\n%s", err, tail(out, 20))
		s.dart.Why = "dart pub get failed"
		return
	}
	raw, _ := runIn(dartDir, dart, "test", "--reporter", "json")
	s.dart = parseDartTest(raw, s.dart)
	s.dart.Ran = true
	switch {
	case s.dart.Failed > 0:
		s.gap("12.7", "the Dart runtime failed %d of %d tests", s.dart.Failed, s.dart.Passed+s.dart.Failed)
	case s.dart.Passed == 0:
		s.gap("12.7", "`dart test` reported no tests at all:\n%s", tail(raw, 20))
	default:
		s.note("12.7", "`dart test` over the shared fixtures: **%d passed, %d failed, %d skipped** across %d suites.",
			s.dart.Passed, s.dart.Failed, s.dart.Skipped, len(s.dart.Suites))
	}
	// The two cases §12.7 names by hand have to be among the suites
	// that ran, not merely among the ones that exist.
	for _, want := range []string{"scenarios_test.dart", "loading_test.dart", "markup_test.dart", "runtime_format_test.dart"} {
		if suiteOf(s.dart.Suites, want) == nil {
			s.gap("12.7", "`%s` did not run, so §12.7's claim about it is unproven", want)
		}
	}

	// Startup: measured on every run and recorded, never gated on wall
	// clock (§6.4's amendment — a laptop is not a mid-range Android
	// device). The tool fails only on the two properties that hold on
	// any machine.
	s.dart.Startup = s.measure("startup", dartDir, func(report string) (string, []string) {
		// The output path is the compiler's own working directory's, and
		// the binary resolves the shared fixtures from where it sits, so
		// it has to be built inside the package.
		const rel = ".dart_tool/m4_startup_budget"
		if out, err := runIn(dartDir, dart, "compile", "exe", "tool/startup_budget.dart", "-o", rel); err != nil {
			return "", []string{"dart compile exe failed: " + tail(out, 5)}
		}
		abs, err := filepath.Abs(filepath.Join(dartDir, rel))
		if err != nil {
			return "", []string{err.Error()}
		}
		out, err := runIn(dartDir, abs, "--report", report)
		if err != nil {
			return out, []string{"the startup budget tool did not succeed: " + firstLine(tail(out, 3))}
		}
		return out, nil
	})

	// Size: a real `flutter build --analyze-size`, three times, against
	// a fixture app with and without the package. Slow, and the only
	// method §6.4 names.
	flutter, ferr := findTool("flutter", "GLOSSA_FLUTTER", "")
	switch {
	case os.Getenv("GLOSSA_M4_SKIP_SIZE_BUDGET") != "":
		s.dart.Size.Why = "GLOSSA_M4_SKIP_SIZE_BUDGET is set"
		s.gap("12.7", "the §6.4 size budget was not measured: %s", s.dart.Size.Why)
	case ferr != nil:
		s.dart.Size.Why = "no Flutter SDK on this machine: " + ferr.Error()
		s.gap("12.7", "the §6.4 size budget was not measured: %s", s.dart.Size.Why)
	default:
		s.dart.Flutter = toolVersion(flutter, "--version")
		if out, err := runIn(flutterDir, flutter, "pub", "get"); err != nil {
			s.dart.Size.Why = "flutter pub get failed: " + tail(out, 5)
			s.gap("12.7", "the §6.4 size budget was not measured: %s", s.dart.Size.Why)
			break
		}
		s.dart.Size = s.measure("size", flutterDir, func(report string) (string, []string) {
			out, err := runIn(flutterDir, dart, "run", "tool/size_budget.dart", "--report", report)
			if err != nil {
				return out, []string{"the §6.4 size budget is not met"}
			}
			return out, nil
		})
	}
	if s.dart.Size.Ran && !s.dart.Size.OK {
		s.gap("12.7", "the §6.4 size budget is over: %s", strings.Join(s.dart.Size.Lines, " "))
	}
	if s.dart.Startup.Ran && !s.dart.Startup.OK {
		s.gap("12.7", "the §6.4 startup budget's enforced properties do not hold: %s",
			strings.Join(s.dart.Startup.Lines, " "))
	}
}

// measure runs one budget tool and keeps the lines the report prints.
func (s *scenario) measure(name, dir string, fn func(report string) (string, []string)) budget {
	start := time.Now()
	report := filepath.Join(s.t.TempDir(), name+"-budget.txt")
	out, problems := fn(report)
	b := budget{Ran: true, OK: len(problems) == 0, Seconds: time.Since(start).Seconds()}
	if raw, err := os.ReadFile(report); err == nil && len(raw) > 0 {
		out = string(raw)
	}
	b.Lines = budgetLines(out)
	if len(problems) > 0 {
		b.Why = strings.Join(problems, "; ")
	}
	return b
}

// budgetLines is a budget report as the exit report quotes it: every
// line the tool printed, without the commands it ran to get there. The
// prose stays, because §6.4's numbers do not mean anything without the
// assumptions the tool prints beside them.
var command = regexp.MustCompile(`^\s*\$ `)

func budgetLines(out string) []string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if command.MatchString(l) {
			continue
		}
		keep = append(keep, strings.TrimRight(l, " \t"))
	}
	// Trim the blank lines at either end.
	for len(keep) > 0 && strings.TrimSpace(keep[0]) == "" {
		keep = keep[1:]
	}
	for len(keep) > 0 && strings.TrimSpace(keep[len(keep)-1]) == "" {
		keep = keep[:len(keep)-1]
	}
	return keep
}

func suiteOf(suites []dartSuite, name string) *dartSuite {
	for i, s := range suites {
		if s.Name == name {
			return &suites[i]
		}
	}
	return nil
}

// parseDartTest reads `dart test --reporter json`: one JSON object per
// line, suites and their tests.
func parseDartTest(out string, into dartReport) dartReport {
	type event struct {
		Type  string `json:"type"`
		Suite *struct {
			ID   int    `json:"id"`
			Path string `json:"path"`
		} `json:"suite"`
		Test *struct {
			ID      int    `json:"id"`
			Name    string `json:"name"`
			SuiteID int    `json:"suiteID"`
		} `json:"test"`
		TestID  int    `json:"testID"`
		Result  string `json:"result"`
		Hidden  bool   `json:"hidden"`
		Skipped bool   `json:"skipped"`
	}
	paths := map[int]string{}
	suiteOfTest := map[int]int{}
	counts := map[int]*dartSuite{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		switch e.Type {
		case "suite":
			if e.Suite != nil {
				paths[e.Suite.ID] = filepath.Base(e.Suite.Path)
			}
		case "testStart":
			if e.Test != nil && !e.Hidden {
				suiteOfTest[e.Test.ID] = e.Test.SuiteID
			}
		case "testDone":
			if e.Hidden {
				continue
			}
			id, ok := suiteOfTest[e.TestID]
			if !ok {
				continue
			}
			s := counts[id]
			if s == nil {
				s = &dartSuite{Name: paths[id], Fixtures: suiteFixtures[paths[id]]}
				counts[id] = s
			}
			switch {
			case e.Skipped:
				s.Skipped++
				into.Skipped++
			case e.Result == "success":
				s.Passed++
				into.Passed++
			default:
				s.Failed++
				into.Failed++
			}
		}
	}
	for _, s := range counts {
		if s.Name == "" {
			continue
		}
		into.Suites = append(into.Suites, *s)
	}
	sort.Slice(into.Suites, func(i, j int) bool { return into.Suites[i].Name < into.Suites[j].Name })
	return into
}

// findTool resolves a binary from an environment variable, PATH, or an
// extra directory this machine is known to keep it in.
func findTool(name, env, extra string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, nil
		}
		return "", fmt.Errorf("%s=%s is not there", env, v)
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if extra != "" {
		p := filepath.Join(extra, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not on PATH (set %s)", name, env)
}

func toolVersion(bin string, args ...string) string {
	out, _ := exec.Command(bin, args...).CombinedOutput()
	return firstLine(string(out))
}

// runIn runs a command in a directory relative to this package.
func runIn(dir, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.String(), err
}

var _ = strconv.Itoa
