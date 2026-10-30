# The upgrade from the combination versions-upgrade-from.yaml names to the
# pins, on the stack's ports under a Compose project of its own, so it never
# touches the data of make up's stack.

.PHONY: test-upgrade
test-upgrade: | $(GO_CACHE) ## Install the previous combination, give people access, upgrade to the pins and check nothing was lost
	COMPOSE_PROJECT_NAME=$(STACK_PROJECT)-upgrade GO_IMAGE=$(GO_IMAGE) $(ROOT)/scripts/upgrade-test.sh
