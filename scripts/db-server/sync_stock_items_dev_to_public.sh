#!/usr/bin/env bash
# Operator-only script for the Postgres server (root@postgres-db).
# Coding agents: NEVER run this script against the real server.
# Replaces 'public' with a checked copy of 'dev' in one transaction, after a
# backup of 'public'; see sync_schema.sh (keep the two files together).
exec "$(dirname "$(readlink -f "$0")")/sync_schema.sh" dev public
