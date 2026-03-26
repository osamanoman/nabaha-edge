# Single-stage build — no external dependencies needed
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o nabaha-edge ./cmd/nabaha-edge/

# Minimal runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/nabaha-edge /usr/local/bin/nabaha-edge

EXPOSE 5060/udp 5060/tcp
EXPOSE 10000-20000/udp

ENV NABAHA_EDGE_TOKEN=""
ENV NABAHA_API_BASE="https://nabaha.otekit.com"

ENTRYPOINT ["nabaha-edge"]
