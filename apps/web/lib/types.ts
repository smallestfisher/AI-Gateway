// Type definitions mirroring the Go Admin API DTOs (internal/admin/store.go).

export type Protocol = "openai_chat" | "anthropic_messages" | "openai_responses";

export interface Provider {
  id?: string;
  name: string;
  protocol: string;
  base_url: string;
  api_key?: string; // write-only; never returned by the gateway
  proxy_id?: string | null;
  timeout_ms: number;
  connect_timeout_ms: number;
  max_retries: number;
  weight: number;
  priority: number;
  enabled: boolean;
  hc_error_rate: number;
  hc_p95_ttft_ms: number;
  hc_window_sec: number;
  hc_cooldown_sec: number;
  metadata?: Record<string, unknown>;
}

export interface Model {
  id?: string;
  alias: string;
  display_name: string;
  description?: string;
  enabled: boolean;
}

export interface ModelChannel {
  id?: string;
  model_id: string;
  provider_id: string;
  upstream_model: string;
  weight: number;
  priority: number;
  enabled: boolean;
}

export interface UpstreamModel {
  id: string;
  display_name?: string;
}

export interface BulkModelChannelItem {
  upstream_model: string;
  alias: string;
  display_name?: string;
}

export interface BulkModelChannelInput {
  items: BulkModelChannelItem[];
  weight: number;
  priority: number;
  enabled: boolean;
}

export interface BulkModelChannelRowResult {
  alias: string;
  upstream_model: string;
  status: "created" | "skipped" | string;
  model_id?: string;
  channel_id?: string;
  error?: string;
}

export interface BulkModelChannelResult {
  created_models: number;
  created_channels: number;
  skipped_channels: number;
  items: BulkModelChannelRowResult[];
}

export interface DiagnosticError {
  code: string;
  message: string;
  body_preview?: string;
}

export interface DiagnosticUsage {
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens?: number;
  cache_read_tokens?: number;
  reasoning_tokens?: number;
}

export interface DiagnosticResult {
  ok: boolean;
  mode: "upstream" | "gateway";
  request_id?: string;
  client_protocol?: string;
  alias?: string;
  provider_id?: string;
  provider_name?: string;
  protocol?: string;
  upstream_model?: string;
  latency_ms: number;
  http_status?: number;
  stop_reason?: string;
  usage?: DiagnosticUsage;
  response_preview?: string;
  error?: DiagnosticError;
}

export interface UpstreamTestInput {
  upstream_model: string;
  message?: string;
  timeout_ms?: number;
  stream?: boolean;
}

export interface GatewayTestInput {
  client_protocol: Protocol | string;
  alias: string;
  provider_id?: string;
  upstream_model?: string;
  message?: string;
  timeout_ms?: number;
  stream?: boolean;
}

export type ProfileScope = "default" | "provider" | "model";

export interface ClientProfile {
  id?: string;
  name: string;
  scope: ProfileScope;
  target_id?: string | null;
  headers?: Record<string, string>;
  user_agent?: string;
  origin?: string;
  referer?: string;
  strip_client_headers: boolean;
  enabled: boolean;
}

export type PolicyMode = "failover" | "weighted" | "auto";

export interface RouterPolicy {
  id?: string;
  scope: "global" | "model";
  model_id?: string | null;
  mode: PolicyMode;
  params?: Record<string, unknown>;
  enabled: boolean;
}

export interface User {
  id?: string;
  name: string;
  email?: string;
  status: "active" | "disabled";
  balance: number;
  rpm: number;
  tpm: number;
  whitelist: string[];
  created_at?: string;
  updated_at?: string;
}

export interface APIKey {
  id?: string;
  user_id: string;
  name: string;
  key_prefix: string;
  key?: string;
  status: "active" | "revoked";
  last_used_at?: string | null;
  created_at?: string;
}

// Mirrors internal/admin/logs.go RequestLog — one row of request_logs.
export interface RequestLog {
  id: string;
  timestamp: string; // RFC3339
  user_id?: string;
  api_key_id?: string;
  protocol: Protocol | string;
  model: string;
  provider_id?: string;
  upstream_model?: string;
  stream: boolean;
  status: string; // success | error | no_channel | no_available_channel
  http_status?: number;
  stop_reason?: string;
  ttft_ms?: number;
  latency_ms?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  reasoning_tokens?: number;
  error_code?: string;
  error_msg?: string;
  request_id: string;
}

export interface LogList {
  data: RequestLog[];
  total: number;
}

export interface LogFilter {
  user_id?: string;
  model?: string;
  provider_id?: string;
  protocol?: string;
  status?: string;
  q?: string;
  stream?: boolean;
  from?: string;
  to?: string;
  limit: number;
  offset: number;
}
