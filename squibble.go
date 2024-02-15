// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Package squibble provides a schema migration assistant for SQLite databases.
//
// # Overview
//
// A Schema value manages the schema of a SQLite database that will be modified
// over time.  The current database schema is stored in the Current field, and
// migrations from previous versions are captured as UpdateRules.
//
// When the program starts up, it should pass the open database to the Apply
// method of the Schema. This verifies that the Schema is valid, then checks
// whether the database is up-to-date. If not, it applies any relevant update
// rules to bring it to the current state. If Apply fails, the database is
// rolled back.
//
// The Schema tracks schema versions by hashing the SQL text with SHA256, and
// it stores a record of upgrades in a _schema_history table that it maintains.
// Apply creates this table if it does not already exist, and updates it as
// update rules are applied.
//
// # Update Rules
//
// The Updates field of the Schema must contain an ordered list of update rules
// for all the versions of the schema prior to the Current one, from oldest to
// newest. Each rule has the hash of a previous schema version and a function
// that can be applied to the database to upgrade it to the next version in
// sequence.
//
// When revising the schema, you must add a new rule mapping the old (existing)
// schema to the new one. These rules are intended to be a permanent record of
// changes, and should be committed into source control as part of the
// program. As a consistency check, each rule must also declare the hash of the
// target schema it upgrades to.
//
// When Apply runs, it looks for the most recent version of the schema recorded
// in the _schema_history table. If there is none, and the database is
// otherwise empty, the current schema is assumed to be the initial version,
// and it is applied directly. Otherwise, Apply compares the hash of the most
// recent update to the current version: If they differ, it finds the most
// recent update hash in the Updates list, and applies all the updates from
// that point forward. If this succeeds, the current schema is recorded as the
// latest version in _schema_history.
//
// # Validation
//
// You use the Validate function to check that the current schema in the
// special sqlite_schema table maintained by SQLite matches a schema written as
// SQL text. If not, it reports a diff describing the differences between what
// the text wants and what the real schema has.
package squibble

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"
)

const historyTableName = "_schema_history"

// Schema defines a family of SQLite schema versions over time, expressed as a
// SQL definition of the current version of the schema, plus an ordered
// collection of upgrade rules that define how to update each version to the
// next.
type Schema struct {
	// Current is the SQL definition of the most current version of the schema.
	// It must not be empty.
	Current string

	// Updates is a sequence of schema update rules. The slice must contain an
	// entry for each schema version prior to the newest.
	Updates []UpdateRule

	// Logf is where logs should be sent; the default is log.Printf.
	Logf func(string, ...any)
}

// An UpdateRule defines a schema upgrade.
type UpdateRule struct {
	// Source is the hex-encoded SHA256 digest of the schema at which this
	// update applies. It must not be empty.
	Source string

	// Target is the hex-encoded SHA256 digest of the schema reached by applying
	// this update.  It must not be empty.
	Target string

	// Apply applies the necessary changes to update the schema to the next
	// version in sequence. It must not be nil.
	//
	// An apply function can use squibble.Logf(ctx, ...) to write log messages
	// to the logger defined by the associated Schema.
	Apply func(ctx context.Context, tx *sql.Tx) error
}

func (s *Schema) logf(msg string, args ...any) {
	if s == nil || s.Logf == nil {
		log.Printf(msg, args...)
	} else {
		s.Logf(msg, args...)
	}
}

type scLogKey struct{}

// Logf sends a log message to the logger attached to ctx, or to log.Printf if
// ctx does not have a logger attached. The context passed to the apply
// function of an UpdateRule will have this set to the logger for the Schema.
func Logf(ctx context.Context, msg string, args ...any) {
	s, _ := ctx.Value(scLogKey{}).(*Schema)
	s.logf(msg, args...)
}

