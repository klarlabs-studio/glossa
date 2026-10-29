package v0

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func marked() markerFacts {
	return markerFacts{database: "v0_restore", databaseOID: 16384, tableExists: true, readOnlyByDef: true,
		rows: []markerRow{{databaseOID: 16384, databaseName: "v0_restore", restore: Restore{DumpName: "d.sql.gz"}}}}
}

func TestAMarkedRestoreIsAccepted(t *testing.T) {
	r, err := decideRestore(marked())
	if err != nil || r.Database != "v0_restore" || r.DumpName != "d.sql.gz" {
		t.Fatalf("restore = %+v, %v", r, err)
	}
}

func TestEveryMissingConditionRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*markerFacts)
		why  string
	}{
		"no marker table (a live v0.3 database)": {func(f *markerFacts) { f.tableExists, f.rows = false, nil }, "no glossa_v0_restore.marker"},
		"an empty marker":                        {func(f *markerFacts) { f.rows = nil }, "0 rows"},
		"a marker copied from another database":  {func(f *markerFacts) { f.rows[0].databaseOID = 1 }, "was copied"},
		"a marker naming another database":       {func(f *markerFacts) { f.rows[0].databaseName = "glossa" }, "was copied"},
		"a writable database":                    {func(f *markerFacts) { f.readOnlyByDef = false }, "read-only"},
	} {
		t.Run(name, func(t *testing.T) {
			f := marked()
			tc.edit(&f)
			_, err := decideRestore(f)
			var nr *NotARestoreError
			if !errors.As(err, &nr) || !strings.Contains(err.Error(), tc.why) || !IsRefusal(err) {
				t.Fatalf("err = %v, want a refusal saying %q", err, tc.why)
			}
		})
	}
}

// The checker and the SQL the restore script runs must agree on the
// table and columns, or every restore would be refused (or worse,
// a check would read a column nobody writes).
func TestTheMarkerSQLWritesWhatTheCheckReads(t *testing.T) {
	sql, err := os.ReadFile("../../../scripts/v0-restore-marker.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(sql)
	for _, want := range []string{
		"CREATE SCHEMA " + MarkerSchema, "CREATE TABLE " + MarkerTable, "database_oid", "database_name", "dump_name", "dump_sha256",
		"restored_at", "restored_by", "only_row      boolean PRIMARY KEY", "WHERE datname = current_database()",
		"SET default_transaction_read_only = on",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("v0-restore-marker.sql lacks %q", want)
		}
	}
	script, err := os.ReadFile("../../../scripts/v0-restore.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"createdb \"$db\"", "v0-restore-marker.sql", "ON_ERROR_STOP=1 --single-transaction"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("v0-restore.sh lacks %q", want)
		}
	}
}
