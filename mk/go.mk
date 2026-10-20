# dynamo-connect's Go. Every target runs the toolchain in a container as the
# invoking user, with module and build caches under .cache/ so they survive
# between runs. Pinned to 1.26, as the tools are.
GO_IMAGE ?= golang:1.26-bookworm

go_run = docker run --rm \
	-u $(UID_GID) \
	-v $(ROOT):/work \
	-v $(GO_CACHE):/cache \
	-e HOME=/tmp \
	-e GOCACHE=/cache/build \
	-e GOMODCACHE=/cache/mod \
	-e GOFLAGS=-buildvcs=false \
	-e GOTOOLCHAIN=local \
	$(1) \
	-w /work/connect $(GO_IMAGE)
DOCKER_GO = $(call go_run,)

$(GO_CACHE):
	@mkdir -p $@

.PHONY: fmt
fmt: | $(GO_CACHE) ## Format the Go sources in place
	$(DOCKER_GO) gofmt -w .

.PHONY: check-go
check-go: | $(GO_CACHE) ## Vet and test dynamo-connect
	$(DOCKER_GO) sh -c 'go vet ./... && go test -race -count=1 ./...'

.PHONY: check-format
check-format: | $(GO_CACHE) ## Refuse Go sources gofmt would change
	@out="$$($(DOCKER_GO) gofmt -l .)"; [ -z "$$out" ] || { echo "Run make fmt; gofmt would change: $$out"; exit 1; }
