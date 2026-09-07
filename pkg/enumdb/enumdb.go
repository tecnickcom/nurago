/*
Package enumdb loads enumeration sets from relational database tables into
thread-safe enum caches.

It builds in-memory ID-to-name and name-to-ID lookup maps from database reference
tables at service startup.

This package is designed for tables where each row represents a single enum value
with values that can be scanned into (id int, name string). In practice, this
usually corresponds to a numeric primary key column named "id" and a unique
string column named "name".

Example of a MySQL database table that can be used with this package:

	CREATE TABLE IF NOT EXISTS `example` (
	  `id` SMALLINT UNSIGNED NOT NULL,
	  `name` VARCHAR(50) NOT NULL,
	  `disabled` TINYINT NOT NULL DEFAULT 0,
	  PRIMARY KEY (`id`),
	  UNIQUE INDEX `id_UNIQUE` (`id` ASC),
	  UNIQUE INDEX `name_UNIQUE` (`name` ASC))
	ENGINE = InnoDB
	COMMENT = 'Example enumeration table';

# When To Use

  - Reference tables hold (id, name) rows that never change during a
    process's lifetime.
  - You want lookups to avoid the database entirely after startup.
*/
package enumdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tecnickcom/nurago/pkg/enumcache"
)

// EnumDB maps each enumeration table name with the corresponding enumeration cache.
type EnumDB map[string]*enumcache.EnumCache

// EnumTableQuery maps each enumeration table name with the SQL query string used to read the data.
type EnumTableQuery map[string]string

// New loads enum caches for all tables listed in queries.
//
// Each query should return rows compatible with (id int, name string). The
// result map is keyed by table name for direct access from application code.
//
// Loading is all-or-nothing: the first table that fails aborts the whole call and
// returns a nil map with an error wrapping the underlying failure. When several
// tables would fail, which one surfaces is not deterministic because queries is a
// map.
func New(ctx context.Context, db *sql.DB, queries EnumTableQuery) (EnumDB, error) {
	enum := make(EnumDB, len(queries))

	for table, query := range queries {
		cache, err := loadTableEnumCache(ctx, db, query)
		if err != nil {
			return nil, fmt.Errorf("enumdb: failed to load the enumeration table '%s': %w", table, err)
		}

		enum[table] = cache
	}

	return enum, nil
}

// loadTableEnumCache executes query and builds one in-memory EnumCache.
//
// It scans each row into id/name pairs and stores them for fast bidirectional
// lookups at runtime. The query carries no parameters and is run once, so it is
// executed directly without a prepared statement.
//
//nolint:nonamedreturns
func loadTableEnumCache(ctx context.Context, db *sql.DB, query string) (cache *enumcache.EnumCache, err error) {
	rows, qerr := db.QueryContext(ctx, query)
	if qerr != nil {
		return nil, fmt.Errorf("enumdb: failed executing query: %w", qerr)
	}

	defer func() {
		err = errors.Join(err, rows.Close())
	}()

	cache = enumcache.New()

	var (
		id   int
		name string
	)

	for rows.Next() {
		serr := rows.Scan(&id, &name)
		if serr != nil {
			return nil, fmt.Errorf("enumdb: unable to scan row: %w", serr)
		}

		cache.Set(id, name)
	}

	rerr := rows.Err()
	if rerr != nil {
		return nil, fmt.Errorf("enumdb: failed reading the rows: %w", rerr)
	}

	return cache, nil
}
