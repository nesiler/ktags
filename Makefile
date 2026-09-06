GO      ?= go
PKGS    := ./...
GOFILES := $(shell git ls-files '*.go' 2>/dev/null | grep -v '^spikes/')

.PHONY: help hooks check fmt fmt-check vet lint test build guard spike-%

help: ## list targets
	@grep -E '^[a-zA-Z_%-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*## "}{printf "  %-12s %s\n", $$1, $$2}'

hooks: ## activate .githooks for this clone (once)
	git config core.hooksPath .githooks
	chmod +x .githooks/* scripts/guard.sh
	@echo "hooks active: $$(git config core.hooksPath)"

check: fmt-check vet lint test ## the gate: fmt + vet + lint + test -race

fmt: ## gofmt in place
	@[ -z "$(GOFILES)" ] || gofmt -w $(GOFILES)

fmt-check: ## fail if gofmt would change anything
	@out="$$( [ -z "$(GOFILES)" ] || gofmt -l $(GOFILES) )"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet $(PKGS)

lint: ## golangci-lint (v2)
	golangci-lint run $(PKGS)

test: ## go test -race
	$(GO) test -race -count=1 $(PKGS)

build: ## build the binary into ./bin
	$(GO) build -o bin/ktags ./cmd/ktags

guard: ## doctor: tools, hooks, gh auth, attribution
	scripts/guard.sh doctor

spike-%: ## run a TUI spike, e.g. make spike-tui-04-bubbletea-palette
	cd spikes/$* && $(GO) run .
