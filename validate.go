// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// Validate checks whether the current schema of db appears to match the
// specified schema, and reports an error if there are discrepancies.
// An error reported by Validate has concrete type ValidationError if
// the schemas differ.
func Validate(ctx context.Context, db DBConn, schema string) error {
	comp, err := schemaTextToRows(ctx, schema)
	if err != nil {
		return err
	}
	main, err := readSchema(ctx, db, "main")
	if err != nil {
		return err
	}
	if diff := diffSchema(main, comp); diff != "" {
		return ValidationError{Diff: diff}
	}
	return nil
}

func schemaTextToRows(ctx context.Context, schema string) ([]schemaRow, error) {
	vdb, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return nil, fmt.Errorf("create validation db: %w", err)
	}
	defer vdb.Close()
	tx, err := vdb.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return readSchema(ctx, tx, "main")
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
	TableName string      // affiliated table name (== Name for tables and views)
	Columns   []schemaCol // for tables, the columns
	SQL       string      // the text of the definition (maybe)
}

type mapKey struct {
	Type, Name string
}

func (s schemaRow) mapKey() mapKey { return mapKey{s.Type, s.Name} }

type schemaCol struct {
	Name       string // column name
	Type       string // type description
	NotNull    bool   // whether the column is marked NOT NULL
	Default    any    // the default value
	PrimaryKey bool   // whether this column is part of the primary key
	Hidden     int    // 0=normal, 1=hidden, 2=generated virtual, 3=generated stored
}

func (c schemaCol) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%q %s", c.Name, c.Type)
	if c.NotNull {
		fmt.Fprint(&sb, " not null")
	} else {
		fmt.Fprint(&sb, " null")
	}
	if c.Default != nil {
		fmt.Fprintf(&sb, " default=%v", c.Default)
	}
	if c.PrimaryKey {
		fmt.Fprint(&sb, " primary key")
	}
	return sb.String()
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

func compareSchemaCols(a, b schemaCol) int {
	if v := cmp.Compare(a.Type, b.Type); v != 0 {
		return v
	} else if v := cmp.Compare(a.Name, b.Name); v != 0 {
		return v
	}
	return cmp.Compare(
		fmt.Sprintf("%v %v %v %d", a.NotNull, a.PrimaryKey, a.Default != nil, a.Hidden),
		fmt.Sprintf("%v %v %v %d", b.NotNull, b.PrimaryKey, b.Default != nil, b.Hidden),
	)
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
		} else if tblName == "_schema_history" || tblName == "sqlite_sequence" {
			continue // skip the history and sequence tables and their indices
		} else if strings.HasPrefix(name, "sqlite_autoindex_") {
			continue // skip auto-generates SQLite indices
		}
		out = append(out, schemaRow{Type: rtype, Name: name, TableName: tblName, SQL: sql.String})

		// For tables: Read out the column information.
		if rtype == "table" {
			cols, err := readColumns(ctx, db, root, name)
			if err != nil {
				return nil, err
			}
			out[len(out)-1].Columns = cols
		}
	}
	slices.SortFunc(out, compareSchemaRows)
	return out, nil
}

// readColumns reads the schema metadata for the columns of the specified table.
func readColumns(ctx context.Context, db DBConn, root, table string) ([]schemaCol, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA %s.table_xinfo('%s')`, root, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []schemaCol
	for rows.Next() {
		var idIgnored, notNull, isPK, hidden int
		var name, ctype string
		var defValue any

		if err := rows.Scan(&idIgnored, &name, &ctype, &notNull, &defValue, &isPK, &hidden); err != nil {
			return nil, fmt.Errorf("scan %s columns: %w", table, err)
		}
		out = append(out, schemaCol{
			Name:       name,
			Type:       strings.ToUpper(ctype), // normalize
			NotNull:    notNull != 0,
			Default:    defValue,
			PrimaryKey: isPK != 0,
			Hidden:     hidden,
		})
	}
	slices.SortFunc(out, compareSchemaCols)
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
