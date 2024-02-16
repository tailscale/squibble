// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Program squibble is a command-line utility for managing SQLite schemas.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/creachadair/command"
	"github.com/creachadair/flax"
	"github.com/tailscale/squibble"

	_ "modernc.org/sqlite"
)

func main() {
	root := &command.C{
		Name: filepath.Base(os.Args[0]),
		Help: `A utility for managing SQLite schema updates.`,

		Commands: []*command.C{
			{
				Name:  "diff",
				Usage: "<db-path> <schema-path>",
				Help:  `Compute the schema diff between a SQLite database and a SQL schema.`,
				Run:   command.Adapt(runDiff),
			},
			{
				Name:  "digest",
				Usage: "<path>",
				Help: `Compute the schema digest for a database or SQL definition.

By default, the input is treated as SQL text if path ends in .sql, otherwise it
must be a SQLite database. Use --sql to explicitly specify SQL input.

The output has the form:

   db:  <hex>  -- if the input was a SQLite database
   sql: <hex>  -- if the input was a SQL schema file
`,
				SetFlags: command.Flags(flax.MustBind, &digestFlags),
				Run:      command.Adapt(runDigest),
			},
			{
				Name:     "history",
				Usage:    "<db-path>",
				Help:     `Print the schema history for a SQLite database.`,
				SetFlags: command.Flags(flax.MustBind, &historyFlags),
				Run:      command.Adapt(runHistory),
			},
			command.HelpCommand(nil),
			command.VersionCommand(),
		},
	}
	env := root.NewEnv(nil).MergeFlags(true)
	command.RunOrFail(env, os.Args[1:])
}

var digestFlags struct {
	SQL bool `flag:"sql,Treat input as SQL text"`
}

func runDiff(env *command.Env, dbPath, sqlPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	sql, err := os.ReadFile(sqlPath)
	if err != nil {
		return err
	}
	dbHash, err := squibble.DBDigest(env.Context(), db, "main")
	if err != nil {
		return err
	}
	sqlHash, err := squibble.SQLDigest(string(sql))
	if err != nil {
		return err
	}
	fmt.Println("db: ", dbHash)
	fmt.Println("sql:", sqlHash)
	if err := squibble.Validate(env.Context(), db, string(sql)); err != nil {
		fmt.Println(err.(squibble.ValidationError).Diff)
		return errors.New("schema differs")
	}
	return nil
}

func runDigest(env *command.Env, path string) error {
	if digestFlags.SQL || filepath.Ext(path) == ".sql" {
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hash, err := squibble.SQLDigest(string(text))
		if err != nil {
			return err
		}
		fmt.Println("sql:", hash)
		return nil
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	hash, err := squibble.DBDigest(env.Context(), db, "main")
	if err != nil {
		return err
	}
	fmt.Println("db: ", hash)
	return nil
}

var historyFlags struct {
	JSON bool `flag:"json,Write history records as JSON"`
}

func runHistory(env *command.Env, dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	hr, err := squibble.History(env.Context(), db)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	for _, h := range hr {
		if historyFlags.JSON {
			enc.Encode(h)
		} else {
			fmt.Printf("%s\t%s\t[%d bytes]\n", h.Timestamp.Format(time.RFC3339), h.Digest, len(h.Schema))
		}
	}
	return nil
}
