# dynamo-connect, the job that connects the three tools once. Pinned to Go 1.26
# as the tools are.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY samples ./samples
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dynamo-connect .
# The stack checks, which the chart's helm test runs against the installed suite.
RUN CGO_ENABLED=0 go test -c -tags stack -trimpath -ldflags="-s -w" -o /dynamo-check .

FROM alpine:3.22
# What dynamo-connect remembers between runs; a volume mounted here starts
# out owned by dynamo.
RUN adduser -D -u 10001 dynamo && install -d -o dynamo -g dynamo -m 700 /var/lib/dynamo-connect
COPY --from=build /dynamo-connect /usr/local/bin/dynamo-connect
COPY --from=build /dynamo-check /usr/local/bin/dynamo-check
USER dynamo
ENTRYPOINT ["/usr/local/bin/dynamo-connect"]
