// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Program squibble is a command-line utility for managing SQLite schemas.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

   db <hex>   -- if the input was a SQLite database
   sql <hex>  -- if the input was a SQL schema file
`,
				SetFlags: command.Flags(flax.MustBind, &digestFlags),
				Run:      command.Adapt(runDigest),
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
	if err := squibble.Validate(context.Background(), db, string(sql)); err != nil {
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
		fmt.Println("sql", hash)
		return nil
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	hash, err := squibble.DBDigest(context.Background(), db, "main")
	if err != nil {
		return err
	}
	fmt.Println("db", hash)
	return nil
}
