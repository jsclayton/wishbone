// Package imagerefs knows which image keys a database still uses. internal/backup
// checks that every one of them is in a snapshot.
//
// A snapshot can be of a database that the binary has not migrated yet: the
// automatic snapshot before a migration, and `wishbone backup` on a database
// from an older release. So each schema has its own query, and [KeysAt] picks
// the one that fits the database's goose version. A migration that adds or
// changes a column that holds an image key also adds an entry to [schemas].
// [schemas] needs an entry for every database version that the binary accepts
// (see store.ErrDatabaseTooOld), and no entry for a version it refuses.
package imagerefs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/store"
)

// imagesKeysSQL selects every image key in a database that has the images
// table (the schema from the baseline on). Every row of images has a file,
// whether or not a resource holds the row: the sweep deletes the row first, and
// the file after. So the backup checks every row, and a snapshot is never short
// of a file for a row it has.
const imagesKeysSQL = `SELECT key FROM images`

// ErrUnknownSchema means no image-key query fits the database's goose
// version.
var ErrUnknownSchema = errors.New("no image-key query for this database version")

// schemas lists the image-key query for each schema, oldest first. Each query
// fits the goose versions from since up to the next entry's since. The last
// entry fits every newer version. An empty sql means no table holds an image
// key. A database at version 0 is new and has no tables. A version below the
// first entry is older than the binary supports, and the version guard refuses it.
var schemas = []struct {
	since int64
	sql   string
}{
	{since: store.BaselineVersion, sql: imagesKeysSQL}, // the baseline schema, with images
}

// KeysAt returns the image keys that a database at goose version uses. It
// reads the database with the query for that schema, not the newest one, so a
// backup works on a database that isn't migrated yet.
func KeysAt(ctx context.Context, q sqlx.QueryerContext, version int64) ([]string, error) {
	if version == 0 {
		return nil, nil
	}
	for i := len(schemas) - 1; i >= 0; i-- {
		if version < schemas[i].since {
			continue
		}
		if schemas[i].sql == "" {
			return nil, nil
		}
		var keys []string
		if err := sqlx.SelectContext(ctx, q, &keys, schemas[i].sql); err != nil {
			return nil, err
		}
		return keys, nil
	}
	return nil, fmt.Errorf("%w: %d", ErrUnknownSchema, version)
}
