# Common tasks. Requires Go (toolchain from go.mod), Rust (cargo) and, for
# integration tests, a Postgres reachable via TEST_DATABASE_URL.

TEST_DATABASE_URL ?= postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
COMPOSE_DEV  = docker compose -f deploy/docker-compose.yml
COMPOSE_PROD = docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env
SERVICES = auth directory delivery history media

.PHONY: all mls-ffi gen build cli services test test-unit test-rust lint up down logs dev-ca prod-up

all: build

## Rust MLS core (static library linked by the Go client via cgo)
mls-ffi:
	cd mls-ffi && cargo build --release

## Regenerate protobuf + connect-go code (needs buf, protoc-gen-go, protoc-gen-connect-go)
gen:
	rm -rf gen && buf lint && buf generate

build: services cli

services:
	@mkdir -p bin
	@for s in $(SERVICES); do CGO_ENABLED=0 go build -o bin/$$s-service ./services/$$s/cmd/$$s-service || exit 1; done

cli: mls-ffi
	@mkdir -p bin
	go build -o bin/kantra ./cmd/kantra

test-rust:
	cd mls-ffi && cargo test

test-unit: mls-ffi
	go test ./client/... ./pkg/...

## Everything, incl. Postgres-backed service and end-to-end tests.
## Set TEST_S3_ENDPOINT/TEST_S3_ACCESS_KEY/TEST_S3_SECRET_KEY to use a real MinIO.
test: mls-ffi test-rust
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l $$(git ls-files '*.go' | grep -v '^gen/'))"
	cd mls-ffi && cargo clippy --release --all-targets -- -D warnings

## Local stack (https://localhost)
up:
	$(COMPOSE_DEV) up -d --build

down:
	$(COMPOSE_DEV) down

logs:
	$(COMPOSE_DEV) logs -f

## Export Caddy's local root CA so the CLI can trust https://localhost:
##   bin/kantra -server https://localhost -ca deploy/caddy-root.crt ...
dev-ca:
	$(COMPOSE_DEV) exec -T caddy cat /data/caddy/pki/authorities/local/root.crt > deploy/caddy-root.crt

prod-up:
	$(COMPOSE_PROD) up -d --build
