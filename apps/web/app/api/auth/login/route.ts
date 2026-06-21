import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { ADMIN_COOKIE, GATEWAY_URL, SESSION_MAX_AGE } from "@/lib/env";

// Verify the submitted token against the gateway, then set an httpOnly cookie.
export async function POST(req: Request) {
  const { token } = (await req.json().catch(() => ({}))) as { token?: string };
  if (!token || typeof token !== "string") {
    return NextResponse.json(
      { error: { message: "Token is required" } },
      { status: 400 },
    );
  }

  const verify = await fetch(`${GATEWAY_URL}/api/admin/config/version`, {
    headers: { Authorization: `Bearer ${token}` },
    cache: "no-store",
  }).catch(() => null);

  if (!verify || !verify.ok) {
    return NextResponse.json(
      { error: { message: "Invalid or rejected token" } },
      { status: 401 },
    );
  }

  const c = await cookies();
  c.set(ADMIN_COOKIE, token, {
    httpOnly: true,
    sameSite: "lax",
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge: SESSION_MAX_AGE,
  });
  return NextResponse.json({ ok: true });
}
