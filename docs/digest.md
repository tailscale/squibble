# SQLite Schema DIgests

The `squibble` package uses a cryptographic hash of the SQLite database schema
to identify versions. The algorithm used to compute the hash is as follows:

- From each row of the `sqlite_schema` virtual table, read the `type`, `name`,
  `tbl_name`, and `sql` columns.

- Sort the resulting tuples lexicographically by `type`, then `name`, then
  `tbl_name`, then `sql`. If the `sql` column is null, treat it as an empty
  string.

- Remove from the resulting list any row whose `tbl_name` matches the name
  of the schema history table (`_schema_history`).

- Convert the tuple into a compact array of JSON objects ending with a newline:
   ```json
   [{"Type":<type>,"Name":<name>,"TableName":<tbl-name>,"SQL":<sql>},...]<NL>
   ```

- Compute the SHA256 digest of the resulting JSON text.

- Encode the digest as a string of lower-case hexadecimal digits.

## SQL Schemas

To compute the digest for a schema definition encoded in SQL text:

- Create an empty SQLite database.

- Execute the schema definition.

- Apply the algorithm above to compute the digest for the database schema.
