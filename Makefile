# The GUI links the platform webview through cgo. On Linux it uses GTK 3 and
# WebKitGTK 4.1 (tag gtk3): older distributions have them, Wails' default
# GTK 4 / WebKitGTK 6 would leave them out. `nogui` builds the CLI and
# `qcs web` only, and cross-compiles anywhere.
TAGS ?=
ifeq ($(shell uname -s),Linux)
  TAGS += gtk3
endif

.PHONY: build cli test vet
build:
	go build -tags "$(TAGS)" -trimpath -o qcs ./cmd/qcs
cli:
	go build -tags nogui -trimpath -o qcs ./cmd/qcs
test:
	go test -tags "$(TAGS)" ./...
vet:
	go vet -tags "$(TAGS)" ./...
