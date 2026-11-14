#!/usr/bin/env bash
# Reject skipped subtests too; go test indents their status lines.
if grep -qE -- '^[[:space:]]*--- (FAIL|SKIP):' "$1"; then
  echo "Test failure or skip detected" >&2
  exit 1
fi
