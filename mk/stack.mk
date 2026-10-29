# The three tools at the versions versions.yaml pins, on one Keycloak. The
# project name and every published port derive from the checkout's path, so
# worktrees running their gates side by side never share a database or collide
# on a port. Every compose call finds the file and the project through the
# exported variables below.

STACK_HASH := $(shell printf '%s' '$(ROOT)' | cksum | cut -d' ' -f1)
STACK_PROJECT := dynamo-$(STACK_HASH)

# Ten ports per checkout from 30100, clear of Exciter's 10000 to 19999,
# Stator's 20000 to 29999, Exciter's suite at 30000 and the kernel's
# ephemeral ports from 32768.
STACK_PORT_SLOTS := 260
STACK_PORT_STRIDE := 10
STACK_PORT_BASE ?= $(shell echo $$((30100 + $(STACK_HASH) % $(STACK_PORT_SLOTS) * $(STACK_PORT_STRIDE))))
stack_port = $(shell echo $$(($(STACK_PORT_BASE) + $(1))))

KEYCLOAK_PORT     := $(call stack_port,0)
STATOR_WEB_PORT   := $(call stack_port,1)
ARMATURE_WEB_PORT := $(call stack_port,2)
EXCITER_WEB_PORT  := $(call stack_port,3)
MAILPIT_PORT      := $(call stack_port,4)

pin = $(shell sed -n 's/^$(1): *//p' $(ROOT)/versions.yaml)
STATOR_VERSION   := $(call pin,stator)
ARMATURE_VERSION := $(call pin,armature)
EXCITER_VERSION  := $(call pin,exciter)

# Where each tool's release workflow pushes its images, as <prefix>-<image>.
STATOR_IMAGES   ?= ghcr.io/praetorianer777/stator
ARMATURE_IMAGES ?= ghcr.io/cloudster1/armature
EXCITER_IMAGES  ?= ghcr.io/cloudster1/exciter

# Credentials for the throwaway stack, the tools' own development defaults
# where they have one; override in the environment for anything else. Client
# secrets are the realm's, in deploy/keycloak/realm.json.
KEYCLOAK_ADMIN    ?= admin
KEYCLOAK_PASSWORD ?= admin
S3_ACCESS_KEY ?= dynamo
S3_SECRET_KEY ?= dynamo-dev-secret

STATOR_POSTGRES_PASSWORD ?= stator
STATOR_APP_DB_PASSWORD   ?= stator_app
STATOR_ADMIN_DB_PASSWORD ?= stator_admin
STATOR_VALKEY_PASSWORD   ?= stator_valkey
STATOR_SECRET_KEY        ?= c3RhdG9yLWRldmVsb3BtZW50LW9ubHktc2VjcmV0LWs=
STATOR_ADMIN_EMAIL       ?= admin@stator.test
STATOR_ADMIN_PASSWORD    ?= stator admin password

ARMATURE_POSTGRES_PASSWORD ?= armature
ARMATURE_APP_DB_PASSWORD   ?= armature_app
ARMATURE_ADMIN_DB_PASSWORD ?= armature_admin
ARMATURE_VALKEY_PASSWORD   ?= armature_redis
ARMATURE_SECRET_KEY        ?= YXJtYXR1cmUtZGV2LWtleS1ub3QtYS1zZWNyZXQtMDA=
ARMATURE_ADMIN_EMAIL       ?= admin@armature.test
ARMATURE_ADMIN_PASSWORD    ?= armature admin password

EXCITER_POSTGRES_PASSWORD ?= exciter
EXCITER_APP_DB_PASSWORD   ?= exciter_app
EXCITER_ADMIN_DB_PASSWORD ?= exciter_admin
EXCITER_VALKEY_PASSWORD   ?= exciter_valkey
EXCITER_SECRET_KEY        ?= ZXhjaXRlci1kZXZlbG9wbWVudC1vbmx5LXNlY3JldC0=
EXCITER_ADMIN_EMAIL       ?= admin@exciter.test
EXCITER_ADMIN_PASSWORD    ?= exciter admin password

# Sign-in. Left empty, OIDC_ISSUER is the bundled Keycloak's realm dynamo,
# which the browser reaches on KEYCLOAK_PORT and the tools as keycloak:8080.
# Set it to use a Keycloak (or another realm) of one's own: the bundled one
# then stays off, and OIDC_BACKCHANNEL is "public-origin=reachable-origin"
# only when the tools cannot reach the issuer at its public address.
# docs/keycloak.md says what the realm must hold.
OIDC_ISSUER ?=
ifeq ($(strip $(OIDC_ISSUER)),)
OIDC_ISSUER := http://localhost:$(KEYCLOAK_PORT)/realms/dynamo
OIDC_BACKCHANNEL ?= http://localhost:$(KEYCLOAK_PORT)=http://keycloak:8080
export COMPOSE_PROFILES := keycloak
else
OIDC_BACKCHANNEL ?=
endif
OIDC_GROUPS_CLAIM ?= groups
STATOR_OIDC_CLIENT_ID       ?= stator
STATOR_OIDC_CLIENT_SECRET   ?= stator-dev-secret
ARMATURE_OIDC_CLIENT_ID     ?= armature
ARMATURE_OIDC_CLIENT_SECRET ?= armature-dev-secret
EXCITER_OIDC_CLIENT_ID      ?= exciter
EXCITER_OIDC_CLIENT_SECRET  ?= exciter-dev-secret
# The host the tools fetch the issuer's keys from, which their outbound
# guards must let through when it is a private address.
OIDC_REACH := $(if $(strip $(OIDC_BACKCHANNEL)),$(lastword $(subst =, ,$(OIDC_BACKCHANNEL))),$(OIDC_ISSUER))
OIDC_HOST := $(shell printf '%s' '$(OIDC_REACH)' | sed -E 's,^[a-z]+://([^/:]+).*,\1,')

