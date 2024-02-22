-- This table records a history of the schema updates applied to the database
-- by a squibble.Schema. The contents of this table are not required for the
-- migrator to work, the records here are for record-keeping and debugging.
CREATE TABLE IF NOT EXISTS _schema_history (
  -- Unix-epoch microseconds at which this schema was applied.
  timestamp INTEGER UNIQUE NOT NULL,

  -- A hex-coded SHA256 digest of the schema applied.
  digest TEXT NOT NULL,

  -- The SQL schema definition text, zstd compressed.
  schema BLOB
);
