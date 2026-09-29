# Changelog

Newest first. Each release is tagged (`v1.0.0`, ...) and available from the Releases page.

## 1.0.0 — first public release

The first version published for self-hosting.

- Browser UI: All Tasks workspace with inline editing, filters, sorting and configurable columns; Kanban board;
  rich-text descriptions with screenshot paste; command palette; live updates; light and dark theme; mobile
  layout.
- Tasks: statuses, priorities, types (bug, feature, improvement), labels, manual sequence, created, updated and
  completed dates, attachments.
- Projects: keys and colours, favourites, archiving, and owner, editor and viewer roles.
- API and agents: REST API (46 operations) and an MCP server (18 tools) with scoped, revocable API tokens
  that can expire and be rotated, per-token MCP rate limiting, an MCP audit log (`lightbacklog mcp-audit list`)
  and an optional IP allowlist for remote MCP access.
- Operations: single container, SQLite storage with full-text search, automatic migrations with a
  pre-migration snapshot, built-in verified backup and restore, health check, and a hardened compose file
  (read-only root filesystem, no capabilities, memory limit).
