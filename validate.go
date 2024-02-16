// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"

	gocmp "github.com/google/go-cmp/cmp"
)

// Validate checks whether the current schema of db appears to match the
// specified schema, and reports an error if there are discrepancies.
// An error reported by Validate has concrete type ValidationError.
func Validate(ctx context.Context, db DBConn, schema string) error {
	comp, err := schemaTextToRows(ctx, db, schema)
	if err != nil {
		return err
	}
	main, err := readSchema(ctx, db, "main")
	if err != nil {
		return err
	}
	if diff := gocmp.Diff(main, comp); diff != "" {
		return ValidationError{Diff: diff}
	}
	return nil
}

func schemaTextToRows(ctx context.Context, db DBConn, schema string) ([]schemaRow, error) {
	vdb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("create validation db: %w", err)
	}
	defer vdb.Close()
	if _, err := vdb.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return readSchema(ctx, vdb, "main")
}

// ValidationError is the concrete type of errors reported by the Validate
// function.
type ValidationError struct {
	// Diff is a human readable summary of the difference between what was in
	// the database (-lhs) and the expected schema (+rhs).
	Diff string
}

func (v ValidationError) Error() string {
	return fmt.Sprintf("invalid schema (-got, +want):\n%s", v.Diff)
}

type schemaRow struct {
	Type      string // e.g., "index", "table", "trigger", "view"
	Name      string
	TableName string // affiliated table name (== Name for tables and views)
	SQL       string // the text of the definition (maybe)
}

func compareSchemaRows(a, b schemaRow) int {
	if v := cmp.Compare(a.Type, b.Type); v != 0 {
		return v
	} else if v := cmp.Compare(a.Name, b.Name); v != 0 {
		return v
	} else if v := cmp.Compare(a.TableName, b.TableName); v != 0 {
		return v
	}
	return cmp.Compare(a.SQL, b.SQL)
}

// DBConn is the subset of the sql.DB interface needed by the functions defined
// in this package.
type DBConn interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// readSchema reads the schema for the specified database and returns the
// resulting rows sorted into a stable order. Rows belonging to the history
// table and any affiliated indices are filtered out.
func readSchema(ctx context.Context, db DBConn, root string) ([]schemaRow, error) {
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf(`SELECT type, name, tbl_name, sql FROM %s.sqlite_schema`, root),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []schemaRow
	for rows.Next() {
		var rtype, name, tblName string
		var sql sql.NullString
		if err := rows.Scan(&rtype, &name, &tblName, &sql); err != nil {
			return nil, fmt.Errorf("scan %s schema: %w", root, err)
		} else if tblName == "_schema_history" {
			continue // skip the history table and its indices
		}
		out = append(out, schemaRow{Type: rtype, Name: name, TableName: tblName, SQL: sql.String})
	}
	slices.SortFunc(out, compareSchemaRows)
	return out, nil
}

// schemaIsEmpty reports whether the schema for the specified database is
// essentially empty (meaning, it is either empty or contains only a history
// table).
func schemaIsEmpty(ctx context.Context, db DBConn, root string) bool {
	main, err := readSchema(ctx, db, root)
	if err != nil {
		return false
	}
	return len(main) == 0 || (len(main) == 1 && main[0].Name == historyTableName)
}
