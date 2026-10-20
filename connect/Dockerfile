# dynamo-connect, the job that connects the three tools once. Pinned to Go 1.26
# as the tools are.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dynamo-connect .

FROM alpine:3.22
RUN adduser -D -u 10001 dynamo
COPY --from=build /dynamo-connect /usr/local/bin/dynamo-connect
USER dynamo
ENTRYPOINT ["/usr/local/bin/dynamo-connect"]
