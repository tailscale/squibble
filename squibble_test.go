package squibble_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/squibble"
	_ "modernc.org/sqlite"
)

func mustOpenDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file://"+path)
	if err != nil {
		t.Fatalf("Open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustTableSchema(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	var schema string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_schema WHERE name = ? AND type = 'table'`, table,
	).Scan(&schema)
	if err != nil {
		t.Fatalf("Read schema for table %q: %v", table, err)
	}
	return schema
}

func checkTableSchema(t *testing.T, db *sql.DB, table, want string) {
	t.Helper()
	if got := mustTableSchema(t, db, table); !strings.EqualFold(got, want) {
		t.Fatalf("Schema for table %q: got %q, want %q", table, got, want)
	}
}

func TestEmptySchema(t *testing.T) {
	db := mustOpenDB(t)

	invalid := new(squibble.Schema)
	if err := invalid.Apply(context.Background(), db); err == nil {
		t.Error("Apply should have failed, but did not")
	}
}

func TestInitSchema(t *testing.T) {
	db := mustOpenDB(t)
	const schema = `create table foo (x text)`

	s := &squibble.Schema{Current: schema, Logf: t.Logf}
	if err := s.Apply(context.Background(), db); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	checkTableSchema(t, db, "foo", schema)
}

func TestUpgrade(t *testing.T) {
	db := mustOpenDB(t)

	const v1 = `create table foo (x text)`
	const v2 = `create table foo (x text, y text)`
	const v3 = `create table foo (x text, y text); create table bar (z integer not null)`

	t.Run("InitV1", func(t *testing.T) {
		s := &squibble.Schema{Current: v1, Logf: t.Logf}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Fatalf("Apply v1: unexpected error: %v", err)
		}
		checkTableSchema(t, db, "foo", v1)
	})

	t.Run("V1toV2", func(t *testing.T) {
		s := &squibble.Schema{
			Current: v2,
			Updates: []squibble.UpdateRule{
				{squibble.SchemaHash(v1), squibble.SchemaHash(v2),
					squibble.Exec(`ALTER TABLE foo ADD COLUMN y text`)},
			},
			Logf: t.Logf,
		}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Fatalf("Apply v2: unexpected error: %v", err)
		}
		checkTableSchema(t, db, "foo", v2)
	})

	t.Run("V2toV3", func(t *testing.T) {
		s := &squibble.Schema{
			Current: v3,
			Updates: []squibble.UpdateRule{
				{squibble.SchemaHash(v1), squibble.SchemaHash(v2),
					squibble.Exec(`ALTER TABLE foo ADD COLUMN y text`)},
				{squibble.SchemaHash(v2), squibble.SchemaHash(v3),
					squibble.Exec(`CREATE TABLE bar (z integer not null)`)},
			},
			Logf: t.Logf,
		}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Fatalf("Apply v3: unexpected error: %v", err)
		}
		checkTableSchema(t, db, "foo", v2)
		checkTableSchema(t, db, "bar", `create table bar (z integer not null)`)
	})

	t.Run("History", func(t *testing.T) {
		hr, err := squibble.History(context.Background(), db)
		if err != nil {
			t.Fatalf("History: unexpected error: %v", err)
		}
		t.Log("History of upgrades (chronological):")
		for i, h := range hr {
			t.Logf("[%d] %s %s %q", i+1, time.UnixMicro(h.Timestamp).UTC().Format(time.RFC3339Nano),
				h.Digest, h.Schema)
		}
	})
}

