# All Go services in one image (pure Go, no cgo). Select with SERVICE:
#   docker build --build-arg SERVICE=auth-service .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY gen ./gen
COPY pkg ./pkg
COPY services ./services
COPY cmd/s3init ./cmd/s3init
ARG SERVICE
RUN test -n "$SERVICE" && \
    if [ "$SERVICE" = "s3init" ]; then path=./cmd/s3init; else path=./services/${SERVICE%-service}/cmd/$SERVICE; fi && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app "$path"

FROM alpine:3.22
# alpine ships ca-certificates-bundle and busybox wget (used by healthchecks).
RUN adduser -D -H -u 10001 app
USER app
COPY --from=build /out/app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
