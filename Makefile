.PHONY: all generate build clean

# Variables
TARGET_NAME := mcphost
CMD_PATH := ./cmd # Assuming main.go is in the root, or adjust to ./cmd if main is in cmd/mcphost.go
GO_FILES := $(shell find . -name '*.go' -not -path "./vendor/*")

all: build

# Generate yaegi symbols for system and history packages
generate:
	@echo "--- Generating yaegi symbols ---"
	@yaegi github.com/sannysanoff/mcphost/pkg/system github.com/sannysanoff/mcphost/pkg/history
	@echo "Generated github_com-sannysanoff-mcphost-pkg-system.go and github_com-sannysanoff-mcphost-pkg-history.go"

# Builds the application. Depends on 'generate' to ensure symbols are created first.
build: generate
	@echo "--- Building application ---"
	@go build -o $(TARGET_NAME) $(CMD_PATH)
	@echo "Build complete: $(TARGET_NAME)"

# Removes the built binary.
clean:
	@echo "--- Cleaning ---"
	@rm -f $(TARGET_NAME)
	# Add commands here to clean generated files if they are not in .gitignore
	# For example: @find . -name 'yaegi_symbols_*.go' -delete

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
