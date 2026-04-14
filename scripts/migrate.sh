#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

DATABASE_URL="${DATABASE_URL:-postgres://tally:tally@localhost:5432/tally}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-$REPO_ROOT/migrations}"

if ! command -v psql >/dev/null 2>&1; then
  echo "psql is required but was not found in PATH" >&2
  exit 1
fi

if [ ! -d "$MIGRATIONS_DIR" ]; then
  echo "migrations directory not found: $MIGRATIONS_DIR" >&2
  exit 1
fi

echo "Applying migrations from $MIGRATIONS_DIR"

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL

shopt -s nullglob
migration_files=("$MIGRATIONS_DIR"/*.sql)

if [ ${#migration_files[@]} -eq 0 ]; then
  echo "No migration files found."
  exit 0
fi

for file in "${migration_files[@]}"; do
  version="$(basename "$file")"

  already_applied="$(
    psql "$DATABASE_URL" -t -A -v ON_ERROR_STOP=1 \
      -c "SELECT 1 FROM schema_migrations WHERE version = '$version' LIMIT 1"
  )"

  if [ "$already_applied" = "1" ]; then
    echo "Skipping $version (already applied)"
    continue
  fi

  echo "Applying $version"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$file"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
    -c "INSERT INTO schema_migrations (version) VALUES ('$version')"
done

echo "Migrations complete."
