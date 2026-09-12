BINARY  := flockdeck
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist

# On Windows the GUI subsystem is used so that launching the application from a
# shortcut or file manager does not flash a console window behind it. A program
# linked that way is given no console, even by a terminal, so the program
# borrows the terminal's itself (useConsole in console_windows.go).
WINFLAGS := $(LDFLAGS) -H=windowsgui

# Beside it on Windows goes $(CHAT).exe, the same program linked as a console
# program. An API agent's pane runs Flockdeck's own chat client as its process,
# and a pane is a pseudo-console, which Windows attaches only to console
# programs; run from the GUI build the pane stays blank.
CHAT := $(BINARY)-chat

# Where go install puts programs, for the twin it cannot install itself.
GOBIN_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

# Every target builds with cgo disabled, so all platforms cross-compile from
# any one machine with nothing but the Go toolchain installed.
export CGO_ENABLED = 0

PLATFORMS := \
	windows/amd64 \
	windows/arm64 \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64

.PHONY: all build install test race vet fmt check clean dist package $(PLATFORMS)

all: check build

build:
ifeq ($(shell go env GOOS),windows)
	go build -ldflags "$(WINFLAGS)" -o $(BINARY).exe .
	go build -ldflags "$(LDFLAGS)" -o $(CHAT).exe .
else
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .
endif

install:
ifeq ($(shell go env GOOS),windows)
	go install -ldflags "$(WINFLAGS)" .
	go build -ldflags "$(LDFLAGS)" -o "$(GOBIN_DIR)/$(CHAT).exe" .
else
	go install -ldflags "$(LDFLAGS)" .
endif

test:
	go test ./...

# The race detector needs a C toolchain; skipped by default because the app
# itself never requires one.
race:
	CGO_ENABLED=1 go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: vet test

dist: $(PLATFORMS)

# The Windows builds take the GUI subsystem here too, as build and package do;
# without it a binary from dist opens a console window behind the interface.
$(PLATFORMS):
	@mkdir -p $(DIST)
	GOOS=$(word 1,$(subst /, ,$@)) GOARCH=$(word 2,$(subst /, ,$@)) \
		go build -ldflags "$(if $(filter windows,$(word 1,$(subst /, ,$@))),$(WINFLAGS),$(LDFLAGS))" \
		-o $(DIST)/$(BINARY)-$(word 1,$(subst /, ,$@))-$(word 2,$(subst /, ,$@))$(if $(filter windows,$(word 1,$(subst /, ,$@))),.exe,) .
	$(if $(filter windows,$(word 1,$(subst /, ,$@))),GOOS=windows GOARCH=$(word 2,$(subst /, ,$@)) \
		go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(CHAT)-windows-$(word 2,$(subst /, ,$@)).exe .,)
	@echo "built $(DIST)/$(BINARY)-$(subst /,-,$@)"

# package cross-builds every platform and writes the archives and checksums a
# release is made of, which is exactly what CI publishes. Run it before tagging
# to see what a release would contain, or to hand someone a build.
package:
	go run ./cmd/release -version $(VERSION) -out $(DIST)

clean:
	rm -rf $(DIST) $(BINARY) $(BINARY).exe $(CHAT).exe
