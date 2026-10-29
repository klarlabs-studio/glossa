//go:build integration

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli"
)

const brotTMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="glossa-cli-test" creationtoolversion="1" segtype="sentence" o-tmf="test" adminlang="en" srclang="de" datatype="plaintext"/>
  <body>
    <tu tuid="fresh-rolls">
      <tuv xml:lang="de"><seg>Frische Brötchen</seg></tuv>
      <tuv xml:lang="en"><seg>Fresh rolls</seg></tuv>
    </tu>
  </body>
</tmx>
`

const welcomeTBX = `<?xml version="1.0" encoding="UTF-8"?>
<tbx xmlns="urn:iso:std:iso:30042:ed-2" type="TBX-Basic" style="dca" xml:lang="en">
  <tbxHeader><fileDesc><sourceDesc><p>glossa-cli-test</p></sourceDesc></fileDesc></tbxHeader>
  <text>
    <body>
      <conceptEntry id="welcome">
        <langSec xml:lang="de">
          <termSec><term>Willkommen</term><termNote type="administrativeStatus">preferredTerm-admn-sts</termNote></termSec>
        </langSec>
        <langSec xml:lang="en">
          <termSec><term>Welcome</term><termNote type="administrativeStatus">supersededTerm-admn-sts</termNote></termSec>
        </langSec>
      </conceptEntry>
    </body>
  </text>
