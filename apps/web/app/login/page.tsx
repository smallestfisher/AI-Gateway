"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "sonner";
import { Loader2, KeyRound } from "lucide-react";

// useSearchParams() forces a client-side render bailout during static prerender.
// The form is split into a child component wrapped in <Suspense> so the
// prerenderable fallback becomes the static HTML and the form renders after hydration.
function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const [token, setToken] = useState("");
  const [loading, setLoading] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body?.error?.message ?? "登录失败");
      }
      const next = params.get("next") || "/providers";
      router.replace(next);
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "登录失败");
      setLoading(false);
    }
  }

  const expired = params.get("reason") === "expired";

  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/30 p-6">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <div className="flex size-10 items-center justify-center rounded-lg border bg-background">
            <KeyRound className="size-5 text-muted-foreground" />
          </div>
          <div>
            <h1 className="text-lg font-semibold tracking-tight">
              AI Agent Gateway
            </h1>
            <p className="text-sm text-muted-foreground">
              使用管理员 Token 登录
            </p>
          </div>
        </div>

        <form
          onSubmit={onSubmit}
          className="space-y-4 rounded-xl border bg-card p-6 shadow-sm"
        >
          <div className="space-y-2">
            <Label htmlFor="token">管理员 Token</Label>
            <Input
              id="token"
              type="password"
              autoComplete="off"
              placeholder="GATEWAY_ADMIN_TOKEN"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoFocus
              required
            />
          </div>
          {expired && (
            <p className="text-xs text-destructive">
              会话已过期，请重新登录。
            </p>
          )}
          <Button type="submit" className="w-full" disabled={loading}>
            {loading && <Loader2 className="size-4 animate-spin" />}
            登录
          </Button>
        </form>
        <p className="mt-4 text-center text-xs text-muted-foreground">
          Token 会交由网关验证，并保存在 httpOnly Cookie 中。
        </p>
      </div>
    </div>
  );
}

function LoginFallback() {
  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/30 p-6">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <div className="flex size-10 items-center justify-center rounded-lg border bg-background">
            <KeyRound className="size-5 text-muted-foreground" />
          </div>
          <div>
            <h1 className="text-lg font-semibold tracking-tight">
              AI Agent Gateway
            </h1>
            <p className="text-sm text-muted-foreground">
              使用管理员 Token 登录
            </p>
          </div>
        </div>
        <div className="flex items-center justify-center rounded-xl border bg-card p-6 shadow-sm">
          <Loader2 className="size-5 animate-spin text-muted-foreground" />
        </div>
      </div>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense fallback={<LoginFallback />}>
      <LoginForm />
    </Suspense>
  );
}
