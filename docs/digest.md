# SQLite Schema Digests

## For a SQLite Database

The `squibble` package uses a cryptographic hash of the SQLite database schema
to identify versions. The algorithm used to compute the hash is as follows:

- From each row of the [`sqlite_schema`][sqstab] virtual table, read the
  `type`, `name`, `tbl_name`, and `sql` columns.

- Sort the resulting tuples lexicographically by `type`, then `name`, then
  `tbl_name`, then `sql`. If any column is null, treat it as the empty string.

- Remove from the resulting list any row whose `tbl_name` matches the name of
  the schema history table (`_schema_history`) and `sqlite_sequence`, along
  with any other tables not intended to be managed by squibble.

- Sort the rows increasing by type, name, table name, and SQL.

- For each row of type `table`, record the `name`, `type`, `notull`,
  `dflt_value`, `pk`, and `hidden` fields from the output of `pragma
  table_xinfo`, and set the `SQL` field to empty.

  The SQL field is not included in the hash for tables, because it is not
  canonicalized and there are many different spellings in SQL that could lead
  to an equivalent table definition. The column metadata are more stable.

- Convert each column into a compact JSON object:

   ```json
   {"Name":"<name>","Type":"<type>","NotNull":bool,"Default":"<text>","PrimaryKey":bool,"Hidden":int}
   ```

- Convert each row into a compact JSON object:

   ```json
   {"Type":"<type>","Name":"<name>","TableName":"<tbl-name>",Columns:[<rows>],"SQL":"<text>"}
   ```

- Accumulate the rows into an array, compact the representation (removing all
  grammatically unnecessary whitespace), and append a single Unicode newline (10).

- Compute the SHA256 digest of this resulting JSON text.

- Encode the digest as a string of lower-case hexadecimal digits.

## For a SQL Schema Definition

To compute the digest for a schema definition encoded in SQL text:

- Create an empty SQLite database.

- Execute the schema definition.

- Apply the algorithm above to compute the digest for the database schema.

## Notes

For objects other than tables (notably views), the digest algorithm depends
upon the stability of the normalization of SQL schema definitions performed by
SQLite. If the normalization rules change, the digest may change. Squibble
attempts to mitigate this by further canonicalizing the SQL text that SQLite
lightly normalizes, by splitting it on newlines, trimming leading and trailing
whitespace from each, then concatenating the result with spaces so the whole
query is on a single line.

[sqstab]: https://sqlite.org/schematab.html
