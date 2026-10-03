# Bibli — developer shortcuts. None of this is needed to run Bibli:
# there, it is a single binary (README) or `docker compose up -d`.

DEV_ADDR ?= 127.0.0.1:8080
DEV_DB   ?= data/biblio.db

.PHONY: help dev dev-docker stop build dist demo test check fmt clean

help: ## Show this help
	@echo "Bibli — available targets:"
	@grep -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | \
		awk -F'\t' '{printf "  %-12s %s\n", $$1, $$2}'

dev: stop ## Run the dev server, rebuilding on every change (no Docker)
	@BIBLI_DEV_ADDR=$(DEV_ADDR) BIBLI_DEV_DB=$(DEV_DB) sh scripts/dev.sh

dev-docker: ## Same, but through the image an organisation would run
	docker compose -f docker-compose.dev.yml up --build

stop: ## Stop the Docker dev container, which would hold the port and the database
	@if [ -n "$$(docker ps -q -f name=bibli-dev 2>/dev/null)" ]; then \
		echo "stopping the bibli-dev container, which holds $(DEV_ADDR) and $(DEV_DB)"; \
		docker stop bibli-dev >/dev/null; \
	fi

build: ## Compile the binary into dist/bibli
	go build -o dist/bibli ./app

dist: ## Build the Mac, Windows and Linux desktop packages into dist/
	packaging/macos.sh dist
	packaging/windows.sh dist
	packaging/linux.sh dist

demo: build stop ## Reset DEV_DB and fill it with the demo dataset (books, borrowers, loans)
	@mkdir -p $(dir $(DEV_DB))
	@rm -f $(DEV_DB)
	@BIBLI_ADMIN_PASSWORD=dev dist/bibli -db $(DEV_DB) -addr 127.0.0.1:0 -backup-dir "" -cache-dir "" -secure-cookies=false & \
		pid=$$!; \
		for i in $$(seq 1 30); do \
			sqlite3 $(DEV_DB) "SELECT 1 FROM sqlite_master WHERE name = 'book'" 2>/dev/null | grep -q 1 && break; \
			sleep 0.1; \
		done; \
		kill $$pid 2>/dev/null; wait $$pid 2>/dev/null || true
	sqlite3 $(DEV_DB) < app/demo.sql
	@echo "demo data loaded into $(DEV_DB)"

test: ## Run the test suite
	go test ./...

check: ## What must pass before every commit
	gofmt -l .
	go vet ./...
	GOOS=windows go vet ./...
	go test ./...

fmt: ## Format the Go sources in place
	gofmt -w .

clean: ## Remove build artefacts
	rm -rf dist