# Who of the realm is let into all three before their first sign-in.
DYNAMO_MEMBERS ?= alice@suite.test=admin,bob@suite.test=member

# What a server sets in deploy/.env (deploy/.env.example), here at what the
# development stack has always been.
STATOR_URL   := http://localhost:$(STATOR_WEB_PORT)
ARMATURE_URL := http://localhost:$(ARMATURE_WEB_PORT)
EXCITER_URL  := http://localhost:$(EXCITER_WEB_PORT)
KEYCLOAK_URL := http://localhost:$(KEYCLOAK_PORT)
KEYCLOAK_DB_PASSWORD ?= keycloak
DYNAMO_ENV := development
SECURE_COOKIES := false
SMTP_ADDR := mailpit:1025
STATOR_MAIL_FROM   := Stator <no-reply@stator.test>
ARMATURE_MAIL_FROM := Armature <no-reply@armature.test>
EXCITER_MAIL_FROM  := Exciter <no-reply@exciter.test>
DYNAMO_CONNECT_IMAGE := dynamo-connect:dev

export COMPOSE_FILE := $(ROOT)/deploy/docker-compose.yml
export COMPOSE_PROJECT_NAME := $(STACK_PROJECT)
# Everything comes from here: a deploy/.env made to try the server setup must
# not reach the development stack, least of all its COMPOSE_PROFILES.
export COMPOSE_ENV_FILES := /dev/null
export KEYCLOAK_PORT STATOR_WEB_PORT ARMATURE_WEB_PORT EXCITER_WEB_PORT MAILPIT_PORT
export STATOR_VERSION ARMATURE_VERSION EXCITER_VERSION STATOR_IMAGES ARMATURE_IMAGES EXCITER_IMAGES
export KEYCLOAK_ADMIN KEYCLOAK_PASSWORD S3_ACCESS_KEY S3_SECRET_KEY DYNAMO_MEMBERS
export OIDC_ISSUER OIDC_BACKCHANNEL OIDC_GROUPS_CLAIM OIDC_HOST
export STATOR_OIDC_CLIENT_ID STATOR_OIDC_CLIENT_SECRET ARMATURE_OIDC_CLIENT_ID ARMATURE_OIDC_CLIENT_SECRET EXCITER_OIDC_CLIENT_ID EXCITER_OIDC_CLIENT_SECRET
export STATOR_URL ARMATURE_URL EXCITER_URL KEYCLOAK_URL KEYCLOAK_DB_PASSWORD DYNAMO_ENV SECURE_COOKIES
export SMTP_ADDR STATOR_MAIL_FROM ARMATURE_MAIL_FROM EXCITER_MAIL_FROM DYNAMO_CONNECT_IMAGE
export STATOR_POSTGRES_PASSWORD STATOR_APP_DB_PASSWORD STATOR_ADMIN_DB_PASSWORD STATOR_VALKEY_PASSWORD STATOR_SECRET_KEY STATOR_ADMIN_EMAIL STATOR_ADMIN_PASSWORD
export ARMATURE_POSTGRES_PASSWORD ARMATURE_APP_DB_PASSWORD ARMATURE_ADMIN_DB_PASSWORD ARMATURE_VALKEY_PASSWORD ARMATURE_SECRET_KEY ARMATURE_ADMIN_EMAIL ARMATURE_ADMIN_PASSWORD
export EXCITER_POSTGRES_PASSWORD EXCITER_APP_DB_PASSWORD EXCITER_ADMIN_DB_PASSWORD EXCITER_VALKEY_PASSWORD EXCITER_SECRET_KEY EXCITER_ADMIN_EMAIL EXCITER_ADMIN_PASSWORD

# What the stack publishes, for the cross-tool tests and for people: a shell
# can source it, and so can a Playwright config.
STACK_ENV_FILE := $(ROOT)/.cache/stack.env

