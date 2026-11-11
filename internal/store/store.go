// Package store owns the SQLite database and its migrations.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// BaselineVersion is the version of the baseline migration, which creates the
// whole schema (docs/spec.md, Migration). A database at this version or later
// is one that this binary supports. A database below it that is not empty is
// older than this release supports, and the version guard refuses it.
const BaselineVersion int64 = 20261110030443

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the SQLite connection.
type Store struct {
	db *sqlx.DB
}

// Open opens (or creates) the SQLite database at path and applies the
// connection pragmas wishbone relies on. Every command opens the database
// here, so this is where the version guard runs: Open fails with
// [ErrDatabaseNewer] or [ErrDatabaseTooOld] before anything else touches a
// database that is newer than the binary, or older than it supports.
func Open(ctx context.Context, path string) (*Store, error) {
	// _txlock=immediate takes the write lock at BEGIN, so busy_timeout applies.
	// A deferred BEGIN that reads then writes gets SQLITE_BUSY at once when
	// another process (e.g. the CLI) has written.
	// _time_format=sqlite makes DATE/TIMESTAMP columns scan as time.Time
	// consistently across platforms.
	dsn := "file:" + path + "?_time_format=sqlite" +
		"&_txlock=immediate" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)"

	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// SQLite allows one writer; a single connection avoids "database is locked".
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	st := &Store{db: db}
	if err := st.checkVersion(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

// ErrDatabaseNewer is the error Open returns for a database whose goose
// version is newer than every migration in the binary.
var ErrDatabaseNewer = errors.New("database is newer than this binary")

// DatabaseNewerError says which versions Open compared. It matches
// [ErrDatabaseNewer].
type DatabaseNewerError struct {
	Database int64 // the goose version of the database
	Binary   int64 // the version of the newest migration in the binary
}

func (e *DatabaseNewerError) Error() string {
	return fmt.Sprintf("the database is at migration version %d, but the newest migration in this binary is %d: "+
		"run a newer image, or restore a snapshot made by this version", e.Database, e.Binary)
}

func (e *DatabaseNewerError) Is(target error) bool { return target == ErrDatabaseNewer }

// ErrDatabaseTooOld is the error Open returns for a database that is older
// than this release supports: it has migrations, but not the baseline.
var ErrDatabaseTooOld = errors.New("database is older than this release supports")

// DatabaseTooOldError says which version Open found. It matches
// [ErrDatabaseTooOld].
type DatabaseTooOldError struct {
	Database int64 // the goose version of the database
}

func (e *DatabaseTooOldError) Error() string {
	return fmt.Sprintf("the database is at migration version %d, which is older than this release supports: "+
		"it starts only on a database at version %d or later", e.Database, BaselineVersion)
}

func (e *DatabaseTooOldError) Is(target error) bool { return target == ErrDatabaseTooOld }

// checkVersion is the version guard. A newer binary migrated this database,
// and an older binary doesn't know its schema. A database that has migrations
// but not the baseline is older than this binary supports, and it can't migrate
// it. It only reads, and it passes a database that goose never touched (a new
// install).
func (s *Store) checkVersion(ctx context.Context) error {
	have, err := Version(ctx, s.db)
	if err != nil {
		return err
	}
	if have > 0 && have < BaselineVersion {
		return &DatabaseTooOldError{Database: have}
	}
	p, err := s.provider()
	if err != nil {
		return err
	}
	var newest int64
	for _, src := range p.ListSources() {
		newest = max(newest, src.Version)
	}
	if have > newest {
		return &DatabaseNewerError{Database: have, Binary: newest}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping reports whether the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Migrate applies all pending embedded migrations.
func (s *Store) Migrate(ctx context.Context) error {
	return s.MigrateAfter(ctx, nil)
}

// MigrateAfter applies all pending embedded migrations, like [Store.Migrate].
// First it calls before with the database's goose version and the version of
// the newest migration. If before returns an error, MigrateAfter applies no
// migration and returns that error. With no migration pending, it doesn't
// call before. A nil before is skipped.
func (s *Store) MigrateAfter(ctx context.Context, before func(ctx context.Context, from, to int64) error) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	if !pending {
		return nil
	}
	if before != nil {
		from, to, err := p.GetVersions(ctx)
		if err != nil {
			return fmt.Errorf("migration versions: %w", err)
		}
		if err := before(ctx, from, to); err != nil {
			return err
		}
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// ErrMigrationPending means the database has migrations that only `serve`
// applies.
var ErrMigrationPending = errors.New("migrations are pending")

// EnsureMigrated returns [ErrMigrationPending] when a migration is pending. A
// command other than `serve` calls it, because only `serve` snapshots and
// migrates (docs/spec.md, Migration, Start order).
func (s *Store) EnsureMigrated(ctx context.Context) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	if pending {
		return fmt.Errorf("%w: start the server once first, so it can back up and migrate the database", ErrMigrationPending)
	}
	return nil
}

// MigrateTo applies the pending embedded migrations up to and including
// version, and no further. It builds a database as an older release left it,
// for a test of code that must still read that schema (the snapshot before a
// migration, for one). The server uses [Store.MigrateAfter].
func (s *Store) MigrateTo(ctx context.Context, version int64) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	if _, err := p.UpTo(ctx, version); err != nil {
		return fmt.Errorf("run migrations to %d: %w", version, err)
	}
	return nil
}

func (s *Store) provider() (*goose.Provider, error) {
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("migrations fs: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db.DB, migrations)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return p, nil
}

// Version returns the newest goose version recorded in db, or 0 for a
// database that goose never touched. It only reads, so it works on a
// read-only snapshot.
func Version(ctx context.Context, db sqlx.QueryerContext) (int64, error) {
	var n int
	if err := sqlx.GetContext(ctx, db, &n,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, goose.DefaultTablename); err != nil {
		return 0, fmt.Errorf("find goose table: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	var v int64
	if err := sqlx.GetContext(ctx, db, &v,
		`SELECT COALESCE(MAX(version_id), 0) FROM `+goose.DefaultTablename); err != nil {
		return 0, fmt.Errorf("read goose version: %w", err)
	}
	return v, nil
}

// DB returns the underlying database handle for packages that own queries.
func (s *Store) DB() *sqlx.DB {
	return s.db
}
