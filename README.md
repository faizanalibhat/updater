# Updater Service

A lightweight Go HTTP service that runs on **localhost only** and provides endpoints for Docker updates and remote shell access via [upterm](https://github.com/owenthereal/upterm).

## Endpoints

### `GET /api/health`
Health check — returns service status and current time.

### `POST /api/update`
Triggers a Docker Compose pull, up, and system prune to deploy new changes.

**Request Body** (all fields optional):
```json
{
  "services": ["vm", "auth"],
  "composeFile": "/path/to/docker-compose.yml",
  "workDir": "/root/staging"
}
```

**Response:**
```json
{
  "success": true,
  "steps": [
    {
      "name": "Docker Compose Pull",
      "command": "docker compose pull",
      "output": "...",
      "duration": "12.345s"
    },
    ...
  ]
}
```

### `POST /api/shell`
Starts an [upterm](https://github.com/owenthereal/upterm) terminal sharing session and returns the SSH connection details.

**Request Body** (all fields optional):
```json
{
  "command": "/bin/bash",
  "server": "ssh://uptermd.upterm.dev:22"
}
```

**Response:**
```json
{
  "success": true,
  "sessionId": "abc123...",
  "ssh": "ssh TOKEN@uptermd.upterm.dev",
  "host": "ssh://uptermd.upterm.dev:22",
  "command": "/bin/bash",
  "raw": { ... }
}
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `UPDATER_PORT` | `9876` | Port to listen on (always bound to 127.0.0.1) |
| `COMPOSE_WORK_DIR` | `/root/staging` | Working directory for docker compose commands |
| `COMPOSE_FILE` | *(auto)* | Path to docker-compose file |
| `UPTERM_SERVER` | `ssh://uptermd.upterm.dev:22` | Upterm server address |

## Prerequisites

- **Docker** and **Docker Compose** (for `/api/update`)
- **upterm** CLI installed in `$PATH` (for `/api/shell`)

## Running Locally

```bash
cd updater
go run .
```

## Building

```bash
go build -o updater .
./updater
```

## Docker

```bash
docker build -t updater .
docker run --network host -v /var/run/docker.sock:/var/run/docker.sock updater
```

> **Note**: The container needs access to the Docker socket to manage other containers, and `--network host` to bind to the host's localhost.
