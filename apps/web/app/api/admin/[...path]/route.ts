import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { ADMIN_COOKIE, GATEWAY_URL } from "@/lib/env";

// BFF proxy: read the admin token from the httpOnly cookie, forward the
// request to the gateway as a Bearer-authed call. The browser never holds the
// token. Same-origin, so no CORS.
async function proxy(
  req: Request,
  ctx: { params: Promise<{ path?: string[] }> },
) {
  const { path = [] } = await ctx.params;
  const token = (await cookies()).get(ADMIN_COOKIE)?.value;
  if (!token) {
    return NextResponse.json(
      { error: { code: "unauthorized", message: "no session" } },
      { status: 401 },
    );
  }

  const url = new URL(req.url);
  const target = `${GATEWAY_URL}/api/admin/${path.join("/")}${url.search}`;

  const init: RequestInit = {
    method: req.method,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    cache: "no-store",
    // @ts-expect-error duplex is required when streaming a request body
    duplex: "half",
  };
  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = await req.text();
  }

  const upstream = await fetch(target, init).catch(() => null);
  if (!upstream) {
    return NextResponse.json(
      { error: { code: "upstream_unreachable", message: "gateway unreachable" } },
      { status: 502 },
    );
  }

  if (upstream.status === 401) {
    (await cookies()).delete(ADMIN_COOKIE);
  }

  const res = new NextResponse(upstream.body, { status: upstream.status });
  const ctype = upstream.headers.get("content-type");
  if (ctype) res.headers.set("content-type", ctype);
  return res;
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const DELETE = proxy;
