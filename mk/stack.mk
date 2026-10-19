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

# Who of the realm is let into all three before their first sign-in.
DYNAMO_MEMBERS ?= alice@suite.test=admin,bob@suite.test=member

export COMPOSE_FILE := $(ROOT)/deploy/docker-compose.yml
export COMPOSE_PROJECT_NAME := $(STACK_PROJECT)
export KEYCLOAK_PORT STATOR_WEB_PORT ARMATURE_WEB_PORT EXCITER_WEB_PORT MAILPIT_PORT
export STATOR_VERSION ARMATURE_VERSION EXCITER_VERSION STATOR_IMAGES ARMATURE_IMAGES EXCITER_IMAGES
export KEYCLOAK_ADMIN KEYCLOAK_PASSWORD S3_ACCESS_KEY S3_SECRET_KEY DYNAMO_MEMBERS
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
		'KEYCLOAK_URL=http://localhost:$(KEYCLOAK_PORT)' \
		'STATOR_URL=http://localhost:$(STATOR_WEB_PORT)' \
		'ARMATURE_URL=http://localhost:$(ARMATURE_WEB_PORT)' \
		'EXCITER_URL=http://localhost:$(EXCITER_WEB_PORT)' \
		'MAILPIT_URL=http://localhost:$(MAILPIT_PORT)' \
		'STATOR_VERSION=$(STATOR_VERSION)' \
		'ARMATURE_VERSION=$(ARMATURE_VERSION)' \
		'EXCITER_VERSION=$(EXCITER_VERSION)' \
		> $(STACK_ENV_FILE)

# The connect job runs to its end before up returns, so a stack that came up
# is a connected one.
.PHONY: up
up: stack-env ## Start all three at the pinned versions, connected to each other, and say where they are
	docker compose up -d --wait --quiet-pull
	docker compose run --rm --build connect
	@echo "Stator     http://localhost:$(STATOR_WEB_PORT)    $(STATOR_VERSION)"
	@echo "Armature   http://localhost:$(ARMATURE_WEB_PORT)    $(ARMATURE_VERSION)"
	@echo "Exciter    http://localhost:$(EXCITER_WEB_PORT)    $(EXCITER_VERSION)"
	@echo "Keycloak   http://localhost:$(KEYCLOAK_PORT)    admin / admin; realm dynamo; alice / alice password"
	@echo "Mailpit    http://localhost:$(MAILPIT_PORT)"
	@echo "Ports and versions are in $(STACK_ENV_FILE)."

.PHONY: connect
connect: ## Connect the running tools to each other again
	docker compose run --rm --build connect

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
# nothing left to change.
.PHONY: test-stack
test-stack: stack-up
	docker compose run --rm -e DYNAMO_EXPECT_UNCHANGED=1 connect

# The stack as compose reads it with the variables above, for tests/test-compose.sh.
.PHONY: compose-config
compose-config:
	@docker compose --profile connect config $(ARGS)
