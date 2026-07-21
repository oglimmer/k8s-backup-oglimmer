# Viewer image = the backup2 runner image (which already has kubectl + rclone) + a small Go server.
# Cross-compiled on the build host for speed, then dropped onto the arm64 runtime image.
FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/backup-viewer .

FROM registry.oglimmer.com/backup2:latest
COPY --from=build /out/backup-viewer /usr/local/bin/backup-viewer
USER 10001
WORKDIR /home/backup
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/backup-viewer"]
