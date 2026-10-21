# The umbrella chart, tested in a kind cluster. The kind, helm and kubectl it
# needs run in a container of their own (scripts/kind/Dockerfile).
KIND_TOOLS_IMAGE := dynamo-kind-tools:dev
CONNECT_IMAGE := ghcr.io/praetorianer777/dynamo-connect:$(shell sed -n 's/^appVersion: *"\{0,1\}\([^"]*\)"\{0,1\}/\1/p' $(ROOT)/deploy/charts/dynamo/Chart.yaml)

.PHONY: test-chart
test-chart: ## Install the umbrella chart into kind and check the three come up connected
	[ -n "$$DYNAMO_FROM_RELEASE" ] || docker build -q -t $(CONNECT_IMAGE) $(ROOT)/connect >/dev/null
	docker build -q -t $(KIND_TOOLS_IMAGE) $(ROOT)/scripts/kind >/dev/null
	docker run --rm --network host -v /var/run/docker.sock:/var/run/docker.sock -v $(ROOT):/work:ro \
		-e DYNAMO_CHARTS_FROM_SOURCE -e DYNAMO_FROM_RELEASE -e DYNAMO_KEEP_CLUSTER \
		-e DYNAMO_STATOR_SOURCE -e DYNAMO_ARMATURE_SOURCE -e DYNAMO_EXCITER_SOURCE $(KIND_TOOLS_IMAGE) /work/scripts/chart-test.sh

CHART_VERSION := $(shell sed -n 's/^version: *//p' $(ROOT)/deploy/charts/dynamo/Chart.yaml)
CHART_REGISTRY := oci://ghcr.io/praetorianer777/charts
DOCKER_HELM = docker run --rm -u $(shell id -u):$(shell id -g) -e HOME=/tmp -v $(ROOT):/work -w /work

.PHONY: chart-package chart-push
chart-package: ## Package the umbrella chart with the tools' charts it pins into dist/
	docker build -q -t $(KIND_TOOLS_IMAGE) $(ROOT)/scripts/kind >/dev/null
	$(DOCKER_HELM) $(KIND_TOOLS_IMAGE) sh -c 'helm dependency build deploy/charts/dynamo && helm package deploy/charts/dynamo -d dist'

chart-push: ## Push dist's chart to the registry: REGISTRY_USER and REGISTRY_TOKEN may push there
	$(DOCKER_HELM) -e REGISTRY_USER -e REGISTRY_TOKEN $(KIND_TOOLS_IMAGE) sh -c \
		'echo "$$REGISTRY_TOKEN" | helm registry login ghcr.io -u "$$REGISTRY_USER" --password-stdin >/dev/null && helm push dist/dynamo-$(CHART_VERSION).tgz $(CHART_REGISTRY)'