func TestMultiUpgrade(t *testing.T) {
	db := mustOpenDB(t)

	const v1 = `-- initial schema
create table foo (x text)`
	const v2 = `-- add a column
create table foo (x text, y text)`
	const v3 = `-- add a table
create table foo (x text, y text);
create table bar (z integer not null)`
	const v4 = `-- don't change anything but the comments
create table foo (x text, y text);
create table bar (z integer not null)`
	const v5 = `-- drop a table
create table bar (z integer not null)`

	t.Run("InitV1", func(t *testing.T) {
		s := &squibble.Schema{Current: v1, Logf: t.Logf}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Fatalf("Apply v1: unexpected error: %v", err)
		}
		checkTableSchema(t, db, "foo", `create table foo (x text)`)
	})

	t.Run("V2toV5", func(t *testing.T) {
		s := &squibble.Schema{
			Current: v5,
			Updates: []squibble.UpdateRule{
				// History: v1 → v2 → v3 → v4 → v3 → v4 → v5
				// The cycle exercises the correct handling of repeats.
				{squibble.SchemaHash(v1), squibble.SchemaHash(v2),
					squibble.Exec(`ALTER TABLE foo ADD COLUMN y text`)},
				{squibble.SchemaHash(v2), squibble.SchemaHash(v3),
					squibble.Exec(`CREATE TABLE bar (z integer not null)`)},
				{squibble.SchemaHash(v3), squibble.SchemaHash(v4),
					squibble.Exec(`DROP TABLE foo`)}, // occurrence 1
				{squibble.SchemaHash(v4), squibble.SchemaHash(v3),
					squibble.Exec(`CREATE TABLE foo (x text, y text)`)},
				{squibble.SchemaHash(v3), squibble.SchemaHash(v4),
					squibble.Exec(`DROP TABLE foo`)}, // occurrence 2
				{squibble.SchemaHash(v4), squibble.SchemaHash(v5),
					squibble.NoAction},
			},
			Logf: t.Logf,
		}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Fatalf("Apply v3: unexpected error: %v", err)
		}
		checkTableSchema(t, db, "bar", `create table bar (z integer not null)`)
	})

	t.Run("History", func(t *testing.T) {
		hr, err := squibble.History(context.Background(), db)
		if err != nil {
			t.Fatalf("History: unexpected error: %v", err)
		}
		t.Log("History of upgrades (reverse chronological):")
		for i, h := range hr {
			t.Logf("[%d] %s %s %q", i+1, time.UnixMicro(h.Timestamp).UTC().Format(time.RFC3339Nano),
				h.Digest, h.Schema)
		}
	})

	t.Run("Validate", func(t *testing.T) {
		if err := squibble.Validate(context.Background(), db, v5); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Invalidate", func(t *testing.T) {
		err := squibble.Validate(context.Background(), db, v1)
		var ve squibble.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("Validate: got %v, want %T", err, ve)
		}
		t.Logf("OK, validation diff:\n%s", ve.Diff)
	})
}

func TestUnmanaged(t *testing.T) {
	db := mustOpenDB(t)

	// If the database already has a schema that isn't managed by the Schema,
	// Apply should report an error.
	if _, err := db.Exec(`create table t (a string)`); err != nil {
		t.Fatalf("Initialize schema: %v", err)
	}

	s := &squibble.Schema{Current: `create table u (b integer)`, Logf: t.Logf}
	if err := s.Apply(context.Background(), db); err == nil {
		t.Error("Apply should have failed but did not")
	} else if !strings.Contains(err.Error(), "unmanaged schema") {
		t.Errorf("Apply: got %v, want unmanaged schema", err)
	}
}

func TestInconsistent(t *testing.T) {
	tmp := func(context.Context, *sql.Tx) error { panic("notused") }
	bad1 := &squibble.Schema{
		Current: "ok",
		Updates: []squibble.UpdateRule{
			{"", "def", tmp},    // missing source
			{"abc", "", tmp},    // missing target
			{"abc", "def", nil}, // missing func
		},
	}
	bad2 := &squibble.Schema{
		Current: "ok",
		Updates: []squibble.UpdateRule{
			{"abc", "def", tmp},
			{"ghi", "jkl", tmp}, // missing link from def to ghi
			{"jkl", "mno", tmp}, // missing link to current
		},
	}
	tests := []struct {
		name  string
		input *squibble.Schema
		want  string
	}{
		{"NoCurrent", &squibble.Schema{}, "no current schema"},
		{"NoSource", bad1, "1: missing source"},
		{"NoTarget", bad1, "2: missing target"},
		{"NoFunc", bad1, "3: missing Apply function"},
		{"BadStitch", bad2, "2: want source def, got ghi"},
		{"NoTail", bad2, "missing upgrade to " + squibble.SchemaHash(bad2.Current)},
	}
	db := mustOpenDB(t)
	ctx := context.Background()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Apply(ctx, db)
			if err == nil {
				t.Fatal("Apply should have failed but did not")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Got error %v, want %q", err, tc.want)
			}
		})
	}
}
