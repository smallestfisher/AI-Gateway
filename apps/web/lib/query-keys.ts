// Centralised TanStack Query keys.
export const qk = {
  providers: ["providers"] as const,
  models: ["models"] as const,
  channels: ["channels"] as const,
  upstreamModels: (providerId: string) => ["upstream-models", providerId] as const,
  providerDiagnostics: (providerId: string) => ["provider-diagnostics", providerId] as const,
  profiles: ["profiles"] as const,
  policies: ["policies"] as const,
  users: ["users"] as const,
  keys: (userId: string) => ["keys", userId] as const,
  logs: (filter: unknown) => ["logs", filter] as const,
  configVersion: ["config-version"] as const,
};
