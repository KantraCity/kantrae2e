# Fallback MinIO image built from source, for environments where the
# upstream minio/minio image cannot be pulled:
#   docker build -f deploy/minio.Dockerfile -t kantra/minio:local deploy
#   MINIO_IMAGE=kantra/minio:local docker compose -f deploy/docker-compose.yml up -d
FROM golang:1.26-alpine AS build
ARG MINIO_VERSION=latest
RUN CGO_ENABLED=0 go install -trimpath github.com/minio/minio@${MINIO_VERSION}

FROM alpine:3.22
# alpine already ships a CA bundle and busybox wget.
COPY --from=build /go/bin/minio /usr/bin/minio
EXPOSE 9000 9001
ENTRYPOINT ["/usr/bin/minio"]
