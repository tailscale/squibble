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
   source text in this format. For example:

   ```go
   {
       Source: "8d4f9b3e29aeca09e891460bf5ed08f12b84f6887b46a61082c339d49d7e0be8",
       Target: "b196954e613b770a4a1c0a07b96f6e03cb86923a226c2b53bd523fb759fef3d6",
       Apply: func(ctx context.Context, db squibble.DBConn) error {
           /* Schema diff:

           >> Modify table "Templates"
            ! replace column "raw" BLOB
              with "raw" BLOB not null
            + add column "count" INTEGER not null default=0

           >> Add table "lard"
            + CREATE TABLE lard (z integer, s text unique)

           */
           panic("not implemented")
       },
   },
   ```

   You will still need to fill in the update rule implementation, but a
   human-readable summary of the changes will be included as a comment to make
   it easier to figure out what to write.  As shown in the example above, the
   `squibble.Exec` function can be helpful for simple changes.

   You should delete the comment before merging the rule, for legibility.

## Mixing Migration and In-Place Updates

Some schema changes can be done "in-place", simply by re-applying the schema
without any other migration steps. Typical examples include the addition or
removal of whole tables, views, indexes, or triggers, which can be applied
conditionally with statements like:

```sql
CREATE TABLE IF NOT EXISTS Foo ( ... )

DROP VIEW IF EXISTS Bar;
```

I generally recommend you _not_ combine this style of update with use of the
schema migrator. It works fine to do so, but adds extra friction.

If you do want to manage schema changes this way, you should apply the updated
schema _before_ calling the `Apply` method of the `squibble.Schema`.  If the
new schema has changes that are not compatible with the known migration state,
the `Apply` method will report an error, and you can add an appropriate
migration step.

For example, suppose you have this schema:

```sql
-- Schema 1
CREATE TABLE IF NOT EXISTS Foo (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL
);
```

After executing Schema 1, the migrator will be satisfied: The schema before
migration already looks like Schema 1, so there is nothing to do.

Now say you add a new column:

```sql
-- Schema 2
CREATE TABLE IF NOT EXISTS Foo (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  important BOOL   -- new column
);
```

When executing Schema 2, the database does not change: Table `Foo` already
exists, so SQLite does not do anything. But the migrator sees that the schema
has changed and it doesn't have a migration rule, so you will have to add one:

```go
Updates: []squibble.UpdateRule{{
   Source: "7e4799f89f03e9913d309f50c4cc70963fc5607fb335aa318f9c246fdd336488",
   Target: "dee76ad0f980b8a5b419c4269559576d8413666adfe4a882e77f17b5792cca01",
   Apply:  squibble.Exec(`ALTER TABLE Foo ADD COLUMN important BOOL`),
}}
```

and the migrator will be happy. Now say you add a new table:

```sql
-- Schema 3
CREATE TABLE IF NOT EXISTS Foo (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  important BOOL   -- added in schema 2
);

CREATE TABLE IF NOT EXISTS Bar (comment TEXT NOT NULL);
```

This executes just fine, but now the state of the database seen by the migrator
is different from the last state it has an update for: It has no migration rule
to go from dee76ad0f980b8a5b419c4269559576d8413666adfe4a882e77f17b5792cca01 to
30233f4462f18d591795b1f8b455a5daf3b19c8786e90ec94daf8d3825de0320, which is the
state of the database after Schema 3 was applied.

The migrator needs a rule for this, but the rule can be a no-op:

```sql
Updates: []squibble.UpdateRule{{
   Source: "7e4799f89f03e9913d309f50c4cc70963fc5607fb335aa318f9c246fdd336488",
   Target: "dee76ad0f980b8a5b419c4269559576d8413666adfe4a882e77f17b5792cca01",
   Apply:  squibble.Exec(`ALTER TABLE Foo ADD COLUMN important BOOL`),
}, {
   // This rule tells the migrator how to get to the current state, but
   // the change was already handled by the schema directly.
   Source: "dee76ad0f980b8a5b419c4269559576d8413666adfe4a882e77f17b5792cca01",
   Target: "30233f4462f18d591795b1f8b455a5daf3b19c8786e90ec94daf8d3825de0320",
   Apply:  squibble.NoAction, // does nothing, just marks an update
}}
```
