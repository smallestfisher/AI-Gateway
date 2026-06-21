import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { ADMIN_COOKIE } from "@/lib/env";

export async function POST() {
  const c = await cookies();
  c.delete(ADMIN_COOKIE);
  return NextResponse.json({ ok: true });
}
