#!/bin/sh
set -eu
pg_isready -q -U "${POSTGRES_USER}" -d "${POSTGRES_DB}"
psql -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -Atqc "SELECT 1" | grep -qx 1
