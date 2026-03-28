FROM golang:1.26-alpine AS builder

ARG VERSION=dev
WORKDIR /build
COPY . .
RUN go build -ldflags "-X main.Version=${VERSION}" -o /stack-agent ./cmd/stack-agent

FROM alpine:3.21

RUN adduser -D -u 1000 agent && mkdir -p /opt/stack-agent/data && chown -R agent:agent /opt/stack-agent

COPY --from=builder /stack-agent /stack-agent

USER agent
WORKDIR /opt/stack-agent

EXPOSE 2112

ENTRYPOINT ["/stack-agent"]
