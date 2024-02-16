// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package squibble_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func mustHash(t *testing.T, text string) string {
	t.Helper()
	h, err := squibble.SQLDigest(text)
	if err != nil {
		t.Fatalf("SchemaHash failed: %v", err)
	}
	return h
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
				{mustHash(t, v1), mustHash(t, v2),
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
				{mustHash(t, v1), mustHash(t, v2),
					squibble.Exec(`ALTER TABLE foo ADD COLUMN y text`)},
				{mustHash(t, v2), mustHash(t, v3),
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
			t.Logf("[%d] %s %s %q", i+1, h.Timestamp.Format(time.RFC3339Nano), h.Digest, h.Schema)
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
	const v6 = `-- restore the table
create table foo (x text, y text);
create table bar (z integer not null)`
	const v7 = `-- same, same
create table foo (x text, y text);
create table bar (z integer not null)`

	t.Run("InitV1", func(t *testing.T) {
		s := &squibble.Schema{Current: v1, Logf: t.Logf}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Fatalf("Apply v1: unexpected error: %v", err)
		}
		checkTableSchema(t, db, "foo", `create table foo (x text)`)
	})

	t.Run("V2toV7", func(t *testing.T) {
		s := &squibble.Schema{
			Current: v7,
			Updates: []squibble.UpdateRule{
				// History: v1 → v2 → v3 → (v4 = v3) → v5 → (v6 = v3) → (v7 = v3)
				// The cycle exercises the correct handling of repeats.
				{mustHash(t, v1), mustHash(t, v2),
					squibble.Exec(`ALTER TABLE foo ADD COLUMN y text`)},
				{mustHash(t, v2), mustHash(t, v3),
					squibble.Exec(`CREATE TABLE bar (z integer not null)`)},
				{mustHash(t, v3), mustHash(t, v4),
					squibble.NoAction},
				{mustHash(t, v4), mustHash(t, v5),
					squibble.Exec(`DROP TABLE foo`)},
				{mustHash(t, v5), mustHash(t, v6),
					squibble.Exec(`CREATE TABLE foo (x text, y text)`)},
				{mustHash(t, v6), mustHash(t, v7),
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
		t.Log("History of upgrades (chronological):")
		for i, h := range hr {
			t.Logf("[%d] %s %s %q", i+1, h.Timestamp.Format(time.RFC3339Nano), h.Digest, h.Schema)
		}
	})

	t.Run("Validate", func(t *testing.T) {
		if err := squibble.Validate(context.Background(), db, v7); err != nil {
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

func TestBadUpgrade(t *testing.T) {
	db := mustOpenDB(t)

	const v1 = `create table foo (x text not null)`
	const v2 = `create table foo (x text not null, y integer not null default 0)`

	// Initialize the database with schema v1.
	s := &squibble.Schema{Current: v1, Logf: t.Logf}
	if err := s.Apply(context.Background(), db); err != nil {
		t.Fatalf("Apply initial schema: %v", err)
	}

	// Now target an upgrade to schema v2, but in which the upgrade rule does
	// not produce a result equivalent to v2.
	s.Current = v2
	s.Updates = append(s.Updates, squibble.UpdateRule{
		Source: mustHash(t, v1),
		Target: mustHash(t, v2),
		Apply: squibble.Exec(`
           ALTER TABLE foo ADD COLUMN y INTEGER NOT NULL DEFAULT 0;  -- OK
           ALTER TABLE foo ADD COLUMN z BLOB;  -- not expected
      `),
	})
	if err := s.Apply(context.Background(), db); err == nil {
		t.Error("Apply should have failed, but did not")
	} else {
		t.Logf("Apply: got expected error: %v", err)
	}
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
	tmp := func(context.Context, squibble.DBConn) error { panic("notused") }
	bad1 := &squibble.Schema{
		Current: "create table ok (a text)",
		Updates: []squibble.UpdateRule{
			{"", "def", tmp},    // missing source
			{"abc", "", tmp},    // missing target
			{"abc", "def", nil}, // missing func
		},
		Logf: t.Logf,
	}
	bad2 := &squibble.Schema{
		Current: "create table ok (a text)",
		Updates: []squibble.UpdateRule{
			{"abc", "def", tmp},
			{"ghi", "jkl", tmp}, // missing link from def to ghi
			{"jkl", "mno", tmp}, // missing link to current
		},
		Logf: t.Logf,
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
		{"NoTail", bad2, fmt.Sprintf("missing upgrade from %s to %s", "mno",
			mustHash(t, bad2.Current))},
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

func TestCompatible(t *testing.T) {
	const schema = `create table t (a text); create table u (b integer)`

	t.Run("Empty", func(t *testing.T) {
		db := mustOpenDB(t)

		s := &squibble.Schema{Current: schema, Logf: t.Logf}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Errorf("Apply: unexpected error: %v", err)
		}
		if err := squibble.Validate(context.Background(), db, schema); err != nil {
			t.Errorf("Validate: unexpected error: %v", err)
		}
	})
	t.Run("NonEmpty", func(t *testing.T) {
		db := mustOpenDB(t)

		if _, err := db.Exec(schema); err != nil {
			t.Fatalf("Initializing schema: %v", err)
		}

		s := &squibble.Schema{Current: "-- compatible schema\n" + schema, Logf: t.Logf}
		if err := s.Apply(context.Background(), db); err != nil {
			t.Errorf("Apply: unexpected error: %v", err)
		}
		if err := squibble.Validate(context.Background(), db, schema); err != nil {
			t.Errorf("Validate: unexpected error: %v", err)
		}
	})
}
