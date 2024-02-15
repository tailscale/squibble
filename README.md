# squibble

Package squibble provides a schema migration assistant for SQLite databases.

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
		{"a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447",
			"727e2659ac457a3c86da2203ebd2e7387767ffe9a93501def5a87034ee672750",
			squibble.Exec(`CREATE TABLE foo (bar TEXT)`),
		},
		{"727e2659ac457a3c86da2203ebd2e7387767ffe9a93501def5a87034ee672750",
			"f18496b875133e09906a26ba23ef0e5f4085c1507dc3efee9af619759cb0fafe",
			squibble.Exec(`ALTER TABLE foo ADD COLUMN baz INTEGER NOT NULL`),
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
