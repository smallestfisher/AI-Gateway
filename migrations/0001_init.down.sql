-- Reverse of 0001_init.up.sql (for golang-migrate down). Development-only.

DROP TABLE IF EXISTS config_meta;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS request_logs;
DROP TABLE IF EXISTS mcp_bindings;
DROP TABLE IF EXISTS mcp_servers;
DROP TABLE IF EXISTS router_policies;
DROP TABLE IF EXISTS protocols;
DROP TABLE IF EXISTS client_profiles;
DROP TABLE IF EXISTS model_channels;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS providers;
DROP TABLE IF EXISTS proxy_egress;
DROP TABLE IF EXISTS user_quotas;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
