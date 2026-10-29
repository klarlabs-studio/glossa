//go:build integration || system

// Package v0test runs a Glossa v0.3 database for tests: a Postgres 16
// container with apps/api's own migrations applied — v0.3's schema as
// v0.3 defines it, read from the repository, never copied — a seed, and
// v0.3's backup (pg_dump | gzip, as its backup CronJob takes it)
// restored through platform/scripts/v0-restore.sh, the script operators
// run. Nothing under apps/ is changed or built.
package v0test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	image = "postgres:16-alpine" // v0.3's server major (the retired v0.3 k3s manifests)
	// LiveDB is the database v0.3 itself would use: migrated and seeded,
	// never marked.
	LiveDB   = "glossa"
	user     = "postgres"
	password = "postgres"
)

// Server is a v0.3 Postgres.
type Server struct {
	ctr  *tcpostgres.PostgresContainer
	host string
	port string
}

// RepoRoot is the repository root, from this file's path.
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", ".."))
}

// Migrations are apps/api's up migrations in order.
func Migrations() ([]string, error) {
	files, err := filepath.Glob(filepath.Join(RepoRoot(), "apps", "api", "db", "migrations", "*.up.sql"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("v0test: no apps/api migrations under %s", RepoRoot())
	}
	sort.Strings(files)
	return files, nil
}

// Start boots Postgres with v0.3's schema in LiveDB and seed applied.
func Start(ctx context.Context, seed string) (*Server, error) {
	migrations, err := Migrations()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ctr, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase(LiveDB), tcpostgres.WithUsername(user), tcpostgres.WithPassword(password),
		tcpostgres.WithInitScripts(migrations...),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("v0test: start postgres: %w", err)
	}
	s := &Server{ctr: ctr}
	host, err := ctr.Host(ctx)
	if err != nil {
		s.Close()
		return nil, err
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		s.Close()
		return nil, err
	}
	s.host, s.port = host, port.Port()
	scripts := filepath.Join(RepoRoot(), "platform", "scripts")
	for _, f := range []string{"v0-restore.sh", "v0-restore-marker.sql"} {
		if err := ctr.CopyFileToContainer(ctx, filepath.Join(scripts, f), "/scripts/"+f, 0o755); err != nil {
			s.Close()
			return nil, fmt.Errorf("v0test: copy %s: %w", f, err)
		}
	}
	if seed != "" {
		if _, err := s.SQL(ctx, LiveDB, seed); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// Close removes the container.
func (s *Server) Close() {
	if s.ctr != nil {
		_ = testcontainers.TerminateContainer(s.ctr)
	}
}

// DSN connects to db as the superuser (the role v0-restore.sh runs as),
// or as role/password when given.
func (s *Server) DSN(db string, role ...string) string {
	u, p := user, password
	if len(role) == 2 {
		u, p = role[0], role[1]
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", u, p, s.host, s.port, db)
}

// Shell runs a shell command in the container as the postgres OS user
// (local connections are trusted) and returns its output.
func (s *Server) Shell(ctx context.Context, cmd string) (string, error) {
	code, r, err := s.ctr.Exec(ctx, []string{"su", "postgres", "-c", cmd}, tcexec.Multiplexed())
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	_, _ = io.Copy(&out, r)
	if code != 0 {
		return out.String(), fmt.Errorf("v0test: %q exited %d: %s", cmd, code, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// SQL runs statements in db as the superuser, with read-only defaults
// overridden: tests use it to set up and to tamper, the importer never
// does.
func (s *Server) SQL(ctx context.Context, db, sql string) (string, error) {
	tmp, err := os.CreateTemp("", "v0test-*.sql")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(sql); err != nil {
		return "", err
	}
	_ = tmp.Close()
	in := "/tmp/" + filepath.Base(tmp.Name())
	if err := s.ctr.CopyFileToContainer(ctx, tmp.Name(), in, 0o644); err != nil {
		return "", err
	}
	return s.Shell(ctx, fmt.Sprintf(`PGOPTIONS='-c default_transaction_read_only=off' psql -X -q -v ON_ERROR_STOP=1 -d %s -f %s`, db, in))
}

// Backup takes v0.3's backup of LiveDB the way its CronJob does
// (pg_dump | gzip) and returns the file's path in the container.
func (s *Server) Backup(ctx context.Context, name string) (string, error) {
	path := "/tmp/" + name + ".sql.gz"
	_, err := s.Shell(ctx, fmt.Sprintf("set -o pipefail 2>/dev/null; pg_dump -d %s | gzip > %s", LiveDB, path))
	return path, err
}

// Restore runs platform/scripts/v0-restore.sh on a backup into a new
// database and returns its DSN.
func (s *Server) Restore(ctx context.Context, backup, db string) (string, error) {
	if _, err := s.Shell(ctx, fmt.Sprintf("sh /scripts/v0-restore.sh %s %s", backup, db)); err != nil {
		return "", err
	}
	return s.DSN(db), nil
}
