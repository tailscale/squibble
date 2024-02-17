# squibble

Package squibble provides a schema migration assistant for SQLite databases.

[![GoDoc](https://img.shields.io/static/v1?label=godoc&message=reference&color=white)](https://pkg.go.dev/github.com/tailscale/squibble)
[![CI](https://github.com/tailscale/squibble/actions/workflows/go-presubmit.yml/badge.svg?event=push&branch=main)](https://github.com/tailscale/squibble/actions/workflows/go-presubmit.yml)

A `Schema` value manages the schema of a SQLite database that will be modified
over time.  The current database schema is stored in the Current field, and
migrations from previous versions are captured as `UpdateRules`.

## Example

```go
//go:embed schema.sql
var dbSchema string

var schema = &squibble.Schema{
	Current: dbSchema,

	Updates: []squibble.UpdateRule{
		// Each update gives the digests of the source and target schemas,
		// and a function to modify the first into the second.
		// The digests act as a version marker.
		{"a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447",
			"727e2659ac457a3c86da2203ebd2e7387767ffe9a93501def5a87034ee672750",
			squibble.Exec(`CREATE TABLE foo (bar TEXT)`),
		},
		// The last update must end with the current schema.
		// Note that multiple changes are permitted in a rule.
		{"727e2659ac457a3c86da2203ebd2e7387767ffe9a93501def5a87034ee672750",
			"f18496b875133e09906a26ba23ef0e5f4085c1507dc3efee9af619759cb0fafe",
			squibble.Exec(
				`ALTER TABLE foo ADD COLUMN baz INTEGER NOT NULL`,
				`DROP VIEW quux`,
			),
		},
	},
}

func main() {
   flag.Parse()

   // Open the database as usual.
   db, err := sql.Open("sqlite", "test.db")
   if err != nil {
      log.Fatalf("Open db: %v", err)
   }

   // Apply any schema migrations needed.
   if err := schema.Apply(context.Background(), db); err != nil {
      log.Fatalf("Apply schema: %v", err)
   }

   // ...how you do
}
```

## Usage Outline

For the following, assume your schema is defined in a file `schema.sql` and the
current database is `data.db`.

1. Modify `schema.sql` to look like the schema you want the database to end up
   with.

2. Run `squibble diff data.db schema.sql`. This will print out the difference
   between the database schema and the update, including the computed digests.

   ```
   db:  b9062f812474223063c121d058e23823bf750074d1eba26605bbebbc9fd20dbe
   sql: 76a0ed44d8ad976d1de83bcb67d549dee2ab5bfb5af7d597d2548119e7359455
   < human-readable-ish diff >
   ```

3. Using these digests, a new rule to the end of the `Upgrades` list like:

   ```go
   {
     Source: "b9062f812474223063c121d058e23823bf750074d1eba26605bbebbc9fd20dbe",  // from the db
     Target: "76a0ed44d8ad976d1de83bcb67d549dee2ab5bfb5af7d597d2548119e7359455",  // from the schema
     Apply: squibble.Exec(`
        ALTER TABLE foo ADD COLUMN bar TEXT UNIQUE NOT NULL DEFAULT 'xyzzy';
        DROP VIEW IF EXISTS fuzzypants;
        CREATE INDEX horse_index ON animal (species) WHERE (species = 'horse');
     `),
   }
   ```

   Use `squibble diff --rule data.db schema.sql` to generate a copyable Go
   source text in this format. You will need to fill in the update rule, but
   The human-readable diff will be included as a comment to make it easier to
   figure out what to write. You should delete the comment before merging the
   rule, for legibility.
