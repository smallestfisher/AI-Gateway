// Centralised TanStack Query keys.
export const qk = {
  providers: ["providers"] as const,
  models: ["models"] as const,
  channels: ["channels"] as const,
  profiles: ["profiles"] as const,
  policies: ["policies"] as const,
  users: ["users"] as const,
  keys: (userId: string) => ["keys", userId] as const,
  configVersion: ["config-version"] as const,
};
