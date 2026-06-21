// Typed client for the same-origin /api/admin/* BFF proxy.
// The browser only ever talks to the Next.js route handlers; the admin token
// lives in an httpOnly cookie and never enters client JS.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

function authHeaders(extra?: HeadersInit): HeadersInit {
  return { "Content-Type": "application/json", ...(extra ?? {}) };
}

async function send(path: string, init?: RequestInit): Promise<Response> {
  return fetch(`/api/admin${path}`, {
    ...init,
    headers: authHeaders(init?.headers),
  });
}

function throwUnauthorized(): never {
  if (typeof window !== "undefined") {
    window.location.href = "/login?reason=expired";
  }
  throw new ApiError(401, "session expired");
}

async function fail(res: Response): Promise<ApiError> {
  if (res.status === 401) throwUnauthorized();
  const body = await res.json().catch(() => ({}));
  const msg = (body as { error?: { message?: string } })?.error?.message ?? `request failed (${res.status})`;
  return new ApiError(res.status, msg);
}

async function parse<T>(res: Response): Promise<T> {
  if (res.status === 204) return undefined as unknown as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

export const api = {
  async list<T>(path: string): Promise<T[]> {
    const res = await send(path);
    if (!res.ok) throw await fail(res);
    const body = await parse<{ data: T[] }>(res);
    return body?.data ?? [];
  },

  async get<T>(path: string): Promise<T> {
    const res = await send(path);
    if (!res.ok) throw await fail(res);
    return parse<T>(res);
  },

  async create<T = { id: string }>(path: string, body: unknown): Promise<T> {
    const res = await send(path, { method: "POST", body: JSON.stringify(body) });
    if (!res.ok) throw await fail(res);
    return parse<T>(res);
  },

  async update(path: string, body: unknown): Promise<void> {
    const res = await send(path, { method: "PUT", body: JSON.stringify(body) });
    if (!res.ok && res.status !== 204) throw await fail(res);
  },

  async remove(path: string): Promise<void> {
    const res = await send(path, { method: "DELETE" });
    if (!res.ok && res.status !== 204) throw await fail(res);
  },

  raw: send, // for endpoints with non-standard shapes
};