</tbx>
`

type importDoc struct {
	Mode   string `json:"mode"`
	DryRun bool   `json:"dry_run"`
	Job    struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		ReusedJobID string `json:"reused_job_id"`
		Summary     struct {
			Created, Updated, Unchanged, Conflict, Invalid int
			ByKind                                         map[string]map[string]int `json:"by_kind"`
		} `json:"summary"`
	} `json:"job"`
	Results []struct {
		Kind, Key, Locale, Status, Code, Location, Ref string
		Line                                           int
	} `json:"results"`
}

// interchangeLoop is import/export jobs through the CLI against the
// real server, workers and MinIO: export XLIFF (de → en) → import it
// back with --apply → no changes; a JSON file conflicting with an
// approved translation → exit 1 with the conflict listed; a malformed
// file → exit 4 with file:line:column; a zip of JSON catalogs,
// unzipped; TMX import → tm search finds the unit; TBX import → terms
// check flags its forbidden term.
func interchangeLoop(t *testing.T, r runner) {
	wait := []string{"--poll-interval", "100ms", "--timeout", "2m"}
	with := func(args ...string) []string { return append(args, wait...) }

	// Round trip: every state, so each en translation has a target.
	var exp struct {
		Job struct {
			State   string `json:"state"`
			Written int    `json:"written"`
		} `json:"job"`
		File struct {
			Path     string `json:"path"`
			SHA256   string `json:"sha256"`
			Verified bool   `json:"verified"`
		} `json:"file"`
	}
	r.run(cli.ExitOK, &exp, with("export", "--format", "xliff", "--locale", "en", "--state", "approved,needs_review,draft", "-o", "i18n/en.xlf")...)
	if exp.Job.State != "succeeded" || exp.Job.Written != 4 || !exp.File.Verified || exp.File.Path != filepath.Join("i18n", "en.xlf") {
		t.Fatalf("export xliff = %+v", exp)
	}
	xlf, err := os.ReadFile(filepath.Join(r.dir, "i18n", "en.xlf"))
	if err != nil || !strings.Contains(string(xlf), `trgLang="en"`) || !strings.Contains(string(xlf), "Willkommen") {
		t.Fatalf("en.xlf = %s (%v)", xlf, err)
	}
	var back importDoc
	r.run(cli.ExitOK, &back, with("import", "--format", "xliff", "i18n/en.xlf", "--apply")...)
	s := back.Job.Summary
	if back.Mode != "merge" || back.Job.State != "succeeded" || s.Created+s.Updated+s.Conflict+s.Invalid != 0 || s.Unchanged < 8 ||
		back.Job.ReusedJobID != "" {
		t.Fatalf("xliff round trip changed something: %+v", back)
	}

	// A JSON file against en's approved checkout.pay: a conflict, in
	// the dry run and when applied.
	if err := os.MkdirAll(filepath.Join(r.dir, "incoming"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "incoming", "en.json"), []byte("{\n  \"checkout.pay\": \"Pay now {amount, number}\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range [][]string{{}, {"--apply"}} {
		var conflict importDoc
		r.run(cli.ExitCheckFailed, &conflict, with(append([]string{"import", "--format", "json", "incoming/en.json", "--locale", "en"}, mode...)...)...)
		// Every result says where it is: the member's line and column.
		if conflict.Job.Summary.Conflict != 1 || len(conflict.Results) != 1 || conflict.Results[0].Key != "checkout.pay" ||
			conflict.Results[0].Locale != "en" || conflict.Results[0].Code != "approved_translation_conflict" ||
			conflict.Results[0].Location != "incoming/en.json:2:3" || conflict.Results[0].Ref != "/checkout.pay" {
			t.Fatalf("json conflict %v = %+v", mode, conflict)
		}
	}
	// An XLIFF file without trgLang imports as the --locale given.
	noTrg := strings.Replace(string(xlf), ` trgLang="en"`, "", 1)
	if err := os.WriteFile(filepath.Join(r.dir, "incoming", "vendor.xlf"), []byte(noTrg), 0o644); err != nil {
		t.Fatal(err)
	}
	var vendor importDoc
	r.run(cli.ExitOK, &vendor, with("import", "--format", "xliff", "incoming/vendor.xlf", "--locale", "en")...)
	if vendor.Job.State != "succeeded" || vendor.Job.Summary.ByKind["translation"]["unchanged"] < 4 {
		t.Fatalf("xliff --locale = %+v", vendor)
	}
	// A malformed file fails the job; its problem says where.
	if err := os.WriteFile(filepath.Join(r.dir, "incoming", "broken.json"), []byte("{\n  \"checkout.pay\": \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var broken importDoc
	r.run(cli.ExitPartial, &broken, with("import", "--format", "json", "incoming/broken.json", "--locale", "en")...)
	if broken.Job.State != "failed" || len(broken.Results) == 0 ||
		!strings.HasPrefix(broken.Results[len(broken.Results)-1].Location, "incoming/broken.json:") {
		t.Fatalf("malformed json = %+v", broken)
	}

	// Several locales come zipped; --unzip extracts them.
	var zipped struct {
		Extracted []struct{ Path string } `json:"extracted"`
	}
	r.run(cli.ExitOK, &zipped, with("export", "--format", "json", "--locale", "de,en", "--state", "approved,needs_review,draft", "--unzip", "-o", "exported")...)
	if len(zipped.Extracted) != 2 {
		t.Fatalf("unzip = %+v", zipped)
	}
	de, err := os.ReadFile(filepath.Join(r.dir, "exported", "de.json"))
	if err != nil || !strings.Contains(string(de), `"home.title": "Willkommen"`) {
		t.Errorf("exported de.json = %s (%v)", de, err)
	}

	// TMX into the project's memory; tm search finds it. Applying the
	// same file again reuses the first job's result.
	if err := os.WriteFile(filepath.Join(r.dir, "brot.tmx"), []byte(brotTMX), 0o644); err != nil {
		t.Fatal(err)
	}
	var tmx importDoc
	r.run(cli.ExitOK, &tmx, with("tm", "import", "brot.tmx", "--apply")...)
	if tmx.Job.Summary.ByKind["tm_unit"]["created"] != 1 {
		t.Fatalf("tm import = %+v", tmx)
	}
	var again importDoc
	r.run(cli.ExitOK, &again, with("tm", "import", "brot.tmx", "--apply")...)
	if again.Job.ReusedJobID != tmx.Job.ID {
		t.Errorf("re-import = %+v", again.Job)
	}
	var search struct {
		Matches []struct {
			Score      int    `json:"score"`
			TargetText string `json:"target_text"`
		} `json:"matches"`
	}
	r.run(cli.ExitOK, &search, "tm", "search", "Frische Brötchen", "--to", "en")
	if len(search.Matches) == 0 || search.Matches[0].TargetText != "Fresh rolls" || search.Matches[0].Score < 100 {
		t.Fatalf("tm search after the TMX import = %+v", search)
	}

	// TBX into the termbase: "Welcome" is forbidden in en, and en
	// home.title says it.
	if err := os.WriteFile(filepath.Join(r.dir, "welcome.tbx"), []byte(welcomeTBX), 0o644); err != nil {
		t.Fatal(err)
	}
	var tbx importDoc
	r.run(cli.ExitOK, &tbx, with("terms", "import", "welcome.tbx", "--apply")...)
	if tbx.Job.Summary.ByKind["concept"]["created"] != 1 {
		t.Fatalf("terms import = %+v", tbx)
	}
	var check struct {
		Findings []struct{ Code, Key, Text string } `json:"findings"`
	}
	r.run(cli.ExitCheckFailed, &check, "terms", "check", "--locale", "en")
	flagged := false
	for _, f := range check.Findings {
		if f.Code == "term_forbidden" && f.Key == "home.title" && f.Text == "Welcome" {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("terms check after the TBX import = %+v", check)
	}

	// The workspace's memory, through its own route: tenant-wide units,
	// listed with the project's jobs.
	var ws importDoc
	r.run(cli.ExitOK, &ws, with("tm", "import", "brot.tmx", "--scope", "tenant", "--apply")...)
	if ws.Job.Summary.ByKind["tm_unit"]["created"] != 1 {
		t.Fatalf("workspace tm import = %+v", ws)
	}

	var ns struct {
		Namespaces []struct {
			Name   string `json:"name"`
			Active int    `json:"active_messages"`
		} `json:"namespaces"`
	}
	r.run(cli.ExitOK, &ns, "namespaces")
	if len(ns.Namespaces) == 0 || ns.Namespaces[0].Active == 0 {
		t.Errorf("namespaces = %+v", ns)
	}

	var jobs struct {
		Jobs []struct {
			Direction, Format, State string
			ProjectID                *string `json:"project_id"`
		} `json:"jobs"`
	}
	r.run(cli.ExitOK, &jobs, "jobs", "list", "--limit", "0")
	if len(jobs.Jobs) != 11 || jobs.Jobs[0].Format != "tmx" || jobs.Jobs[0].ProjectID != nil {
		t.Errorf("jobs list = %+v", jobs)
	}
}
