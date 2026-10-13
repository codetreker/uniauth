SHELL := /bin/bash

export GOCACHE := $(CURDIR)/.tmp/go-build
export GOMODCACHE := $(CURDIR)/.tmp/go-mod
export GOPATH := $(CURDIR)/.tmp/go
export GOTMPDIR := $(CURDIR)/.tmp/go-tmp
export TMPDIR := $(CURDIR)/.tmp/tmp
export XDG_CACHE_HOME := $(CURDIR)/.tmp/pnpm-cache
export XDG_DATA_HOME := $(CURDIR)/.tmp/pnpm-data

.PHONY: setup dirs frontend build test coverage check browser mockup

dirs:
	mkdir -p .tmp/bin .tmp/go-tmp .tmp/tmp

setup: dirs
	go mod download
	pnpm install --frozen-lockfile --store-dir .tmp/pnpm-store
	cd web/console && pnpm install --frozen-lockfile --store-dir ../../.tmp/pnpm-store

frontend: dirs
	cd web/console && pnpm build

build: frontend
	go build -trimpath -o .tmp/bin/uniauth ./cmd/uniauth

test: coverage
	cd web/console && pnpm test

coverage: frontend
	bash scripts/coverage.sh

check: frontend
	python3 scripts/check-docs.py
	git diff --check
	test -z "$$(gofmt -l cmd internal web/console/*.go)"
	go vet ./...
	cd web/console && pnpm check && pnpm format:check

browser: build
	pnpm test:browser

mockup:
	cd web/console && pnpm mockup
