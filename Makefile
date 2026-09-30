SHELL := /bin/sh
.DEFAULT_GOAL := help

GO ?= go

BACKEND := backend
# Native applications do not read Compose's environment file themselves.
LOAD_ENV = set -a; if [ -f .env ]; then . ./.env; fi; set +a;

.PHONY: help setup doctor db-up db-down db-reset db-export db-import backend-dev sample-target notification-receiver sample-job web-dev backend-check local-isolation-check web-check verify format down build run security-check demo lambda-build

# The local binary must never link the cloud path (ADR 0008 D7): these
# packages and everything beneath them are cloud-only.
CLOUD_ONLY_PACKAGES := github.com/lavinhoque33/statusforge/backend/internal/hosteddynamo github.com/lavinhoque33/statusforge/backend/internal/cloudtargetpolicy github.com/lavinhoque33/statusforge/backend/internal/cloudwork github.com/aws/aws-lambda-go github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/sqs
EMPTY :=
SPACE := $(EMPTY) $(EMPTY)
CLOUD_ONLY_PATTERN := ^($(subst $(SPACE),|,$(CLOUD_ONLY_PACKAGES)))(/|$$)

help:
	@printf '%s\n' \
	  'StatusForge development (run from the repository root; local-only, no AWS)' \
	  '  setup          Create missing .env, download Go modules, install locked web dependencies' \
	  '  doctor         Check native tools and Docker access' \
	  '  db-up/db-down  Start/wait for or stop DynamoDB Local on 127.0.0.1; data is retained' \
	  '  db-reset       DESTRUCTIVE: remove DynamoDB Local containers and data volume (CONFIRM=yes)' \
	  '  db-export      Export private DynamoDB JSONL data (TABLE= optional, FILE= optional)' \
	  '  db-import      Import validated JSONL into empty table (FILE= and TABLE= required)' \
	  '  build/run      Build embedded web and versioned binary; run on loopback with DynamoDB Local' \
	  '  lambda-build   Cross-compile the planner and worker bootstrap binaries (linux/arm64; build only, no deploy)' \
	  '  backend-dev    Run the API on 127.0.0.1:8080 with root .env configuration' \
	  '  sample-target  Run the controlled sample target fixture on 127.0.0.1:8090' \
	  '  notification-receiver  Run the local notification receiver on 127.0.0.1:8091' \
	  '  sample-job     Run the sample heartbeat job fixture on 127.0.0.1:8092' \
	  '  web-dev        Run Vite on 127.0.0.1:5173 with the /api proxy' \
	  '  security-check  Run pinned local security checks (separate from verify)' \
	  '  demo           Seed and run the isolated demonstration table (DEMO_RESET=yes to reset)' \
	  '  backend-check  gofumpt, golines (100 cols), go vet, race-enabled tests, build, local dependency isolation' \
	  '  web-check      Lint, types, formatting, tests, production build' \
	  '  verify         Run both check suites' \
	  '  format         Apply golines + gofumpt and Prettier' \
	  '  down           Remove Compose containers/network; retain the data volume'

.env:
	cp .env.example .env

setup: .env
	cd $(BACKEND) && $(GO) mod download
	npm --prefix web ci

doctor:
	@sh infrastructure/scripts/doctor.sh

db-up: .env
	docker compose up -d --wait dynamodb

db-down:
	docker compose stop dynamodb

db-reset:
	@if [ "$(CONFIRM)" != "yes" ]; then \
	  printf '%s\n' 'Refusing: this deletes all local DynamoDB data. Re-run as: make db-reset CONFIRM=yes'; exit 1; fi
	docker compose down --volumes

db-export: .env
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/statusforge export $(if $(TABLE),--table $(TABLE),) $(if $(FILE),--out $(abspath $(FILE)),--out ../.local/exports/$(or $(TABLE),statusforge)-$$(date -u +%Y%m%dT%H%M%SZ).jsonl)

db-import: .env
	@if [ -z "$(FILE)" ] || [ -z "$(TABLE)" ]; then printf '%s\n' 'Specify FILE= and TABLE='; exit 2; fi
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/statusforge import --in $(abspath $(FILE)) --table $(TABLE)

