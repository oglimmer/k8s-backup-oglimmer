#!/bin/bash
set -eu

# 1) the backup runner image
docker buildx build \
    --tag registry.oglimmer.com/backup2 \
    --platform linux/arm64 \
    --push \
    build/ -f build/Dockerfile

# 2) the viewer image — FROM the runner image above, so it must be built/pushed second
docker buildx build \
    --tag registry.oglimmer.com/backup2-viewer \
    --platform linux/arm64 \
    --push \
    viewer/ -f viewer/Dockerfile
