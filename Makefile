SHELL := /bin/bash

export GOCACHE := $(CURDIR)/.tmp/go-build
export GOMODCACHE := $(CURDIR)/.tmp/go-mod
export GOPATH := $(CURDIR)/.tmp/go
export GOTMPDIR := $(CURDIR)/.tmp/go-tmp
export TMPDIR := $(CURDIR)/.tmp/tmp
export XDG_CACHE_HOME := $(CURDIR)/.tmp/pnpm-cache
export XDG_DATA_HOME := $(CURDIR)/.tmp/pnpm-data

.PHONY: setup dirs frontend sdks build test test-release coverage check browser mockup

dirs:
	mkdir -p .tmp/bin .tmp/go-tmp .tmp/tmp

setup: dirs
	go mod download
	cd sdk/go && GOWORK=off go mod download
	cd e2e/sdk && GOWORK=off go mod download
	cd e2e/browser && GOWORK=off go mod download
	pnpm install --frozen-lockfile --store-dir .tmp/pnpm-store
	cd web/console && pnpm install --frozen-lockfile --store-dir ../../.tmp/pnpm-store
	cd sdk/js && pnpm install --frozen-lockfile --store-dir ../../.tmp/pnpm-store

frontend: dirs
	cd web/console && pnpm build

sdks: dirs
	cd sdk/go && GOWORK=off go build ./...
	cd sdk/js && pnpm build

build: frontend sdks
	go build -trimpath -o .tmp/bin/uniauth ./cmd/uniauth

test: coverage test-release
	cd web/console && pnpm test
	cd sdk/js && pnpm test && pnpm test:package
	bash scripts/test-go-sdk-consumer.sh
	cd e2e/sdk && GOWORK=off go test -race -count=1 -timeout=2m ./...

test-release: dirs
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_release_image.py'

coverage: frontend
	bash scripts/coverage.sh
	bash scripts/coverage.sh sdk/go

check: frontend
	python3 scripts/check-docs.py
	git diff --check
	test -z "$$(gofmt -l cmd internal sdk/go e2e/sdk e2e/browser web/console/*.go)"
	go vet ./...
	cd sdk/go && GOWORK=off go vet ./...
	cd e2e/sdk && GOWORK=off go vet ./...
	cd e2e/browser && GOWORK=off go vet ./...
	cd web/console && pnpm check && pnpm format:check
	cd sdk/js && pnpm check && pnpm format:check
	cd sdk/js && pnpm exec prettier --check ../../e2e/sdk/integration.mjs

browser: build
	pnpm test:browser

mockup:
	cd web/console && pnpm mockup
