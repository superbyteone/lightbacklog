-- Optional expiry for API tokens. NULL (the default for every existing token) means "never
-- expires", preserving today's behavior; new tokens may set a TTL at creation time.
ALTER TABLE api_tokens ADD COLUMN expires_at INTEGER;
