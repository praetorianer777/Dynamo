# The suite on a server with Docker Compose alone (docs/deploying-compose.md):
# the same deploy/docker-compose.yml, its settings in deploy/.env, which
# deploy/compose-secrets.sh completes. Here: the realm it imports, and the
# test that starts the stack that way.

JQ := docker run --rm -i ghcr.io/jqlang/jq:1.7.1

# The canonical realm without the demo users, each tool's client sent back to
# that tool's address only and holding its secret as a placeholder, which
# Keycloak fills in from its environment when it imports the realm.
REALM_SERVER_FILTER := del(.users) | .clients |= map( \
	if (.clientId | IN("stator", "armature", "exciter")) then \
		(.clientId | ascii_upcase) as $$t \
		| .secret = "$${DYNAMO_\($$t)_CLIENT_SECRET}" \
		| .redirectUris = ["$${\($$t)_URL}/*"] \
		| .webOrigins = ["$${\($$t)_URL}"] \
	else . end)

.PHONY: realm-server
realm-server: ## Make deploy/keycloak/realm.server.json from the canonical realm again
	@$(JQ) '$(REALM_SERVER_FILTER)' < $(ROOT)/deploy/keycloak/realm.json > $(ROOT)/deploy/keycloak/realm.server.json.tmp
	@mv $(ROOT)/deploy/keycloak/realm.server.json.tmp $(ROOT)/deploy/keycloak/realm.server.json

# For tests/test-realm.sh, which compares it with the file.
.PHONY: realm-server-print
realm-server-print:
	@$(JQ) '$(REALM_SERVER_FILTER)' < $(ROOT)/deploy/keycloak/realm.json

# Two ports of the checkout's range that the development stack leaves free,
# so a gate may run both; recursive, since stack.mk is read after this file.
SERVER_PORT = $(call stack_port,5)

.PHONY: test-server
test-server: | $(GO_CACHE) ## Start the suite as a server does, over HTTPS from deploy/.env.example, and run the stack checks
	SERVER_PROJECT=$(STACK_PROJECT)-server SERVER_PORT=$(SERVER_PORT) GO_IMAGE=$(GO_IMAGE) $(ROOT)/scripts/server-test.sh
