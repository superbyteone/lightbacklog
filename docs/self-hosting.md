# Self-hosting LightBacklog

This guide covers running LightBacklog on a server you control: HTTPS, backups, restore, and upgrades.
For a first try on your own machine, the quick start in [INSTALL.md](../INSTALL.md) is enough.

## What you need

- A Linux host with Docker and the Docker Compose plugin (v2).
- About 128 MB of RAM (idle use is 3 to 5 MB) and a little disk: the data is one SQLite file plus uploads.
- For access beyond your own machine: a hostname and a reverse proxy that terminates HTTPS
  (Nginx, Caddy, Traefik, ...). The application itself speaks plain HTTP on `127.0.0.1:8100`.

## Run it

```bash
git clone https://github.com/superbyteone/lightbacklog.git && cd lightbacklog
make up            # creates ./data and ./backups, builds the image, starts the container
docker compose exec -e LB_PASSWORD='choose-a-strong-password' app /lightbacklog user create --username admin
curl http://127.0.0.1:8100/healthz
```

The first account is always an administrator. Passwords are read from `$LB_PASSWORD` or stdin, never from
arguments.

## HTTPS with Nginx

[`deploy/nginx.example.conf`](../deploy/nginx.example.conf) is a complete virtual host: HTTP to HTTPS redirect,
ACME challenge directory, HSTS, rate limits for login and general traffic, correct handling of the live-update
stream (`/api/v1/events`, buffering off), an upload size limit, and `/mcp` blocked from the outside by default.
Replace `backlog.example.com` with your hostname, obtain a certificate (for example with certbot), then:

```bash
sudo nginx -t && sudo systemctl reload nginx
```

With another proxy, keep the same properties: pass `Host`, `X-Real-IP`, `X-Forwarded-For` and
`X-Forwarded-Proto`; do not buffer or time out `/api/v1/events`; do not expose `/mcp` unless you have deliberately
turned on remote MCP access as described below.

If the proxy is not on the same host, or the app is reachable from untrusted networks, review `LB_TRUST_PROXY`
in the configuration table in [INSTALL.md](../INSTALL.md).

## MCP: local-only by default, or a supported remote mode

MCP (the `/mcp` endpoint, used by AI agents) is **local-only by default**: the app binds to `127.0.0.1:8100`,
and refuses any request that arrives with proxy headers (`X-Forwarded-For` / `X-Real-IP`). Agents on the same
host connect to `http://127.0.0.1:8100/mcp` directly with a bearer token, exactly as [`docs/agents.md`](agents.md)
describes; nothing below is needed for that case.

Sometimes an external system genuinely needs to reach one instance's MCP server directly — for example, a
customer's own application talking to their LightBacklog instance. That is supported, but it is a deliberate
opt-in with its own protections, not just "turn off a safety check":

- **`LB_MCP_ALLOWED_CIDRS`** — a comma-separated list of the specific IPs/CIDRs allowed to reach `/mcp` through
  your reverse proxy (e.g. `203.0.113.4/32,198.51.100.0/24`). Unset (the default), every proxied request to
  `/mcp` is refused, regardless of anything else. `0.0.0.0/0` and `::/0` are rejected at startup — list the
  actual systems that need access, not "everything."
- **Per-token rate limiting** — every MCP call is capped per token (`LB_MCP_RATE_RPS`/`LB_MCP_RATE_BURST`,
  default 5 requests/sec with a burst of 20) so one leaked or malfunctioning credential can't overload the
  instance, and repeated bad bearer tokens from one peer are throttled the same way failed logins are.
- **Token expiry** — give an externally-used token a TTL (`token create --expires-days N`, or `expires_in_days`
  over the REST API) rather than a credential that's valid forever, and rotate it periodically with
  `token rotate` (or `POST /api/v1/tokens/{id}/rotate`) — the old secret stops working and the new one starts
  working atomically, so there's never a gap.
- **An audit trail** — every MCP call is recorded (token, tool, outcome, timing, client IP) in `mcp_audit_log`,
  kept for `LB_MCP_AUDIT_RETENTION_DAYS` (default 90). Inspect it with `lightbacklog mcp-audit list [--token id]`.

To turn this on: set `LB_MCP_ALLOWED_CIDRS` in `compose.yaml`, then switch `deploy/nginx.example.conf`'s `/mcp`
block from the default `return 404;` to the commented-out proxy block right below it (see that file — it also
adds its own rate-limit zone as defense-in-depth on top of the application's own allowlist and per-token limit).
Give the external system its own token, scoped to only the project(s) it needs and preferably read-only, with an
expiry — treat it like any other externally-shared credential.

## Backups

`lightbacklog backup` writes `lightbacklog-<UTC time>.tar.gz`: a consistent database snapshot (made with
`VACUUM INTO`) plus every uploaded file. It verifies the archive by reading it back and prunes archives older
than `LB_BACKUP_KEEP_DAYS` (default 14).

```bash
docker compose exec -T app /lightbacklog backup      # run one now
```

To run it daily, install the systemd units in `deploy/` (edit `WorkingDirectory` in the service first):

```bash
sudo cp deploy/lightbacklog-backup.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now lightbacklog-backup.timer
```

Copy `./backups` to another machine or storage; a backup on the same disk does not protect you from losing the
disk.

## Restore

`restore` extracts an archive into a **new, empty** directory and verifies it (SQLite integrity check, foreign
keys, every uploaded file present). It never touches live data.

```bash
docker compose down
mv data data.old-$(date +%F)
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD/backups:/backups:ro" -v "$PWD:/out" --read-only --tmpfs /tmp \
  lightbacklog:latest restore /backups/lightbacklog-<time>.tar.gz /out/data
make up
```

## Upgrade

```bash
git pull            # or download and unpack the new release
make up             # rebuilds the image and restarts the container
```

Schema migrations run automatically at start. When any are pending, a snapshot of the previous database is
first written to `data/pre-migration/`. Read the [changelog](../CHANGELOG.md) before upgrading.

## Admin CLI

Inside the container (`docker compose exec -T app /lightbacklog ...`):
`user create|list|set-password`, `token create|list|revoke|rotate`, `mcp-audit list`, `backup`, `restore`,
`healthcheck`, `version`.
