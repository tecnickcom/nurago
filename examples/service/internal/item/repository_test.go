package item

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// testTime is the fixed creation time used by the repository tests.
func testTime() time.Time {
	return time.Date(2025, time.January, 1, 0, 0, 1, 0, time.UTC)
}

// newTestRepository returns a repository whose read and write connections are
// the same mocked database, so a single mock covers both paths.
func newTestRepository(t *testing.T) (*Repository, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())

		_ = db.Close()
	})

	return NewRepository(db, db), mock
}

// itemRows builds a result set with the repository's column list.
func itemRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "quantity", "created_at"})
}

func TestNewRepository(t *testing.T) {
	t.Parallel()

	require.NotNil(t, NewRepository(nil, nil))
}

func TestRepository_Get(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectItem)).
			WithArgs("test-id").
			WillReturnRows(itemRows().AddRow("test-id", "test", 3, testTime()))

		got, err := repo.Get(t.Context(), "test-id")

		require.NoError(t, err)
		require.Equal(t, &Item{ID: "test-id", Name: "test", Quantity: 3, CreatedAt: testTime()}, got)
	})

	t.Run("returns ErrNotFound for a missing row", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectItem)).
			WithArgs("test-id").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.Get(t.Context(), "test-id")

		require.ErrorIs(t, err, ErrNotFound)
		require.Nil(t, got)
	})

	t.Run("fails on a scan error", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectItem)).
			WithArgs("test-id").
			WillReturnRows(itemRows().AddRow("test-id", "test", "not-a-number", testTime()))

		got, err := repo.Get(t.Context(), "test-id")

		require.Error(t, err)
		require.Nil(t, got)
	})
}

func TestRepository_List(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectPage)).
			WithArgs(uint(2)).
			WillReturnRows(itemRows().
				AddRow("id-2", "second", 2, testTime()).
				AddRow("id-1", "first", 1, testTime()))

		got, err := repo.List(t.Context(), 2)

		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Equal(t, "id-2", got[0].ID)
	})

	t.Run("returns an empty page", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectPage)).
			WithArgs(uint(2)).
			WillReturnRows(itemRows())

		got, err := repo.List(t.Context(), 2)

		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("fails on a query error", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectPage)).
			WithArgs(uint(2)).
			WillReturnError(errTest)

		got, err := repo.List(t.Context(), 2)

		require.ErrorIs(t, err, errTest)
		require.Nil(t, got)
	})

	t.Run("fails on a scan error", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectPage)).
			WithArgs(uint(1)).
			WillReturnRows(itemRows().AddRow("id-1", "first", "not-a-number", testTime()))

		got, err := repo.List(t.Context(), 1)

		require.Error(t, err)
		require.Nil(t, got)
	})

	t.Run("fails on a row iteration error", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectQuery(regexp.QuoteMeta(sqlSelectPage)).
			WithArgs(uint(1)).
			WillReturnRows(itemRows().
				AddRow("id-1", "first", 1, testTime()).
				RowError(0, errTest))

		got, err := repo.List(t.Context(), 1)

		require.ErrorIs(t, err, errTest)
		require.Nil(t, got)
	})
}

func TestRepository_Create(t *testing.T) {
	t.Parallel()

	newItem := func() *Item {
		return &Item{ID: "test-id", Name: "test", Quantity: 3, CreatedAt: testTime()}
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(sqlInsertItem)).
			WithArgs("test-id", "test", uint(3), testTime()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.Create(t.Context(), newItem()))
	})

	t.Run("maps a duplicate name to ErrConflict", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(sqlInsertItem)).
			WillReturnError(&mysql.MySQLError{Number: mysqlErrDupEntry, Message: "duplicate entry"})
		mock.ExpectRollback()

		err := repo.Create(t.Context(), newItem())

		require.ErrorIs(t, err, ErrConflict)
	})

	t.Run("leaves any other failure unmapped", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(sqlInsertItem)).WillReturnError(errTest)
		mock.ExpectRollback()

		err := repo.Create(t.Context(), newItem())

		require.ErrorIs(t, err, errTest)
		require.NotErrorIs(t, err, ErrConflict)
	})

	t.Run("fails when the transaction cannot start", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectBegin().WillReturnError(errTest)

		require.ErrorIs(t, repo.Create(t.Context(), newItem()), errTest)
	})
}

func TestRepository_Delete(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectExec(regexp.QuoteMeta(sqlDeleteItem)).
			WithArgs("test-id").
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.Delete(t.Context(), "test-id"))
	})

	t.Run("returns ErrNotFound when no row matched", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectExec(regexp.QuoteMeta(sqlDeleteItem)).
			WithArgs("test-id").
			WillReturnResult(sqlmock.NewResult(0, 0))

		require.ErrorIs(t, repo.Delete(t.Context(), "test-id"), ErrNotFound)
	})

	t.Run("fails on an exec error", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectExec(regexp.QuoteMeta(sqlDeleteItem)).
			WithArgs("test-id").
			WillReturnError(errTest)

		require.ErrorIs(t, repo.Delete(t.Context(), "test-id"), errTest)
	})

	t.Run("fails when the affected row count is unavailable", func(t *testing.T) {
		t.Parallel()

		repo, mock := newTestRepository(t)

		mock.ExpectExec(regexp.QuoteMeta(sqlDeleteItem)).
			WithArgs("test-id").
			WillReturnResult(sqlmock.NewErrorResult(errTest))

		require.ErrorIs(t, repo.Delete(t.Context(), "test-id"), errTest)
	})
}
