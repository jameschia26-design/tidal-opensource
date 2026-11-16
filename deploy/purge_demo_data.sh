#!/usr/bin/env bash
set -euo pipefail

# Run against PostgreSQL instance running shared-postgres container on VPS,
# or via DATABASE_URL if psql is locally installed.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SQL_FILE="${DIR}/purge_demo_data.sql"

if [ ! -f "${SQL_FILE}" ]; then
  echo "Error: ${SQL_FILE} not found" >&2
  exit 1
fi

echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] Starting Tidal demo data purge (7-day retention)..."

if [ -n "${DATABASE_URL:-}" ]; then
  psql "${DATABASE_URL}" -f "${SQL_FILE}"
elif docker ps --format '{{.Names}}' | grep -q 'shared-postgres'; then
  CONTAINER="$(docker ps -q -f "name=shared-postgres" | head -n1)"
  docker exec -i "${CONTAINER}" psql -U postgres -d tidal_demo < "${SQL_FILE}"
else
  echo "Error: No DATABASE_URL set and no shared-postgres container found." >&2
  exit 1
fi

echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] Tidal demo data purge complete."
