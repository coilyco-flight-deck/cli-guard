.PHONY: build test vet lint tidy cover docs docs-cli docs-serve godoc-update

build: ## Build all packages.
	go build ./...

test: ## Run the unit test suite.
	go test ./...

vet: ## go vet across the tree.
	go vet ./...

lint: ## Lint with golangci-lint.
	golangci-lint run ./...

tidy: ## go mod tidy.
	go mod tidy

cover: ## Unit tests with a coverage profile.
	go test -coverprofile=coverage.out ./...

docs: ## Build the mkdocs site into ./site.
	mkdocs build --strict

docs-cli: ## Overlay the per-example CLI reference under site/cli/ via cli-web-docs.
	cd scripts/gen-webdocs && go run .

docs-serve: ## Serve mkdocs locally with live reload on 127.0.0.1:8000.
	mkdocs serve

godoc-update: ## Regenerate godoc-current.txt; commit the diff to land API changes.
	./scripts/check-godoc-current.sh --update
