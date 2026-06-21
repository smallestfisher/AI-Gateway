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
  status: "active" | "revoked";
  last_used_at?: string | null;
  created_at?: string;
}
