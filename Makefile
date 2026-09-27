SHELL := /bin/sh
.DEFAULT_GOAL := help

GO ?= go

BACKEND := backend
# Native applications do not read Compose's environment file themselves.
LOAD_ENV = set -a; if [ -f .env ]; then . ./.env; fi; set +a;

.PHONY: help setup doctor db-up db-down db-reset backend-dev sample-target web-dev backend-check web-check verify format down

help:
	@printf '%s\n' \
	  'StatusForge development (run from the repository root; local-only, no AWS)' \
	  '  setup          Create missing .env, download Go modules, install locked web dependencies' \
	  '  doctor         Check native tools and Docker access' \
	  '  db-up/db-down  Start/wait for or stop DynamoDB Local on 127.0.0.1; data is retained' \
	  '  db-reset       DESTRUCTIVE: remove DynamoDB Local containers and data volume (CONFIRM=yes)' \
	  '  backend-dev    Run the API on 127.0.0.1:8080 with root .env configuration' \
	  '  sample-target  Run the controlled sample target fixture on 127.0.0.1:8090' \
	  '  web-dev        Run Vite on 127.0.0.1:5173 with the /api proxy' \
	  '  backend-check  gofumpt, golines (100 cols), go vet, race-enabled tests, build' \
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

backend-dev: .env
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/statusforge

sample-target: .env
	@$(LOAD_ENV) cd $(BACKEND) && $(GO) run ./cmd/sample-target

web-dev: .env
	@$(LOAD_ENV) npm --prefix web run dev

backend-check:
	@cd $(BACKEND) && unformatted="$$($(GO) tool gofumpt -l .)" && if [ -n "$$unformatted" ]; then printf 'formatting needed (run make format):\n%s\n' "$$unformatted"; exit 1; fi
	@cd $(BACKEND) && unformatted="$$($(GO) tool golines -l -m 100 .)" && if [ -n "$$unformatted" ]; then printf 'formatting needed (run make format):\n%s\n' "$$unformatted"; exit 1; fi
	cd $(BACKEND) && $(GO) vet ./...
	cd $(BACKEND) && $(GO) test -race -count=1 ./...
	cd $(BACKEND) && $(GO) build -o /dev/null ./...

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