// Apply applies any pending schema migrations to the given database.  It
// reports an error immediately if s is not consistent (per Check); otherwise
// it creates a new transaction and attempts to apply all applicable upgrades
// to db within it. If this succeeds and the transaction commits successfully,
// then Apply succeeds. Otherwise, the transaction is rolled back and Apply
// reports the reason wny.
func (s *Schema) Apply(ctx context.Context, db *sql.DB) error {
	if err := s.Check(); err != nil {
		return err
	}

	s.logf("Checking schema version...")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Stage 1: Create the schema versions table, if it does not exist.
	// TODO(creachadair): Plumb an option for the table name.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
  timestamp INTEGER UNIQUE NOT NULL,  -- Unix epoch microseconds.
  digest TEXT NOT NULL,               -- hex-coded SHA256 of schema text
  schema TEXT                         -- schema text (optional)
)`, historyTableName)); err != nil {
		return fmt.Errorf("create schema history: %w", err)
	}

	// Stage 2: Check whether the schema is up-to-date.
	curHash := SchemaHash(s.Current)
	hr, err := History(ctx, tx)
	if err != nil {
		return fmt.Errorf("reading update history: %w", err)
	} else if len(hr) == 0 {
		// Case 1: There is no schema present in the history table. If the
		// database is otherwise empty, apply the current one.
		if !schemaIsEssentiallyEmpty(ctx, tx, "main") {
			return errors.New("database has an unmanaged schema already")
		}

		s.logf("No schema is defined, applying initial schema %s", curHash)
		if _, err := tx.ExecContext(ctx, s.Current); err != nil {
			return fmt.Errorf("apply current schema: %w", err)
		}
		if err := s.addVersion(ctx, tx, HistoryRow{
			Timestamp: time.Now().UnixMicro(),
			Digest:    curHash,
			Schema:    s.Current,
		}); err != nil {
			return err
		}
		return tx.Commit()
	}

	// Case 2: The current schema is already up-to-date.
	latest := hr[len(hr)-1]
	if latest.Digest == curHash {
		s.logf("Schema is up-to-date at digest %s", curHash)
		return nil
	}

	// Stage 3: Apply pending upgrades.
	s.logf("Current schema digest is %s", curHash)
	s.logf("Latest DB schema digest is %s (%s)", latest.Digest, formatTime(latest.Timestamp))

	// N.B. It is possible that a given schema will repeat in the history.  In
	// that case, however, it doesn't matter which one we start from: All the
	// upgrades following ANY copy of that schema apply to all of them.  We
	// choose the last, just because it's less work if that happens.
	i := s.firstPendingUpdate(latest.Digest)
	if i < 0 {
		return fmt.Errorf("no update found for digest %s (this binary may be too old)", latest.Digest)
	}

	// Apply all the updates from the latest hash to the present.
	s.logf("Applying %d pending schema upgrades", len(s.Updates)-i)
	uctx := context.WithValue(ctx, scLogKey{}, s)
	for j, update := range s.Updates[i:] {
		if err := update.Apply(uctx, tx); err != nil {
			return fmt.Errorf("update failed at digest %s: %w", update.Source, err)
		}
		s.logf("[%d] updated to digest %s", i+j+1, update.Target)
	}
	// Now record that we made it to the front of the history.
	if err := s.addVersion(ctx, tx, HistoryRow{
		Timestamp: time.Now().UnixMicro(),
		Digest:    curHash,
		Schema:    s.Current,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("upgrades failed: %w", err)
	}
	s.logf("Schema successfully updated to digest %s", curHash)
	return nil
}

func (s *Schema) addVersion(ctx context.Context, tx *sql.Tx, version HistoryRow) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (timestamp, digest, schema) VALUES (?, ?, ?)`, historyTableName),
		version.Timestamp, version.Digest, version.Schema,
	)
	if err != nil {
		return fmt.Errorf("record schema %s: %w", version.Digest, err)
	}
	return nil
}

func (s *Schema) firstPendingUpdate(digest string) int {
	for i := len(s.Updates) - 1; i >= 0; i-- {
		if s.Updates[i].Source == digest {
			return i
		}
	}
	return -1
}

// Check reports an error if there are consistency problems with the schema
// definition that prevent it from being applied.
//
// A Schema is consistent if it has a non-empty Current schema text, all the
// update rules are correctly stitched (prev.Target == next.Source), and the
// last update rule in the sequence has the current schema as its target.
func (s *Schema) Check() error {
	if s.Current == "" {
		return errors.New("no current schema is defined")
	}
	var errs []error
	var last string
	for i, u := range s.Updates {
		if u.Source == "" {
			errs = append(errs, fmt.Errorf("upgrade %d: missing source", i+1))
		}
		if u.Target == "" {
			errs = append(errs, fmt.Errorf("upgrade %d: missing target", i+1))
		}
		if u.Apply == nil {
			errs = append(errs, fmt.Errorf("upgrade %d: missing Apply function", i+1))
		}

		if last != "" && u.Source != last {
			errs = append(errs, fmt.Errorf("upgrade %d: want source %s, got %s", i+1, last, u.Source))
		}
		last = u.Target
	}
	hc := SchemaHash(s.Current)
	if last != "" && last != hc {
		errs = append(errs, fmt.Errorf("missing upgrade to %s", hc))
	}
	return errors.Join(errs...)
}

// History reports the history of schema upgrades recorded by db in
// chronological order.
func History(ctx context.Context, db DBConn) ([]HistoryRow, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(
		`SELECT timestamp, digest, schema FROM %s ORDER BY timestamp`, historyTableName,
	))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		var cur HistoryRow
		if err := rows.Scan(&cur.Timestamp, &cur.Digest, &cur.Schema); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		out = append(out, cur)
	}
	return out, nil
}

// HistoryRow is a row in the schema history maintained by the Schema type.
type HistoryRow struct {
	Timestamp int64  // Unix epoch microseconds
	Digest    string // The digest of the schema at this update
	Schema    string // The SQL of the schema at this update
}

// SchemaHash computes a hex-encoded SHA256 digest of the specified text.
func SchemaHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func formatTime(us int64) string {
	return time.UnixMicro(us).UTC().Format(time.RFC3339Nano)
}
