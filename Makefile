.PHONY: build run test test-unit test-integration test-e2e clean docker-up docker-down migrate swagger swagger-check spec spec-check generate generate-check generate-sdk generate-sdk-check

OPENAPI3_SPEC := /tmp/e2a-openapi3.yaml
PY_CODEGEN_VENV := sdks/python/.venv-codegen
PY_CODEGEN_REQUIREMENTS := sdks/python/codegen-requirements.txt
PY_CODEGEN_PIP := $(PY_CODEGEN_VENV)/bin/pip
PY_CODEGEN := $(PY_CODEGEN_VENV)/bin/datamodel-codegen

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

swagger:
	swag init --generalInfo cmd/e2a/main.go --parseDependency --parseInternal --output web/public --outputTypes yaml
	mv web/public/swagger.yaml web/public/openapi.yaml

swagger-check:
	swag init --generalInfo cmd/e2a/main.go --parseDependency --parseInternal --output /tmp/swag-check --outputTypes yaml
	diff -u web/public/openapi.yaml /tmp/swag-check/swagger.yaml

# spec regenerates the /v1 OpenAPI 3.1 document (api/openapi.yaml) directly
# from the live Huma handlers — the source of truth for SDK codegen + docs.
# This replaces the legacy swag-annotation pipeline (the `swagger` target
# above) as resources finish moving onto /v1; the SDK-codegen switchover to
# this file is tracked separately.
spec:
	go test ./internal/httpapi/ -run TestSpecGoldenNoDrift -update-spec -count=1
	@echo "==> Regenerated api/openapi.yaml from the /v1 handlers"

# spec-check is the contract-drift gate: fails if api/openapi.yaml lags the
# handlers. Runs in CI as part of the normal test suite (TestSpecGoldenNoDrift);
# this is the explicit entrypoint.
spec-check:
	go test ./internal/httpapi/ -run TestSpecGoldenNoDrift -count=1

generate: swagger generate-sdk

generate-sdk:
	@echo "==> Converting Swagger 2.0 → OpenAPI 3.0"
	cd sdks/typescript && npm run generate:openapi3
	@echo "==> Generating TypeScript types"
	cd sdks/typescript && npm run generate:types
	@echo "==> Setting up Python codegen venv"
	@current_reqs="$$(mktemp)"; \
	if test -d $(PY_CODEGEN_VENV); then \
		$(PY_CODEGEN_PIP) freeze 2>/dev/null | sort > "$$current_reqs"; \
		if diff -u $(PY_CODEGEN_REQUIREMENTS) "$$current_reqs" >/dev/null; then \
			echo "==> Reusing pinned Python codegen venv"; \
		else \
			echo "==> Installing pinned Python codegen dependencies"; \
			$(PY_CODEGEN_PIP) install -q --upgrade -r $(PY_CODEGEN_REQUIREMENTS); \
		fi; \
	else \
		echo "==> Installing pinned Python codegen dependencies"; \
		python3 -m venv $(PY_CODEGEN_VENV); \
		$(PY_CODEGEN_PIP) install -q --upgrade -r $(PY_CODEGEN_REQUIREMENTS); \
	fi; \
	rm -f "$$current_reqs"
	@echo "==> Generating Python Pydantic models"
	$(PY_CODEGEN) \
		--input $(OPENAPI3_SPEC) \
		--input-file-type openapi \
		--output sdks/python/src/e2a/v1/generated/ \
		--output-model-type pydantic_v2.BaseModel \
		--target-python-version 3.10 \
		--snake-case-field \
		--allow-population-by-field-name \
		--enum-field-as-literal all \
		--disable-timestamp

generate-check: swagger-check generate-sdk-check

generate-sdk-check: generate-sdk
	@echo "==> Checking generated code is up to date"
	git diff --exit-code sdks/typescript/src/v1/generated/ sdks/python/src/e2a/v1/generated/
