# Stage 1: Build the nabaha-edge Go binary
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o nabaha-edge ./cmd/nabaha-edge/

# Stage 2: Get LiveKit SIP binary
FROM livekit/sip:latest AS sip

# Stage 3: Final minimal image
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates libopus0 libopusfile0 libsoxr0 \
    && rm -rf /var/lib/apt/lists/*

# Copy binaries
COPY --from=builder /build/nabaha-edge /usr/local/bin/nabaha-edge
COPY --from=sip /usr/bin/livekit-sip /usr/local/bin/livekit-sip

# Config directory
RUN mkdir -p /etc/nabaha-edge

# SIP ports
EXPOSE 5060/udp 5060/tcp
# RTP ports
EXPOSE 10000-20000/udp
# Health check
EXPOSE 8080/tcp

ENV NABAHA_EDGE_TOKEN=""
ENV NABAHA_API_BASE="https://nabaha.otekit.com"

ENTRYPOINT ["nabaha-edge"]
