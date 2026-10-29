-- The v0.3 restore marker (RFC 0006 §7.2).
--
-- `glossa import --from v0 --v0-db` reads a Glossa v0.3 database
-- directly, so it must never be pointed at v0.3's live database. It runs
-- only against a database that carries all three of these, and refuses
-- otherwise; there is no flag to skip the check:
--
--   1. exactly one row in glossa_v0_restore.marker;
--   2. that row names this database by OID and name, so a dump of a
--      marked database restored somewhere else is not marked;
--   3. the database itself defaults to read-only transactions
--      (ALTER DATABASE … SET default_transaction_read_only = on), under
--      which v0.3's API cannot write, so a live v0.3 database never has
--      it.
--
-- v0-restore.sh runs this, as a superuser, in the database it has just
-- created and restored into, with the psql variables dump_name and
-- dump_sha256. Do not run it by hand against any other database.

\set ON_ERROR_STOP 1

BEGIN;

CREATE SCHEMA glossa_v0_restore;

CREATE TABLE glossa_v0_restore.marker (
  only_row      boolean PRIMARY KEY DEFAULT true CHECK (only_row),
  database_oid  oid         NOT NULL,
  database_name name        NOT NULL,
  dump_name     text        NOT NULL CHECK (dump_name <> ''),
  dump_sha256   text        NOT NULL CHECK (dump_sha256 ~ '^[0-9a-f]{64}$'),
  restored_at   timestamptz NOT NULL DEFAULT now(),
  restored_by   name        NOT NULL DEFAULT current_user
);

INSERT INTO glossa_v0_restore.marker (database_oid, database_name, dump_name, dump_sha256)
SELECT oid, datname, :'dump_name', :'dump_sha256'
FROM pg_database
WHERE datname = current_database();

COMMIT;

ALTER DATABASE :"DBNAME" SET default_transaction_read_only = on;
