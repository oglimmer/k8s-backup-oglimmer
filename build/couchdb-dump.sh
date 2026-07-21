#!/usr/bin/env bash
#
# Dump a single CouchDB database to a _bulk_docs-restorable JSON file.
#
# Usage: couchdb-dump.sh <base_url> <db> <user> <pass> <out_file>
#   base_url  e.g. http://couchdb-prod-svc-couchdb.couchdb.svc.cluster.local:5984
#
# Restore with:
#   curl -X POST "<base_url>/<db>/_bulk_docs" -H 'Content-Type: application/json' -d @<out_file>
#
# _rev is stripped so documents insert cleanly into a fresh database (new_edits defaults to true).
# _all_docs?include_docs=true also returns _design/* docs, so views are preserved.
set -euo pipefail

BASE_URL=$1
DB=$2
USER=$3
PASS=$4
OUT=$5

curl -fsS --user "${USER}:${PASS}" \
    "${BASE_URL}/${DB}/_all_docs?include_docs=true" \
    | jq '{docs: [.rows[].doc | del(._rev)]}' \
    > "$OUT"
