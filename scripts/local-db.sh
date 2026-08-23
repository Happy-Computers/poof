#!/bin/sh
# Local WSL Postgres setup for Infinity Storage auth.
# Installs nothing; assumes postgresql is already installed via apt.
set -eu

DB_NAME=infinity_storage
DB_USER=infinity_storage
DB_PASS="${INFINITY_STORAGE_DB_PASS:?Set INFINITY_STORAGE_DB_PASS to a strong password}"
DB_PASS_URL="$(node -e 'process.stdout.write(encodeURIComponent(process.argv[1]))' "$DB_PASS")"
MIGRATIONS_DIR="$(cd "$(dirname "$0")/../supabase/migrations" && pwd)"

sudo -u postgres psql -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${DB_USER}') THEN
        CREATE ROLE ${DB_USER} LOGIN PASSWORD '${DB_PASS}';
    ELSE
        ALTER ROLE ${DB_USER} PASSWORD '${DB_PASS}';
    END IF;
END
\$\$;
SELECT 'CREATE DATABASE ${DB_NAME} OWNER ${DB_USER}'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${DB_NAME}')\gexec
SQL

sudo -u postgres psql -v ON_ERROR_STOP=1 -d "$DB_NAME" <<SQL
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'anon') THEN
        CREATE ROLE anon NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'authenticated') THEN
        CREATE ROLE authenticated NOLOGIN;
    END IF;
END
\$\$;
SQL

for file in "${MIGRATIONS_DIR}"/*.sql; do
    echo "applying $(basename "$file")"
    PGPASSWORD="$DB_PASS" psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "$DB_USER" -d "$DB_NAME" -f "$file"
done

echo "DATABASE_URL=postgresql://${DB_USER}:${DB_PASS_URL}@127.0.0.1:5432/${DB_NAME}"
