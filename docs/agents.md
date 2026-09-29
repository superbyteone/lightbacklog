# Using LightBacklog from AI agents

LightBacklog is designed so Claude Code, Codex and other agents on the same machine can manage projects and
backlogs without a browser. There are two equivalent interfaces over the same business logic:

- **MCP** (recommended): `http://127.0.0.1:8100/mcp`, 18 tools.
- **REST**: `/api/v1`, described by [`/api/v1/openapi.yaml`](../internal/api/openapi.yaml).

Agents connect to the **local** address directly, by default: a reverse proxy in front of the server should not
expose `/mcp`, and the server refuses MCP requests that arrive through a proxy unless you have explicitly set
`LB_MCP_ALLOWED_CIDRS` to the specific external IPs/CIDRs that need remote access (see "MCP: local-only by
default, or a supported remote mode" in [docs/self-hosting.md](self-hosting.md)). Every MCP call is also rate-limited
per token and recorded in an audit log (`lightbacklog mcp-audit list`).

## 1. Create a token

Tokens act as their owner, can be `read` or `write`, and can be limited to specific projects. They are shown once.

```bash
# inside the container:
docker compose exec app /lightbacklog token create --user admin --name claude-code --scope write
docker compose exec app /lightbacklog token create --user admin --name codex-webapp --scope write --project WEB --project API
```

Add `--expires-days N` to have the token expire automatically instead of being valid forever, and rotate it
periodically with `token rotate --user admin --id <id>` (the old secret stops working and the new one starts
working atomically, so there's no gap). List and revoke from the CLI with `/lightbacklog token list --user admin`
and `/lightbacklog token revoke --user admin --name codex`. Tokens can also be created and revoked in the web UI
(Settings → API tokens). Store the secret in an environment variable, e.g. `LIGHTBACKLOG_TOKEN`, not in a
repository.

## 2. Register the MCP server

Claude Code:

```bash
claude mcp add --transport http lightbacklog http://127.0.0.1:8100/mcp \
  --header "Authorization: Bearer $LIGHTBACKLOG_TOKEN"
```

Codex (`~/.codex/config.toml`, or `codex mcp add lightbacklog --url http://127.0.0.1:8100/mcp --bearer-token-env-var LIGHTBACKLOG_TOKEN`):

```toml
[mcp_servers.lightbacklog]
url = "http://127.0.0.1:8100/mcp"
bearer_token_env_var = "LIGHTBACKLOG_TOKEN"
```

Codex only reads the token from an environment variable, so `LIGHTBACKLOG_TOKEN` must be set in the environment of whatever launches Codex (a shell profile, or the service that runs it). Claude Code takes the header directly, as above. Verify with `claude mcp list` / `codex mcp get lightbacklog`.

## 3. Tools

| Area | Tools |
|---|---|
| Vocabulary | `get_meta` (statuses, priorities), `list_labels`, `create_label` |
| Projects | `list_projects` (with `query`), `get_project`, `create_project`, `update_project`, `archive_project`, `favorite_project` |
| Tasks | `list_tasks` (filters, sorting, pagination), `search_tasks`, `get_task`, `create_task`, `update_task`, `move_task`, `delete_task` |
| Attachments | `add_attachment` (base64), `list_attachments` |

## 4. Rules of the road (suggested AGENTS.md / CLAUDE.md snippet)

```markdown
## Task tracking (LightBacklog)
- Track work in LightBacklog through the `lightbacklog` MCP server. Each task belongs to one project (use its key, e.g. `WEB`).
- Before creating a task, search for it (`search_tasks`) or pass a stable `external_ref` (e.g. the issue/PR URL) to
  `create_task`; the same `external_ref` in a project returns the existing task instead of a duplicate.
- Update status as you work: `todo` → `in_progress` → `done` (use `blocked` with a note in the description).
- Pass the task's `version` to `update_task` when editing descriptions to avoid overwriting concurrent edits.
- Use `sequence` (a whole number, 1 first) when the order of work matters, e.g. among several high-priority tasks; `list_tasks` accepts `sort: "sequence"`.
- Deleting projects is a human-only action (browser session, archived projects only); agents can archive with `archive_project`. Deleting a *task* is permanent for agents (`delete_task`), so prefer `update_task` to set `status: done`.
- Classify work with `type` (`feature` or `bug`; optional): set it on `create_task`/`update_task`, filter with `list_tasks` `type: ["bug"]` (or `none` for untyped), and read the valid values from `get_meta`.
- Write descriptions in Markdown; use `- [ ]` checklists. Attach screenshots with `add_attachment` and paste the
  returned Markdown snippet into the description.
```

## 5. REST quick reference

```bash
B=http://127.0.0.1:8100; H="Authorization: Bearer $LIGHTBACKLOG_TOKEN"

curl -s -H "$H" "$B/api/v1/projects?q=web"
curl -s -H "$H" "$B/api/v1/tasks?project=WEB&status=todo,in_progress&label=bug&sort=-priority&limit=20"
curl -s -H "$H" "$B/api/v1/tasks?q=login+redirect&include_total=true"

# Create (safe to retry: same external_ref -> same task)
curl -s -H "$H" -H 'Content-Type: application/json' "$B/api/v1/tasks" \
  -d '{"project":"WEB","title":"Fix login redirect","priority":"high","labels":["bug"],"external_ref":"gh-123"}'

# Update with optimistic concurrency (If-Match carries the task version)
curl -s -X PATCH -H "$H" -H 'Content-Type: application/json' -H 'If-Match: "3"' "$B/api/v1/tasks/WEB-42" \
  -d '{"status":"in_progress","description":"- [ ] reproduce\n- [ ] fix"}'

# Upload a screenshot, then embed it
curl -s -H "$H" -F file=@shot.png "$B/api/v1/tasks/WEB-42/attachments"
```

Changes you make appear immediately in any open browser (live updates over `/api/v1/events`), so it is fine to work while a human watches the board.

Errors are `application/problem+json`, e.g. `{"code":"validation_failed","errors":[{"field":"status","message":"unknown value ..."}]}`.
Common codes: `validation_failed` (422), `not_found` (404, also for projects you cannot access), `forbidden`,
`insufficient_scope`, `version_conflict` (409, with the current task in `current`), `project_archived`,
`unsupported_file_type` (415), `file_too_large` (413), `rate_limited` (429).
