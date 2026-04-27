# Build a release binary for snapsec-agent. The agent runs as a systemd
# service on the host (not inside a container); this image is used purely
# as a portable build environment / artefact source for self-update.

FROM golang:1.22-alpine AS builder

ARG VERSION=dev
RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/snapsec-agent .

# Final stage: minimal scratch image so the binary can be copied out
# (e.g. `docker create --name x snapsec-agent && docker cp x:/snapsec-agent ./`).
FROM scratch
COPY --from=builder /out/snapsec-agent /snapsec-agent
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/snapsec-agent"]
