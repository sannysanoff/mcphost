.PHONY: all generate build clean

# Variables
TARGET_NAME := mcphost
CMD_PATH := ./cmd # Assuming main.go is in the root, or adjust to ./cmd if main is in cmd/mcphost.go
GO_FILES := $(shell find . -name '*.go' -not -path "./vendor/*")

all: build

# Generated files
GENERATED := github_com-sannysanoff-mcphost-pkg-system.go github_com-sannysanoff-mcphost-pkg-history.go

# Source files to track
SYSTEM_SRC := $(shell find pkg/system -type f -name '*.go')
HISTORY_SRC := $(shell find pkg/history -type f -name '*.go')

# Generate yaegi symbols only when sources change
github_com-sannysanoff-mcphost-pkg-system.go: $(SYSTEM_SRC)
	@echo "--- Generating system symbols ---"
	@yaegi github.com/sannysanoff/mcphost/pkg/system
	@touch $@

github_com-sannysanoff-mcphost-pkg-history.go: $(HISTORY_SRC)
	@echo "--- Generating history symbols ---"
	@yaegi github.com/sannysanoff/mcphost/pkg/history
	@touch $@

generate: $(GENERATED)

# Builds the application. Depends on 'generate' to ensure symbols are created first.
build: generate
	@echo "--- Building application ---"
	@go build -o $(TARGET_NAME) $(CMD_PATH)
	@echo "Build complete: $(TARGET_NAME)"

# Removes the built binary and generated files.
clean:
	@echo "--- Cleaning ---"
	@rm -f $(TARGET_NAME) $(GENERATED)

# Run tests
test:
	@echo "--- Running tests ---"
	@go test ./...

# Format Go files
fmt:
	@echo "--- Formatting Go files ---"
	@go fmt ./...

# Lint Go files (requires golangci-lint)
lint:
	@echo "--- Linting Go files ---"
	@golangci-lint run
