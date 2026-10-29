//go:build system

package m5_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ── §12.4 Staged rollout across three runtimes ───────────────────────

// cohortTablePath is the generator's table: 10,000 fixture installation
// ids with the cohort SPEC §1.4 assigns each under one salt. It is
// computed by runtimes/testdata/gen/generate.py, which is none of the
// runtimes — the fourth implementation §12.4 checks each runtime
// against. (It lives beside `loading/`, not in it: every runtime's
// loading suite parses each loading file as a load sequence.)
var cohortTablePath = filepath.Join("runtimes", "testdata", "rollout", "cohorts.json")

type cohortTable struct {
	Salt          string
	ExpCandidates map[string]int
	ids           []string
	cohort        map[string]int
}

type rolloutReport struct {
	IDs          int
	TableSalt    string
	ExpAt10      int
	ManifestSalt string
	Rows         []runtimeRow
}

// runtimeRow is one runtime's answer for one phase.
type runtimeRow struct {
	Phase, Runtime string
	Candidates     int
	Disagree       int
	FirstDisagree  string
	Err            string
	OK             bool
}

var runtimes = []string{"js", "go", "dart"}

func loadCohortTable() (*cohortTable, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(), cohortTablePath))
	if err != nil {
		return nil, fmt.Errorf("the generator's cohort table %s does not exist (%v): SPEC §1.4's fixtures are not generated", cohortTablePath, err)
	}
	var doc struct {
		Salt          string            `json:"salt"`
		ExpCandidates map[string]int    `json:"expCandidates"`
		Installations []json.RawMessage `json:"installations"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", cohortTablePath, err)
	}
	t := &cohortTable{Salt: doc.Salt, ExpCandidates: doc.ExpCandidates, cohort: map[string]int{}}
	for _, row := range doc.Installations {
		var pair []any
		if err := json.Unmarshal(row, &pair); err != nil || len(pair) != 2 {
			return nil, fmt.Errorf("%s: an installation row is not [id, cohort]: %s", cohortTablePath, row)
		}
		id, _ := pair[0].(string)
		c, _ := pair[1].(float64)
		t.ids = append(t.ids, id)
		t.cohort[id] = int(c)
	}
	if len(t.ids) != 10000 {
		return nil, fmt.Errorf("%s holds %d installation ids, want 10,000", cohortTablePath, len(t.ids))
	}
	return t, nil
}

// driverInput is what every runtime driver reads.
type driverInput struct {
	EdgeURL     string   `json:"edgeURL"`
	DeliveryKey string   `json:"deliveryKey"`
	Environment string   `json:"environment"`
	IDs         []string `json:"ids"`
	Rollout     bool     `json:"rollout"`
	Manifest    string   `json:"manifest,omitempty"`
}

// runRuntime drives one runtime over the ids and returns, per id, the
// release it activated.
func (s *scenario) runRuntime(rt string, in driverInput) (map[string]*string, error) {
	dir := s.t.TempDir()
	inPath, outPath := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json")
	raw, _ := json.Marshal(in)
	if err := os.WriteFile(inPath, raw, 0o644); err != nil {
		return nil, err
	}
	here, _ := filepath.Abs(".")
	var cmd *exec.Cmd
	switch rt {
	case "js":
		runtimeDir := filepath.Join(repoRoot(), "runtimes", "js", "glossa")
		if _, err := os.Stat(filepath.Join(runtimeDir, "dist", "runtime", "index.js")); err != nil {
			return nil, fmt.Errorf("@klarlabs-studio/glossa is not built (runtimes/js/glossa/dist): `make system-m5` builds it")
		}
		cmd = exec.Command("node", filepath.Join(here, "testdata", "rollout", "rollout.mjs"), inPath, outPath, runtimeDir)
	case "go":
		cmd = exec.Command("go", "run", "./internal/systemtest/m5/testdata/rollout/go", inPath, outPath)
		cmd.Dir = platformDir()
	case "dart":
		pkg, err := dartPackageConfig()
		if err != nil {
			return nil, err
		}
		cmd = exec.Command("dart", "--packages="+pkg, filepath.Join(here, "testdata", "rollout", "rollout.dart"), inPath, outPath)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s", driverFailure(stderr.String(), err))
	}
	out := map[string]*string{}
	b, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal(b, &out)
}

// driverFailure says why a driver could not run, in one line. A driver
// that does not compile against its runtime is the ordinary case before
// the runtime implements SPEC §1.4, and the compiler names what is
// missing: an unknown field (Go) or named parameter (Dart).
func driverFailure(stderr string, err error) string {
	var missing []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		if i := strings.Index(l, "unknown field "); i >= 0 {
			missing = append(missing, l[i:])
		} else if i := strings.Index(l, "No named parameter"); i >= 0 {
			missing = append(missing, l[i:])
		}
	}
	if len(missing) > 0 {
		return "the runtime has no rollout surface — the driver does not compile: " + strings.Join(missing, "; ")
	}
	return fmt.Sprintf("the driver failed (%v): %s", err, lastLines(stderr, 3))
}

// dartPackageConfig resolves runtimes/dart's packages once.
func dartPackageConfig() (string, error) {
	dir := filepath.Join(repoRoot(), "runtimes", "dart")
	pkg := filepath.Join(dir, ".dart_tool", "package_config.json")
	if _, err := os.Stat(pkg); err == nil {
		return pkg, nil
	}
	if _, err := exec.LookPath("dart"); err != nil {
		return "", fmt.Errorf("no Dart SDK on PATH")
	}
	cmd := exec.Command("dart", "pub", "get")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("dart pub get in runtimes/dart: %v: %s", err, lastLines(string(out), 3))
	}
	return pkg, nil
}

// judge compares one runtime's answers with the generator's cohorts: an
// id is in the candidate exactly when its cohort is below percent×100.
// The comparison is against the fixture's expected cohorts, never
// against another runtime.
func judge(got map[string]*string, ids []string, cohort map[string]int, percent int, stable, candidate string) (cands, disagree int, firstBad string) {
	for _, id := range ids {
		active := ""
		if p := got[id]; p != nil {
			active = *p
		}
		if active == candidate {
			cands++
		}
		want := stable
		if cohort[id] < percent*100 {
			want = candidate
		}
		if active != want {
			disagree++
			if firstBad == "" {
				firstBad = fmt.Sprintf("`%s` (cohort %d) is on %s, want %s", id, cohort[id], short(active), short(want))
			}
		}
	}
	return cands, disagree, firstBad
}

// across runs every runtime for one phase and records each one's row;
// it returns the runtimes that did not hold.
func (s *scenario) across(phase string, in driverInput, ids []string, cohort map[string]int, percent int, stable, candidate string) []string {
	var bad []string
	for _, rt := range runtimes {
		row := runtimeRow{Phase: phase, Runtime: rt}
		got, err := s.runRuntime(rt, in)
		if err != nil {
			row.Err = err.Error()
			bad = append(bad, fmt.Sprintf("%s: %s", rt, err))
			s.rollout.Rows = append(s.rollout.Rows, row)
			continue
		}
		row.Candidates, row.Disagree, row.FirstDisagree = judge(got, ids, cohort, percent, stable, candidate)
		row.OK = row.Disagree == 0
		if percent == 10 && in.Rollout {
			share := float64(row.Candidates) / float64(len(ids))
			if share < 0.09 || share > 0.11 {
				row.OK = false
			}
		}
		if !row.OK {
			why := fmt.Sprintf("%s: %d of %d ids activate the candidate and %d disagree with the generator",
				rt, row.Candidates, len(ids), row.Disagree)
			if row.FirstDisagree != "" {
				why += " — first " + row.FirstDisagree
			}
			bad = append(bad, why)
		}
		s.rollout.Rows = append(s.rollout.Rows, row)
	}
	return bad
}

func (s *scenario) stagedRollout() {
	const id = "12.4"
	table, err := loadCohortTable()
	if !s.step(id, "the generator's cohort table: 10,000 installation ids with their SPEC §1.4 cohorts", func() error { return err }) {
		s.unreached(id, "each runtime implements SPEC §1.4", "start a rollout at 10 %", "the edge's manifest carries it",
			"three runtimes at 10 %", "rollout support off", "advance to 50 %", "abort", "complete")
		return
	}
	s.rollout.IDs, s.rollout.TableSalt, s.rollout.ExpAt10 = len(table.ids), table.Salt, table.ExpCandidates["10"]

	// Project A: a stable release in production and a candidate in
	// staging, one key that reads both.
	var key struct {
		Key string `json:"key"`
	}
	s.owner.do(http.MethodPost, s.projectPathOf(s.projectA, "/delivery-keys"), map[string]any{
		"name": "m5-ledger", "scope": map[string]any{"environments": []string{"production", "staging"}, "branches": false},
	}, http.StatusCreated, &key)
	stable, err := s.publish(s.owner, s.projectA, "production", map[string]any{"note": "stable"}, http.StatusCreated)
	if err != nil {
		s.gap(id, "the fixture's stable release of project A failed: %v", err)
		return
	}
	s.touch(s.projectA, unitKey("a", 2), "Kandidat für den gestaffelten Rollout")
	candidate, err := s.publish(s.owner, s.projectA, "staging", map[string]any{"note": "candidate"}, http.StatusCreated)
	if err != nil {
		s.gap(id, "the fixture's candidate release of project A failed: %v", err)
		return
	}
	if ok, served := s.edgeServes(key.Key, "production", stable.ID, 20*time.Second); !ok {
		s.gap(id, "the edge never served the stable release (it served %s)", served)
		return
	}
	if ok, served := s.edgeServes(key.Key, "staging", candidate.ID, 20*time.Second); !ok {
		s.gap(id, "the edge never served the candidate release in staging (it served %s)", served)
		return
	}
	base := driverInput{EdgeURL: s.d.edgeURL, DeliveryKey: key.Key, Environment: "production", IDs: table.ids, Rollout: true}

	// The runtimes' side on its own, before the platform has a say: the
	// edge's real production manifest with a `rollout` member the
	// harness adds under the table's salt, its candidate the edge's real
	// staging release. Verification is off in the drivers (no public
	// keys), so the probe is accepted as a manifest; every artifact is
	// still the edge's. This says, per runtime, whether SPEC §1.4 is
	// implemented — independently of whether the platform can start a
	// rollout.
	s.step(id, "each runtime implements SPEC §1.4 (probe: the edge's manifests, a 10 % rollout under the table's salt)", func() error {
		probe, err := s.probeManifest(key.Key, table.Salt, 10)
		if err != nil {
			return err
		}
		in := base
		in.Manifest = probe
		bad := s.across("probe at 10 %", in, table.ids, table.cohort, 10, stable.ID, candidate.ID)
		in.Rollout = false
		bad = append(bad, s.across("probe, rollout support off", in, table.ids, table.cohort, 0, stable.ID, candidate.ID)...)
		if len(bad) > 0 {
			return fmt.Errorf("runtimes without SPEC §1.4 rollout: %s", strings.Join(unique(bad), "; "))
		}
		return nil
	})

	var rollout, rolloutETag string
	if !s.step(id, "start a rollout of the candidate at 10 % in project A's `production`", func() error {
		var out struct {
			ID string `json:"id"`
		}
		h, err := s.owner.try(http.MethodPost, s.rolloutsPath(s.projectA, "production"),
			map[string]any{"release_id": candidate.ID, "percent": 10}, http.StatusCreated, &out)
		if err != nil {
			return missing("starting a rollout (POST "+s.rolloutsPath(s.projectA, "production")+")", err)
		}
		rollout, rolloutETag = out.ID, h.Get("ETag")
		return nil
	}) {
		s.unreached(id, "the edge's manifest carries it", "three runtimes at 10 %, against the generator", "rollout support off",
			"advance to 50 % keeps every 10 % installation", "abort returns all of them to stable", "complete moves the pointer")
		return
	}
	var cohort map[string]int
	if !s.step(id, "the edge's signed manifest carries the rollout: 10 %, the candidate, a salt", func() error {
		ok, state := softly(15*time.Second, func() (bool, string) {
			m, err := s.fetchManifest(key.Key, "production")
			if err != nil {
				return false, err.Error()
			}
			if m.Rollout == nil {
				return false, "no `rollout` member"
			}
			s.rollout.ManifestSalt = m.Rollout.Salt
			return m.Rollout.Percent == 10 && m.Rollout.Candidate.Release.ID == candidate.ID && m.Rollout.Salt != "" &&
				m.Release.ID == stable.ID, fmt.Sprintf("rollout %+v over release %s", *m.Rollout, short(m.Release.ID))
		})
		if !ok {
			return fmt.Errorf("the edge serves %s", state)
		}
		var err error
		cohort, err = generatorCohorts(s.rollout.ManifestSalt, table.ids)
		return err
	}) {
		s.unreached(id, "three runtimes at 10 %, against the generator", "rollout support off",
			"advance to 50 % keeps every 10 % installation", "abort returns all of them to stable", "complete moves the pointer")
		return
	}
	s.step(id, "three runtimes at 10 %: within 9–11 %, and each agrees id for id with the generator", func() error {
		if bad := s.across("10 %", base, table.ids, cohort, 10, stable.ID, candidate.ID); len(bad) > 0 {
			return fmt.Errorf("%s", strings.Join(bad, "; "))
		}
		return nil
	})
	s.step(id, "rollout support off: every id stays on stable", func() error {
		in := base
		in.Rollout = false
		if bad := s.across("10 %, rollout support off", in, table.ids, cohort, 0, stable.ID, candidate.ID); len(bad) > 0 {
			return fmt.Errorf("%s", strings.Join(bad, "; "))
		}
		return nil
	})
	s.step(id, "advance to 50 %: every 10 % installation stays in the candidate", func() error {
		// A PATCH carries the ETag it is based on (the API's
		// optimistic-concurrency convention): the rollout's, from its
		// start.
		if _, err := s.owner.try(http.MethodPatch, s.rolloutPath(s.projectA, "production", rollout),
			map[string]any{"percent": 50}, http.StatusOK, nil, "If-Match", rolloutETag); err != nil {
			return missing("advancing the rollout", err)
		}
		if ok, state := softly(15*time.Second, func() (bool, string) {
			m, err := s.fetchManifest(key.Key, "production")
			if err != nil || m.Rollout == nil {
				return false, fmt.Sprint("no rollout: ", err)
			}
			return m.Rollout.Percent == 50, fmt.Sprintf("%d %%", m.Rollout.Percent)
		}); !ok {
			return fmt.Errorf("the edge's manifest is at %s, want 50 %%", state)
		}
		if bad := s.across("50 %", base, table.ids, cohort, 50, stable.ID, candidate.ID); len(bad) > 0 {
			return fmt.Errorf("%s", strings.Join(bad, "; "))
		}
		return nil
	})
	s.step(id, "abort: every installation is back on stable at the next refresh", func() error {
		if _, err := s.owner.try(http.MethodPost, s.rolloutPath(s.projectA, "production", rollout)+"/abort",
			map[string]any{}, http.StatusOK, nil); err != nil {
			return missing("aborting the rollout", err)
		}
		if ok, _ := softly(15*time.Second, func() (bool, string) {
			m, err := s.fetchManifest(key.Key, "production")
			return err == nil && m.Rollout == nil, ""
		}); !ok {
			return fmt.Errorf("the edge's manifest still carries the rollout")
		}
		if bad := s.across("aborted", base, table.ids, cohort, 0, stable.ID, candidate.ID); len(bad) > 0 {
			return fmt.Errorf("%s", strings.Join(bad, "; "))
		}
		return nil
	})
	s.step(id, "complete: the pointer moves to the candidate and the rollout member is gone", func() error {
		var out struct {
			ID string `json:"id"`
		}
		if _, err := s.owner.try(http.MethodPost, s.rolloutsPath(s.projectA, "production"),
			map[string]any{"release_id": candidate.ID, "percent": 10}, http.StatusCreated, &out); err != nil {
			return fmt.Errorf("a second rollout could not start: %w", err)
		}
		if _, err := s.owner.try(http.MethodPost, s.rolloutPath(s.projectA, "production", out.ID)+"/completion",
			map[string]any{}, http.StatusOK, nil); err != nil {
			return missing("completing the rollout", err)
		}
		if ok, state := softly(15*time.Second, func() (bool, string) {
			m, err := s.fetchManifest(key.Key, "production")
			if err != nil {
				return false, err.Error()
			}
			return m.Release.ID == candidate.ID && m.Rollout == nil, "release " + short(m.Release.ID)
		}); !ok {
			return fmt.Errorf("the edge serves %s", state)
		}
		return nil
	})
}

func unique(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// probeManifest is the edge's production manifest with a rollout member
// whose candidate is the edge's staging manifest.
func (s *scenario) probeManifest(key, salt string, percent int) (string, error) {
	prod, err := s.fetchManifest(key, "production")
	if err != nil {
		return "", err
	}
	staging, err := s.fetchManifest(key, "staging")
	if err != nil {
		return "", err
	}
	var p, c map[string]any
	if err := json.Unmarshal(prod.raw, &p); err != nil {
		return "", err
	}
	if err := json.Unmarshal(staging.raw, &c); err != nil {
		return "", err
	}
	candidate := map[string]any{}
	for _, k := range []string{"release", "locales", "fallback", "artifacts"} {
		if v, ok := c[k]; ok {
			candidate[k] = v
		}
	}
	p["rollout"] = map[string]any{"id": "ro_m5probe", "percent": percent, "salt": salt, "candidate": candidate}
	b, err := json.Marshal(p)
	return string(b), err
}

// generatorCohorts asks runtimes/testdata/gen/generate.py — the fourth
// implementation, which shares no code with any runtime — for each id's
// cohort under the manifest's salt.
func generatorCohorts(salt string, ids []string) (map[string]int, error) {
	gen := filepath.Join(repoRoot(), "runtimes", "testdata", "gen")
	script := `import json, sys
sys.path.insert(0, sys.argv[1])
import generate
d = json.load(sys.stdin)
print(json.dumps({k: generate.cohort(d["salt"], k) for k in d["ids"]}))`
	in, _ := json.Marshal(map[string]any{"salt": salt, "ids": ids})
	cmd := exec.Command("python3", "-c", script, gen)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("generate.py could not compute cohorts for the manifest's salt: %v: %s", err, lastLines(stderr.String(), 3))
	}
	cohort := map[string]int{}
	return cohort, json.Unmarshal(out, &cohort)
}
