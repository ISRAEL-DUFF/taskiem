# Local checks: make check runs the go and typescript jobs' main steps and
# the licence gate; CI runs more (CONTRIBUTING.md#local-checks).
# The test database, exported so that every go test sees it: the database
# tests skip themselves without it. Start one with make db-up, point this at
# any Postgres 16, or pass TASKIEM_TEST_DATABASE_URL= to skip those tests.
TASKIEM_TEST_DATABASE_URL ?= postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
export TASKIEM_TEST_DATABASE_URL
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: check lint test test-db validate licences ts web e2e build spike load up down db-up db-down image chart docs docs-check atlas atlas-stale

check: lint test validate licences ts

lint:
	golangci-lint run ./...

test:
	go test -tags e2e ./...

# Every test, the database ones included, uncached (as the go job runs them).
test-db:
	go test -count=1 -tags e2e ./...

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

# Browser tests against the real binary; needs TASKIEM_TEST_DATABASE_URL and
# psql. web/e2e/serve.sh builds its own binary with -tags e2e into
# bin/taskiem-e2e, so bin/taskiem stays what make build made.
e2e: web
	pnpm --filter @taskiem/web e2e

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/taskiem ./cmd/taskiem

# The real engine at the G1 rate (tools/loadtest -h for the flags).
load:
	go run ./tools/loadtest -rate 500 -duration 60s

spike:
	go run ./engine/spike/cmd/loadtest -mode latency -rate 500 -duration 30s -inline

# The whole single-node stack (docs/operations.md).
up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down

db-up:
	docker compose -f deploy/docker-compose.yml up -d postgres

# Stops the database only; make down stops the whole stack.
db-down:
	docker compose -f deploy/docker-compose.yml stop postgres

image:
	docker build --build-arg VERSION=$(VERSION) -t taskiem:$(VERSION) .

# Lints the Helm chart and regenerates the plain manifests from it (docs/kubernetes.md).
chart:
	helm lint --strict deploy/helm/taskiem
	{ echo "# Generated from deploy/helm/taskiem by: helm template taskiem deploy/helm/taskiem --namespace taskiem"; \
	  echo "# Do not edit; change the chart and regenerate (docs/kubernetes.md). Create the taskiem and taskiem-migrate Secrets first."; \
	  helm template taskiem deploy/helm/taskiem --namespace taskiem --set ingress.enabled=true; } > deploy/kubernetes/taskiem.yaml

# The public site: the product website and, under docs/, the docs and API
# reference (CONTRIBUTING.md#docs-and-the-api-reference, docs/strategy/website.md).
# Checks every docs link first; the site is written to site/ (not committed).
# DOCS_BASE is the path it is served under; DOCS_SOURCE links unpublished files.
# SITE_APP, SITE_WHATSAPP and SITE_EMAIL are the website's sign-up address,
# WhatsApp number and contact email; each is left off the site while empty.
DOCS_BASE ?= /
DOCS_SOURCE ?=
SITE_APP ?=
SITE_WHATSAPP ?=
SITE_EMAIL ?=
docs:
	go run ./tools/docsite -out site -base '$(DOCS_BASE)' -source '$(DOCS_SOURCE)' -app '$(SITE_APP)' -whatsapp '$(SITE_WHATSAPP)' -email '$(SITE_EMAIL)'

docs-check:
	go run ./tools/docsite -check

# The engineering atlas (docs/atlas/README.md): build it, and list the pages
# whose code has changed since they were last checked.
atlas:
	node docs/atlas/atlas.mjs build

atlas-stale:
	node docs/atlas/atlas.mjs stale
