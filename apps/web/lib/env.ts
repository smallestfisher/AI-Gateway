// Server-side runtime configuration for the web app (BFF).
// The browser never sees GATEWAY_URL — only the Route Handlers do.

export const GATEWAY_URL = (process.env.GATEWAY_URL ?? "http://localhost:8080").replace(
  /\/+$/,
  "",
);

export const ADMIN_COOKIE = "admin_token";
export const SESSION_MAX_AGE = 60 * 60 * 24 * 7; // 7 days
