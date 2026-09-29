# Installing LightBacklog

This guide covers installing, configuring, upgrading and developing LightBacklog. For an overview of the project, see [README.md](README.md). The requirements are listed there.

## Install

```bash
git clone https://github.com/superbyteone/lightbacklog.git
cd lightbacklog
make up
```

`make up` creates `./data` and `./backups`, builds the image, and starts the container on
`http://127.0.0.1:8100`. Without `make`:

```bash
mkdir -p data backups
LB_UID=$(id -u) LB_GID=$(id -g) docker compose up -d --build
```

Create the first account (the first account is always an administrator). The password is read from
`$LB_PASSWORD` or stdin, never from arguments:

```bash
docker compose exec -e LB_PASSWORD='choose-a-strong-password' app /lightbacklog user create --username admin
```

Open <http://127.0.0.1:8100> and sign in. To reach it from other machines, put HTTPS in front of it; see
[docs/self-hosting.md](docs/self-hosting.md) for a ready-made Nginx configuration, backups, restore and upgrades.

## Configuration

Configuration is by environment variable (set them in `compose.yaml` under `environment:`).

| Variable | Default | Meaning |
|---|---|---|
| `LB_DATA_DIR` | `./data` (`/data` in the container) | Database and uploads |
| `LB_ADDR` | `127.0.0.1:8100` (`0.0.0.0:8100` in the container) | Listen address |
| `LB_BACKUP_DIR` | `./backups` (`/backups` in compose) | Where `backup` writes archives |
| `LB_BACKUP_KEEP_DAYS` | `14` | Archives older than this are pruned by `backup` |
| `LB_MAX_UPLOAD_MB` | `10` | Largest accepted upload |
| `LB_TRUST_PROXY` | `true` | Honour `X-Real-IP` and `X-Forwarded-Proto` from private-network peers (your reverse proxy) |
| `LB_COOKIE_SECURE` | `false` | Force the `Secure` flag on cookies (it is set automatically for HTTPS requests) |
| `LB_MCP_ALLOWED_CIDRS` | unset | Comma-separated IPs/CIDRs allowed to reach `/mcp` through a reverse proxy; unset means never (local-only). See [docs/self-hosting.md](docs/self-hosting.md). |
| `LB_MCP_RATE_RPS` / `LB_MCP_RATE_BURST` | `5` / `20` | Per-token MCP call rate limit |
| `LB_MCP_AUDIT_RETENTION_DAYS` | `90` | How long `mcp_audit_log` rows are kept |

## AI agents

Create a token and register the MCP server with your agent:

```bash
docker compose exec app /lightbacklog token create --user admin --name my-agent --scope write
claude mcp add --transport http lightbacklog http://127.0.0.1:8100/mcp --header "Authorization: Bearer $LIGHTBACKLOG_TOKEN"
```

Details for Claude Code, Codex, the REST API, and a suggested `AGENTS.md` snippet are in
[docs/agents.md](docs/agents.md).

## Backup, restore and upgrade

```bash
docker compose exec -T app /lightbacklog backup   # database snapshot + uploads, verified, into ./backups
git pull && make up                                # upgrade; migrations run automatically
```

Before any schema upgrade the previous database is snapshotted to `data/pre-migration/`. Restore, scheduled
backups and details are in [docs/self-hosting.md](docs/self-hosting.md).

## Development

```bash
make test          # go vet + go test in a Go container
```

The backend is Go (`cmd/lightbacklog`, `internal/`), the frontend is Svelte 5 + Vite + TypeScript in `web/`
(built into `web/dist` and embedded with `go:embed`), and schema migrations live in `migrations/`. See
[CONTRIBUTING.md](CONTRIBUTING.md) before sending changes.
