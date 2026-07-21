#!/usr/bin/env bash
#
# Cluster backup runner.
#
#   1. dumps CouchDB (per-app), MariaDB (all DBs), Postgres (pg_dumpall) and the lunchy volume
#   2. bundles + gzips them
#   3. encrypts to an age *public* key (the matching private key is kept OFFLINE — a compromised
#      cluster can therefore never decrypt its own backups)
#   4. uploads to Google Drive via rclone and prunes copies older than RETENTION_DAYS
#
# Every input is an environment variable. Non-secret config comes from a ConfigMap, secrets from a
# Secret (see k8s/). Nothing sensitive lives in this image or in git.
set -euo pipefail

log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; }
die() { log "ERROR: $*" >&2; exit 1; }

# --- required config -------------------------------------------------------
: "${AGE_RECIPIENT:?age public key (recipient) is required}"
: "${RCLONE_REMOTE:?rclone remote name is required, e.g. gdrive:}"
RETENTION_DAYS=${RETENTION_DAYS:-6}

# Optional targets — each block is skipped when its config is absent, so this script works in
# clusters that don't run every database.
COUCHDB_HOST=${COUCHDB_HOST:-}
COUCHDB_TARGETS=${COUCHDB_TARGETS:-}        # JSON: [{"db":..,"user":..,"pass":..}, ...]
MARIADB_POD=${MARIADB_POD:-}
MARIADB_CONTAINER=${MARIADB_CONTAINER:-mariadb}
MARIADB_ROOT_PASSWORD=${MARIADB_ROOT_PASSWORD:-}
POSTGRES_POD=${POSTGRES_POD:-}
POSTGRES_USER=${POSTGRES_USER:-postgres}
POSTGRES_PASSWORD=${POSTGRES_PASSWORD:-}
LUNCHY_SELECTOR=${LUNCHY_SELECTOR:-}
LUNCHY_PATH=${LUNCHY_PATH:-/mnt/lunchy/}

WORKDIR=$(mktemp -d)
STAGE="$WORKDIR/stage"
mkdir -p "$STAGE"
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

# --- CouchDB ---------------------------------------------------------------
if [[ -n "$COUCHDB_TARGETS" && -n "$COUCHDB_HOST" ]]; then
    count=$(jq 'length' <<<"$COUCHDB_TARGETS")
    for i in $(seq 0 $((count - 1))); do
        db=$(jq -r ".[$i].db"   <<<"$COUCHDB_TARGETS")
        user=$(jq -r ".[$i].user" <<<"$COUCHDB_TARGETS")
        pass=$(jq -r ".[$i].pass" <<<"$COUCHDB_TARGETS")
        couchdb-dump.sh "$COUCHDB_HOST" "$db" "$user" "$pass" "$STAGE/couchdb-${db}.json"
        log "CouchDB dumped: $db"
    done
else
    log "CouchDB: no targets configured, skipping"
fi

# --- MariaDB ---------------------------------------------------------------
if [[ -n "$MARIADB_POD" && -n "$MARIADB_ROOT_PASSWORD" ]]; then
    # MYSQL_PWD keeps the password out of the target pod's argv (unlike -p<pass>).
    dbs=$(kubectl exec "$MARIADB_POD" -c "$MARIADB_CONTAINER" -- \
        env MYSQL_PWD="$MARIADB_ROOT_PASSWORD" \
        mariadb -N -u root -e "show databases" \
        | grep -vE '^(lost\+found|information_schema|mysql|performance_schema|sys)$' \
        | paste -sd' ' -)
    # --single-transaction --quick: consistent InnoDB snapshot with NO table locks, so a live
    # (mid-day) database is dumped without blocking application writes.
    # word-splitting on $dbs is intentional: each DB name is a separate --databases arg
    # shellcheck disable=SC2086
    kubectl exec "$MARIADB_POD" -c "$MARIADB_CONTAINER" -- \
        env MYSQL_PWD="$MARIADB_ROOT_PASSWORD" \
        mariadb-dump -u root --single-transaction --quick --databases $dbs \
        > "$STAGE/mariadb-all.sql"
    log "MariaDB dumped: $dbs"
else
    log "MariaDB: not configured, skipping"
fi

# --- Postgres --------------------------------------------------------------
if [[ -n "$POSTGRES_POD" && -n "$POSTGRES_PASSWORD" ]]; then
    # pg_dumpall covers every database plus roles/globals.
    kubectl exec "$POSTGRES_POD" -- \
        env PGPASSWORD="$POSTGRES_PASSWORD" \
        pg_dumpall -U "$POSTGRES_USER" \
        > "$STAGE/postgres-all.sql"
    log "Postgres dumped"
else
    log "Postgres: not configured, skipping"
fi

# --- lunchy volume ---------------------------------------------------------
if [[ -n "$LUNCHY_SELECTOR" ]]; then
    pod=$(kubectl get po -l "$LUNCHY_SELECTOR" -o jsonpath='{.items[0].metadata.name}')
    [[ -n "$pod" ]] || die "no pod found for selector '$LUNCHY_SELECTOR'"
    kubectl exec "$pod" -- tar -cf - "$LUNCHY_PATH" | gzip > "$STAGE/lunchy.tar.gz"
    log "lunchy volume copied from $pod"
else
    log "lunchy: not configured, skipping"
fi

# --- bundle, encrypt, upload ----------------------------------------------
[[ -n "$(ls -A "$STAGE")" ]] || die "nothing was dumped — refusing to upload an empty backup"

STAMP=$(date -u '+%Y-%m-%dT%H-%M-%SZ')
ARCHIVE="$WORKDIR/backup-${STAMP}.tar.gz.age"

tar -C "$STAGE" -cf - . | gzip | age -r "$AGE_RECIPIENT" -o "$ARCHIVE"
log "bundle encrypted -> $(basename "$ARCHIVE") ($(du -h "$ARCHIVE" | cut -f1))"

rclone copy "$ARCHIVE" "$RCLONE_REMOTE" --stats-one-line
log "uploaded to $RCLONE_REMOTE"

rclone delete "$RCLONE_REMOTE" --min-age "${RETENTION_DAYS}d" --stats-one-line || true
log "pruned copies older than ${RETENTION_DAYS}d"

log "backup complete"
