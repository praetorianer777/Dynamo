# The umbrella chart, tested in a kind cluster. The kind, helm and kubectl it
# needs run in a container of their own (scripts/kind/Dockerfile).
KIND_TOOLS_IMAGE := dynamo-kind-tools:dev
CONNECT_IMAGE := ghcr.io/praetorianer777/dynamo-connect:$(shell sed -n 's/^appVersion: *"\{0,1\}\([^"]*\)"\{0,1\}/\1/p' $(ROOT)/deploy/charts/dynamo/Chart.yaml)

.PHONY: test-chart
test-chart: | $(GO_CACHE) ## Install the umbrella chart into kind and check the three come up connected
	docker build -q -t $(CONNECT_IMAGE) $(ROOT)/connect >/dev/null
	$(call go_run,-e CGO_ENABLED=0) go test -c -tags stack -o /work/.cache/stacktest .
	docker build -q -t $(KIND_TOOLS_IMAGE) $(ROOT)/scripts/kind >/dev/null
	docker run --rm --network host -v /var/run/docker.sock:/var/run/docker.sock -v $(ROOT):/work:ro \
		-e DYNAMO_CHARTS_FROM_SOURCE -e DYNAMO_KEEP_CLUSTER \
		-e DYNAMO_STATOR_SOURCE -e DYNAMO_ARMATURE_SOURCE -e DYNAMO_EXCITER_SOURCE $(KIND_TOOLS_IMAGE) /work/scripts/chart-test.sh