build:
	npm --prefix web run build
	rm -rf backend/internal/webui/dist
	mkdir -p backend/internal/webui/dist backend/bin
	cp -R web/dist/. backend/internal/webui/dist/
	@version="$$(git describe --tags --always --dirty 2>/dev/null || printf dev)"; cd $(BACKEND) && $(GO) build -tags release -ldflags "-X github.com/lavinhoque33/statusforge/backend/internal/buildinfo.Version=$$version" -o bin/statusforge ./cmd/statusforge

run: db-up build
	@$(LOAD_ENV) $(if $(TABLE),STATUSFORGE_DYNAMODB_TABLE=$(TABLE)) ./backend/bin/statusforge

lambda-build:
	rm -rf backend/bin/lambda
	@version="$$(git describe --tags --always --dirty 2>/dev/null || printf dev)"; cd $(BACKEND) && for fn in planner worker; do \
	  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -tags lambda.norpc -trimpath -ldflags "-X github.com/lavinhoque33/statusforge/backend/internal/buildinfo.Version=$$version" -o bin/lambda/$$fn/bootstrap ./cmd/lambda-$$fn || exit 1; done

security-check:
	sh infrastructure/scripts/security-check.sh

demo: .env build db-up
	@$(LOAD_ENV) \
	  $(if $(STATUSFORGE_HTTP_ADDR),STATUSFORGE_HTTP_ADDR='$(STATUSFORGE_HTTP_ADDR)') \
	  $(if $(STATUSFORGE_SAMPLE_TARGET_ADDR),STATUSFORGE_SAMPLE_TARGET_ADDR='$(STATUSFORGE_SAMPLE_TARGET_ADDR)') \
	  $(if $(STATUSFORGE_RECEIVER_ADDR),STATUSFORGE_RECEIVER_ADDR='$(STATUSFORGE_RECEIVER_ADDR)') \
	  $(if $(STATUSFORGE_SAMPLE_JOB_ADDR),STATUSFORGE_SAMPLE_JOB_ADDR='$(STATUSFORGE_SAMPLE_JOB_ADDR)') \
	  $(if $(STATUSFORGE_DYNAMODB_ENDPOINT),STATUSFORGE_DYNAMODB_ENDPOINT='$(STATUSFORGE_DYNAMODB_ENDPOINT)') \
	  STATUSFORGE_DYNAMODB_TABLE=$(or $(DEMO_TABLE),statusforge_demo) sh infrastructure/scripts/demo.sh

backend-dev: .env
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/statusforge

sample-target: .env
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/sample-target

notification-receiver: .env
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/notification-receiver

sample-job: .env
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/sample-job

web-dev: .env
	@$(LOAD_ENV) npm --prefix web run dev

backend-check: local-isolation-check
	@cd $(BACKEND) && unformatted="$$($(GO) tool gofumpt -l .)" && if [ -n "$$unformatted" ]; then printf 'formatting needed (run make format):\n%s\n' "$$unformatted"; exit 1; fi
	@cd $(BACKEND) && unformatted="$$($(GO) tool golines -l -m 100 .)" && if [ -n "$$unformatted" ]; then printf 'formatting needed (run make format):\n%s\n' "$$unformatted"; exit 1; fi
	cd $(BACKEND) && $(GO) vet ./...
	cd $(BACKEND) && $(GO) test -race -count=1 ./...
	cd $(BACKEND) && $(GO) build -o /dev/null ./...

local-isolation-check:
	@cd $(BACKEND) && deps="$$($(GO) list -deps ./cmd/statusforge)" || exit 1; linked="$$(printf '%s\n' "$$deps" | grep -E '$(CLOUD_ONLY_PATTERN)')"; if [ -n "$$linked" ]; then printf 'cmd/statusforge must not link cloud-only packages:\n%s\n' "$$linked"; exit 1; fi; printf '%s\n' 'cmd/statusforge links no cloud-only packages'

web-check:
	npm --prefix web run lint
	npm --prefix web run typecheck
	npm --prefix web run format:check
	npm --prefix web test
	npm --prefix web run build

verify: backend-check web-check

format:
	cd $(BACKEND) && $(GO) tool golines -w -m 100 --base-formatter "$(GO) tool gofumpt" .
	cd $(BACKEND) && $(GO) tool gofumpt -w .
	npm --prefix web run format

down:
	docker compose down
