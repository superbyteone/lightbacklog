# Security policy

## Supported versions

Only the latest release receives security fixes.

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Use GitHub's private reporting instead:
open the repository's **Security** tab and choose **Report a vulnerability**. Include what you found, how to
reproduce it, and which version you tested. You will get a reply as soon as possible, and a fix will be
released before the details are made public.

## Hardening notes for operators

- The container listens on plain HTTP and is bound to `127.0.0.1`. Always put a reverse proxy with HTTPS in
  front of it before exposing it to a network (see [docs/self-hosting.md](docs/self-hosting.md)).
- The MCP endpoint (`/mcp`) is meant for local agents only by default. The server refuses any request that
  arrives with proxy headers unless `LB_MCP_ALLOWED_CIDRS` explicitly lists the external system(s) that need
  it; every MCP call is also rate-limited per token and recorded in an audit log
  (`lightbacklog mcp-audit list`). See "MCP: local-only by default, or a supported remote mode" in
  [docs/self-hosting.md](docs/self-hosting.md) before exposing it to anything beyond a local agent.
- API tokens are shown once. Give each agent its own token, prefer read-only or project-limited scopes, set an
  expiry for anything used outside your own machine (`token create --expires-days`), rotate periodically
  (`token rotate`), and revoke tokens you no longer use.
- Back up `./data` regularly and keep copies off the machine (see [INSTALL.md](INSTALL.md)).
