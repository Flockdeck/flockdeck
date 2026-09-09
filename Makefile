BINARY  := agent-wrapper
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist

# On Windows the GUI subsystem is used so that launching the application from a
# shortcut or file manager does not flash a console window behind it. Standard
# handles are still inherited when it is started from a terminal, so output on
# the console keeps working.
WINFLAGS := $(LDFLAGS) -H=windowsgui

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

.PHONY: all build install test vet fmt check clean dist $(PLATFORMS)

all: check build

build:
ifeq ($(shell go env GOOS),windows)
	go build -ldflags "$(WINFLAGS)" -o $(BINARY).exe .
else
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .
endif

install:
	go install -ldflags "$(LDFLAGS)" .

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

$(PLATFORMS):
	@mkdir -p $(DIST)
	GOOS=$(word 1,$(subst /, ,$@)) GOARCH=$(word 2,$(subst /, ,$@)) \
		go build -ldflags "$(LDFLAGS)" \
		-o $(DIST)/$(BINARY)-$(word 1,$(subst /, ,$@))-$(word 2,$(subst /, ,$@))$(if $(filter windows,$(word 1,$(subst /, ,$@))),.exe,) .
	@echo "built $(DIST)/$(BINARY)-$(subst /,-,$@)"

clean:
	rm -rf $(DIST) $(BINARY) $(BINARY).exe
