GO ?= go
PYTHON ?= python3
NPM ?= npm

.PHONY: build ci fmt fmt-check helm profiles sdk-test security test vet

build:
	$(GO) build ./...

fmt:
	gofmt -w internal cmd

fmt-check:
	@test -z "$$(gofmt -l internal cmd)" || { \
		echo "gofmt is required for:"; gofmt -l internal cmd; exit 1; \
	}

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

profiles:
	$(GO) run ./cmd/runeward --config-dir examples/safe validate --strict

sdk-test:
	PYTHONPATH=adapters/python $(PYTHON) -m unittest discover -s adapters/python/tests
	cd adapters/typescript && $(NPM) ci --ignore-scripts && $(NPM) test

helm:
	helm lint deploy/helm/runeward
	helm template runeward deploy/helm/runeward --set server.enabled=true >/dev/null

security:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...

# Mirrors the source, SDK, Charter, Helm, and reachable-vulnerability checks
# required before merge. Container scans and runtime E2E tests remain CI gates.
ci: fmt-check vet test build profiles sdk-test helm security
