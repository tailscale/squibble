# SQLite Schema Digests

## For a SQLite Database

The `squibble` package uses a cryptographic hash of the SQLite database schema
to identify versions. The algorithm used to compute the hash is as follows:

- From each row of the [`sqlite_schema`][sqstab] virtual table, read the
  `type`, `name`, `tbl_name`, and `sql` columns.

- Sort the resulting tuples lexicographically by `type`, then `name`, then
  `tbl_name`, then `sql`. If any column is null, treat it as the empty string.

- Remove from the resulting list any row whose `tbl_name` matches the name
  of the schema history table (`_schema_history`).

- Convert the tuple into a compact array of JSON objects ending with a newline:
   ```json
   [{"Type":"<type>","Name":"<name>","TableName":"<tbl-name>","SQL":"<sql>"},...]<NL>
   ```

- Compute the SHA256 digest of the resulting JSON text.

- Encode the digest as a string of lower-case hexadecimal digits.

## For a SQL Schema Definition

To compute the digest for a schema definition encoded in SQL text:

- Create an empty SQLite database.

- Execute the schema definition.

- Apply the algorithm above to compute the digest for the database schema.

## Notes

This algorithm depends upon the stability of the normalization of SQL schema
definitions performed by SQLite. If the normalization rules change, the digest
may change.

This approach also means that definitions which are semantically equivalent may
not hash equal. For example, given:

```sql
CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  UNIQUE (name)
);

CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  name TEXT UNIQUE NOT NULL
);
```

These two tables are equivalent (both have the same columns and generate the
same uniqueness index), but because the schema includes the spelling of the
definition, they would not hash the same.

[sqstab]: https://sqlite.org/schematab.html
