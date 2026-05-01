# snapsec-agent

A small, single-binary Go agent that runs on every on-prem product host. It
is **capability-driven**: the [admin panel](https://admin.snapsec.co) returns
a list of *actions* in response to the agent's heartbeat, and the agent only
executes actions whose capability it has registered locally.

## Responsibilities

1. **Service install** — Registers itself as a systemd unit
   (`snapsec-agent.service`) that survives host restarts.
2. **Auto-update** — Each heartbeat may carry a `latest_version` /
   `download_url`; the agent atomically replaces its own binary and asks
   systemd to restart it.
3. **Capability execution** — Runs predefined capabilities against actions
   delivered in heartbeat responses.

## Capabilities

| Name | Description | Params |
|---|---|---|
| `update_application` | Runs `./setup.sh update` from the configured product install dir. | `install_dir` *(optional override)* |
| `repair_infra` | Stops infrastructure (`docker compose down`), restarts docker service, and runs `./setup.sh update`. | `install_dir` *(optional override)* |
| `set_license_expiry` | Connects to the local mongo and updates `license.expires_at` on an org document. | `org_id` *(hex ObjectId, required)*, `expires_at` *(RFC3339, required)*, `database`, `collection` |

## Workflow

```
+---------+   setup.sh install            +-------------+
|  Host   | ----------------------------> | snapsec-    |
| (root)  |   --admin-url/--enrollment    | agent       |
+---------+                               +------+------+
                                                 |
            (1) POST /api/v1/agents/register     |
                + hostname/os/arch/caps          v
                                          +-------------+
                                          | admin.      |
                                          | snapsec.co  |
                                          +------+------+
            (2) { agent_id }   <-----------------|
                stored in /etc/snapsec-agent/config.yaml
                                                 |
            (3) POST /api/v1/agents/heartbeat    |
                every N seconds         -------->|
            (4) { actions[], latest_version }    |
                <--------------------------------|
            (5) execute capabilities, report results next heartbeat
            (6) if latest_version != current  ->  self-update + restart
```

## Install on a host

```bash
# Build (or grab a release tarball)
go build -ldflags="-X main.version=$(git describe --tags --always)" -o snapsec-agent .

# Install as systemd service
sudo ./snapsec-agent --install \
  --admin-url https://admin.snapsec.co \
  --enrollment-token <ONE_TIME_TOKEN_FROM_ADMIN_PANEL> \
  --install-dir /root/staging \
  --mongo-uri mongodb://127.0.0.1:27017 \
  --mongo-db snapsec
```

This writes `/etc/systemd/system/snapsec-agent.service`, `enable`s it, and
starts it. The agent auto-registers and stores `agent_id` into
`/etc/snapsec-agent/config.yaml`.

```bash
sudo ./snapsec-agent --status     # systemctl status
sudo ./snapsec-agent --uninstall  # stop + remove unit
sudo ./snapsec-agent --version
journalctl -u snapsec-agent -f
```

## Configuration (`/etc/snapsec-agent/config.yaml`)

```yaml
agent_id: ""                   # filled in on first successful registration
admin_url: https://admin.snapsec.co
enrollment_token: ""           # cleared after registration
heartbeat_interval_seconds: 30
install_dir: /root/staging
mongo_uri: mongodb://127.0.0.1:27017
mongo_database: snapsec
current_version: ""
```

Override with `--config /path/to/config.yaml` or `SNAPSEC_AGENT_CONFIG=...`.

## API contract (admin side)

### `POST /api/v1/agents/register`

```json
{
  "hostname": "...",
  "os": "linux",
  "arch": "amd64",
  "agent_version": "v1.2.3",
  "capabilities": ["update_application", "set_license_expiry"],
  "enrollment_token": "..."
}
```

Response:

```json
{ "agent_id": "..." }
```

### `POST /api/v1/agents/heartbeat`

```json
{
  "agent_id": "...",
  "agent_version": "v1.2.3",
  "hostname": "...",
  "capabilities": ["update_application", "set_license_expiry"],
  "timestamp": "2026-04-27T10:00:00Z",
  "last_results": [
    {
      "action_id": "...",
      "capability": "update_application",
      "success": true,
      "output": "...",
      "started_at": "...",
      "completed_at": "..."
    }
  ]
}
```

Response:

```json
{
  "actions": [
    { "id": "act_1", "type": "update_application", "params": {} },
    { "id": "act_2", "type": "set_license_expiry",
      "params": { "org_id": "65...", "expires_at": "2027-01-01T00:00:00Z" } }
  ],
  "latest_version": "v1.2.4",
  "download_url": "https://releases.snapsec.co/agent/v1.2.4/snapsec-agent",
  "download_sha256": "abc...",
  "heartbeat_interval_seconds": 30
}
```

## Layout

```
main.go                          # CLI: --install / --uninstall / run
internal/config/                 # config.yaml load/save
internal/capabilities/           # registry + capability implementations
internal/agent/                  # client + register/heartbeat loop + self-update
```
