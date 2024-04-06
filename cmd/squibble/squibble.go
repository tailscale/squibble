// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Program squibble is a command-line utility for managing SQLite schemas.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creachadair/command"
	"github.com/creachadair/flax"
	"github.com/creachadair/mds/slice"
	"github.com/tailscale/squibble"

	_ "modernc.org/sqlite"
)

func main() {
	root := &command.C{
		Name: filepath.Base(os.Args[0]),
		Help: `A utility for managing SQLite schema updates.`,

		Commands: []*command.C{
			{
				Name:     "diff",
				Usage:    "<db-path> <schema-path>",
				Help:     `Compute the schema diff between a SQLite database and a SQL schema.`,
				SetFlags: command.Flags(flax.MustBind, &diffFlags),
				Run:      command.Adapt(runDiff),
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
				Usage:    "<db-path> [<digest>/latest]",
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

var diffFlags struct {
	Rule bool `flag:"rule,Render the diff as a rule template"`
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
	dbHash, err := squibble.DBDigest(env.Context(), db)
	if err != nil {
		return err
	}
	sqlHash, err := squibble.SQLDigest(string(sql))
	if err != nil {
		return err
	}
	verr := squibble.Validate(env.Context(), db, string(sql))

	// Case 1: We are asked to print an update rule template.  In this case, it
	// is an error if there is NO difference, since a template doesn't make
	// sense in that case.
	if diffFlags.Rule {
		if verr == nil {
			return fmt.Errorf("schema is identical (digest %s)", dbHash)
		}

		// Render the diff digests as Go source.
		//
		// To make the Go formatter work, we need a valid top-level declaration.
		// here we use a variable declaration, with a stylized form that we can
		// recognize and trip back off afterward.
		var buf bytes.Buffer
		const prefix = "var _ = XX"
		fmt.Fprintf(&buf, `%[1]s{
        Source: %[2]q,
        Target: %[3]q,
        Apply: func(ctx context.Context, db squibble.DBConn) error {
          /* Schema diff:
%[4]s
           */
          panic("not implemented")
        },
      }`, prefix, dbHash, sqlHash, verr.(squibble.ValidationError).Diff)

		// If this fails, it probably means the code above is wrong.
		src, err := format.Source(buf.Bytes())
		if err != nil {
			return fmt.Errorf("format source template: %w", err)
		}
		// N.B. Add a trailing comma so it can be spliced into a slice literal.
		fmt.Println(strings.TrimPrefix(string(src)+",", prefix))
		return nil
	}

	// Case 2: We are asked to print a diff.
	fmt.Println("db: ", dbHash)
	fmt.Println("sql:", sqlHash)
	if verr != nil {
		fmt.Println(verr.(squibble.ValidationError).Diff)
		return errors.New("schema differs")
	}
	return nil
}

var digestFlags struct {
	SQL bool `flag:"sql,Treat input as SQL text"`
}

func runDigest(env *command.Env, path string) error {
	kind, digest, err := loadDigest(env.Context(), path, digestFlags.SQL)
	if err != nil {
		return fmt.Errorf("compute %s digest: %w", kind, err)
	}
	fmt.Printf("%s: %s\n", kind, digest)
	return nil
}

var historyFlags struct {
	JSON bool `flag:"json,Write history records as JSON"`
}

func runHistory(env *command.Env, dbPath string, digest ...string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	hr, err := squibble.History(env.Context(), db)
	if err != nil {
		return err
	}
	if len(digest) != 0 {
		if digest[0] == "latest" {
			hr = hr[len(hr)-1:]
		} else {
			hr = slice.Partition(hr, func(r squibble.HistoryRow) bool {
				for _, d := range digest {
					if strings.HasPrefix(r.Digest, d) {
						return true
					}
				}
				return false
			})
		}
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

func loadDigest(ctx context.Context, path string, forceSQL bool) (kind, digest string, _ error) {
	if !forceSQL && filepath.Ext(path) != ".sql" {
		if _, err := os.Stat(path); err == nil {
			db, err := sql.Open("sqlite", path)
			if err == nil {
				defer db.Close()
				d, err := squibble.DBDigest(ctx, db)
				return "db", d, err
			}
		}
		// fallthrough
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return "sql", "", err
	}
	d, err := squibble.SQLDigest(string(text))
	return "sql", d, err
}
