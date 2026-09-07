package enumdb_test

import (
	"context"
	"fmt"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/tecnickcom/nurago/pkg/enumdb"
)

func ExampleNew() {
	// A real program would use a *sql.DB from sql.Open. The mock keeps the
	// example self-contained and its output deterministic.
	db, mock, err := sqlmock.New()
	if err != nil {
		fmt.Println(err)

		return
	}

	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT id, name FROM status").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "pending").
			AddRow(2, "active").
			AddRow(3, "archived"),
	)

	// One query per enumeration table, each returning (id int, name string).
	queries := enumdb.EnumTableQuery{
		"status": "SELECT id, name FROM status WHERE disabled = 0 ORDER BY id",
	}

	enum, err := enumdb.New(context.TODO(), db, queries)
	if err != nil {
		fmt.Println(err)

		return
	}

	// The result is keyed by table name and each value is a thread-safe
	// bidirectional cache, ready for lookups without further database access.
	name, err := enum["status"].Name(2)
	fmt.Println(name, err)

	id, err := enum["status"].ID("archived")
	fmt.Println(id, err)

	fmt.Println(enum["status"].SortNames())

	// Output:
	// active <nil>
	// 3 <nil>
	// [active archived pending]
}
