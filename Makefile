# Kumo build
#   make            build web UI + server binary (./dist/kumo)
#   make desktop    also install the Electron shell's deps
#   make test       run the Go tests
#   make windows    cross-compile the Windows server (./dist/windows/kumo.exe);
#                   the Windows installer itself is built on Windows, see
#                   packaging/windows/README.md

PREFIX ?= /usr
GO ?= go
NPM ?= npm
# Not named LDFLAGS/GOFLAGS on purpose: makepkg exports C linker flags in
# LDFLAGS (e.g. -Wl,-O1) which the Go linker doesn't understand.
GO_BUILDFLAGS ?= -trimpath
GO_LDFLAGS ?= -s -w

.PHONY: all web embed server desktop test clean run windows

all: server

web:
	cd web && $(NPM) ci --no-audit --no-fund && $(NPM) run build

embed: web
	rm -rf server/internal/webui/dist
	mkdir -p server/internal/webui/dist
	cp -r web/dist/. server/internal/webui/dist/
	touch server/internal/webui/dist/.keep

server: embed
	mkdir -p dist
	cd server && CGO_ENABLED=0 $(GO) build $(GO_BUILDFLAGS) -ldflags "$(GO_LDFLAGS)" -o ../dist/kumo ./cmd/kumo

windows: embed
	mkdir -p dist/windows
	cd server && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GO_BUILDFLAGS) -ldflags "$(GO_LDFLAGS)" -o ../dist/windows/kumo.exe ./cmd/kumo

desktop:
	cd desktop && $(NPM) ci --no-audit --no-fund

test:
	cd server && $(GO) test ./...

run: server
	./dist/kumo --web-ui

clean:
	rm -rf dist web/dist server/internal/webui/dist/* desktop/node_modules
