FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o updater .

# --- Runtime stage ---
FROM alpine:3.20

RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
    docker-cli-compose \
    openssh-client \
    curl \
    bash

# Install upterm
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then ARCH="amd64"; elif [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi && \
    curl -fsSL "https://github.com/owenthereal/upterm/releases/latest/download/upterm_linux_${ARCH}.tar.gz" | \
    tar xz -C /usr/local/bin upterm

WORKDIR /app
COPY --from=builder /app/updater .

# Bind to localhost only
ENV UPDATER_PORT=9876

EXPOSE 9876

CMD ["./updater"]
