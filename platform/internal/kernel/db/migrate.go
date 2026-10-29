package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"go.klarlabs.de/glossa/platform/db/migrations"
)

// Migrator applies the embedded migrations. It must connect as the
// schema owner (MIGRATION_DATABASE_URL), never as the application role.
// golang-migrate takes a Postgres advisory lock, so replicas starting
// together serialize instead of racing.
type Migrator struct {
	m      *migrate.Migrate
	logger *slog.Logger
}

// NewMigrator connects to dsn, once, and prepares the embedded
// migrations. Retry.NewMigrator retries the connection.
func NewMigrator(dsn string, logger *slog.Logger) (*Migrator, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, permanent(fmt.Errorf("db: load embedded migrations: %w", err))
	}
	sqlDB, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		return nil, permanent(fmt.Errorf("db: open migration connection: %w", err))
	}
	driver, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: init migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		_ = driver.Close()
		return nil, permanent(fmt.Errorf("db: init migrator: %w", err))
	}
	m.Log = migrateLogger{logger}
	return &Migrator{m: m, logger: logger}, nil
}

// Up applies every pending migration. No pending migration is not an error.
func (mg *Migrator) Up(ctx context.Context) error {
	if err := mg.withContext(ctx, mg.m.Up); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// Down reverts every migration. Intended for tests and local resets.
func (mg *Migrator) Down(ctx context.Context) error {
	if err := mg.withContext(ctx, mg.m.Down); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: migrate down: %w", err)
	}
	return nil
}

// Version reports the applied version and whether it is dirty.
func (mg *Migrator) Version() (version uint, dirty bool, err error) {
	version, dirty, err = mg.m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}

// Close releases the migration connection.
func (mg *Migrator) Close() error {
	srcErr, dbErr := mg.m.Close()
	return errors.Join(srcErr, dbErr)
}

// withContext asks golang-migrate to stop after the current migration
// when ctx is cancelled; it has no context-aware API of its own.
func (mg *Migrator) withContext(ctx context.Context, run func() error) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			select {
			case mg.m.GracefulStop <- true:
			default:
			}
		case <-done:
		}
	}()
	return run()
}

// migrateLogger adapts slog to golang-migrate's logger interface.
type migrateLogger struct{ l *slog.Logger }

func (m migrateLogger) Printf(format string, v ...any) {
	m.l.Info("migrate: " + fmt.Sprintf(format, v...))
}

func (m migrateLogger) Verbose() bool { return false }
