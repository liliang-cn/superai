# SuperLeo
#
# A go.work one directory up lists sibling modules but not this one, so plain
# `go build ./...` here fails with "directory prefix . does not contain modules
# listed in go.work". Every Go/Wails command below pins GOWORK=off.

SHELL := /bin/bash
export GOWORK := off

APP  := build/bin/SuperLeo.app
DMG  := build/bin/SuperLeo.dmg
BIN  := $(APP)/Contents/MacOS/SuperLeo

.DEFAULT_GOAL := help

## help: list the targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'

## dev: run the app with hot-reloading frontend
dev:
	wails dev

## build: build the native app for this machine -> build/bin/SuperLeo.app
build:
	wails build

## run: build, then (re)launch the app
run: build
	@pkill -f "SuperLeo.app/Contents/MacOS/SuperLeo" || true
	@sleep 1
	open $(APP)

## package: universal (arm64 + Intel) app + drag-to-install .dmg
package:
	./scripts/package-macos.sh

## cli: build the headless one-prompt binary -> build/bin/superleo
cli:
	go build -o build/bin/superleo ./cmd/superleo

## web: build the windowless web-server binary -> build/bin/superleo-web (run: superleo-web serve)
web:
	cd frontend && npm run build
	go build -o build/bin/superleo-web .

## serve: build and run the web server on 127.0.0.1:43117
serve: web
	./build/bin/superleo-web serve

## cchook: build the small Claude Code hook for prompts and tool calls -> build/bin/superleo-cchook (ship it beside superleo)
cchook:
	go build -trimpath -ldflags "-s -w" -o build/bin/superleo-cchook ./cmd/superleo-cchook

## daemon: build the background scheduler binary
daemon:
	go build -o build/bin/superleo-daemon ./cmd/superleo-daemon

## install-daemon: install the launchd job that fires schedules while the app is closed
install-daemon:
	./scripts/install-daemon.sh

## uninstall-daemon: remove the launchd job (schedules are kept)
uninstall-daemon:
	./scripts/install-daemon.sh --uninstall

## daemon-status: is the scheduler running, and what is scheduled
daemon-status:
	./scripts/install-daemon.sh --status

## bindings: regenerate the Wails TypeScript bindings after changing App methods
bindings:
	wails generate module

## test: go vet + the full Go test suite
test:
	go vet ./...
	go test ./... -count=1 -timeout 300s

## smoke: live end-to-end test against the configured model (needs credentials)
smoke:
	SUPERLEO_SMOKE=1 go test ./backend/ -run TestSmokeLive -v -count=1 -timeout 15m

## check: everything CI would run — Go tests plus a frontend typecheck and build
check: test
	cd frontend && npx tsc --noEmit
	cd frontend && npm run build

## fmt: gofmt the Go sources
fmt:
	gofmt -w $(shell find . -name '*.go' -not -path './frontend/*')

## deps: install frontend dependencies
deps:
	cd frontend && npm install

## clean: remove build output and the frontend bundle
clean:
	rm -rf build/bin frontend/dist

.PHONY: help dev build run package cli cchook daemon install-daemon uninstall-daemon daemon-status bindings test smoke check fmt deps clean