.PHONY: stack-env
stack-env:
	@mkdir -p $(dir $(STACK_ENV_FILE))
	@printf '%s\n' \
		'COMPOSE_PROJECT_NAME=$(STACK_PROJECT)' \
		'OIDC_ISSUER=$(OIDC_ISSUER)' \
		'STATOR_URL=$(STATOR_URL)' \
		'ARMATURE_URL=$(ARMATURE_URL)' \
		'EXCITER_URL=$(EXCITER_URL)' \
		'MAILPIT_URL=http://localhost:$(MAILPIT_PORT)' \
		'STATOR_VERSION=$(STATOR_VERSION)' \
		'ARMATURE_VERSION=$(ARMATURE_VERSION)' \
		'EXCITER_VERSION=$(EXCITER_VERSION)' \
		'STATOR_ADMIN_EMAIL=$(STATOR_ADMIN_EMAIL)' \
		'STATOR_ADMIN_PASSWORD=$(STATOR_ADMIN_PASSWORD)' \
		'ARMATURE_ADMIN_EMAIL=$(ARMATURE_ADMIN_EMAIL)' \
		'ARMATURE_ADMIN_PASSWORD=$(ARMATURE_ADMIN_PASSWORD)' \
		'EXCITER_ADMIN_EMAIL=$(EXCITER_ADMIN_EMAIL)' \
		'EXCITER_ADMIN_PASSWORD=$(EXCITER_ADMIN_PASSWORD)' \
		'KROKI_DRAWS=stator,exciter' \
		> $(STACK_ENV_FILE)

.PHONY: check-host
check-host: ## Check that this machine can run the stack, and say what to do about what is missing
	@$(ROOT)/scripts/check-host.sh $(KEYCLOAK_PORT) $(STATOR_WEB_PORT) $(ARMATURE_WEB_PORT) $(EXCITER_WEB_PORT) $(MAILPIT_PORT)

# The connect job runs to its end before up returns, so a stack that came up
# is a connected one.
.PHONY: up
up: check-host stack-env ## Start all three at the pinned versions, connected to each other, and say where they are
	docker compose up -d --wait --quiet-pull
	docker compose run --rm --build connect
	@echo "Stator     http://localhost:$(STATOR_WEB_PORT)    $(STATOR_VERSION)"
	@echo "Armature   http://localhost:$(ARMATURE_WEB_PORT)    $(ARMATURE_VERSION)"
	@echo "Exciter    http://localhost:$(EXCITER_WEB_PORT)    $(EXCITER_VERSION)"
	@echo "Sign-in    $(OIDC_ISSUER)$(if $(filter keycloak,$(COMPOSE_PROFILES)),    admin / admin; alice / alice password)"
	@echo "Mailpit    http://localhost:$(MAILPIT_PORT)"
	@echo "Ports and versions are in $(STACK_ENV_FILE)."

.PHONY: connect
connect: ## Connect the running tools to each other again
	docker compose run --rm --build connect

# GITHUB_TOKEN, when set, raises GitHub's rate limit from 60 requests an hour to 5000.
SOURCES ?= sparkplug
.PHONY: sample-data
sample-data: ## Import sample data into the running tools: make sample-data SOURCES=evcc,owner/repo
	docker compose run --rm --build -e DYNAMO_SAMPLE_SOURCES='$(SOURCES)' -e DYNAMO_SAMPLE_MAX_ISSUES -e GITHUB_TOKEN connect sample-data

.PHONY: down
down: ## Stop the stack, keeping its data
	docker compose down

.PHONY: reset
reset: ## Stop the stack and delete its data, so the next up starts from nothing
	docker compose down -v --remove-orphans
	@rm -f $(STACK_ENV_FILE)

.PHONY: logs
logs: ## Follow the stack's logs: make logs S=stator-api for one service
	docker compose logs -f $(S)

.PHONY: ps
ps: ## Say which services run and how they are
	docker compose ps

# The gate's stack: what up does, without the greeting.
.PHONY: stack-up
stack-up: stack-env
	docker compose up -d --wait --quiet-pull
	docker compose run --rm --build connect

.PHONY: stack-down
stack-down:
	docker compose down -v --remove-orphans
	@rm -f $(STACK_ENV_FILE)

# Two runs of the connect job: the first wires the stack, the second must find
# nothing left to change. The sample data likewise, before the checks look at it.
.PHONY: test-stack
test-stack: stack-up
	docker compose run --rm connect sample-data
	docker compose run --rm -e DYNAMO_EXPECT_UNCHANGED=1 connect sample-data
	$(MAKE) test-signin CHECK_SAMPLE=sparkplug
	docker compose run --rm -e DYNAMO_EXPECT_UNCHANGED=1 connect

# Signs in through the realm as a browser would and tests every webhook, from
# the host's network, since the issuer is the address browsers use. With
# CHECK_SAMPLE=sparkplug it also checks the imported sample.
CHECK_SAMPLE ?=
.PHONY: test-signin
test-signin: | $(GO_CACHE) ## Sign in to all three through Keycloak and test every webhook on the running stack
	$(call go_run,--network host -e DYNAMO_STACK_ENV=/work/.cache/stack.env -e DYNAMO_SAMPLE_SOURCES=$(CHECK_SAMPLE)) go test -count=1 -tags stack -run Stack -v ./...

# The stack as compose reads it with the variables above, for tests/test-compose.sh.
.PHONY: compose-config
compose-config:
	@docker compose --profile connect config $(ARGS)
