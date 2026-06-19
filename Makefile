.PHONY: build run test test-unit test-integration test-e2e cover cover-check clean docker-up docker-down migrate spec spec-check generate generate-check generate-sdk generate-sdk-check generate-sdk-ts generate-sdk-py

# OpenAPI Generator for the /v1 SDK base. Pinned to a released tag (never
# :latest/SNAPSHOT) so output is reproducible for the drift gate. Run via
# Docker — no local Java needed.
OAG_IMAGE := openapitools/openapi-generator-cli:v7.16.0

build:
	go build -o bin/e2a ./cmd/e2a

run: build
	./bin/e2a -config config.yaml

test:
	E2A_TEST_DATABASE_URL="postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable" go test -tags integration -p 1 ./...

test-unit:
	go test -short ./internal/headers/ ./internal/outbound/ ./internal/relay/ ./internal/config/ ./internal/webhook/ ./internal/approvaltoken/ ./internal/limits/ ./internal/httpapi/ ./internal/ratelimit/

test-integration:
	E2A_TEST_DATABASE_URL="postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable" go test -p 1 ./internal/identity/ ./internal/agent/ ./internal/hitlworker/ ./internal/hitlnotify/ ./internal/limits/ ./internal/relay/

test-e2e:
	E2A_TEST_DATABASE_URL="postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable" go test -tags integration -p 1 ./internal/e2e/

# cover writes a coverage profile across the internal packages (needs Postgres
# on :5433, like `make test`; -p 1 avoids cross-package test-DB contention).
# cover-check enforces the per-package floors in .testcoverage.yml. CI runs the
# same gate via the vladopajic/go-test-coverage action.
GO_TEST_COVERAGE_VERSION ?= v2.14.3
cover:
	E2A_TEST_DATABASE_URL="postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable" go test -p 1 -covermode=atomic -coverprofile=cover.out ./internal/...

cover-check: cover
	go run github.com/vladopajic/go-test-coverage/v2@$(GO_TEST_COVERAGE_VERSION) --config=.testcoverage.yml

clean:
	rm -rf bin/

docker-up:
	docker compose up -d

docker-down:
	docker compose down

migrate:
	@for f in migrations/*.sql; do \
		echo "Applying $$f ..."; \
		psql "postgres://e2a:e2a@localhost:5433/e2a?sslmode=disable" -f "$$f"; \
	done

# spec regenerates the /v1 OpenAPI 3.1 document (api/openapi.yaml) directly
# from the live Huma handlers — the single source of truth for SDK codegen and
# the rendered API reference. (The dashboard's API-reference page copies
# api/openapi.yaml into web/public/openapi.yaml at build time via the web
# `sync-openapi` script. The old swag-annotation pipeline has been removed.)
spec:
	go test ./internal/httpapi/ -run TestSpecGoldenNoDrift -update-spec -count=1
	@echo "==> Regenerated api/openapi.yaml from the /v1 handlers"

# spec-check is the contract-drift gate: fails if api/openapi.yaml lags the
# handlers. Runs in CI as part of the normal test suite (TestSpecGoldenNoDrift);
# this is the explicit entrypoint.
spec-check:
	go test ./internal/httpapi/ -run TestSpecGoldenNoDrift -count=1

generate: spec generate-sdk

# generate-sdk regenerates both /v1 SDK client bases from the canonical
# api/openapi.yaml via OpenAPI Generator (the `generate-sdk-ts` /
# `generate-sdk-py` targets below). The retired swag + datamodel-codegen
# pipeline (Swagger 2.0 → OpenAPI 3.0 → openapi-typescript / datamodel-codegen)
# has been removed; the hand-written ergonomic layer wraps the OAG output.
generate-sdk: generate-sdk-ts generate-sdk-py

generate-check: spec-check generate-sdk-check

generate-sdk-check: generate-sdk
	@echo "==> Checking generated code is up to date"
	git diff --exit-code sdks/typescript/src/v1/generated/ sdks/python/src/e2a/v1/generated/

# generate-sdk-ts regenerates the TypeScript /v1 client base from the canonical
# api/openapi.yaml using OpenAPI Generator's `typescript` generator (NOT
# typescript-fetch, which fails TS2590 on wide models — see Slice 8). Output
# lands in sdks/typescript/src/v1/generated/; the hand-written ergonomic layer
# wraps it. Package scaffolding is suppressed via .openapi-generator-ignore.
generate-sdk-ts:
	@echo "==> Generating TS /v1 client base via $(OAG_IMAGE)"
	bash sdks/typescript/scripts/generate-oag.sh

# generate-sdk-py regenerates the Python /v1 client base (package e2a.v1.generated)
# from api/openapi.yaml using OpenAPI Generator's `python` generator with the
# httpx library (async-native, matches async-only Python + the hand-written
# layer's HTTP client). Output is the leaf package only; see the script.
generate-sdk-py:
	@echo "==> Generating Python /v1 client base via $(OAG_IMAGE)"
	bash sdks/python/scripts/generate-oag.sh
