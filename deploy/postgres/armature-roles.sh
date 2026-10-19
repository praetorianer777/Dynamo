#!/bin/sh
# Runs once, on first initialisation of the primary. Neither application role
# is a superuser; armature_admin serves sign-up, sign-in and the outbox relay,
# and the migrations keep the secret tables from armature_app.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-SQL
    CREATE ROLE armature_app LOGIN PASSWORD '${APP_DB_PASSWORD:-armature_app}';
    CREATE ROLE armature_admin LOGIN PASSWORD '${ADMIN_DB_PASSWORD:-armature_admin}';
    CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${REPLICATION_PASSWORD:-replicator}';

    GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO armature_app, armature_admin;
    GRANT USAGE ON SCHEMA public TO armature_app, armature_admin;

    -- The connection pool watches pg_stat_wal_receiver to tell a caught-up
    -- standby from one whose receiver has silently dropped. Read-only stat
    -- access is the least privilege that answers that question.
    GRANT pg_read_all_stats TO armature_app, armature_admin;

    -- Everything the migrations create later is granted to the runtime roles
    -- automatically, so a new table is never accidentally unreachable.
    ALTER DEFAULT PRIVILEGES FOR ROLE "$POSTGRES_USER" IN SCHEMA public
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO armature_app, armature_admin;
    ALTER DEFAULT PRIVILEGES FOR ROLE "$POSTGRES_USER" IN SCHEMA public
        GRANT USAGE, SELECT ON SEQUENCES TO armature_app, armature_admin;
    ALTER DEFAULT PRIVILEGES FOR ROLE "$POSTGRES_USER" IN SCHEMA public
        GRANT EXECUTE ON FUNCTIONS TO armature_app, armature_admin;
SQL

# Allow the replica to connect for streaming replication.
cat >> "$PGDATA/pg_hba.conf" <<-HBA
	host replication replicator all scram-sha-256
HBA
