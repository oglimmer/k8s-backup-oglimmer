# Backup runner image: dumps cluster databases, encrypts with age, ships to Google Drive via rclone.
# The image itself is stateless and holds NO secrets or config — everything is injected at runtime
# via env (ConfigMap + Secret) by the CronJob.
FROM debian:bookworm-slim

# kubectl is pinned to the cluster's minor version (v1.32), matching the rest of this repo.
ARG KUBECTL_CHANNEL=v1.32

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gpg jq tar gzip age rclone tzdata bash coreutils; \
    install -d -m 0755 /etc/apt/keyrings; \
    curl -fsSL "https://pkgs.k8s.io/core:/stable:/${KUBECTL_CHANNEL}/deb/Release.key" \
        | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/${KUBECTL_CHANNEL}/deb/ /" \
        > /etc/apt/sources.list.d/kubernetes.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends kubectl; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

COPY backup.sh couchdb-dump.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/backup.sh /usr/local/bin/couchdb-dump.sh

# Run as an unprivileged user; in-cluster auth comes from the mounted ServiceAccount token.
# (debian already has a system user named "backup" at uid 34, hence the distinct name.)
RUN useradd --uid 10001 --user-group --create-home --home-dir /home/backup \
        --shell /usr/sbin/nologin backupjob
USER 10001
WORKDIR /home/backup

ENTRYPOINT ["/usr/local/bin/backup.sh"]
