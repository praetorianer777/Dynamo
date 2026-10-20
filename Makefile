# Each area brings its own targets in mk/<area>.mk; run-tests.sh calls the
# gate targets (check-format, check-go, test-stack) when they exist.
.DEFAULT_GOAL := help

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
UID_GID := $(shell id -u):$(shell id -g)
# Here and not in mk/go.mk: the mk files are read in name order, and a rule
# in mk/chart.mk naming $(GO_CACHE) before go.mk set it depended on nothing,
# so Docker created the directory as root and the build cache was refused.
GO_CACHE := $(ROOT)/.cache/go

include $(wildcard $(ROOT)/mk/*.mk)

.PHONY: test-all
test-all: ## The whole gate, exactly as CI and the push hook run it
	$(ROOT)/run-tests.sh

.PHONY: help
help:
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | sort
