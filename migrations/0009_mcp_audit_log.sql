-- Forensic trail of MCP tool calls: who (token/user), what tool, when, and the outcome. No
-- argument/payload column on purpose -- task content can be sensitive, and this is an audit
-- log, not a debug trace. token_id/user_id go NULL rather than disappearing if the owning
-- token or user is later removed, so history survives account cleanup.
CREATE TABLE mcp_audit_log (
    id          TEXT PRIMARY KEY,
    token_id    TEXT REFERENCES api_tokens(id) ON DELETE SET NULL,
    user_id     TEXT REFERENCES users(id) ON DELETE SET NULL,
    tool        TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('ok','error')),
    error_code  TEXT,
    client_ip   TEXT,
    duration_ms INTEGER NOT NULL,
    created_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX mcp_audit_log_token_idx ON mcp_audit_log(token_id, created_at);
CREATE INDEX mcp_audit_log_created_idx ON mcp_audit_log(created_at);
