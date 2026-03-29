FROM golang:1.26-alpine AS builder

ARG VERSION=dev
WORKDIR /build
COPY . .
RUN go build -ldflags "-X main.Version=${VERSION}" -o /steward ./cmd/steward

FROM docker:27-cli

RUN mkdir -p /opt/steward/data

COPY --from=builder /steward /steward

WORKDIR /opt/steward

EXPOSE 2112

ENTRYPOINT ["/steward"]
