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
	vdb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return fmt.Errorf("create validation db: %w", err)
	}
	defer vdb.Close()
	if _, err := vdb.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	main, err := readSchema(ctx, db, "main")
	if err != nil {
		return err
	}
	slices.SortFunc(main, compareSchemaRows)
	comp, err := readSchema(ctx, vdb, "main")
	if err != nil {
		return err
	}
	slices.SortFunc(comp, compareSchemaRows)

	if diff := gocmp.Diff(main, comp); diff != "" {
		return ValidationError{Diff: diff}
	}
	return nil
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
	TableName string         // affiliated table name (== Name for tables and views)
	SQL       sql.NullString // the text of the definition (maybe)
}

func compareSchemaRows(a, b schemaRow) int {
	if v := cmp.Compare(a.Type, b.Type); v != 0 {
		return v
	} else if v := cmp.Compare(a.Name, b.Name); v != 0 {
		return v
	} else if v := cmp.Compare(a.TableName, b.TableName); v != 0 {
		return v
	}
	return cmp.Compare(a.SQL.String, b.SQL.String)
}

// DBConn is the subset of the sql.DB interface needed by the functions defined
// in this package.
type DBConn interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

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
		var cur schemaRow
		if err := rows.Scan(&cur.Type, &cur.Name, &cur.TableName, &cur.SQL); err != nil {
			return nil, fmt.Errorf("scan %s schema: %w", root, err)
		} else if cur.TableName == "_schema_history" {
			continue // skip the history table and its indices
		}
		out = append(out, cur)
	}
	return out, nil
}

func schemaIsEssentiallyEmpty(ctx context.Context, db DBConn, root string) bool {
	sr, err := readSchema(ctx, db, root)
	if err != nil {
		return false
	}
	return len(sr) == 0 || (len(sr) == 1 && sr[0].Name == historyTableName)
}
