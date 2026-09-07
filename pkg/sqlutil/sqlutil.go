/*
Package sqlutil quotes identifiers and string literals when generating SQL query
fragments dynamically.

# What It Provides

[New] returns a configurable [SQLUtil] instance exposing:

  - [SQLUtil.QuoteID] for quoting identifiers (schema/table/column names).
  - [SQLUtil.QuoteValue] for quoting string literal values.

The default implementation is mysql-like:

  - identifiers are split by "." and each segment is wrapped in backticks,
    with embedded backticks escaped as doubled backticks (no other escaping:
    inside backtick quotes only the backtick is special).
  - values are wrapped in single quotes, with embedded single quotes doubled
    and control characters escaped (`\0`, `\n`, `\r`, `\\`, `\Z`); the empty
    string yields an empty quoted literal (two single quotes).

# Customization

Use options to adapt quoting rules for different SQL dialects:

  - [WithQuoteIDFunc] replaces identifier quoting behavior.
  - [WithQuoteValueFunc] replaces value quoting behavior.

This adapts quoting to Postgres, SQLite, or other dialects.

# Important Boundary

This package is intended for quoting identifiers and string literals in dynamic
query generation. It is not a replacement for prepared statements and query
parameterization. Continue using placeholders and bound parameters for runtime
data whenever possible.

# Limitations

The default value quoting is correct for MySQL-like databases running in the
default SQL mode over an ASCII-compatible, single-byte-safe connection charset
(for example utf8mb4 or latin1). It is NOT safe for untrusted input in two
situations:

  - NO_BACKSLASH_ESCAPES SQL mode: backslash is not an escape character, so the
    backslash escaping applied here (\n, \\, \Z, ...) is interpreted literally
    and silently corrupts the stored value.
  - Non-self-synchronizing multibyte charsets (GBK, Big5, SJIS, ...): byte-wise
    escaping is the classic vector for escape-function SQL injection because a
    lead byte can consume the escaping backslash.

Use bound parameters for untrusted data, and supply [WithQuoteValueFunc] /
[WithQuoteIDFunc] to match the exact rules of another dialect or charset.

# Usage

	u, err := sqlutil.New()
	if err != nil {
	    return err
	}

	col := u.QuoteID("users.email")      // `users`.`email`
	val := u.QuoteValue("o'reilly")      // 'o''reilly'
	query := "SELECT " + col + " FROM " + u.QuoteID("users") + " WHERE " + col + " = " + val
	_ = query

# When To Use

  - A table or column name comes from configuration and cannot be a bind
    parameter.
  - You are generating DDL or a migration statement.
*/
package sqlutil

import (
	"errors"
	"strings"
)

var (
	// ErrNilQuoteIDFunc is returned when the identifier quoting function is nil.
	ErrNilQuoteIDFunc = errors.New("the QuoteID function must be set")

	// ErrNilQuoteValueFunc is returned when the value quoting function is nil.
	ErrNilQuoteValueFunc = errors.New("the QuoteValue function must be set")
)

// SQLQuoteFunc is the type of function called to quote a string (ID or value).
type SQLQuoteFunc func(s string) string

// SQLUtil is the structure that helps to manage a SQL DB connection.
type SQLUtil struct {
	quoteIDFunc    SQLQuoteFunc
	quoteValueFunc SQLQuoteFunc
}

// New constructs SQL utility with configurable identifier and value quoting functions (default: MySQL-style).
func New(opts ...Option) (*SQLUtil, error) {
	c := defaultSQLUtil()

	for _, applyOpt := range opts {
		applyOpt(c)
	}

	err := c.validate()
	if err != nil {
		return nil, err
	}

	return c, nil
}

// defaultSQLUtil creates a SQLUtil instance with default settings.
func defaultSQLUtil() *SQLUtil {
	return &SQLUtil{
		quoteIDFunc:    defaultQuoteID,
		quoteValueFunc: defaultQuoteValue,
	}
}

// QuoteID quotes identifiers (schema/table/column names) with configurable SQL dialect rules.
func (c *SQLUtil) QuoteID(s string) string {
	return c.quoteIDFunc(s)
}

// QuoteValue quotes string literal values with configurable SQL dialect escape rules; includes surrounding quotes.
func (c *SQLUtil) QuoteValue(s string) string {
	return c.quoteValueFunc(s)
}

// validate checks if the SQLUtil instance is properly configured.
func (c *SQLUtil) validate() error {
	if c.quoteIDFunc == nil {
		return ErrNilQuoteIDFunc
	}

	if c.quoteValueFunc == nil {
		return ErrNilQuoteValueFunc
	}

	return nil
}

// defaultQuoteID is the QuoteID default function for mysql-like databases.
// Inside backtick-quoted identifiers only the backtick is special (backslash is
// a literal character), so backticks are doubled and no other escaping is applied.
// The empty string is returned unquoted; every "."-separated segment is quoted
// independently, so a trailing "." (e.g. "a.") yields a trailing empty identifier.
func defaultQuoteID(s string) string {
	if s == "" {
		return s
	}

	parts := strings.Split(s, ".")

	for k, v := range parts {
		parts[k] = "`" + strings.ReplaceAll(v, "`", "``") + "`"
	}

	return strings.Join(parts, ".")
}

// defaultQuoteValue is the QuoteValue default function for mysql-like databases.
// The empty string is returned as an empty quoted literal (two single quotes)
// so the result is always a valid SQL string literal.
func defaultQuoteValue(s string) string {
	return "'" + escapeValue(s) + "'"
}

// escapeValue escapes the characters that are special inside a single-quoted
// MySQL string literal: embedded single quotes are doubled and control
// characters are backslash-escaped (\0, \n, \r, \\, \Z). This is done in a
// single pass; inputs with no special character are returned unchanged to avoid
// a needless allocation.
func escapeValue(s string) string {
	if !strings.ContainsAny(s, "'\x00\n\r\\\x1a") {
		return s
	}

	dest := make([]byte, 0, 2*len(s))

	for i := range len(s) {
		c := s[i]

		switch c {
		case 0:
			dest = append(dest, '\\', '0')
		case '\n':
			dest = append(dest, '\\', 'n')
		case '\r':
			dest = append(dest, '\\', 'r')
		case '\\':
			dest = append(dest, '\\', '\\')
		case '\032':
			dest = append(dest, '\\', 'Z')
		case '\'':
			dest = append(dest, '\'', '\'')
		default:
			dest = append(dest, c)
		}
	}

	return string(dest)
}
