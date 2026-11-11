package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// A request reads the user it acts for, and writes later, in a transaction that
// checks the caller's role again. A race test needs a commit between the two, so
// it needs a hook that runs when the write's transaction begins: before(), then
// BEGIN. The write's service gets a pool of its own on the same database file,
// whose connections run the hook first. The hook makes requests, which use the
// test server's own pool, so it never waits for the service's connection.

// racingBegin is a driver connection that runs a function before it begins a
// transaction. The other methods of the connection are the ones database/sql uses
// when a driver has only Prepare.
type racingBegin struct {
	driver.Conn
	before func()
}

func (c racingBegin) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.before()
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

type racingConnector struct {
	dsn    string
	before func()
}

func (c racingConnector) Connect(context.Context) (driver.Conn, error) {
	inner, err := (&sqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return racingBegin{Conn: inner, before: c.before}, nil
}

func (c racingConnector) Driver() driver.Driver { return &sqlite.Driver{} }

// racingPool opens the database of db again, in a pool whose transactions first
// run the function that a test stores in the returned pointer, once. It is armed
// when a test stores a function there. The DSN repeats the pragmas of store.Open.
func racingPool(t *testing.T, db *sqlx.DB) (*sqlx.DB, *atomic.Pointer[func()]) {
	t.Helper()
	var file string
	require.NoError(t, db.GetContext(t.Context(), &file, `SELECT file FROM pragma_database_list WHERE name = 'main'`))
	var armed atomic.Pointer[func()]
	pool := sqlx.NewDb(sql.OpenDB(racingConnector{
		dsn: "file:" + file + "?_time_format=sqlite&_txlock=immediate&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		before: func() {
			if f := armed.Swap(nil); f != nil {
				(*f)()
			}
		},
	}), "sqlite")
	pool.SetMaxOpenConns(2) // the hook's own requests authenticate on this pool too
	t.Cleanup(func() { _ = pool.Close() })
	return pool, &armed
}
