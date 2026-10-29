package migrations_test

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"

	"go.klarlabs.de/glossa/platform/db/migrations"
)

// TestTheStreamLoads is the cheapest guard there is on one migration
// stream shared by every bounded context (RFC 0002 §6): two contexts
// developed side by side can each claim the same NNNN, and
// golang-migrate then refuses the whole source rather than the one
// file — the server fails at startup, before a single migration runs,
// and every context goes down with it. No database, no build tag: a
// number taken twice is caught by `go test ./...`.
func TestTheStreamLoads(t *testing.T) {
	if _, err := iofs.New(migrations.FS, "."); err != nil {
		t.Fatalf("the embedded migrations do not form a stream: %v", err)
	}
}
