# backup2

Kubernetes-native backup for a self-hosted cluster. A `CronJob` dumps the cluster's databases
(CouchDB, MariaDB, Postgres) and a data volume, encrypts the bundle to an **offline** age key, and
uploads it to Google Drive via rclone. A small web UI shows the last run's logs and the list of
backups, with a "run now" button.

## Design

- **Native `CronJob`** — no long-running daemon, no in-container cron.
- **Scoped `ServiceAccount`** — the runner may only list pods and `exec` the database pods; the
  viewer may only read jobs/pods/logs and create jobs. No cluster-admin kubeconfig.
- **No secrets in git** — only `*.example.yaml` templates are committed. The real `Secret` is
  applied out-of-band and git-ignored. The container image holds no secrets or config; everything
  is injected at runtime.
- **age encryption to a public key** — the private key stays offline, so a compromised cluster can
  create backups but can never decrypt them.
- **rclone** to Google Drive (auth refresh, retention, resumable uploads).
- **Hardened image** — non-root, read-only root filesystem, all capabilities dropped.
- **Lock-free dumps** — `mariadb-dump --single-transaction --quick` so live databases aren't blocked.

## Layout

```
build/                        runner image (backup.sh, couchdb-dump.sh, Dockerfile)
viewer/                       web UI image (Go server + Dockerfile), FROM the runner image
build.sh                      buildx → registry.oglimmer.com/backup2 + backup2-viewer (arm64)
helm/backup2/                 Helm chart (CronJob, ConfigMap, viewer, Ingress, RBAC)
helm/argocd/backup2-app.yaml  optional ArgoCD Application (Helm source)
secret.example.yaml           credentials Secret template        (fill in → secret.yaml, git-ignored)
oidc-middleware.example.yaml  Traefik OIDC SSO middleware        (fill in → oidc-middleware.yaml, git-ignored)
```

The Helm chart creates **no** Secret and **no** OIDC middleware — both carry secrets, so they are
applied out-of-band and git-ignored. The chart references the Secret by name (`existingSecret`) and
the middleware by name (`ingress.oidcMiddleware`).

## One-time setup

### 1. Backup encryption key (age)

```bash
age-keygen -o backup2-age.key        # prints the public recipient
```

Put the **public** key (`age1…`) into `ageRecipient` in `helm/backup2/values.yaml`. Store the
private key file somewhere safe **outside the repo and cluster** (password manager / offline).
Without it, backups cannot be decrypted — that is the point.

### 2. Google Drive token (rclone)

On any machine with a browser:

```bash
rclone authorize "drive"             # log in, copy the printed JSON token
```

Note the Drive folder ID (from its URL). If you authorize with your own Google OAuth client, also
note its client id/secret; a plain `rclone authorize "drive"` uses rclone's built-in client, in
which case leave client id/secret unset.

### 3. Create the Secrets (out-of-band)

```bash
cp secret.example.yaml secret.yaml   # git-ignored
# fill in: CouchDB passwords, MariaDB/Postgres root passwords, rclone token, Drive folder id
kubectl apply -f secret.yaml
```

### 4. Build the images

```bash
./build.sh
```

### 5. OIDC SSO middleware (out-of-band)

The UI is protected by Keycloak SSO via a Traefik OIDC middleware. It holds the Keycloak
`clientSecret` inline (the plugin can't read a Kubernetes Secret), so it's applied out-of-band:

```bash
cp oidc-middleware.example.yaml oidc-middleware.yaml   # git-ignored
# fill in clientSecret; adjust allowedUsers as needed
kubectl apply -f oidc-middleware.yaml
```

### 6. Install with Helm

```bash
helm install backup2 ./helm/backup2 --namespace default
# upgrades: helm upgrade backup2 ./helm/backup2 --namespace default
# or, GitOps: kubectl apply -f helm/argocd/backup2-app.yaml
```

## Web UI

An always-on viewer at **https://backup.oglimmer.com** shows the last run's logs (live from the
Kubernetes API) and the list of backups on Drive, with a **Run backup now** button. It runs the
runner image + a small Go server, as a `ServiceAccount` with read-only job/pod access plus
`create jobs`. Access is gated by **Keycloak OIDC SSO** at the ingress (the out-of-band middleware
from step 5), restricted to `allowedUsers`.

Disable the UI entirely with `--set viewer.enabled=false`.

## Run on demand (CLI)

```bash
kubectl create job --from=cronjob/backup backup-manual -n default
kubectl logs -f job/backup-manual -n default
```

## Restore

Pull the archive from Google Drive, then decrypt with the **offline** private key:

```bash
age -d -i backup2-age.key backup-<stamp>.tar.gz.age | tar -xzf -
```

You get `postgres-all.sql`, `mariadb-all.sql`, `couchdb-<db>.json`, `lunchy.tar.gz`. Restore each:

```bash
# Postgres
kubectl exec -i postgres-0 -- env PGPASSWORD=… psql -U postgres < postgres-all.sql

# MariaDB
kubectl exec -i mariadb-0 -c mariadb -- env MYSQL_PWD=… mariadb -u root < mariadb-all.sql

# CouchDB (per database; _rev was stripped so docs insert into a fresh DB)
curl -X POST "http://<host>:5984/<db>/_bulk_docs" \
     -H 'Content-Type: application/json' --user <user>:<pass> -d @couchdb-<db>.json

# lunchy
tar -xzf lunchy.tar.gz
```
