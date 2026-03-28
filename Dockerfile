FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY . .
RUN go build -o /stack-agent ./cmd/stack-agent

FROM alpine:3.21

RUN adduser -D -u 1000 agent

COPY --from=builder /stack-agent /stack-agent

USER agent

ENTRYPOINT ["/stack-agent"]
