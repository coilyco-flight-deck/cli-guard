.PHONY: build test vet lint tidy fmt cover docs-cli godoc-update release-artifacts release-package release-check pre-commit

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

fmt: ## Format Go source.
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

cover: ## Unit tests with a coverage profile.
	go test -coverprofile=coverage.out ./...

docs-cli: ## Render the CLI reference under site/cli/ via cli-web-docs.
	cd scripts/gen-webdocs && go run .

godoc-update: ## Regenerate godoc-current.txt; commit the diff to land API changes.
	./scripts/check-godoc-current.sh --update

release-artifacts: ## Build the tagged umbra binary matrix and SHA256SUMS.
	./scripts/build-umbra-release.sh "$(VERSION)" "$(or $(DIST_DIR),dist)"

release-package: ## Render Homebrew and Scoop metadata from tagged umbra binaries.
	./scripts/render-umbra-packaging.sh "$(VERSION)" "$(or $(DIST_DIR),dist)"

release-check: ## Verify umbra checksums, package metadata, and native version.
	./scripts/check-umbra-release.sh "$(VERSION)" "$(or $(DIST_DIR),dist)"

pre-commit: ## Run every repository hook.
	pre-commit run --all-files
