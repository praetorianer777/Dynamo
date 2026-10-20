# Each area brings its own targets in mk/<area>.mk; run-tests.sh calls the
# gate targets (check-format, check-go, test-stack) when they exist.
.DEFAULT_GOAL := help

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
UID_GID := $(shell id -u):$(shell id -g)

include $(wildcard $(ROOT)/mk/*.mk)

.PHONY: test-all
test-all: ## The whole gate, exactly as CI and the push hook run it
	$(ROOT)/run-tests.sh

.PHONY: help
help:
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | sort
