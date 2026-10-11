# MinIO built from source. MinIO stopped publishing community images on
# Docker Hub (minio/minio), so docker compose builds this one (image
# kantra/minio:local). Pin a version with --build-arg MINIO_VERSION=<module version>.
FROM golang:1.26-alpine AS build
ARG MINIO_VERSION=latest
RUN CGO_ENABLED=0 go install -trimpath github.com/minio/minio@${MINIO_VERSION}

FROM alpine:3.22
# alpine already ships a CA bundle and busybox wget.
COPY --from=build /go/bin/minio /usr/bin/minio
EXPOSE 9000 9001
ENTRYPOINT ["/usr/bin/minio"]
