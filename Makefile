# Local checks mirror CI (.github/workflows/ci.yml).
TASKIEM_TEST_DATABASE_URL ?= postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: check lint test test-db validate licences ts web e2e build spike up down db-up db-down

check: lint test validate licences ts

lint:
	golangci-lint run ./...

test:
	go test ./...

test-db:
	TASKIEM_TEST_DATABASE_URL='$(TASKIEM_TEST_DATABASE_URL)' go test -count=1 ./engine/db/...

validate:
	go run ./cmd/taskiem validate flows/*/*.wd.json connectors/*/manifest.yaml

licences:
	pnpm licenses list --json > .npm-licences.json
	go run ./tools/licencecheck -npm .npm-licences.json
	rm -f .npm-licences.json

ts:
	pnpm install --frozen-lockfile
	pnpm lint
	pnpm typecheck
	pnpm test

web:
	pnpm --filter @taskiem/web build

# Browser tests against the real binary; needs TASKIEM_TEST_DATABASE_URL and psql.
e2e: web
	TASKIEM_TEST_DATABASE_URL='$(TASKIEM_TEST_DATABASE_URL)' pnpm --filter @taskiem/web e2e

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/taskiem ./cmd/taskiem

spike:
	TASKIEM_TEST_DATABASE_URL='$(TASKIEM_TEST_DATABASE_URL)' go run ./engine/spike/cmd/loadtest -mode latency -rate 500 -duration 30s -inline

# The whole single-node stack (docs/operations.md).
up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down

db-up:
	docker compose -f deploy/docker-compose.yml up -d postgres

db-down:
	docker compose -f deploy/docker-compose.yml down
