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

	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
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

// racingConnector opens connections to the database at the DSN, each wrapped
// with the hook of a test.
type racingConnector struct {
	dsn  string
	wrap func(driver.Conn) driver.Conn
}

func (c racingConnector) Connect(context.Context) (driver.Conn, error) {
	inner, err := (&sqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return c.wrap(inner), nil
}

func (c racingConnector) Driver() driver.Driver { return &sqlite.Driver{} }

// racingPoolOf opens the database of db again, in a pool of two connections
// that are wrapped with the hook of a test. The DSN repeats the pragmas of
// store.Open.
func racingPoolOf(t *testing.T, db *sqlx.DB, wrap func(driver.Conn) driver.Conn) *sqlx.DB {
	t.Helper()
	var file string
	require.NoError(t, db.GetContext(t.Context(), &file, `SELECT file FROM pragma_database_list WHERE name = 'main'`))
	pool := sqlx.NewDb(sql.OpenDB(racingConnector{
		dsn:  "file:" + file + "?_time_format=sqlite&_txlock=immediate&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		wrap: wrap,
	}), "sqlite")
	pool.SetMaxOpenConns(2) // the hook's own requests use this pool too
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// racingPool opens the database of db again, in a pool whose transactions first
// run the function that a test stores in the returned pointer, once. It is armed
// when a test stores a function there.
func racingPool(t *testing.T, db *sqlx.DB) (*sqlx.DB, *atomic.Pointer[func()]) {
	t.Helper()
	var armed atomic.Pointer[func()]
	before := func() {
		if f := armed.Swap(nil); f != nil {
			(*f)()
		}
	}
	return racingPoolOf(t, db, func(inner driver.Conn) driver.Conn { return racingBegin{Conn: inner, before: before} }), &armed
}

// A request that reads a collection runs more than one statement, and a write
// can commit between two of them. A test of what the request then answers needs
// a hook that runs before a statement of the request's choice. The services
// that read get a pool of their own, as in [racingPool], whose connections count
// the queries and run the hook before the one that the test names.

// racingQuery is a driver connection that runs a function before each query. It
// passes BeginTx on, so a read-only transaction stays one.
type racingQuery struct {
	driver.Conn
	before func()
}

func (c racingQuery) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.before()
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c racingQuery) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

// queryRace is the hook of [racingReads].
type queryRace struct {
	// left is how many queries are left until the function runs.
	left  atomic.Int64
	armed atomic.Pointer[func()]
}

// before arms the hook: f runs once, before the n-th query from now of a
// service that reads through the racing pool. The queries that f makes itself
// come after it, so they don't run it again.
func (r *queryRace) before(n int, f func()) {
	r.left.Store(int64(n))
	r.armed.Store(&f)
}

// ran says whether the armed function ran, and disarms the hook.
func (r *queryRace) ran() bool { return r.armed.Swap(nil) == nil }

func (r *queryRace) query() {
	if r.left.Add(-1) != 0 {
		return
	}
	if f := r.armed.Swap(nil); f != nil {
		(*f)()
	}
}

// racingReads builds a server whose user and group services read through a
// pool that runs the hook before a query (see [queryRace]).
func racingReads(t *testing.T) (*env, *queryRace) {
	t.Helper()
	race := &queryRace{}
	e := newEnv(t, func(o *Options) {
		pool := racingPoolOf(t, o.Store.(*store.Store).DB(), func(inner driver.Conn) driver.Conn {
			return racingQuery{Conn: inner, before: race.query}
		})
		o.Users = user.NewService(pool, o.Now)
		o.Groups = group.NewService(pool, o.Now)
	})
	return e, race
}
